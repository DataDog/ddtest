// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package usagetelemetry

const (
	maxFeatureCounts = 10
	maxFeatureFlags  = 3
)

// FeatureEvidence contains the owned probe's assertions, never customer text.
// Unavailable measurements are omitted or null, rather than fabricated zeros.
type FeatureEvidence struct {
	FailureCategory string         `json:"failure_category,omitempty"`
	Counts          []FeatureCount `json:"counts,omitempty"`
	Flags           []FeatureFlag  `json:"flags,omitempty"`
}

type FeatureCount struct {
	Name       string `json:"name"`
	Comparison string `json:"comparison"`
	Expected   int    `json:"expected"`
	Observed   *int   `json:"observed"`
}

type FeatureFlag struct {
	Name     string `json:"name"`
	Expected bool   `json:"expected"`
	Observed bool   `json:"observed"`
}

func (e FeatureEvidence) sanitized() *FeatureEvidence {
	e.FailureCategory = diagnosticEnum(e.FailureCategory, "", "retry_not_observed", "telemetry_count_mismatch",
		"native_result_mismatch", "management_tag_missing", "skip_not_observed", "unexpected_test_events",
		"probe_execution_count_mismatch", "nonzero_exit", "source_file_missing", "timed_out",
		"runner_results_unavailable", "suite_errors", "configuration_errors", "unexpected_tests_selected",
		"skippable_request_missing", "controls_unavailable", "not_supported", "capability_not_advertised", "not_exercised")
	counts, flags := e.Counts, e.Flags
	e.Counts, e.Flags = nil, nil
	for _, count := range counts[:min(len(counts), maxFeatureCounts)] {
		count.Name = diagnosticEnum(count.Name, "matching_retries", "telemetry_attempts", "telemetry_passed", "telemetry_failed",
			"native_passed", "native_failed", "native_executed", "native_results", "native_counted_results",
			"executed_test_events", "total_test_events", "probe_body_executions", "exit_code")
		count.Comparison = diagnosticEnum(count.Comparison, "equal", "at_least")
		count.Expected = featureEvidenceNumber(count.Name, count.Expected)
		if count.Observed != nil {
			observed := featureEvidenceNumber(count.Name, *count.Observed)
			count.Observed = &observed
		}
		e.Counts = append(e.Counts, count)
	}
	for _, flag := range flags[:min(len(flags), maxFeatureFlags)] {
		flag.Name = diagnosticEnum(flag.Name, "quarantined", "disabled", "attempt_to_fix", "skipped",
			"native_repetition_evidence", "disabled_native_result", "planner_skipped")
		e.Flags = append(e.Flags, flag)
	}
	return &e
}

func featureEvidenceNumber(name string, value int) int {
	if name == "exit_code" {
		return max(-1, min(value, 255))
	}
	return diagnosticCount(value)
}
