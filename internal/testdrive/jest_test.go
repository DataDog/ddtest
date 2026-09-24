// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/kballard/go-shellquote"
	"github.com/stretchr/testify/require"
)

func testRun(status string, exit int) validationRun {
	traceStatus := map[string]string{"passed": "pass", "failed": "fail"}[status]
	return validationRun{ExitCode: exit, Tests: []jestTest{{File: "/project/test.js", Name: "works", Status: status}}, Facts: intake.Facts{
		TestEventCount: 1, Tests: []intake.Test{{Name: "works", SourceFile: "/project/test.js", Attempts: []intake.TestRun{{Status: traceStatus}}}},
	}}
}

func TestCompareJestContract(t *testing.T) {
	for _, status := range []string{"passed", "failed"} {
		t.Run(status, func(t *testing.T) {
			exit := 0
			if status == "failed" {
				exit = 1
			}
			baseline := testRun(status, exit)
			require.Equal(t, "compatible", compareJest(withoutTelemetry(baseline), baseline).Status)
		})
	}
	baseline := withoutTelemetry(testRun("passed", 0))
	failed := testRun("failed", 1)
	require.Equal(t, "suspected regression", compareJest(baseline, failed).Status)
	failed.Tests[0].Failure = "expected true"
	differentFailure := testRun("failed", 1)
	differentFailure.Tests[0].Failure = "expected false"
	require.Equal(t, "suspected regression", compareJest(withoutTelemetry(failed), differentFailure).Status)
	missing := testRun("passed", 0)
	missing.Tests = nil
	require.Equal(t, "suspected regression", compareJest(baseline, missing).Status)
	noTelemetry := testRun("passed", 0)
	noTelemetry.Facts = intake.Facts{}
	require.Equal(t, "inconclusive", compareJest(baseline, noTelemetry).Status)
	wrongTelemetry := testRun("passed", 0)
	wrongTelemetry.Facts.Tests[0].Name = "different test"
	require.Equal(t, "inconclusive", compareJest(baseline, wrongTelemetry).Status)
	require.Equal(t, "inconclusive", compareJest(validationRun{}, validationRun{}).Status)
	baseline.Tests = append(baseline.Tests, baseline.Tests[0])
	require.Equal(t, "suspected regression", compareJest(baseline, testRun("passed", 0)).Status)
}

func TestJestJSONComparisonIgnoresTimingOrderAndStacks(t *testing.T) {
	a, b := validationRun{}, validationRun{}
	first := `{"testResults":[{"name":"/repo/a.test.js","assertionResults":[{"fullName":"suite first","status":"passed","duration":2},{"fullName":"suite second","status":"failed","failureMessages":["Error: expected 2\n at project.js:1"]}]}]}`
	second := `{"testResults":[{"name":"/repo/a.test.js","assertionResults":[{"fullName":"suite second","status":"failed","failureMessages":["Error: expected 2\n at dd-trace.js:100\n at project.js:1"]},{"fullName":"suite first","status":"passed","duration":100}]}]}`
	path := filepath.Join(t.TempDir(), "results.json")
	require.NoError(t, os.WriteFile(path, []byte(first), 0600))
	readJestResults(path, &a)
	require.NoError(t, os.WriteFile(path, []byte(second), 0600))
	readJestResults(path, &b)
	require.Equal(t, outcomeKeys(a), outcomeKeys(b))
}

func TestScenarioEnvironmentsDisableUnrelatedBehavior(t *testing.T) {
	for _, feature := range append([]string{""}, jestFeatures...) {
		env := testEnvironment("/trace/ci/init.js", "http://127.0.0.1:1234", "session")
		configureScenarioEnvironment(env, feature)
		require.Equal(t, "false", env["DD_CIVISIBILITY_IMPACTED_TESTS_DETECTION_ENABLED"])
		require.Equal(t, "false", env["DD_TEST_FAILED_TEST_REPLAY_ENABLED"])
		require.Equal(t, feature == "auto-retries", env["DD_CIVISIBILITY_FLAKY_RETRY_ENABLED"] == "true")
		require.Equal(t, feature == "early-flake-detection", env["DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED"] == "true")
		require.Equal(t, slices.Contains([]string{"quarantine", "disabled", "attempt-to-fix"}, feature), env["DD_TEST_MANAGEMENT_ENABLED"] == "true")
	}
}

