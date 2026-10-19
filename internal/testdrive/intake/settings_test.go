// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package intake

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/testoptimization/api"
	"github.com/stretchr/testify/require"
)

func TestPythonFeatureFixturesKeepHistoricalAndModuleIdentity(t *testing.T) {
	scenario := Scenario{Framework: "pytest", Feature: "skipping", Module: "tests.integration", Suite: "test_example.py", Test: "test_probe", SourceFile: "tests/integration/test_example.py"}
	response := httptest.NewRecorder()
	handleKnownTests(response, nil, scenario)
	var known struct {
		Data struct {
			Attributes struct {
				Tests map[string]map[string][]string
			}
		}
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &known))
	require.Equal(t, []string{"ddtest_existing_test"}, known.Data.Attributes.Tests["pytest"]["ddtest_known_test.py"])
	response = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, constants.SkippableTestsURLPath, bytes.NewBufferString(`{"data":{"attributes":{"test_level":"test"}}}`))
	handleSkippableTests(response, request, scenario)
	var skipped struct {
		Data []struct {
			Attributes struct {
				Suite, Name    string
				Configurations map[string]string
			}
		}
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &skipped))
	require.Len(t, skipped.Data, 1)
	require.Equal(t, scenario.Module, skipped.Data[0].Attributes.Configurations["test.bundle"])
	require.Equal(t, scenario.Suite, skipped.Data[0].Attributes.Suite)
	require.Equal(t, scenario.Test, skipped.Data[0].Attributes.Name)
}

func TestSettingsEnablesTestOptimizationCoverage(t *testing.T) {
	server, err := Start(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, server.Close())
	})

	requestJSON, err := json.Marshal(api.SettingsRequest{Data: api.SettingsRequestHeader{
		ID: "request-123", Type: constants.SettingsRequestType,
		Attributes: api.SettingsRequestData{Service: "intake-qa", RepositoryURL: "https://github.com/DataDog/ddtest"},
	}})
	require.NoError(t, err)
	requestBody := bytes.NewReader(requestJSON)
	response, err := testHTTPClient().Post(server.URL()+constants.SettingsURLPath, constants.ContentTypeJSON, requestBody)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, response.Body.Close())
	})
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Equal(t, constants.ContentTypeJSON, response.Header.Get("Content-Type"))

	var settings api.SettingsResponse
	require.NoError(t, json.NewDecoder(response.Body).Decode(&settings))
	require.Equal(t, "request-123", settings.Data.ID)
	require.Equal(t, constants.SettingsResponseType, settings.Data.Type)
	require.True(t, settings.Data.Attributes.ItrEnabled)
	require.True(t, settings.Data.Attributes.CodeCoverage)
	require.False(t, settings.Data.Attributes.TestsSkipping)
	require.False(t, settings.Data.Attributes.RequireGit)
	require.True(t, settings.Data.Attributes.CoverageReportUploadEnabled)
	require.True(t, settings.Data.Attributes.ImpactedTestsEnabled)
	require.True(t, settings.Data.Attributes.FlakyTestRetriesEnabled)
	require.True(t, settings.Data.Attributes.DIEnabled)
	require.True(t, settings.Data.Attributes.KnownTestsEnabled)
	require.True(t, settings.Data.Attributes.EarlyFlakeDetection.Enabled)
	require.Equal(t, 1, settings.Data.Attributes.EarlyFlakeDetection.SlowTestRetries.FiveS)
	require.True(t, settings.Data.Attributes.TestManagement.Enabled)
}

func TestAdvancedFeatureEndpointsReturnSafeEmptyDatasets(t *testing.T) {
	server, err := Start(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, server.Close())
	})

	tests := []struct {
		path     string
		contains string
	}{
		{path: constants.KnownTestsURLPath, contains: `"jest":{}`},
		{path: constants.SkippableTestsURLPath, contains: `"data":[]`},
		{path: constants.TestManagementTestsURLPath, contains: `"modules":{}`},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			response, err := testHTTPClient().Post(server.URL()+test.path, constants.ContentTypeJSON, bytes.NewBufferString(`{"data":{}}`))
			require.NoError(t, err)
			t.Cleanup(func() {
				require.NoError(t, response.Body.Close())
			})
			require.Equal(t, http.StatusOK, response.StatusCode)

			var body bytes.Buffer
			_, err = body.ReadFrom(response.Body)
			require.NoError(t, err)
			require.Contains(t, body.String(), test.contains)
		})
	}
}

func TestGitNegotiation(t *testing.T) {
	handler := newHandler()
	search := httptest.NewRecorder()
	handler.ServeHTTP(search, httptest.NewRequest(http.MethodPost, "/api/v2/git/repository/search_commits", bytes.NewBufferString(`{"data":[]}`)))
	require.Equal(t, http.StatusOK, search.Code)
	require.JSONEq(t, `{"data":[]}`, search.Body.String())
	pack := httptest.NewRecorder()
	handler.ServeHTTP(pack, httptest.NewRequest(http.MethodPost, "/api/v2/git/repository/packfile", bytes.NewBufferString("pack")))
	require.Equal(t, http.StatusNoContent, pack.Code)
	unknown := httptest.NewRecorder()
	handler.ServeHTTP(unknown, httptest.NewRequest(http.MethodPost, "/api/v2/git/repository/unknown", nil))
	require.Equal(t, http.StatusNotFound, unknown.Code)
	require.Contains(t, unknown.Body.String(), "unsupported")
}

