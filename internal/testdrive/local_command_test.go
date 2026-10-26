package testdrive

import (
	"bytes"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/stretchr/testify/require"
)

func TestLocalCommandDoesNotRequireRepositoryCIOnboarding(t *testing.T) {
	for _, tc := range []struct {
		name, status       string
		focused, checkOnly bool
		workflow           bool
	}{
		{"selected command", "PASSED", true, false, false},
		{"repository scope", "INCOMPLETE", false, false, false},
		{"configuration checks", "INCOMPLETE", true, true, false},
		{"onboarding workflow", "INCOMPLETE", true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			result := completeValidationFixture("pytest")
			result.FocusedCommand, result.CheckOnly = tc.focused, tc.checkOnly
			if tc.workflow {
				result.Workflow = &WorkflowEvidence{}
			}
			result.Runs[1].Coverage = &coverageSummary{Status: "observed", CoveredTests: 297, TotalTests: 297}
			result.Scope = &onboard.ValidationScope{Unresolved: []string{"unavailable CI wrapper"}}
			result.Configurations = []configurationResult{{TestCommand: onboard.TestCommand{Command: "tox -e py312"}, Status: "not exercised"}}
			result.CIRuntime = &onboard.RuntimeCheck{Status: "incompatible", Jobs: []onboard.RuntimeFinding{{Code: "missing_instrumentation", Status: "incompatible"}}}
			result.CISelection = &verdict{Status: "incompatible"}
			var output bytes.Buffer
			err := finishValidation(&output, root, result)
			report := readValidationReport(t, root)
			require.Equal(t, tc.status, report.Summary.Status)
			require.Equal(t, "incompatible", report.CIRuntime.Status)
			require.Equal(t, "not exercised", report.Configurations[0].Status)
			require.Zero(t, report.Summary.ConfigurationCoverage.Validated)
			if tc.status == "PASSED" {
				require.NoError(t, err)
				require.True(t, report.Success)
				require.Empty(t, report.Summary.BlockingChecks)
				require.Len(t, report.Summary.CIAdvisories, 4)
				require.Contains(t, output.String(), "CI advisory (outside selected command): CI runtime: incompatible")
				require.Contains(t, report.Summary.FinalResponse, "Local testdrive: PASSED (selected command)")
				require.NotContains(t, report.Summary.FinalResponse, "Onboarding validation: PASSED")
				require.NotContains(t, report.Summary.FinalResponse, "CI edits require")
				require.NotContains(t, report.Summary.ExecutedValidation.Reason, "static checks")
				require.Contains(t, report.Summary.OnboardingResponse, "Other repository configurations, CI execution and Datadog backend processing remain unvalidated")
			} else {
				require.Error(t, err)
				require.False(t, report.Success)
				require.Empty(t, report.Summary.CIAdvisories)
				require.Contains(t, strings.Join(report.Summary.BlockingChecks, "\n"), "CI runtime: incompatible")
			}
		})
	}
}

func TestLocalCommandStillRequiresAllExecutionEvidence(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*validationResult)
	}{
		{"missing pair", func(r *validationResult) { r.Runs = r.Runs[:1] }},
		{"failed feature", func(r *validationResult) { r.Features[0].Status = "failed" }},
		{"missing coverage", func(r *validationResult) { r.Runs[1].Coverage.Status = "not observed" }},
		{"cleanup failed", func(r *validationResult) { r.Cleanup.Status = "failed" }},
		{"runtime incompatible", func(r *validationResult) { r.Preflight.Verdict.Status = "incompatible" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := completeValidationFixture("pytest")
			result.FocusedCommand = true
			result.Runs[1].Coverage = &coverageSummary{Status: "observed", CoveredTests: 1, TotalTests: 1}
			result.CIRuntime = &onboard.RuntimeCheck{Status: "incompatible"}
			tc.edit(&result)
			root := t.TempDir()
			require.Error(t, finishValidation(&bytes.Buffer{}, root, result))
			report := readValidationReport(t, root)
			require.False(t, report.Success)
			require.Equal(t, "INCOMPLETE", report.Summary.Status)
		})
	}
}

func TestLocalCommandCIPreservationRemainsBlockingAfterBaseline(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		baseline     []string
		passed       bool
	}{
		{name: "initial inventory unavailable", status: "inconclusive", passed: true},
		{name: "baseline cannot be checked", status: "inconclusive", baseline: []string{"original row"}},
		{name: "matrix was removed", status: "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := completeValidationFixture("pytest")
			result.FocusedCommand = true
			result.CIBaseline = tc.baseline
			result.CIPreservation = &verdict{Status: tc.status, Reason: "CI inventory unavailable"}
			prepareValidationResult(t.TempDir(), &result)
			summary := summarizeValidation(result)
			require.Equal(t, tc.passed, result.Success)
			if tc.passed {
				require.Empty(t, summary.BlockingChecks)
				require.Contains(t, strings.Join(summary.CIAdvisories, "\n"), "CI preservation: inconclusive")
			} else {
				require.Contains(t, strings.Join(summary.BlockingChecks, "\n"), "CI preservation: "+tc.status)
			}
		})
	}
}
