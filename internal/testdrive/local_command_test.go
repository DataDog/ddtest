package testdrive

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
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

func TestLocalCommandCIPreservationSeparatesUnknownInventoryFromRemoval(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		baseline     []string
		passed       bool
	}{
		{name: "initial inventory unavailable", status: "inconclusive", passed: true},
		{name: "baseline cannot be checked", status: "inconclusive", baseline: []string{"original row"}, passed: true},
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

func TestPytestObjectMatrixPreservationScope(t *testing.T) {
	const workflow = "jobs:\n  test:\n    strategy:\n      matrix:\n%s    steps:\n      - run: python -m pytest\n"
	for _, baseline := range []bool{false, true} {
		for _, mode := range []string{"focused", "all", "check-only", "onboarding"} {
			t.Run(fmt.Sprintf("%s/baseline=%t", mode, baseline), func(t *testing.T) {
				root := t.TempDir()
				path := filepath.Join(root, ".github/workflows/test.yml")
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
				var original []string
				if baseline {
					requireWriteFile(t, path, fmt.Sprintf(workflow, "        python: ['3.12', '3.11']\n"))
					seed := completeValidationFixture("pytest")
					seed.FocusedCommand = true
					require.NoError(t, finishValidation(&bytes.Buffer{}, root, seed))
					original = readValidationReport(t, root).CIBaseline
					require.Len(t, original, 2)
				}
				requireWriteFile(t, path, fmt.Sprintf(workflow, "        config:\n          - {python: '3.12'}\n"))
				scope, err := onboard.DiscoverValidationScope(root, "pytest")
				require.NoError(t, err)
				require.Contains(t, strings.Join(scope.Unresolved, "\n"), "non-scalar value")
				result := completeValidationFixture("pytest")
				result.Scope = &scope
				result.FocusedCommand = mode != "all"
				result.CheckOnly = mode == "check-only"
				if mode == "onboarding" {
					result.Workflow = &WorkflowEvidence{}
				}
				err = finishValidation(&bytes.Buffer{}, root, result)
				report := readValidationReport(t, root)
				require.Equal(t, "inconclusive", report.CIPreservation.Status)
				require.Contains(t, report.CIPreservation.Reason, "non-scalar value")
				require.Equal(t, original, report.CIBaseline, "an inconclusive check must retain the original baseline")
				if mode != "focused" {
					require.Error(t, err)
					require.False(t, report.Success)
					require.Contains(t, strings.Join(report.Summary.BlockingChecks, "\n"), "CI preservation: inconclusive")
					return
				}
				require.NoError(t, err)
				require.True(t, report.Success)
				require.Equal(t, "PASSED", report.Summary.SelectedValidation.Status)
				require.Empty(t, report.Summary.BlockingChecks)
				require.Contains(t, strings.Join(report.Summary.CIAdvisories, "\n"), "CI preservation: inconclusive")
				if baseline {
					// Once CI is readable, a removed row must still fail against
					// the baseline retained through the inconclusive local run.
					requireWriteFile(t, path, fmt.Sprintf(workflow, "        python: ['3.12']\n"))
					result.Scope = nil
					require.Error(t, finishValidation(&bytes.Buffer{}, root, result))
					report = readValidationReport(t, root)
					require.Equal(t, "failed", report.CIPreservation.Status)
					require.Equal(t, 1, report.CIPreservation.DifferenceCount)
					require.False(t, report.Success)
					require.Contains(t, strings.Join(report.Summary.BlockingChecks, "\n"), "CI preservation: failed")
				}
			})
		}
	}
}
