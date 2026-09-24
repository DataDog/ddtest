// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package intake

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/testoptimization/api"
)

const (
	settingsResponseID = "test-settings"

	testdriveCorrelationID = "ddtest-testdrive"
)

// Scenario configures exactly one local feature experiment. The zero value is reporting-only.
type Scenario struct {
	Feature string
	Module  string
	Suite   string
	Test    string
}

func newHandler() http.Handler { return scenarioHandler(Scenario{}) }

func scenarioHandler(scenario Scenario) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+constants.SettingsURLPath, func(w http.ResponseWriter, r *http.Request) { handleScenarioSettings(w, r, scenario) })
	mux.HandleFunc("POST "+constants.KnownTestsURLPath, handleKnownTests)
	mux.HandleFunc("POST "+constants.SkippableTestsURLPath, func(w http.ResponseWriter, r *http.Request) { handleSkippableTests(w, r, scenario) })
	mux.HandleFunc("POST "+constants.TestManagementTestsURLPath, func(w http.ResponseWriter, r *http.Request) { handleTestManagement(w, r, scenario) })
	mux.HandleFunc("POST "+constants.SearchCommitsURLPath, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"data": []any{}})
	})
	mux.HandleFunc("POST "+constants.SendPackFilesURLPath, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, request *http.Request) {
		// Unknown control APIs must not look like successfully collected events.
		for _, prefix := range []string{"/api/v2/git/", "/api/v2/libraries/", "/api/v2/ci/libraries/", "/api/v2/ci/tests/", "/api/v2/test/libraries/"} {
			if strings.HasPrefix(request.URL.Path, prefix) {
				http.Error(w, "unsupported local intake endpoint", http.StatusNotFound)
				return
			}
		}
		if request.Method == http.MethodPost {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, request)
	})
	return mux
}

func handleScenarioSettings(w http.ResponseWriter, request *http.Request, scenario Scenario) {
	var settingsRequest api.SettingsRequest
	if err := json.NewDecoder(request.Body).Decode(&settingsRequest); err != nil {
		http.Error(w, "invalid settings request", http.StatusBadRequest)
		return
	}

	responseID := settingsRequest.Data.ID
	if responseID == "" {
		responseID = settingsResponseID
	}

	response := api.SettingsResponse{}
	response.Data.ID = responseID
	response.Data.Type = constants.SettingsResponseType
	response.Data.Attributes = api.SettingsResponseData{
		CodeCoverage:            true,
		ItrEnabled:              true,
		TestsSkipping:           scenario.Feature == "skipping",
		FlakyTestRetriesEnabled: scenario.Feature == "auto-retries",
		KnownTestsEnabled:       scenario.Feature == "early-flake-detection",
		EarlyFlakeDetection: api.EarlyFlakeDetectionSettings{
			Enabled:                scenario.Feature == "early-flake-detection",
			SlowTestRetries:        api.SlowTestRetries{FiveS: 2, TenS: 2, ThirtyS: 2, FiveM: 2},
			FaultySessionThreshold: 100,
		},
		TestManagement: api.TestManagementSettings{
			Enabled:             scenario.Feature == "quarantine" || scenario.Feature == "disabled" || scenario.Feature == "attempt-to-fix",
			AttemptToFixRetries: 2,
		},
	}

	w.Header().Set("Content-Type", constants.ContentTypeJSON)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}

func handleKnownTests(w http.ResponseWriter, _ *http.Request) {
	// JavaScript tracers validate the module entry before enabling EFD. Return
	// an empty dataset for every supported runner, preserving Jest's retry loop.
	tests := make(map[string]any)
	for _, name := range []string{"jest", "mocha", "vitest", "playwright", "cucumber", "cypress", "pytest", "rspec", "minitest"} {
		tests[name] = map[string]any{}
	}
	writeJSON(w, map[string]any{
		"data": map[string]any{
			"attributes": map[string]any{
				"tests": tests,
			},
		},
	})
}

func handleSkippableTests(w http.ResponseWriter, _ *http.Request, scenario Scenario) {
	data := []any{}
	if scenario.Feature == "skipping" {
		data = append(data, map[string]any{"type": "suite", "attributes": map[string]any{"suite": scenario.Suite}})
	}
	writeJSON(w, map[string]any{
		"data": data,
		"meta": map[string]any{
			"correlation_id": testdriveCorrelationID,
			"coverage":       map[string]any{},
		},
	})
}

func handleTestManagement(w http.ResponseWriter, _ *http.Request, scenario Scenario) {
	modules := map[string]any{}
	property := map[string]string{"quarantine": "quarantined", "disabled": "disabled", "attempt-to-fix": "attempt_to_fix"}[scenario.Feature]
	if property != "" {
		modules[scenario.Module] = map[string]any{"suites": map[string]any{
			scenario.Suite: map[string]any{"tests": map[string]any{
				scenario.Test: map[string]any{"properties": map[string]any{property: true}},
			}},
		}}
	}
	writeJSON(w, map[string]any{"data": map[string]any{"attributes": map[string]any{"modules": modules}}})
}

func writeJSON(w http.ResponseWriter, response any) {
	w.Header().Set("Content-Type", constants.ContentTypeJSON)
	_ = json.NewEncoder(w).Encode(response)
}
