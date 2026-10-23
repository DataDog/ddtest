// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package intake

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/testoptimization/api"
)

const (
	settingsResponseID = "test-settings"
	ScenarioRetryCount = 2

	testdriveCorrelationID = "ddtest-testdrive"
)

// Scenario configures exactly one local feature experiment. The zero value preserves ordinary testdrive settings.
type Scenario struct {
	Feature string
	Module  string
	Suite   string
	Test    string
	// SourceFile is the repository-relative path used by Jest suite skipping.
	// Suite remains the reported identity used by test management.
	SourceFile string
	// SuiteSelector is an adapter's explicit suite identity when its tracer
	// consumes test.suite rather than a repository-relative source file.
	SuiteSelector string
}

func newHandler() http.Handler { return scenarioHandler(Scenario{}) }

func scenarioHandler(scenario Scenario) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+constants.SettingsURLPath, func(w http.ResponseWriter, r *http.Request) { handleScenarioSettings(w, r, scenario) })
	mux.HandleFunc("POST "+constants.KnownTestsURLPath, func(w http.ResponseWriter, r *http.Request) { handleKnownTests(w, r, scenario) })
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
			SlowTestRetries:        api.SlowTestRetries{FiveS: ScenarioRetryCount, TenS: ScenarioRetryCount, ThirtyS: ScenarioRetryCount, FiveM: ScenarioRetryCount},
			FaultySessionThreshold: 100,
		},
		TestManagement: api.TestManagementSettings{
			Enabled:             scenario.Feature == "quarantine" || scenario.Feature == "disabled" || scenario.Feature == "attempt-to-fix",
			AttemptToFixRetries: ScenarioRetryCount,
		},
	}

	if scenario.Feature == "" {
		response.Data.Attributes = api.SettingsResponseData{
			CodeCoverage:                true,
			CoverageReportUploadEnabled: true,
			// Testdrive runs the whole suite and disables git upload. Some tracers
			// wait for that upload before fetching skippable tests, so advertising
			// skipping here can prevent Jest from ever starting.
			TestsSkipping:           false,
			ItrEnabled:              true,
			ImpactedTestsEnabled:    true,
			FlakyTestRetriesEnabled: true,
			DIEnabled:               true,
			KnownTestsEnabled:       true,
			EarlyFlakeDetection: api.EarlyFlakeDetectionSettings{
				Enabled:                true,
				SlowTestRetries:        api.SlowTestRetries{FiveS: 1, TenS: 1, ThirtyS: 1, FiveM: 1},
				FaultySessionThreshold: 100,
			},
			TestManagement: api.TestManagementSettings{
				Enabled:             true,
				AttemptToFixRetries: 1,
			},
		}
	}
	w.Header().Set("Content-Type", constants.ContentTypeJSON)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}

func handleKnownTests(w http.ResponseWriter, _ *http.Request, scenario Scenario) {
	// JavaScript tracers validate the module entry before enabling EFD. Return
	// an empty dataset for every supported runner, preserving Jest's retry loop.
	tests := make(map[string]any)
	for _, name := range []string{"jest", "webdriverio", "mocha", "vitest", "playwright", "cucumber", "cypress", "pytest", "rspec", "minitest"} {
		tests[name] = map[string]any{}
	}
	if scenario.Module != "" {
		tests[scenario.Module] = map[string]any{}
	}
	writeJSON(w, map[string]any{
		"data": map[string]any{
			"attributes": map[string]any{
				"tests": tests,
			},
		},
	})
}

func handleSkippableTests(w http.ResponseWriter, r *http.Request, scenario Scenario) {
	var request struct {
		Data struct {
			Attributes struct {
				TestLevel string `json:"test_level"`
			}
		}
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, "invalid skippable request", http.StatusBadRequest)
		return
	}
	level := request.Data.Attributes.TestLevel
	if level != "" && level != "suite" && level != "test" {
		http.Error(w, "unsupported skipping level", http.StatusBadRequest)
		return
	}
	data := []any{}
	suite := scenario.SourceFile
	if scenario.SuiteSelector != "" {
		suite = scenario.SuiteSelector
	}
	if scenario.Feature == "skipping" && scenario.SourceFile != "" {
		if level == "test" && scenario.Suite != "" && scenario.Test != "" {
			data = append(data, map[string]any{"type": "test", "attributes": map[string]any{"suite": scenario.Suite, "name": scenario.Test}})
		} else if level != "test" {
			data = append(data, map[string]any{"type": "suite", "attributes": map[string]any{"suite": suite}})
		}
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
