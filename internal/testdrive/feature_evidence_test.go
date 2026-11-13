package testdrive

import (
	"encoding/json"
	"testing"

	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/DataDog/ddtest/internal/usagetelemetry"
	"github.com/stretchr/testify/require"
)

func featureEvidenceFixture(feature string) (validationRun, intake.Test) {
	identity := intake.Test{Module: "jest", Suite: "PRIVATE_SUITE", SourceFile: "PRIVATE_PATH", Name: "PRIVATE_TEST"}
	event := func(status, retry string) intake.Event {
		tags := map[string]string{"test.module": identity.Module, "test.suite": identity.Suite, "test.name": identity.Name, "test.status": status}
		if retry != "" {
			tags["test.is_retry"], tags["test.retry_reason"] = "true", retry
		}
		return intake.Event{Type: "test", Tags: tags}
	}
	run := validationRun{framework: "jest", Tests: []jestTest{{Name: identity.Name, Status: "passed"}}}
	switch feature {
	case "auto-retries":
		run.Facts.Events = []intake.Event{event("fail", ""), event("pass", "auto_test_retry")}
	case "early-flake-detection", "attempt-to-fix":
		reason := "early_flake_detection"
		if feature == "attempt-to-fix" {
			reason = "attempt_to_fix"
		}
		run.Facts.Events = []intake.Event{event("pass", ""), event("pass", reason)}
		run.Facts.Events[0].Tags["test.test_management.is_attempt_to_fix"] = "true"
		run.Tests = append(run.Tests, run.Tests[0])
	case "skipping":
		run.Tests = nil
		skipped := event("skip", "")
		skipped.Type = "test_suite_end"
		skipped.Tags["test.skipped_by_itr"] = "true"
		run.Facts.Events = []intake.Event{skipped}
	case "quarantine":
		run.Tests[0].Status = "failed"
		run.Facts.Events = []intake.Event{event("fail", "")}
		run.Facts.Events[0].Tags["test.test_management.is_quarantined"] = "true"
	case "disabled":
		run.Tests[0].Status = "skipped"
		run.Facts.Events = []intake.Event{event("skip", "")}
		run.Facts.Events[0].Tags["test.test_management.is_test_disabled"] = "true"
	}
	for _, event := range run.Facts.Events {
		if event.Type == "test" {
			run.Facts.TestEventCount++
		}
	}
	return run, identity
}

func evidenceCount(t *testing.T, evidence *usagetelemetry.FeatureEvidence, name string) usagetelemetry.FeatureCount {
	t.Helper()
	require.NotNil(t, evidence)
	for _, count := range evidence.Counts {
		if count.Name == name {
			return count
		}
	}
	t.Fatalf("missing evidence count %s", name)
	return usagetelemetry.FeatureCount{}
}

func TestFeatureEvidenceIncludesExpectationsForEveryControlledFeature(t *testing.T) {
	for _, feature := range []string{"auto-retries", "early-flake-detection", "skipping", "quarantine", "disabled", "attempt-to-fix"} {
		t.Run(feature, func(t *testing.T) {
			run, identity := featureEvidenceFixture(feature)
			result := evaluateFeature(feature, run, identity)
			require.Equal(t, "passed", result.Status)
			require.NotNil(t, result.Evidence)
			require.Empty(t, result.Evidence.FailureCategory)
			for _, count := range result.Evidence.Counts {
				require.NotNil(t, count.Observed)
				if count.Comparison == "equal" {
					require.Equal(t, count.Expected, *count.Observed, count.Name)
				} else {
					require.GreaterOrEqual(t, *count.Observed, count.Expected, count.Name)
				}
			}
			for _, flag := range result.Evidence.Flags {
				require.Equal(t, flag.Expected, flag.Observed, flag.Name)
			}
			exit := evidenceCount(t, result.Evidence, "exit_code")
			require.Zero(t, exit.Expected)
			require.Zero(t, *exit.Observed)
			data, err := json.Marshal(result.Evidence)
			require.NoError(t, err)
			require.NotContains(t, string(data), "PRIVATE_")

			run.ExitCode = 1
			failed := evaluateFeature(feature, run, identity)
			require.Equal(t, "failed", failed.Status)
			require.Equal(t, "nonzero_exit", failed.Evidence.FailureCategory)
			require.Equal(t, 1, *evidenceCount(t, failed.Evidence, "exit_code").Observed)
		})
	}
}

