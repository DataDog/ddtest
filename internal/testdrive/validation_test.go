// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestReportSizeDoesNotScaleWithTestEvents(t *testing.T) {
	root := t.TempDir()
	run := validationRun{Name: "reporting-only", Command: "npm test -- --json", Instrumented: true, ExitCode: 1}
	for range 10000 {
		run.Tests = append(run.Tests, jestTest{Name: "raw-test-name", Status: "failed", Failure: "raw-test-failure"})
		run.SuiteErrors = append(run.SuiteErrors, "raw-suite-error")
		run.Facts.Tests = append(run.Facts.Tests, intake.Test{Name: "raw-telemetry-test"})
		run.Facts.Events = append(run.Facts.Events, intake.Event{Type: "test", Tags: map[string]string{"error.stack": "raw-event-stack"}})
	}
	run.Facts.TestEventCount = len(run.Facts.Events)
	result := validationResult{Compatibility: verdict{Status: "suspected regression"}, Runs: []runSummary{run.summary()}}
	difference := strings.Repeat("difference", 1000)
	for range 10000 {
		result.Compatibility.Differences = append(result.Compatibility.Differences, difference)
	}
	require.Error(t, finishValidation(&bytes.Buffer{}, root, result))
	data, err := os.ReadFile(validationPath(root))
	require.NoError(t, err)
	require.Less(t, len(data), 13000)
	for _, absent := range []string{"raw-test-name", "raw-test-failure", "raw-suite-error", "raw-telemetry-test", "raw-event-stack", `"telemetry"`, `"output"`, `"tests"`} {
		require.NotContains(t, string(data), absent)
	}
	var report validationResult
	require.NoError(t, json.Unmarshal(data, &report))
	require.False(t, report.Success)
	require.Equal(t, 10000, report.Compatibility.DifferenceCount)
	require.Len(t, report.Compatibility.Differences, 10)
	require.Equal(t, run.Command, report.Runs[0].Command)
	require.Equal(t, 1, *report.Runs[0].ExitCode)
	require.Equal(t, 10000, report.Runs[0].TestCounts.Failed)
	require.Equal(t, 10000, report.Runs[0].SuiteErrorCount)
	require.Equal(t, 10000, report.Runs[0].TestEventCount)
}

func TestReportSuccessReflectsValidationAndBoundsSetupErrors(t *testing.T) {
	for _, tc := range []struct {
		name          string
		compatibility string
		feature       string
		err           string
		success       bool
	}{
		{"matching failing tests", "compatible", "passed", "", true},
		{"regression", "suspected regression", "passed", "", false},
		{"inconclusive", "inconclusive", "passed", "", false},
		{"failed feature", "compatible", "failed", "", false},
		{"unvalidated feature", "compatible", "unvalidated", "", false},
		{"setup failure", "compatible", "passed", strings.Repeat("install output ", 10000), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			result := validationResult{Error: tc.err, Compatibility: verdict{Status: tc.compatibility},
				Features: []featureResult{{Name: "auto-retries", Status: tc.feature}},
				Runs:     []runSummary{(validationRun{Name: "baseline", Command: "npm test", ExitCode: 1}).summary()}}
			err := finishValidation(&bytes.Buffer{}, root, result)
			require.Equal(t, tc.success, err == nil)
			data, err := os.ReadFile(validationPath(root))
			require.NoError(t, err)
			var report validationResult
			require.NoError(t, json.Unmarshal(data, &report))
			require.Equal(t, tc.success, report.Success)
			require.Equal(t, root, report.WorkingDirectory)
			require.Less(t, len(report.Error), 1100)
		})
	}
}

func TestRunSummaryDistinguishesUnstartedCommandsAndProbeModes(t *testing.T) {
	unstarted := (validationRun{Name: "baseline"}).summary()
	require.Nil(t, unstarted.ExitCode)
	require.Empty(t, unstarted.Command)
	probe := (validationRun{Name: "auto-retries", Command: "npx jest --runTestsByPath probe.test.js",
		Instrumented: true, ProbeMode: "fail-once", Tests: []jestTest{{Status: "passed"}, {Status: "pending"}}}).summary()
	require.Equal(t, "fail-once", probe.ProbeMode)
	require.True(t, probe.Instrumented)
	require.Equal(t, 0, *probe.ExitCode)
	require.Equal(t, testCounts{Passed: 1, Skipped: 1}, probe.TestCounts)
}