func TestProbeUsesProjectTestLocationAndDoesNotOverwrite(t *testing.T) {
	root := t.TempDir()
	original := filepath.Join(root, "existing.test.js")
	requireWriteFile(t, original, "original")
	run := testRun("passed", 0)
	run.Tests[0].File = original
	path, err := createJestProbe(root, run)
	require.NoError(t, err)
	defer func() { require.NoError(t, os.Remove(path)) }()
	require.NotEqual(t, original, path)
	resolvedRoot, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	require.Equal(t, resolvedRoot, filepath.Dir(path))
	contents, err := os.ReadFile(original)
	require.NoError(t, err)
	require.Equal(t, "original", string(contents))
	run.Tests[0].File = filepath.Join(t.TempDir(), "outside.test.js")
	requireWriteFile(t, run.Tests[0].File, "original")
	_, err = createJestProbe(root, run)
	require.Error(t, err)
}

func TestFeatureAssertionsRequireBehaviorAndTelemetry(t *testing.T) {
	identity := intake.Test{Module: "jest", Suite: "probe.test.js", Name: probeName}
	event := func(status, reason string, extra map[string]string) intake.Event {
		tags := map[string]string{"test.module": "jest", "test.suite": "probe.test.js", "test.name": probeName, "test.status": status}
		if reason != "" {
			tags["test.is_retry"] = "true"
			tags["test.retry_reason"] = reason
		}
		for key, value := range extra {
			tags[key] = value
		}
		return intake.Event{Type: "test", Tags: tags}
	}
	cases := map[string][]intake.Event{
		"auto-retries":          {event("fail", "", nil), event("pass", "auto_test_retry", nil)},
		"early-flake-detection": {event("pass", "", nil), event("pass", "early_flake_detection", nil)},
		"quarantine":            {event("fail", "", map[string]string{"test.test_management.is_quarantined": "true"})},
		"disabled":              {event("skip", "", map[string]string{"test.test_management.is_test_disabled": "true"})},
		"attempt-to-fix":        {event("pass", "", map[string]string{"test.test_management.is_attempt_to_fix": "true"}), event("pass", "attempt_to_fix", nil)},
		"skipping":              {{Type: "test_suite_end", Tags: map[string]string{"test.suite": "probe.test.js", "test.status": "skip", "test.skipped_by_itr": "true"}}},
	}
	for feature, events := range cases {
		t.Run(feature, func(t *testing.T) {
			run := validationRun{Facts: intake.Facts{Events: events}}
			if feature != "skipping" {
				run.Tests = []jestTest{{Name: probeName, Status: "passed"}}
				if feature == "disabled" {
					run.Tests[0].Status = "pending"
				}
				if feature == "early-flake-detection" || feature == "attempt-to-fix" {
					run.Tests = append(run.Tests, run.Tests[0])
				}
			}
			require.Equal(t, "passed", evaluateFeature(feature, run, identity).Status)
			run.ExitCode = 1
			require.Equal(t, "failed", evaluateFeature(feature, run, identity).Status)
			require.Equal(t, "failed", evaluateFeature(feature, validationRun{}, identity).Status)
		})
	}
	run := validationRun{ResultError: "missing JSON"}
	require.Equal(t, "inconclusive", evaluateFeature("auto-retries", run, identity).Status)
}

type jsonExecutor struct {
	results  []string
	exits    []error
	calls    int
	envs     []map[string]string
	commands []string
}

func (e *jsonExecutor) CombinedOutput(_ context.Context, command string, args []string, env map[string]string) ([]byte, error) {
	index := slices.Index(args, "--outputFile")
	if index < 0 {
		return nil, errors.New("missing JSON output argument")
	}
	err := os.WriteFile(args[index+1], []byte(e.results[e.calls]), 0600)
	if err != nil {
		return nil, err
	}
	e.envs = append(e.envs, env)
	e.commands = append(e.commands, shellquote.Join(append([]string{command}, args...)...))
	exit := e.exits[e.calls]
	e.calls++
	return []byte("detailed test output"), exit
}