func TestFeatureEvidenceExplainsMissingBehavior(t *testing.T) {
	for _, tc := range []struct{ feature, category string }{
		{"auto-retries", "retry_not_observed"}, {"early-flake-detection", "retry_not_observed"},
		{"skipping", "skip_not_observed"}, {"quarantine", "management_tag_missing"},
		{"disabled", "management_tag_missing"}, {"attempt-to-fix", "management_tag_missing"},
	} {
		t.Run(tc.feature, func(t *testing.T) {
			run, identity := featureEvidenceFixture(tc.feature)
			switch tc.feature {
			case "auto-retries":
				run.Facts.Events = run.Facts.Events[:1]
				run.Facts.TestEventCount = 1
			case "early-flake-detection":
				delete(run.Facts.Events[1].Tags, "test.is_retry")
			case "skipping":
				delete(run.Facts.Events[0].Tags, "test.skipped_by_itr")
			case "quarantine":
				delete(run.Facts.Events[0].Tags, "test.test_management.is_quarantined")
			case "disabled":
				delete(run.Facts.Events[0].Tags, "test.test_management.is_test_disabled")
			case "attempt-to-fix":
				delete(run.Facts.Events[0].Tags, "test.test_management.is_attempt_to_fix")
			}
			result := evaluateFeature(tc.feature, run, identity)
			require.Equal(t, "failed", result.Status)
			require.Equal(t, tc.category, result.Evidence.FailureCategory)
			if tc.feature == "auto-retries" {
				retries := evidenceCount(t, result.Evidence, "matching_retries")
				require.Equal(t, "at_least", retries.Comparison)
				require.Equal(t, 1, retries.Expected)
				require.Zero(t, *retries.Observed)
				attempts := evidenceCount(t, result.Evidence, "telemetry_attempts")
				require.Equal(t, 2, attempts.Expected)
				require.Equal(t, 1, *attempts.Observed)
			}
		})
	}
}

func TestFeatureEvidenceKeepsMissingBodyCountsUnknown(t *testing.T) {
	run, identity := featureEvidenceFixture("early-flake-detection")
	run.framework = "webdriverio"
	result := evaluateFeature("early-flake-detection", run, identity)
	require.Equal(t, "failed", result.Status)
	require.Equal(t, "probe_execution_count_mismatch", result.Evidence.FailureCategory)
	count := evidenceCount(t, result.Evidence, "probe_body_executions")
	require.Equal(t, 2, count.Expected)
	require.Nil(t, count.Observed)
	observed := 1
	run.ProbeExecutions = &observed
	result = evaluateFeature("early-flake-detection", run, identity)
	require.Equal(t, 1, *evidenceCount(t, result.Evidence, "probe_body_executions").Observed)
	observed = 2
	require.Equal(t, "passed", evaluateFeature("early-flake-detection", run, identity).Status)
}

func TestFeatureEvidenceDoesNotInventObservationsForUnavailableResults(t *testing.T) {
	for _, category := range []string{"timed_out", "runner_results_unavailable", "suite_errors", "configuration_errors", "unexpected_tests_selected", "source_file_missing"} {
		t.Run(category, func(t *testing.T) {
			run, identity := featureEvidenceFixture("auto-retries")
			feature := "auto-retries"
			switch category {
			case "timed_out":
				run.Timeout = "PRIVATE_TIMEOUT"
			case "runner_results_unavailable":
				run.ResultError = "PRIVATE_ERROR"
			case "suite_errors":
				run.SuiteErrors = []string{"PRIVATE_ERROR"}
			case "configuration_errors":
				run.Facts.ConfigurationErrors = []string{"PRIVATE_ERROR"}
			case "unexpected_tests_selected":
				run.Tests[0].Name = "PRIVATE_OTHER_TEST"
			case "source_file_missing":
				feature, identity.SourceFile = "skipping", ""
			}
			result := evaluateFeature(feature, run, identity)
			require.Equal(t, "inconclusive", result.Status)
			require.Equal(t, category, result.Evidence.FailureCategory)
			require.Empty(t, result.Evidence.Counts)
			require.Empty(t, result.Evidence.Flags)
			data, err := json.Marshal(result.Evidence)
			require.NoError(t, err)
			require.NotContains(t, string(data), "PRIVATE_")
		})
	}
	unavailable := &validationResult{Framework: "jest"}
	unavailableFeatures(unavailable, "PRIVATE_CONTROL_ERROR")
	require.Len(t, unavailable.Features, 6)
	for _, feature := range unavailable.Features {
		require.Equal(t, "controls_unavailable", feature.Evidence.FailureCategory)
		require.Empty(t, feature.Evidence.Counts)
	}
}

func TestFeatureEvidenceSurvivesKnownTracerLimitations(t *testing.T) {
	run, identity := featureEvidenceFixture("disabled")
	delete(run.Facts.Events[0].Tags, "test.test_management.is_test_disabled")
	failed := evaluateFeature("disabled", run, identity)
	check := wdioJasmineCheck("5.9.0")
	limited := withTracerLimitation(&validationResult{Framework: "webdriverio", Preflight: &check}, failed)
	require.Equal(t, "known limitation", limited.Status)
	require.Equal(t, tracerLimitation, limited.Code)
	require.Equal(t, failed.Code, limited.ObservedCode)
	require.Same(t, failed.Evidence, limited.Evidence)
}
