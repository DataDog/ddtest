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
	require.False(t, settings.Data.Attributes.CoverageReportUploadEnabled)
	require.False(t, settings.Data.Attributes.ImpactedTestsEnabled)
	require.False(t, settings.Data.Attributes.FlakyTestRetriesEnabled)
	require.False(t, settings.Data.Attributes.DIEnabled)
	require.False(t, settings.Data.Attributes.KnownTestsEnabled)
	require.False(t, settings.Data.Attributes.EarlyFlakeDetection.Enabled)
	require.Equal(t, 2, settings.Data.Attributes.EarlyFlakeDetection.SlowTestRetries.FiveS)
	require.False(t, settings.Data.Attributes.TestManagement.Enabled)
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
	for _, feature := range []string{"", "auto-retries", "early-flake-detection", "skipping", "quarantine", "disabled", "attempt-to-fix"} {
		t.Run(feature, func(t *testing.T) {
			handler := scenarioHandler(Scenario{Feature: feature, Module: "custom-module", Suite: "probe.test.js", Test: "probe"})
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
				require.Contains(t, response.Body.String(), `"suite":"probe.test.js"`)
			} else {
				require.Contains(t, response.Body.String(), `"data":[]`)
			}
			response = httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, constants.TestManagementTestsURLPath, bytes.NewBufferString(`{}`)))
			if attributes.TestManagement.Enabled {
				require.Contains(t, response.Body.String(), `"custom-module"`)
				require.Contains(t, response.Body.String(), `"probe"`)
			} else {
				require.Contains(t, response.Body.String(), `"modules":{}`)
			}
		})
	}
}