func TestJestValidationRepeatsUnstablePairAndWritesJSON(t *testing.T) {
	run := preparedTestdrive(t)
	run.platform = &fakeTracer{preloadPath: "/trace/ci/init.js"}
	// This intentionally nonexistent file also makes feature eligibility explicit.
	results := func(status string) string {
		return `{"testResults":[{"name":"/outside/missing.test.js","assertionResults":[{"fullName":"works","status":"` + status + `"}]}]}`
	}
	executor := &jsonExecutor{results: []string{results("passed"), results("failed"), results("failed"), results("failed")}, exits: make([]error, 4)}
	run.executor = executor
	run.startIntake = func(string, intake.Scenario) (localIntake, error) {
		facts := intake.Facts{}
		if executor.calls%2 == 1 {
			facts = testRun("passed", 0).Facts
		}
		return &fakeIntake{url: "http://127.0.0.1:1234", findings: facts}, nil
	}
	var output bytes.Buffer
	require.ErrorContains(t, run.Run(t.Context(), &output), "validation is incomplete")
	require.Equal(t, 4, executor.calls)
	require.Equal(t, "false", executor.envs[0]["DD_CIVISIBILITY_ENABLED"])
	require.Equal(t, "true", executor.envs[1]["DD_CIVISIBILITY_ENABLED"])
	require.Contains(t, output.String(), "Compatibility: inconclusive")
	require.Contains(t, output.String(), "Outcomes changed between repeated runs")
	paths, err := filepath.Glob(filepath.Join(run.repositoryRoot, ".testoptimization", "testdrive.json"))
	require.NoError(t, err)
	require.Len(t, paths, 1)
	data, err := os.ReadFile(paths[0])
	require.NoError(t, err)
	require.True(t, json.Valid(data))
	require.NotContains(t, string(data), "detailed test output")
	var result validationResult
	require.NoError(t, json.Unmarshal(data, &result))
	require.False(t, result.Success)
	require.Len(t, result.Runs, 4)
	for i, summary := range result.Runs {
		require.Equal(t, executor.commands[i], summary.Command)
		require.Equal(t, i%2 == 1, summary.Instrumented)
		require.Equal(t, 0, *summary.ExitCode)
	}
	require.NoDirExists(t, run.platform.(*fakeTracer).sessionDirectory)
	html, err := filepath.Glob(filepath.Join(filepath.Dir(paths[0]), "*.html"))
	require.NoError(t, err)
	require.Empty(t, html)
	require.NotContains(t, output.String(), "detailed test output")
}

func TestFinishValidationDoesNotUseSuiteExitAsVerdict(t *testing.T) {
	var output bytes.Buffer
	result := validationResult{Compatibility: verdict{Status: "compatible"}, Runs: []runSummary{(validationRun{Command: "npm test", ExitCode: 1}).summary()}}
	require.NoError(t, finishValidation(&output, t.TempDir(), result))
	require.Contains(t, output.String(), "Results JSON:")
}

func TestJestRunClosesIntakeOnWriteFailure(t *testing.T) {
	run := preparedTestdrive(t)
	session, err := NewSession()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, session.Close()) })
	server := &fakeIntake{url: "http://127.0.0.1:1234"}
	run.startIntake = func(directory string, _ intake.Scenario) (localIntake, error) {
		require.NoError(t, os.RemoveAll(directory))
		return server, nil
	}
	run.executor = &fakeTestdriveExecutor{}
	_, err = run.runJest(t.Context(), &bytes.Buffer{}, session, "/trace/ci/init.js", "baseline", false, intake.Scenario{}, "", "")
	require.ErrorContains(t, err, "save test output")
	require.True(t, server.closed)
}

func TestJestArgsPreserveNpmForwarding(t *testing.T) {
	require.Equal(t, []string{"test", "--", "--json"}, appendJestArgs("npm", []string{"test"}, "--json"))
	require.Equal(t, 1, strings.Count(strings.Join(appendJestArgs("npm", []string{"test", "--"}, "--json"), " "), "-- "))
}

func withoutTelemetry(run validationRun) validationRun { run.Facts = intake.Facts{}; return run }

func TestBaselineRemovesQuotedTracerPreloadsAndPreservesOtherOptions(t *testing.T) {
	got := stripDatadogNodeOptions(`--require "/project space/node_modules/dd-trace/ci/init.js" --import dd-trace/register.js --require "/project space/setup.js" --max-old-space-size=4096`)
	require.Equal(t, `--require "/project space/setup.js" --max-old-space-size=4096`, got)
}

func TestTelemetryRequiresTheCorrectTestFileAndNoRetries(t *testing.T) {
	baseline := withoutTelemetry(testRun("passed", 0))
	instrumented := testRun("passed", 0)
	instrumented.Facts.Tests[0].SourceFile = "/other/test.js"
	require.Equal(t, "inconclusive", compareJest(baseline, instrumented).Status)
	instrumented = testRun("passed", 0)
	instrumented.Facts.Tests[0].Attempts[0].Retry = true
	require.Equal(t, "inconclusive", compareJest(baseline, instrumented).Status)
	baseline.Facts = testRun("passed", 0).Facts
	require.Equal(t, "inconclusive", compareJest(baseline, testRun("passed", 0)).Status)
}

func TestJestRejectsMissingStructuredResults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jest.json")
	requireWriteFile(t, path, `{}`)
	run := validationRun{}
	readJestResults(path, &run)
	require.NotEmpty(t, run.ResultError)
	requireWriteFile(t, path, `{"testResults":[]}`)
	run = validationRun{}
	readJestResults(path, &run)
	require.Empty(t, run.ResultError, "a skipped suite can legitimately produce an empty results array")
}
