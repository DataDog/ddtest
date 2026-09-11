// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package intake

import (
	"encoding/json"
	"net/http"
)

const (
	settingsPath         = "/api/v2/libraries/tests/services/setting"
	settingsResponseID   = "test-settings"
	settingsResponseType = "ci_app_test_service_libraries_settings"
)

type settingsRequest struct {
	Data struct {
		ID string `json:"id"`
	} `json:"data"`
}

type settingsResponse struct {
	Data struct {
		ID         string             `json:"id"`
		Type       string             `json:"type"`
		Attributes settingsAttributes `json:"attributes"`
	} `json:"data"`
}

type settingsAttributes struct {
	CodeCoverage                bool                        `json:"code_coverage"`
	CoverageReportUploadEnabled bool                        `json:"coverage_report_upload_enabled"`
	TestsSkipping               bool                        `json:"tests_skipping"`
	RequireGit                  bool                        `json:"require_git"`
	ITREnabled                  bool                        `json:"itr_enabled"`
	ImpactedTestsEnabled        bool                        `json:"impacted_tests_enabled"`
	FlakyTestRetriesEnabled     bool                        `json:"flaky_test_retries_enabled"`
	DIEnabled                   bool                        `json:"di_enabled"`
	KnownTestsEnabled           bool                        `json:"known_tests_enabled"`
	EarlyFlakeDetection         earlyFlakeDetectionSettings `json:"early_flake_detection"`
	TestManagement              testManagementSettings      `json:"test_management"`
}

type earlyFlakeDetectionSettings struct {
	Enabled                bool           `json:"enabled"`
	SlowTestRetries        map[string]int `json:"slow_test_retries"`
	FaultySessionThreshold int            `json:"faulty_session_threshold"`
}

type testManagementSettings struct {
	Enabled             bool `json:"enabled"`
	AttemptToFixRetries int  `json:"attempt_to_fix_retries"`
}

func newHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+settingsPath, handleSettings)
	return mux
}

func handleSettings(w http.ResponseWriter, request *http.Request) {
	var settingsRequest settingsRequest
	if err := json.NewDecoder(request.Body).Decode(&settingsRequest); err != nil {
		http.Error(w, "invalid settings request", http.StatusBadRequest)
		return
	}

	responseID := settingsRequest.Data.ID
	if responseID == "" {
		responseID = settingsResponseID
	}

	response := settingsResponse{}
	response.Data.ID = responseID
	response.Data.Type = settingsResponseType
	response.Data.Attributes = settingsAttributes{
		CodeCoverage: true,
		ITREnabled:   true,
		EarlyFlakeDetection: earlyFlakeDetectionSettings{
			SlowTestRetries:        map[string]int{"5s": 10, "10s": 5, "30s": 3, "5m": 2},
			FaultySessionThreshold: 30,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(response)
}