func TestCIRuntimeFailurePreventsOverallSuccess(t *testing.T) {
	for _, status := range []string{"compatible", "incompatible", "inconclusive", "not applicable"} {
		t.Run(status, func(t *testing.T) {
			root := t.TempDir()
			result := validationResult{Compatibility: verdict{Status: "compatible"}, Features: []featureResult{{Name: "auto-retries", Status: "passed"}}, CIRuntime: &onboard.RuntimeCheck{Status: status, Reason: "CI runtime result"}}
			var output bytes.Buffer
			err := finishValidation(&output, root, result)
			wantSuccess := status == "compatible" || status == "not applicable"
			require.Equal(t, wantSuccess, err == nil)
			data, readErr := os.ReadFile(validationPath(root))
			require.NoError(t, readErr)
			var report validationResult
			require.NoError(t, json.Unmarshal(data, &report))
			require.Equal(t, wantSuccess, report.Success)
			require.True(t, report.LocalSuccess)
			require.Equal(t, "compatible", report.Compatibility.Status)
			require.Equal(t, status, report.CIRuntime.Status)
			require.Contains(t, output.String(), "CI runtime compatibility: "+status)
		})
	}
}

func TestCIRuntimeReportBoundsDiagnostics(t *testing.T) {
	root := t.TempDir()
	text := strings.Repeat("dynamic workflow input ", 10000)
	check := &onboard.RuntimeCheck{Status: "inconclusive", Reason: text, Jobs: []onboard.RuntimeFinding{{Status: "inconclusive", Node: text, Tracer: text, Requirement: text, Reason: text}}}
	require.Error(t, finishValidation(&bytes.Buffer{}, root, validationResult{CIRuntime: check}))
	data, err := os.ReadFile(validationPath(root))
	require.NoError(t, err)
	require.Less(t, len(data), 7000)
	require.Equal(t, text, check.Reason)
	require.Equal(t, text, check.Jobs[0].Reason)
}

func TestUnresolvedCIScriptKeepsLocalSuccessButPreventsOverallSuccess(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"coverage":"node scripts/run-ci.js"}}`), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".github/workflows/test.yml"), []byte("jobs:\n  tests:\n    steps:\n      - run: npm run coverage\n"), 0644))
	check := onboard.CheckCIRuntimes(t.Context(), root)
	require.Equal(t, "inconclusive", check.Status)
	selection := compareCISelection(check, "6.17.0")
	require.NotNil(t, selection)
	require.Equal(t, "inconclusive", selection.Status)
	result := validationResult{Compatibility: verdict{Status: "compatible"}, Features: []featureResult{{Name: "auto-retries", Status: "passed"}}, CIRuntime: &check, CISelection: selection}
	require.Error(t, finishValidation(&bytes.Buffer{}, root, result))
	data, err := os.ReadFile(validationPath(root))
	require.NoError(t, err)
	var report validationResult
	require.NoError(t, json.Unmarshal(data, &report))
	require.True(t, report.LocalSuccess)
	require.False(t, report.Success)
	require.False(t, report.ChecksPassed)
	require.Equal(t, "npm run coverage", report.CIRuntime.Jobs[0].Command)
}

func TestSeparateCIReviewDoesNotChangeJestTracerAgreement(t *testing.T) {
	check := onboard.RuntimeCheck{
		Status: "compatible",
		Jobs:   []onboard.RuntimeFinding{{Status: "compatible", Tracer: "dd-trace@6.16.0"}},
		Review: []onboard.RuntimeFinding{{Status: "not checked", Command: "yarn build", Reason: strings.Repeat("review ", 1000)}},
	}
	agreement := compareCISelection(check, "6.16.0")
	require.Equal(t, "compatible", agreement.Status)
	root := t.TempDir()
	var output bytes.Buffer
	result := validationResult{CIRuntime: &check, CISelection: agreement, Compatibility: verdict{Status: "compatible"}}
	require.NoError(t, finishValidation(&output, root, result))
	data, err := os.ReadFile(validationPath(root))
	require.NoError(t, err)
	var report validationResult
	require.NoError(t, json.Unmarshal(data, &report))
	require.Len(t, report.CIRuntime.Review, 1)
	require.Less(t, len(report.CIRuntime.Review[0].Reason), 1100)
	require.Contains(t, output.String(), "Review separately")
	require.Contains(t, output.String(), "Keep this JSON report after cleanup")
}
