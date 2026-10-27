// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"encoding/json"
	"fmt"
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
		Runs:          []runSummary{{Role: roleBaseline, Name: "baseline", Command: "npm test", ExitCode: &exit, TestCounts: testCounts{Passed: 23}}},
		Cleanup:       &verdict{Status: "passed", Reason: "ddtest temporary session storage removed."}}
	var output bytes.Buffer
	require.Error(t, finishValidation(&output, root, result))
	report := readValidationReport(t, root)
	block := validationHandoff(report)
	// An incomplete verdict leads with a headline no paraphrase turns into success.
	require.True(t, strings.HasPrefix(block, "NOT COMPLETE: onboarding validation is INCOMPLETE. Report the blockers below; do not describe onboarding as complete or successful.\nOnboarding validation: INCOMPLETE.\nLocal results so far (they do not complete onboarding): executed Vitest validations incomplete (0/1); local validation incomplete.\n"))
	require.NotContains(t, block, "PASSED", "no standalone verdict line contradicts the headline")
	require.Contains(t, block, "Onboarding validation: "+report.Summary.Status+".")
	require.Contains(t, block, "INCOMPLETE")
	require.Contains(t, block, "skipping: failed")
	require.Contains(t, strings.Join(validationFacts(report), "\n"), "23 passed")
	require.Contains(t, block, "source-test identities unavailable")
	require.Contains(t, block, "Features: 0/1")
	require.Contains(t, block, "[Open report](<"+htmlReportPath(root)+">)")
	require.Contains(t, block, "[Results JSON](<"+validationPath(root)+">)")
	require.Contains(t, block, "Verify your own scratch")
	require.NotContains(t, output.String(), "FINAL ONBOARDING RESPONSE", "plain output carries no agent prose block; the JSON summary does")
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
	result := validationResult{Framework: "vitest", Compatibility: verdict{Status: "compatible"},
		CIRuntime: &onboard.RuntimeCheck{Status: "compatible"},
		Features:  []featureResult{{Project: "Web", Name: "skipping", Status: "failed"}, {Project: "Server", Name: "skipping", Status: "inconclusive"}}}
	require.Error(t, finishValidation(&output, root, result))
	data, err := os.ReadFile(validationPath(root))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &result))
	require.False(t, result.Success)
	require.Equal(t, "INCOMPLETE", result.Summary.Status)
	require.Contains(t, result.Summary.BlockingChecks, "Web/skipping: failed")
	require.Contains(t, result.Summary.BlockingChecks, "Server/skipping: inconclusive")
	require.False(t, result.Summary.RealCredentialsRequired)
	require.Equal(t, "not exercised", result.Summary.CIExecution)
	require.Contains(t, output.String(), "Validation verdict: INCOMPLETE")
	require.Contains(t, output.String(), "Real Datadog credentials required for these local checks: no")
	require.Contains(t, output.String(), "Web/skipping: failed")
	require.Contains(t, output.String(), "Server/skipping: inconclusive")
	require.Contains(t, result.Summary.OnboardingResponse, "Onboarding validation: INCOMPLETE.")
	require.Contains(t, result.Summary.OnboardingResponse, "Web/skipping: failed; Server/skipping: inconclusive")
	require.NotEmpty(t, result.Summary.FinalResponse)
	require.NotContains(t, output.String(), result.Summary.FinalResponse, "the final response is for agents and lives in the JSON summary")
}

