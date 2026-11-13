// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import "github.com/DataDog/ddtest/internal/usagetelemetry"

// The recorded assertions also determine the verdict, so diagnostic expectations
// cannot drift from the checks performed on the owned probe.
type featureEvaluation struct {
	usagetelemetry.FeatureEvidence
}

func (e *featureEvaluation) equal(name string, expected, observed int) {
	e.Counts = append(e.Counts, usagetelemetry.FeatureCount{Name: name, Comparison: "equal", Expected: expected, Observed: &observed})
}

func (e *featureEvaluation) atLeast(name string, expected, observed int) {
	e.Counts = append(e.Counts, usagetelemetry.FeatureCount{Name: name, Comparison: "at_least", Expected: expected, Observed: &observed})
}

func (e *featureEvaluation) flag(name string, observed bool) {
	e.Flags = append(e.Flags, usagetelemetry.FeatureFlag{Name: name, Expected: true, Observed: observed})
}

func (e *featureEvaluation) passed() bool {
	for _, flag := range e.Flags {
		if flag.Observed != flag.Expected && e.FailureCategory == "" {
			e.FailureCategory = "management_tag_missing"
			switch flag.Name {
			case "skipped":
				e.FailureCategory = "skip_not_observed"
			case "native_repetition_evidence", "disabled_native_result":
				e.FailureCategory = "native_result_mismatch"
			}
		}
	}
	for _, count := range e.Counts {
		passed := count.Observed != nil && ((count.Comparison == "equal" && *count.Observed == count.Expected) ||
			(count.Comparison == "at_least" && *count.Observed >= count.Expected))
		if passed || e.FailureCategory != "" {
			continue
		}
		e.FailureCategory = "telemetry_count_mismatch"
		switch count.Name {
		case "matching_retries":
			if count.Comparison == "at_least" {
				e.FailureCategory = "retry_not_observed"
			}
		case "native_passed", "native_failed", "native_executed", "native_results", "native_counted_results":
			e.FailureCategory = "native_result_mismatch"
		case "executed_test_events", "total_test_events":
			e.FailureCategory = "unexpected_test_events"
		case "probe_body_executions":
			e.FailureCategory = "probe_execution_count_mismatch"
		case "exit_code":
			e.FailureCategory = "nonzero_exit"
		}
	}
	return e.FailureCategory == ""
}
