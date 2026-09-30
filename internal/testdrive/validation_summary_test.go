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

func TestHandoffUsesCurrentFactsAndExistingClickableReports(t *testing.T) {
	root := filepath.Join(t.TempDir(), "customer project")
	require.NoError(t, os.MkdirAll(filepath.Dir(htmlReportPath(root)), 0755))
	require.NoError(t, os.WriteFile(htmlReportPath(root), []byte("suite findings"), 0600))
	exit := 0
	result := validationResult{Framework: "vitest", HTMLReportCurrent: true,
		Compatibility: verdict{Status: "compatible"},
		Features:      []featureResult{{Name: "skipping", Status: "failed", Reason: "no skip evidence"}},
		Runs:          []runSummary{{Name: "baseline", ExitCode: &exit, TestCounts: testCounts{Passed: 23}}},
		Cleanup:       &verdict{Status: "passed", Reason: "ddtest temporary session storage removed."}}
	var output bytes.Buffer
	require.Error(t, finishValidation(&output, root, result))
	report := readValidationReport(t, root)
	block := validationHandoff(report)
	require.True(t, strings.HasPrefix(block, "Onboarding validation: "+report.Summary.Status+"."))
	require.Contains(t, block, "INCOMPLETE")
	require.Contains(t, block, "skipping failed")
	require.Contains(t, block, "23 passed")
	require.Contains(t, block, "Features: 0/1")
	require.Contains(t, block, "[Open report](<"+htmlReportPath(root)+">)")
	require.Contains(t, block, "[Results JSON](<"+validationPath(root)+">)")
	require.Contains(t, block, "Verify agent-created outputs")
	require.Contains(t, output.String(), block)
}

func TestHandoffDoesNotInventHTMLOrSuccessfulCleanup(t *testing.T) {
	root := t.TempDir()
	result := validationResult{WorkingDirectory: root,
		Summary: validationSummary{OnboardingResponse: "Onboarding validation: INCOMPLETE."},
		Cleanup: &verdict{Status: "failed", Reason: "session could not be removed"}}
	block := validationHandoff(result)
	require.NotContains(t, block, "report.html")
	require.Contains(t, block, "Cleanup: failed")
	result.Cleanup = nil
	require.Contains(t, validationHandoff(result), "Cleanup: not recorded")
}

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
	require.Contains(t, output.String(), "Web/skipping failed")
	require.Contains(t, output.String(), "Server/skipping inconclusive")
	require.Contains(t, result.Summary.OnboardingResponse, "Onboarding validation: INCOMPLETE.")
	require.Contains(t, result.Summary.OnboardingResponse, "Web/skipping failed; Server/skipping inconclusive")
	require.Contains(t, output.String(), result.Summary.FinalResponse)
}

func TestOnboardingResponseDoesNotUpgradeLocalPassOrCheckOnly(t *testing.T) {
	for _, tc := range []struct {
		name          string
		result        validationResult
		status, local string
	}{
		{"local pass with CI blocker", validationResult{LocalSuccess: true, Compatibility: verdict{Status: "compatible"}, CIRuntime: &onboard.RuntimeCheck{Status: "inconclusive"}}, "INCOMPLETE", "PASSED"},
		{"configuration only", validationResult{CheckOnly: true, ChecksPassed: true, CIRuntime: &onboard.RuntimeCheck{Status: "compatible"}}, "INCOMPLETE", "NOT EXERCISED"},
		{"complete locally", validationResult{Success: true, LocalSuccess: true, Compatibility: verdict{Status: "compatible"}, CIRuntime: &onboard.RuntimeCheck{Status: "compatible"}}, "COMPLETE LOCALLY", "PASSED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			summary := summarizeValidation(tc.result)
			require.Contains(t, summary.OnboardingResponse, "Onboarding validation: "+tc.status+".")
			require.Contains(t, summary.OnboardingResponse, "Local validation: "+tc.local+".")
			require.Contains(t, summary.OnboardingResponse, "CI execution and Datadog backend processing: not exercised.")
			for _, blocker := range summary.BlockingChecks {
				require.Contains(t, summary.OnboardingResponse, blocker)
			}
		})
	}
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