func TestOnboardingResponseDoesNotUpgradeLocalPassOrCheckOnly(t *testing.T) {
	for _, tc := range []struct {
		name          string
		result        validationResult
		status, local string
	}{
		{"local pass with CI blocker", validationResult{LocalSuccess: true, Compatibility: verdict{Status: "compatible"}, CIRuntime: &onboard.RuntimeCheck{Status: "inconclusive"}}, "INCOMPLETE", "PASSED"},
		{"configuration only", validationResult{CheckOnly: true, ChecksPassed: true, CIRuntime: &onboard.RuntimeCheck{Status: "compatible"}}, "INCOMPLETE", "NOT EXERCISED"},
		{"complete locally", validationResult{Success: true, LocalSuccess: true, Compatibility: verdict{Status: "compatible"}, CIRuntime: &onboard.RuntimeCheck{Status: "compatible"}}, "PASSED", "PASSED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			summary := summarizeValidation(tc.result)
			require.Contains(t, summary.OnboardingResponse, "Onboarding validation: "+tc.status+".")
			require.Contains(t, summary.OnboardingResponse, "Local validation: "+tc.local+".")
			require.Contains(t, summary.OnboardingResponse, "CI execution and Datadog backend processing: not exercised.")
			for _, blocker := range summary.BlockingChecks[:min(3, len(summary.BlockingChecks))] {
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
	run.Facts.Events = []intake.Event{{Type: "test", Tags: map[string]string{"test.name": probeName, "test.module": "jest", "test.suite": scenario.Suite, "test.source.file": scenario.SourceFile}}}
	d = diagnoseSkipping(run, scenario)
	require.Equal(t, scenario.SourceFile, d.ReturnedSuite)
	require.False(t, d.IdentityMatched, "only an observed skipped suite can match its skip identity")
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
	result.Summary = summary
	require.Contains(t, validationHandoff(result), "CI runtime scope (static): Node versions 2 instrumented, 2 excluded, 0 unverified")
	result.CIRuntime.Jobs = append(result.CIRuntime.Jobs, onboard.RuntimeFinding{Node: "lts/*", Status: "inconclusive"})
	scope := summarizeCIScope(result.CIRuntime)
	require.Equal(t, []string{"lts/*"}, scope.Unverified)
	require.NotContains(t, scope.Excluded, "lts/*")
}

func TestCIScopeDoesNotCountAGuardedActionBranchAsExcluded(t *testing.T) {
	// The same Cypress step is instrumented when the key exists and runs
	// uninstrumented on forks; only a deliberate opt-out is an excluded runtime.
	guarded := onboard.RuntimeFinding{Workflow: "main.yml", Job: "cypress-run", Step: 3, Node: "20", Status: "excluded", Code: onboard.CodeExcludedRuntime, Reason: onboard.GuardedActionBranch}
	instrumented := onboard.RuntimeFinding{Workflow: "main.yml", Job: "cypress-run", Step: 3, Node: "20", Status: "compatible"}
	optOut := onboard.RuntimeFinding{Workflow: "main.yml", Job: "legacy", Step: 2, Node: "20", Status: "excluded", Code: onboard.CodeExcludedRuntime, Reason: "DD_CIVISIBILITY_ENABLED=false excludes this entry."}
	scope := summarizeCIScope(&onboard.RuntimeCheck{Jobs: []onboard.RuntimeFinding{instrumented, guarded, instrumented, guarded}})
	require.Equal(t, []string{"20"}, scope.Instrumented)
	require.Empty(t, scope.Excluded)
	scope = summarizeCIScope(&onboard.RuntimeCheck{Jobs: []onboard.RuntimeFinding{instrumented, guarded, optOut}})
	require.Equal(t, []string{"20"}, scope.Excluded, "a deliberate opt-out on the same Node stays excluded")
	scope = summarizeCIScope(&onboard.RuntimeCheck{Jobs: []onboard.RuntimeFinding{guarded}})
	require.Equal(t, []string{"20"}, scope.Excluded, "a step never instrumented on that Node stays excluded")
}

func TestHandoffSummarizesUnexecutedMochaConsumerWithoutRepeatingCommands(t *testing.T) {
	result := validationResult{Framework: "mocha", Scope: &onboard.ValidationScope{}, Compatibility: verdict{Status: "not exercised"}, Configurations: []configurationResult{{TestCommand: onboard.TestCommand{Command: "npm run build", Resolution: "npm run build -> npm run test-esm -> mocha test.mjs"}, Status: "not exercised", Compatibility: verdict{Status: "not exercised"}, Error: "unresolved cloud credentials"}}, Features: []featureResult{{Name: "skipping", Status: "passed"}}}
	result.Summary = summarizeValidation(result)
	result.Summary.Facts = validationFacts(result)
	response := validationHandoff(result)
	require.Contains(t, response, "0/1 validated; 0 executed, 0 paired; 0 blocked, 1 not exercised")
	require.Contains(t, response, "Full blockers and commands are in Results JSON.")
	require.NotContains(t, response, "npm run build -> npm run test-esm")
	data, err := json.Marshal(result)
	require.NoError(t, err)
	require.Contains(t, string(data), "unresolved cloud credentials")
	require.Contains(t, string(data), "npm run build -\\u003e npm run test-esm")
}

func TestHandoffIncludesActionableCIFindingAlongsideRealCoverage(t *testing.T) {
	finding := onboard.RuntimeFinding{Workflow: ".github/workflows/ci.yml", Job: "build", Step: 5,
		Status: "inconclusive", Reason: "Mocha CI command npm run build has no NYC wrapper. Testdrive adds NYC locally only; wrap the original CI command with npx --no-install nyc --."}
	result := validationResult{Framework: "mocha", Compatibility: verdict{Status: "compatible"},
		CIRuntime: &onboard.RuntimeCheck{Status: "inconclusive", Jobs: []onboard.RuntimeFinding{finding, finding}},
		Runs:      []runSummary{{Name: "reporting-only", Instrumented: true, Coverage: &coverageSummary{Status: "not observed", TotalTests: 10}}}}
	result.Summary = summarizeValidation(result)
	result.Summary.Facts = validationFacts(result)
	handoff := validationHandoff(result)
	require.Contains(t, handoff, "Mocha real-suite coverage was not observed")
	require.Contains(t, handoff, "Real-suite coverage: observed in 0/1 reporting runs; 0/10 results linked")
	require.Contains(t, handoff, "CI configuration: inconclusive")
	require.Equal(t, 1, strings.Count(strings.Join(result.Summary.Facts, "\n"), finding.Reason), "the full actionable finding stays in JSON without matrix duplication")
}

func TestHandoffSummarizesUnexecutedCypressConsumer(t *testing.T) {
	result := validationResult{Framework: "cypress", Configurations: []configurationResult{{TestCommand: onboard.TestCommand{Command: "npm run e2e", Resolution: "npm run e2e -> cypress run"}, Status: "not exercised", Compatibility: verdict{Status: "not exercised"}}}}
	result.Summary = summarizeValidation(result)
	require.Contains(t, validationHandoff(result), "0/1 validated; 0 executed, 0 paired; 0 blocked, 1 not exercised")
	require.NotContains(t, validationHandoff(result), "Mocha")
}

func TestMissingRealCoverageNamesSelectedFramework(t *testing.T) {
	for _, name := range []string{"mocha", "cucumber", "cypress"} {
		t.Run(name, func(t *testing.T) {
			result := validationResult{Framework: name, Compatibility: verdict{Status: "compatible"},
				Runs: []runSummary{{Name: "reporting-only", Instrumented: true, Coverage: &coverageSummary{Status: "not observed"}}}}
			blockers := strings.Join(summarizeValidation(result).BlockingChecks, "\n")
			require.Contains(t, blockers, displayName(name)+" real-suite coverage was not observed")
			if name != "mocha" {
				require.NotContains(t, blockers, "Mocha")
			}
		})
	}
}

func TestCompactHandoffKeepsExecutedScopeCountsAndRepositoryBlockersSeparate(t *testing.T) {
	root := filepath.Join(t.TempDir(), "customer project")
	require.NoError(t, os.MkdirAll(filepath.Dir(htmlReportPath(root)), 0755))
	require.NoError(t, os.WriteFile(htmlReportPath(root), []byte("real suite"), 0600))
	identities, exit := 122, 0
	result := validationResult{Framework: "cypress", WorkingDirectory: root, HTMLReportCurrent: true,
		Scope: &onboard.ValidationScope{}, CIRuntime: &onboard.RuntimeCheck{Status: "compatible"},
		Compatibility: verdict{Status: "compatible"}, Cleanup: &verdict{Status: "passed"},
		ReportingCounts: &executionCounts{TestExecutions: 610, SourceTestIdentities: &identities}}
	for i := range 5 {
		command := fmt.Sprintf("npm run browser-%d", i)
		result.Configurations = append(result.Configurations, configurationResult{TestCommand: onboard.TestCommand{Command: command}, Status: "passed", PairedExecution: true, ExecutionAttempted: true})
		result.Runs = append(result.Runs,
			runSummary{Role: roleBaseline, Name: "baseline", Command: command, ExitCode: &exit},
			runSummary{Role: roleReporting, Name: "reporting-only", Command: command, Instrumented: true, ExitCode: &exit, Coverage: &coverageSummary{Status: "observed", CoveredTests: 122, TotalTests: 122, Level: "test"}})
		for _, feature := range jestFeatures {
			result.Features = append(result.Features, featureResult{Project: command, Name: feature, Status: "passed"})
		}
	}
	for _, status := range []string{"blocked", "blocked", "not exercised"} {
		result.Configurations = append(result.Configurations, configurationResult{TestCommand: onboard.TestCommand{Command: strings.Repeat("long browser command ", 100)}, Status: status, Error: "unavailable browser or OS"})
	}
	result.Summary = summarizeValidation(result)
	response := validationHandoff(result)
	require.Contains(t, response, "Local results so far (they do not complete onboarding): executed Cypress validations passed (5/5); local validation incomplete.")
	require.NotContains(t, response, "PASSED", "partial passes never read as a standalone verdict next to NOT COMPLETE")
	require.Contains(t, response, "Onboarding validation: INCOMPLETE.")
	require.Contains(t, response, "Configurations: 5/8 validated; 5 executed, 5 paired; 2 blocked, 1 not exercised, 0 incomplete.")
	require.Contains(t, response, "Reporting: 610 test executions; 122 source-test identities.")
	require.Contains(t, response, "Features: 30/30 supported checks passed.")
	require.Contains(t, response, "Real-suite coverage: observed in 5/5 reporting runs; 610/610 results linked to source.")
	require.Contains(t, response, "Blockers: 3. Full blockers and commands are in Results JSON.")
	require.Contains(t, response, "CI execution and Datadog backend processing: not exercised.")
	require.Contains(t, response, "[Open report](<"+htmlReportPath(root)+">)")
	require.Contains(t, response, "[Results JSON](<"+validationPath(root)+">)")
	require.Less(t, len(response), 1800, "handoff must not expand with long unvalidated commands")
	require.Len(t, result.Summary.BlockingChecks, 3, "full configuration blockers remain in JSON")
}

func TestCompactHandoffDoesNotInventIdentitiesOrCurrentEvidence(t *testing.T) {
	result := focusedValidationFixture("mocha")
	result.ReportingCounts = &executionCounts{TestExecutions: 96}
	result.CheckOnly = true
	result.LastExecution = &validationEvidence{Status: "passed", Current: false}
	result.Runs[1].Coverage = &coverageSummary{Status: "observed", CoveredTests: 32, TotalTests: 32, Level: "suite"}
	result.Summary = summarizeValidation(result)
	response := validationHandoff(result)
	require.Contains(t, response, "Local results so far (they do not complete onboarding): selected Mocha testdrive not exercised; executed Mocha validations not exercised (0/0); local validation not exercised.")
	require.Contains(t, response, "Onboarding validation: INCOMPLETE.")
	require.Contains(t, response, "Historical reporting (not revalidated): 96 test executions; source-test identities unavailable; this count does not affect coverage or feature results.")
	require.NotContains(t, response, "0 source-test identities")
	require.Contains(t, response, "Historical features (not revalidated): 6/6 supported checks passed.")
	require.Contains(t, response, "suite-level, not independent per-test coverage")
	require.Contains(t, response, "Evidence: check-only")
	require.Contains(t, response, "Evidence: check-only kept the earlier full execution (passed) as history without rerunning it")
	require.NotContains(t, response, "is stale", "check-only doesn't make the last full run stale")
	require.NotContains(t, response, "Open report")
	require.Contains(t, response, "HTML report unavailable")
}

func TestFeatureCountsDoNotHideUnexpectedUnsupportedChecks(t *testing.T) {
	for _, tc := range []struct {
		framework, feature string
		legitimate         bool
	}{
		{"playwright", "skipping", true},
		{"webdriverio", "skipping", true},
		{"jest", "skipping", false},
		{"vitest", "auto-retries", false},
		{"playwright", "quarantine", false},
	} {
		t.Run(tc.framework+"/"+tc.feature, func(t *testing.T) {
			result := validationResult{Framework: tc.framework, Features: []featureResult{{Name: tc.feature, Status: "not supported"}}}
			passed, total, unsupported := validationFeatureCounts(result.Framework, result.Features)
			require.Zero(t, passed)
			if tc.legitimate {
				require.Zero(t, total)
				require.Equal(t, []string{tc.feature}, unsupported)
			} else {
				require.Equal(t, 1, total, "unsupported status does not waive a required feature")
				require.Empty(t, unsupported)
				require.Contains(t, validationFacts(result), "Features: 0/1 project-feature checks passed.")
			}
		})
	}
}

func TestHandoffExplainsAnUncreditedFocusedExecution(t *testing.T) {
	result := pairedReport("focused", "passed")
	result.FocusedCommand = true
	result.Configurations = []configurationResult{{
		TestCommand:            onboard.TestCommand{Command: "npm run test-ci", Environment: map[string]string{"npm_config_loglevel": "silent"}},
		Status:                 "not exercised",
		EnvironmentDifferences: []string{"npm_config_loglevel"},
		Compatibility:          verdict{Status: "not exercised", Reason: "CI environment differs for: npm_config_loglevel; run --all to validate those values."},
	}}
	result.Summary = summarizeValidation(result)
	response := validationHandoff(result)
	require.Contains(t, response, "Configurations: 0/1 validated; 0 executed, 0 paired")
	require.Contains(t, response, "This run's command (pnpm exec jest --config original.js) executed and was paired, but matches no discovered CI configuration exactly, so no CI configuration is credited (CI environment differs for: npm_config_loglevel; run --all to validate those values).")
}

func TestHandoffExplainsUnavailableIdentitiesAndOmitsHeadlineWhenComplete(t *testing.T) {
	result := completeValidationFixture("jest")
	result.ReportingCounts = &executionCounts{TestExecutions: 1222, Reason: "Source-test identities are unavailable because native file/name identities repeat within a configuration."}
	result.Summary = validationSummary{Status: "COMPLETE LOCALLY", LocalValidation: "PASSED", CIConfiguration: "compatible"}
	response := validationHandoff(result)
	require.Contains(t, response, "source-test identities unavailable (native file/name identities repeat within a configuration); this count does not affect coverage or feature results.")
	require.True(t, strings.HasPrefix(response, "Onboarding validation: COMPLETE LOCALLY."), response)
}

func TestHandoffBlockersShowOneLineExcerptsOfCIScripts(t *testing.T) {
	result := completeValidationFixture("playwright")
	result.Scope = &onboard.ValidationScope{Unresolved: []string{"docs.yml / generate / step 4 (gem install rack\ncd rack-3.2*\nbundle exec rdoc\n): Repository script could not be resolved: gem install rack\ncd rack-3.2*\n"}}
	result.Summary = summarizeValidation(result)
	response := validationHandoff(result)
	require.Contains(t, response, "unresolved CI scope: docs.yml / generate / step 4 (gem install rack …): Repository script could not be resolved: gem install rack …")
	require.NotContains(t, response, "cd rack-3.2*", "multi-line scripts stay in Results JSON")
}
