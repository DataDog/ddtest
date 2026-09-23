// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package api

import "github.com/DataDog/ddtest/internal/settings"

type (
	SettingsRequest struct {
		Data SettingsRequestHeader `json:"data"`
	}

	SettingsRequestHeader struct {
		ID         string              `json:"id"`
		Type       string              `json:"type"`
		Attributes SettingsRequestData `json:"attributes"`
	}

	SettingsRequestData struct {
		Service        string                     `json:"service,omitempty"`
		Env            string                     `json:"env,omitempty"`
		RepositoryURL  string                     `json:"repository_url,omitempty"`
		Branch         string                     `json:"branch,omitempty"`
		Sha            string                     `json:"sha,omitempty"`
		TestLevel      settings.TestSkippingLevel `json:"test_level,omitempty"`
		Configurations testConfigurations         `json:"configurations,omitempty"`
	}

	SettingsResponse struct {
		Data struct {
			ID         string               `json:"id"`
			Type       string               `json:"type"`
			Attributes SettingsResponseData `json:"attributes"`
		} `json:"data,omitempty"`
	}

	SettingsResponseData struct {
		CodeCoverage                bool                        `json:"code_coverage"`
		CoverageReportUploadEnabled bool                        `json:"coverage_report_upload_enabled"`
		ImpactedTestsEnabled        bool                        `json:"impacted_tests_enabled"`
		DIEnabled                   bool                        `json:"di_enabled"`
		EarlyFlakeDetection         EarlyFlakeDetectionSettings `json:"early_flake_detection"`
		FlakyTestRetriesEnabled     bool                        `json:"flaky_test_retries_enabled"`
		ItrEnabled                  bool                        `json:"itr_enabled"`
		RequireGit                  bool                        `json:"require_git"`
		TestsSkipping               bool                        `json:"tests_skipping"`
		KnownTestsEnabled           bool                        `json:"known_tests_enabled"`
		TestManagement              TestManagementSettings      `json:"test_management"`
	}
)

type EarlyFlakeDetectionSettings struct {
	Enabled                bool            `json:"enabled"`
	SlowTestRetries        SlowTestRetries `json:"slow_test_retries"`
	FaultySessionThreshold int             `json:"faulty_session_threshold"`
}

type SlowTestRetries struct {
	FiveS   int `json:"5s"`
	TenS    int `json:"10s"`
	ThirtyS int `json:"30s"`
	FiveM   int `json:"5m"`
}

type TestManagementSettings struct {
	Enabled             bool `json:"enabled"`
	AttemptToFixRetries int  `json:"attempt_to_fix_retries"`
}
