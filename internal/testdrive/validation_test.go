// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"encoding/json"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
	"os"
	"strings"
	"testing"
)

func TestCIRuntimeReportBoundsDiagnostics(t *testing.T) {
	root := t.TempDir()
	text := strings.Repeat("dynamic workflow input ", 10000)
	check := &onboard.RuntimeCheck{Status: "inconclusive", Reason: text, Jobs: []onboard.RuntimeFinding{{Status: "inconclusive", Node: text, Tracer: text, Requirement: text, Reason: text}}}
	require.Error(t, finishValidation(&bytes.Buffer{}, root, validationResult{CIRuntime: check}))
	data, err := os.ReadFile(validationPath(root))
	require.NoError(t, err)
	require.Less(t, len(data), 9500) // Includes the bounded, copyable final response. // Includes bounded final-response facts and runtime scope.
	require.Equal(t, text, check.Reason)
	require.Equal(t, text, check.Jobs[0].Reason)
}

func TestConfigurationChecksRequireCIAndTracerAgreement(t *testing.T) {
	for _, tc := range []struct {
		status, version string
		passed          bool
	}{
		{"compatible", "5.128.0", true},
		{"incompatible", "5.128.0", false},
		{"inconclusive", "", false},
		{"compatible", "5.127.0", false},
	} {
		t.Run(tc.status+tc.version, func(t *testing.T) {
			root := t.TempDir()
			check := onboard.RuntimeCheck{Status: tc.status, Jobs: []onboard.RuntimeFinding{{Status: tc.status, Tracer: "dd-trace@" + tc.version}}}
			result := validationResult{CheckOnly: true, Preflight: &jestPreflight{Verdict: verdict{Status: "compatible"}}, CIRuntime: &check, CISelection: compareCISelection(check, "5.128.0")}
			err := finishValidation(&bytes.Buffer{}, root, result)
			require.Equal(t, tc.passed, err == nil)
			data, err := os.ReadFile(validationPath(root))
			require.NoError(t, err)
			var report validationResult
			require.NoError(t, json.Unmarshal(data, &report))
			require.Equal(t, tc.passed, report.ChecksPassed)
		})
	}
}

func readValidationReport(t *testing.T, root string) validationResult {
	t.Helper()
	data, err := os.ReadFile(validationPath(root))
	require.NoError(t, err)
	var report validationResult
	require.NoError(t, json.Unmarshal(data, &report))
	return report
}

func TestReportSizeDoesNotScaleWithTestEvents(t *testing.T) {
	root := t.TempDir()
	run := validationRun{Role: roleReporting, Name: "reporting-only", Command: "npm test -- --json", Instrumented: true, ExitCode: 1}
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
	require.Less(t, len(data), 16000) // Includes bounded final and onboarding response copies; never raw events.
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