func TestScenarioResponsesEnableOnlySelectedBehavior(t *testing.T) {
	for _, feature := range []string{"reporting-only", "auto-retries", "early-flake-detection", "skipping", "quarantine", "disabled", "attempt-to-fix"} {
		t.Run(feature, func(t *testing.T) {
			handler := scenarioHandler(Scenario{Feature: feature, Module: "custom-module", Suite: "../../src/probe.test.js", SourceFile: "src/probe.test.js", Test: "probe"})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, constants.SettingsURLPath, bytes.NewBufferString(`{"data":{}}`)))
			require.Equal(t, http.StatusOK, response.Code)
			var settings api.SettingsResponse
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &settings))
			attributes := settings.Data.Attributes
			require.Equal(t, feature == "auto-retries", attributes.FlakyTestRetriesEnabled)
			require.Equal(t, feature == "early-flake-detection", attributes.EarlyFlakeDetection.Enabled)
			require.Equal(t, feature == "skipping", attributes.TestsSkipping)
			require.Equal(t, feature == "quarantine" || feature == "disabled" || feature == "attempt-to-fix", attributes.TestManagement.Enabled)
			require.False(t, attributes.ImpactedTestsEnabled)
			require.False(t, attributes.DIEnabled)
			response = httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, constants.SkippableTestsURLPath, bytes.NewBufferString(`{}`)))
			if feature == "skipping" {
				require.Contains(t, response.Body.String(), `"suite":"src/probe.test.js"`)
				require.NotContains(t, response.Body.String(), "../../")
			} else {
				require.Contains(t, response.Body.String(), `"data":[]`)
			}
			response = httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, constants.TestManagementTestsURLPath, bytes.NewBufferString(`{}`)))
			if attributes.TestManagement.Enabled {
				require.Contains(t, response.Body.String(), `"custom-module"`)
				require.Contains(t, response.Body.String(), `"../../src/probe.test.js"`)
				require.Contains(t, response.Body.String(), `"probe"`)
			} else {
				require.Contains(t, response.Body.String(), `"modules":{}`)
			}
		})
	}
}

func TestSkippingWithoutSourceDoesNotReturnTheSuiteIdentity(t *testing.T) {
	response := httptest.NewRecorder()
	handler := scenarioHandler(Scenario{Feature: "skipping", Suite: "../../src/probe.test.js"})
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, constants.SkippableTestsURLPath, bytes.NewBufferString(`{}`)))
	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Body.String(), `"data":[]`)
	require.NotContains(t, response.Body.String(), "probe.test.js")
}

func TestSkippingUsesTheRequestedProtocolLevel(t *testing.T) {
	handler := scenarioHandler(Scenario{Feature: "skipping", Suite: "../../reported/probe.cy.js", SourceFile: "cypress/probe.cy.js", Test: "ddtest validation probe"})
	for _, tc := range []struct{ request, want string }{
		{"{\"data\":{\"attributes\":{\"test_level\":\"test\"}}}", "{\"type\":\"test\",\"attributes\":{\"suite\":\"../../reported/probe.cy.js\",\"name\":\"ddtest validation probe\"}}"},
		{"{\"data\":{\"attributes\":{\"test_level\":\"suite\"}}}", "{\"type\":\"suite\",\"attributes\":{\"suite\":\"cypress/probe.cy.js\"}}"},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, constants.SkippableTestsURLPath, bytes.NewBufferString(tc.request)))
		require.Equal(t, http.StatusOK, response.Code)
		var result struct{ Data []json.RawMessage }
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
		require.Len(t, result.Data, 1)
		require.JSONEq(t, tc.want, string(result.Data[0]))
	}
}

func TestKnownTestsIncludeTheEmittedModule(t *testing.T) {
	response := httptest.NewRecorder()
	scenarioHandler(Scenario{Feature: "early-flake-detection", Module: "named project"}).ServeHTTP(response, httptest.NewRequest(http.MethodPost, constants.KnownTestsURLPath, bytes.NewBufferString(`{}`)))
	require.Equal(t, http.StatusOK, response.Code)
	var data struct {
		Data struct {
			Attributes struct {
				Tests map[string]json.RawMessage `json:"tests"`
			} `json:"attributes"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &data))
	require.JSONEq(t, `{}`, string(data.Data.Attributes.Tests["named project"]))
	require.JSONEq(t, `{}`, string(data.Data.Attributes.Tests["jest"]))
}

func TestSkippingExplicitSuiteSelectorKeepsRepositorySourceIdentity(t *testing.T) {
	scenario := Scenario{Feature: "skipping", Suite: "features/probe.feature", SourceFile: "packages/api/features/probe.feature", SuiteSelector: "features/probe.feature"}
	response := httptest.NewRecorder()
	scenarioHandler(scenario).ServeHTTP(response, httptest.NewRequest(http.MethodPost, constants.SkippableTestsURLPath, bytes.NewBufferString(`{"data":{"attributes":{"test_level":"suite"}}}`)))
	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Body.String(), `"suite":"features/probe.feature"`)
	require.NotContains(t, response.Body.String(), "packages/api")
	require.Equal(t, "packages/api/features/probe.feature", scenario.SourceFile)
}
