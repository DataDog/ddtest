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
	"github.com/stretchr/testify/require"
)

func TestSettingsEnablesTestOptimizationCoverage(t *testing.T) {
	server, err := Start(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, server.Close())
	})

	requestBody := bytes.NewBufferString(`{"data":{"id":"request-123"}}`)
	response, err := testHTTPClient().Post(server.URL()+constants.SettingsURLPath, "application/json", requestBody)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, response.Body.Close())
	})
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Equal(t, "application/json", response.Header.Get("Content-Type"))

	var settings settingsResponse
	require.NoError(t, json.NewDecoder(response.Body).Decode(&settings))
	require.Equal(t, "request-123", settings.Data.ID)
	require.Equal(t, settingsResponseType, settings.Data.Type)
	require.True(t, settings.Data.Attributes.ITREnabled)
	require.True(t, settings.Data.Attributes.CodeCoverage)
	require.True(t, settings.Data.Attributes.TestsSkipping)
	require.False(t, settings.Data.Attributes.RequireGit)
	require.True(t, settings.Data.Attributes.CoverageReportUploadEnabled)
	require.True(t, settings.Data.Attributes.ImpactedTestsEnabled)
	require.True(t, settings.Data.Attributes.FlakyTestRetriesEnabled)
	require.True(t, settings.Data.Attributes.DIEnabled)
	require.True(t, settings.Data.Attributes.KnownTestsEnabled)
	require.True(t, settings.Data.Attributes.EarlyFlakeDetection.Enabled)
	require.Equal(t, 1, settings.Data.Attributes.EarlyFlakeDetection.SlowTestRetries["5s"])
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
			response, err := testHTTPClient().Post(server.URL()+test.path, "application/json", bytes.NewBufferString(`{"data":{}}`))
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
