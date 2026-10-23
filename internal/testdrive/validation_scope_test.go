// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/stretchr/testify/require"
)

func focusedValidationFixture(name string) validationResult {
	exit := 0
	result := validationResult{Framework: name, FocusedCommand: true, ChecksPassed: true, HTMLReportCurrent: true,
		Preflight: &jestPreflight{Verdict: verdict{Status: "compatible"}}, CIRuntime: &onboard.RuntimeCheck{Status: "compatible"},
		Compatibility: verdict{Status: "compatible"}, Cleanup: &verdict{Status: "passed"},
		Scope: &onboard.ValidationScope{}, Configurations: []configurationResult{
			{TestCommand: onboard.TestCommand{Command: "npm test"}, Status: "passed", PairedExecution: true, ExecutionAttempted: true, Compatibility: verdict{Status: "compatible"}},
			{TestCommand: onboard.TestCommand{Command: "npm run other", Resolution: "npm run other -> runner other"}, Status: "not exercised", Compatibility: verdict{Status: "not exercised"}},
		},
		Runs: []runSummary{{Role: roleBaseline, Name: "baseline", Command: "npm test", ExitCode: &exit}, {Role: roleReporting, Name: "reporting-only", Command: "npm test", Instrumented: true, ExitCode: &exit}},
	}
	for _, policy := range featurePolicies(name) {
		status := "passed"
		if policy.Status != "required" {
			status = policy.Status
		}
		result.Features = append(result.Features, featureResult{Name: policy.Name, Status: status, Reason: policy.Reason})
	}

	return result
}

func completeValidationFixture(name string) validationResult {
	result := focusedValidationFixture(name)
	result.FocusedCommand, result.Scope, result.Configurations = false, nil, nil
	return result
}

func TestConfigurationCoveragePrintsTheCompleteStatusPartition(t *testing.T) {
	result := focusedValidationFixture("jest")
	result.Configurations = append(result.Configurations, configurationResult{Status: "incomplete"}, configurationResult{Status: "blocked"})
	var output bytes.Buffer
	require.NoError(t, finishValidation(&output, t.TempDir(), result))
	coverage, _ := summarizeConfigurationCoverage(result)
	require.Equal(t, coverage.Discovered, coverage.Validated+coverage.Blocked+coverage.NotExercised+coverage.Incomplete)
	require.Equal(t, 1, coverage.Incomplete)
	require.Contains(t, output.String(), "Configuration coverage: 1/4 validated; 1 executed, 1 paired, 1 blocked, 1 not exercised, 1 incomplete.")
	output.Reset()
	result.Configurations = nil
	require.NoError(t, finishValidation(&output, t.TempDir(), result))
	require.NotContains(t, output.String(), "0/0 validated")
}

func TestFocusedJestValidationKeepsUnvalidatedRepositoryScope(t *testing.T) {
	for _, name := range []string{"jest"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			result := focusedValidationFixture(name)
			var output bytes.Buffer
			require.NoError(t, finishValidation(&output, root, result))
			report := readValidationReport(t, root)
			require.True(t, report.Success)
			require.Equal(t, "INCOMPLETE", report.Summary.Status)
			require.Equal(t, "PASSED", report.Summary.SelectedValidation.Status)
			require.Equal(t, "PASSED", report.Summary.ExecutedValidation.Status)
			require.Equal(t, 2, report.Summary.ConfigurationCoverage.Discovered)
			require.Equal(t, 1, report.Summary.ConfigurationCoverage.NotExercised)
			require.Contains(t, strings.Join(report.Summary.BlockingChecks, "\n"), "configuration npm run other: not exercised")
		})
	}
}

func TestSelectedValidationDoesNotCertifyMissingOrStaleEvidence(t *testing.T) {
	cases := []struct {
		name string
		edit func(*validationResult)
		want string
	}{
		{"check only", func(r *validationResult) { r.CheckOnly = true }, "NOT EXERCISED"},
		{"missing instrumented run", func(r *validationResult) { r.Runs = r.Runs[:1] }, "INCOMPLETE"},
		{"missing feature", func(r *validationResult) { r.Features = r.Features[:len(r.Features)-1] }, "INCOMPLETE"},
		{"incomplete second project", func(r *validationResult) {
			r.Features = append(r.Features, featureResult{Project: "other", Name: "auto-retries", Status: "passed"})
		}, "INCOMPLETE"},
		{"CI blocked", func(r *validationResult) { r.ChecksPassed = false }, "INCOMPLETE"},
		{"cleanup failed", func(r *validationResult) { r.Cleanup.Status = "failed" }, "INCOMPLETE"},
		{"HTML stale", func(r *validationResult) { r.HTMLReportCurrent = false }, "INCOMPLETE"},
		{"historical execution", func(r *validationResult) { r.LastExecution = &validationEvidence{Current: false} }, "INCOMPLETE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := focusedValidationFixture("jest")
			tc.edit(&result)
			require.Equal(t, tc.want, selectedValidation(result).Status)
		})
	}
}

