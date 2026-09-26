// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestFinalVerdictPreservesFailedAndUnvalidatedProjects(t *testing.T) {
	root := t.TempDir()
	var output bytes.Buffer
	result := validationResult{Compatibility: verdict{Status: "compatible"},
		CIRuntime: &onboard.RuntimeCheck{Status: "compatible"},
		Features:  []featureResult{{Project: "Web", Name: "skipping", Status: "failed"}, {Project: "Server", Name: "skipping", Status: "inconclusive"}}}
	require.Error(t, finishValidation(&output, root, result))
	data, err := os.ReadFile(validationPath(root))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &result))
	require.False(t, result.Success)
	require.Equal(t, "INCOMPLETE", result.Summary.Status)
	require.Equal(t, []string{"Web/skipping failed", "Server/skipping inconclusive"}, result.Summary.BlockingChecks)
	require.False(t, result.Summary.RealCredentialsRequired)
	require.Equal(t, "not exercised", result.Summary.CIExecution)
	require.Contains(t, output.String(), "Validation verdict: INCOMPLETE")
	require.Contains(t, output.String(), "Real Datadog credentials required for these local checks: no")
	require.Contains(t, output.String(), "Web/skipping failed; Server/skipping inconclusive")
}

func TestSkippingDiagnosticsDistinguishMissingRequestAndPathMismatch(t *testing.T) {
	scenario := intake.Scenario{Feature: "skipping", Module: "jest", Suite: "../../src/probe.test.js", SourceFile: "src/probe.test.js", Test: probeName}
	run := validationRun{ExitCode: 1, Tests: []jestTest{{Name: probeName, Status: "failed"}}}
	d := diagnoseSkipping(run, scenario)
	require.Zero(t, d.SkippableRequests)
	require.Empty(t, d.ReturnedSuite)
	require.Equal(t, 1, d.ExecutedTests)
	run.Facts.SettingsRequests, run.Facts.SkippableRequests = 1, 1
	run.Facts.Events = []intake.Event{{Type: "test", Tags: map[string]string{"test.name": probeName, "test.module": "jest", "test.suite": scenario.Suite}}}
	d = diagnoseSkipping(run, scenario)
	require.Equal(t, scenario.SourceFile, d.ReturnedSuite)
	require.True(t, d.IdentityMatched)
	require.False(t, d.SkippedByITR)
	d.PathMismatch = true
	run.Skipping = d
	result := evaluateFeature("skipping", run, intake.Test{Suite: scenario.Suite, SourceFile: scenario.SourceFile, Name: probeName, Module: "jest"})
	require.Equal(t, "failed", result.Status)
	require.Contains(t, result.Reason, "path mismatch")
	require.Contains(t, result.Reason, "Real Datadog credentials are not required")
}

func TestSkippingReportsSuiteNameDifferenceWithoutFailingSuccessfulSkipping(t *testing.T) {
	scenario := intake.Scenario{Feature: "skipping", Module: "jest", Suite: "../../src/probe.test.js", SourceFile: "src/probe.test.js", Test: probeName}
	run := validationRun{Facts: intake.Facts{SkippableRequests: 1, Events: []intake.Event{{Type: "test_suite_end", Tags: map[string]string{
		"test.module": "jest", "test.suite": scenario.SourceFile, "test.status": "skip", "test.skipped_by_itr": "true",
	}}}}}
	run.Skipping = diagnoseSkipping(run, scenario)
	require.True(t, run.Skipping.SkippedByITR)
	require.True(t, run.Skipping.SuiteNameChanged)
	require.False(t, run.Skipping.IdentityMatched)
	require.Equal(t, scenario.Suite, run.Skipping.RequestedSuite)
	require.Equal(t, scenario.SourceFile, run.Skipping.SourceFile)
	require.Equal(t, scenario.SourceFile, run.Skipping.ReturnedSuite)
	require.Equal(t, scenario.SourceFile, run.Skipping.ObservedSuite)
	result := evaluateFeature("skipping", run, intake.Test{Suite: scenario.Suite, SourceFile: scenario.SourceFile, Name: probeName, Module: "jest"})
	require.Equal(t, "passed", result.Status)
	require.Contains(t, result.Reason, "different test.suite")
}