func TestFinalFactsKeepCurrentCrashesAndConcreteCIBlockers(t *testing.T) {
	failed, passed := 1, 0
	result := validationResult{Session: "current", Compatibility: verdict{Status: "inconclusive", Reason: "Outcomes changed between repeated runs."},
		Runs: []runSummary{
			{Name: "baseline", ExitCode: &failed, TestCounts: testCounts{Passed: 586}, SuiteErrorCount: 1},
			{Name: "baseline-repeat", ExitCode: &passed, TestCounts: testCounts{Passed: 587}},
		},
		CIRuntime: &onboard.RuntimeCheck{Status: "incompatible", Jobs: []onboard.RuntimeFinding{{Workflow: "release.yml", Job: "release", Step: 4, Status: "incompatible", Code: "missing_instrumentation", Reason: "Add instrumentation before yarn test."}}},
	}
	facts := validationFacts(result)
	require.Contains(t, facts, "baseline: 586 passed, 0 failed, 0 skipped; 1 suite errors; exit 1.")
	require.Contains(t, facts, "baseline-repeat: 587 passed, 0 failed, 0 skipped; 0 suite errors; exit 0.")
	require.Contains(t, facts, "CI release.yml / release / step 4: Add instrumentation before yarn test.")
	require.Contains(t, facts, "Report: .testoptimization/testdrive.json")
	selection := compareCISelection(*result.CIRuntime, "6.17.0")
	require.Equal(t, "incompatible", selection.Status)
	require.Contains(t, selection.Reason, "release.yml / release / step 4")
	require.NotContains(t, selection.Reason, "unknown workflow syntax")
}

func TestFinalFactsDeduplicateLongMatrixFindingsBeforeTruncating(t *testing.T) {
	result := validationResult{CIRuntime: &onboard.RuntimeCheck{}}
	for step := 1; step <= 12; step++ {
		finding := onboard.RuntimeFinding{Workflow: "ci.yml", Job: "test", Step: step, Status: "inconclusive", Reason: strings.Repeat("long reason ", 40)}
		result.CIRuntime.Jobs = append(result.CIRuntime.Jobs, finding, finding)
	}
	var findings []string
	for _, fact := range validationFacts(result) {
		if strings.HasPrefix(fact, "CI ") {
			findings = append(findings, fact)
		}
	}
	require.Len(t, findings, 8)
	require.NotEqual(t, findings[0], findings[1])
	require.Contains(t, validationFacts(result), "Additional CI findings are in ci_runtime.jobs in the report.")
}

func TestCheckOnlyFactsDoNotCountTheAllFeaturesPlaceholder(t *testing.T) {
	result := validationResult{CheckOnly: true, Features: []featureResult{{Name: "all", Status: "not exercised"}}}
	facts := validationFacts(result)
	require.Contains(t, facts, "Features: not exercised.")
	require.NotContains(t, facts, "Features: 0/1 project-feature checks passed.")
}

func TestVerdictStatesLocalRuntimeAndDeduplicatedCIScope(t *testing.T) {
	result := validationResult{Success: true, LocalSuccess: true,
		Preflight: &jestPreflight{Node: "24.14.1", Verdict: verdict{Status: "compatible"}},
		CIRuntime: &onboard.RuntimeCheck{Status: "compatible", Jobs: []onboard.RuntimeFinding{
			{Node: "14.x", Status: "excluded"}, {Node: "16.x", Status: "excluded"},
			{Node: "18.x", Status: "compatible"}, {Node: "20.x", Status: "compatible"}, {Node: "20.x", Status: "compatible"},
		}},
	}
	summary := summarizeValidation(result)
	require.Equal(t, []string{"18.x", "20.x"}, summary.CIScope.Instrumented)
	require.Equal(t, []string{"14.x", "16.x"}, summary.CIScope.Excluded)
	require.Contains(t, summary.OnboardingResponse, "Local execution runtime: Node 24.14.1")
	require.Contains(t, summary.OnboardingResponse, "excluded from instrumentation: 14.x, 16.x")
	require.Contains(t, summary.OnboardingResponse, "CI execution and Datadog backend processing: not exercised")
	result.CIRuntime.Jobs = append(result.CIRuntime.Jobs, onboard.RuntimeFinding{Node: "lts/*", Status: "inconclusive"})
	scope := summarizeCIScope(result.CIRuntime)
	require.Equal(t, []string{"lts/*"}, scope.Unverified)
	require.NotContains(t, scope.Excluded, "lts/*")
}
