// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package intake

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSettingsEnablesTestOptimizationCoverage(t *testing.T) {
	server, err := Start(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, server.Close())
	})

	requestBody := bytes.NewBufferString(`{"data":{"id":"request-123"}}`)
	response, err := testHTTPClient().Post(server.URL()+settingsPath, "application/json", requestBody)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, response.Body.Close())
	})
	require.Equal(t, http.StatusAccepted, response.StatusCode)
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
		{path: knownTestsPath, contains: `"tests":{"jest":{}}`},
		{path: skippableTestsPath, contains: `"data":[]`},
		{path: testManagementPath, contains: `"modules":{}`},
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