func TestValidationRequiresCompleteEvidence(t *testing.T) {
	cases := []struct {
		name    string
		edit    func(*validationResult)
		local   bool
		success bool
	}{
		{"complete", func(*validationResult) {}, true, true},
		{"missing feature", func(r *validationResult) { r.Features = r.Features[:len(r.Features)-1] }, false, false},
		{"partial project", func(r *validationResult) {
			r.Features = append(r.Features, featureResult{Project: "other", Name: "auto-retries", Status: "passed"})
		}, false, false},
		{"unrecorded cleanup", func(r *validationResult) { r.Cleanup = nil }, true, false},
		{"failed cleanup", func(r *validationResult) { r.Cleanup.Status = "failed" }, true, false},
		{"missing HTML", func(r *validationResult) { r.HTMLReportCurrent = false }, true, false},
		{"missing baseline", func(r *validationResult) { r.Runs = r.Runs[1:] }, false, false},
	}
	for _, framework := range []string{"jest"} {
		for _, tc := range cases {
			t.Run(framework+"/"+tc.name, func(t *testing.T) {
				result := completeValidationFixture(framework)
				tc.edit(&result)
				root := t.TempDir()
				err := finishValidation(&bytes.Buffer{}, root, result)
				require.Equal(t, tc.success, err == nil)
				report := readValidationReport(t, root)
				require.Equal(t, tc.success, report.Success)
				require.Equal(t, tc.local, report.LocalSuccess)
				if !tc.success {
					require.Equal(t, "INCOMPLETE", report.Summary.Status)
					require.NotEmpty(t, report.Summary.BlockingChecks)
				}
			})
		}
	}
}

func TestExecutedCoverageSeparatesUnavailableAndInterruptedConfigurations(t *testing.T) {
	result := validationResult{Framework: "jest", Configurations: []configurationResult{
		{Status: "passed", PairedExecution: true, ExecutionAttempted: true},
		{Status: "blocked", Error: "Firefox unavailable"},
		{Status: "not exercised", Error: "Windows required"},
	}}
	coverage, execution := summarizeConfigurationCoverage(result)
	require.Equal(t, &configurationCoverage{Discovered: 3, Executed: 1, Paired: 1, Validated: 1, Blocked: 1, NotExercised: 1}, coverage)
	require.Equal(t, "PASSED", execution.Status)
	require.Equal(t, 1, execution.Configurations)
	require.Equal(t, 1, execution.Passed)
	result.Configurations = append(result.Configurations, configurationResult{Status: "incomplete", ExecutionAttempted: true, Error: "instrumented command interrupted"})
	coverage, execution = summarizeConfigurationCoverage(result)
	require.Equal(t, 2, coverage.Executed)
	require.Equal(t, 1, coverage.Incomplete)
	require.Equal(t, "INCOMPLETE", execution.Status)
	require.Equal(t, 2, execution.Configurations)
	require.Equal(t, 1, execution.Passed)
	result.CheckOnly = true
	_, execution = summarizeConfigurationCoverage(result)
	require.Equal(t, "NOT EXERCISED", execution.Status)
	require.Zero(t, execution.Configurations)
}

func TestIncompleteAllScopeRemainsAnErrorWhenExecutedConfigurationsPass(t *testing.T) {
	result := focusedValidationFixture("jest")
	result.FocusedCommand = false
	result.Configurations[1].Status = "blocked"
	var output bytes.Buffer
	require.Error(t, finishValidation(&output, t.TempDir(), result))
	report := output.String()
	require.Contains(t, report, "Executed Jest validations: PASSED")
	require.Contains(t, report, "Validation verdict: INCOMPLETE")
}

func TestCheckOnlyCoverageCountsDiscoveredCommands(t *testing.T) {
	scope := onboard.ValidationScope{Commands: []onboard.TestCommand{{Directory: ".", Command: "vitest run a"}, {Directory: ".", Command: "vitest run b"}}}
	coverage, _ := summarizeConfigurationCoverage(validationResult{Framework: "vitest", CheckOnly: true, Scope: &scope})
	require.Equal(t, 2, coverage.Discovered, "check-only reports the same discovered count as a full run")
	require.Equal(t, 2, coverage.NotExercised)
	require.Zero(t, coverage.Validated)
}

func TestNoHTMLBlockerBeforeAnyTestRan(t *testing.T) {
	summary := summarizeValidation(validationResult{Framework: "vitest", Error: "Vitest watch mode is not supported by testdrive"})
	require.NotContains(t, summary.BlockingChecks, "no current HTML findings report")
}
