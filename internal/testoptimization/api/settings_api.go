// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package api

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/telemetry"
)

func (c *transport) GetSettings() (*SettingsResponseData, error) {
	startTime := time.Now()
	defer func() {
		c.backendRequestTimings.Settings = time.Since(startTime)
	}()

	if c.repositoryURL == "" || c.commitSha == "" {
		return nil, fmt.Errorf("testoptimization.GetSettings: repository URL and commit SHA are required")
	}
	c.settingsRawResponse = nil

	body := SettingsRequest{
		Data: SettingsRequestHeader{
			ID:   c.id,
			Type: constants.SettingsRequestType,
			Attributes: SettingsRequestData{
				Service:        c.serviceName,
				Env:            c.environment,
				RepositoryURL:  c.repositoryURL,
				Branch:         c.branchName,
				Sha:            c.commitSha,
				TestLevel:      c.getTestSkippingLevel(),
				Configurations: c.testConfigurations,
			},
		},
	}

	request := c.getPostRequestConfig(constants.SettingsURLPath, body)
	telemetry.GitRequestsSettings(c.telemetryClient, request.Compressed)

	requestStartTime := time.Now()
	response, err := c.handler.SendRequest(*request)
	telemetry.GitRequestsSettingsMs(c.telemetryClient, time.Since(requestStartTime))
	if err != nil {
		telemetry.GitRequestsSettingsErrors(c.telemetryClient, responseStatusCode(response))
		return nil, fmt.Errorf("sending get settings request: %s", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		telemetry.GitRequestsSettingsErrors(c.telemetryClient, response.StatusCode)
	}

	slog.Debug("testoptimization.settings", "responseBody", string(response.Body))
	c.settingsRawResponse = cloneRawMessage(response.Body)

	var responseObject SettingsResponse
	err = response.Unmarshal(&responseObject)
	if err != nil {
		return nil, fmt.Errorf("unmarshalling settings response: %s", err)
	}
	settings := &responseObject.Data.Attributes
	telemetry.GitRequestsSettingsResponse(c.telemetryClient, telemetry.SettingsResponse{
		CodeCoverageEnabled:        settings.CodeCoverage,
		ITRSkippingEnabled:         settings.TestsSkipping,
		EarlyFlakeDetectionEnabled: settings.EarlyFlakeDetection.Enabled,
		FlakyTestRetriesEnabled:    settings.FlakyTestRetriesEnabled,
		TestManagementEnabled:      settings.TestManagement.Enabled,
	})

	return settings, nil
}
