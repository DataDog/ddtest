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
	server, err := Start()
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
	require.False(t, settings.Data.Attributes.TestsSkipping)
	require.False(t, settings.Data.Attributes.RequireGit)
}
