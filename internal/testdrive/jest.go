// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/kballard/go-shellquote"
)

type jestTest struct {
	File    string `json:"file"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Failure string `json:"failure,omitempty"`
}

type jestResults struct {
	TestResults []struct {
		Name             string `json:"name"`
		Message          string `json:"message"`
		Status           string `json:"status"`
		AssertionResults []struct {
			FullName        string   `json:"fullName"`
			Status          string   `json:"status"`
			FailureMessages []string `json:"failureMessages"`
		} `json:"assertionResults"`
	} `json:"testResults"`
}

var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func failureSignature(message string) string {
	var lines []string
	for _, line := range strings.Split(ansiEscape.ReplaceAllString(message, ""), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "at ") {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func readJestResults(path string, run *validationRun) {
	data, err := os.ReadFile(path)
	if err != nil {
		run.ResultError = "Jest did not produce its JSON results"
		return
	}
	var result jestResults
	if err := json.Unmarshal(data, &result); err != nil {
		run.ResultError = "Jest JSON results could not be decoded"
		return
	}
	if result.TestResults == nil {
		run.ResultError = "Jest JSON results are missing the testResults array"
		return
	}
	for _, suite := range result.TestResults {
		if len(suite.AssertionResults) == 0 && suite.Status == "failed" {
			run.SuiteErrors = append(run.SuiteErrors, suite.Name+": "+failureSignature(suite.Message))
		}
		for _, test := range suite.AssertionResults {
			run.Tests = append(run.Tests, jestTest{File: filepath.Clean(suite.Name), Name: test.FullName, Status: test.Status,
				Failure: failureSignature(strings.Join(test.FailureMessages, "\n"))})
		}
	}
	slices.Sort(run.SuiteErrors)
}

func appendJestArgs(command string, args []string, additions ...string) []string {
	args = slices.Clone(args)
	if filepath.Base(command) == "npm" && !slices.Contains(args, "--") {
		args = append(args, "--")
	}
	return append(args, additions...)
}

func probeJestArgs(args []string) []string {
	// Jest ignores duplicate JSON options instead of taking the last one.
	var result []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--coverageThreshold" || args[i] == "--coverage-threshold" {
			i++
			continue
		}
		if strings.HasPrefix(args[i], "--coverageThreshold=") || strings.HasPrefix(args[i], "--coverage-threshold=") {
			continue
		}
		result = append(result, args[i])
	}
	return append(result, "--coverageThreshold={}")
}

func (t *Testdrive) runJest(ctx context.Context, output io.Writer, session *Session, preload, name string, instrumented bool, scenario intake.Scenario, probePath, probeMode string) (run validationRun, runErr error) {
	run.Name = name
	run.Instrumented = instrumented
	run.ProbeMode = probeMode
	run.root = t.repositoryRoot
	if err := ctx.Err(); err != nil {
		return run, err
	}
	directory := filepath.Join(session.Directory(), name)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return run, err
	}
	// Even the baseline receives a local endpoint and disabled uploads, so a
	// tracer imported by the project's own setup cannot contact Datadog.
	server, err := t.startIntake(directory, scenario)
	if err != nil {
		return run, err
	}
	closed := false
	defer func() {
		if !closed {
			runErr = errors.Join(runErr, server.Close())
		}
	}()
	env := t.environment(preload, server.URL(), session.ID()+" "+name)
	configureScenarioEnvironment(env, scenario.Feature)
	if !instrumented {
		env["NODE_OPTIONS"] = stripDatadogNodeOptions(os.Getenv("NODE_OPTIONS"))
		env["DD_CIVISIBILITY_ENABLED"] = "false"
		env["DD_TRACE_ENABLED"] = "false"
	}
	// Package scripts can already set coverageDirectory. Jest treats duplicate
	// string options as arrays, so normalize only our override before parsing.
	coverageDirectory := filepath.Join(directory, "coverage")
	if err := redirectJestCoverage(directory, coverageDirectory, env); err != nil {
		return run, err
	}
	resultPath := filepath.Join(directory, "jest-results.json")
	args := appendJestArgs(t.command, t.args, "--json", "--outputFile", resultPath, "--coverageDirectory", coverageDirectory)
	if probePath != "" {
		// A single synthetic test cannot meet whole-project coverage thresholds.
		// Keep coverage collection enabled, including for Test Impact Analysis.
		args = append(probeJestArgs(args), "--runTestsByPath", probePath, "--testNamePattern", "^"+probeName+"$")
		run.ProbeCoverageThresholdsDisabled = true
		env["DDTEST_PROBE_MODE"] = probeMode
	}
	_, _ = fmt.Fprintf(output, "Running %s...\n", name)
	run.Command = shellquote.Join(append([]string{t.command}, args...)...)
	commandOutput, commandErr := t.executor.CombinedOutput(ctx, t.command, args, env)
	run.ExitCode = commandExitCode(commandErr)
	if err := os.WriteFile(filepath.Join(directory, testOutputFilename), commandOutput, 0600); err != nil {
		return run, fmt.Errorf("save test output: %w", err)
	}
	closeErr := server.Close()
	closed = true
	if closeErr != nil {
		return run, closeErr
	}
	if err := ctx.Err(); err != nil {
		return run, err
	}
	run.Facts, err = server.Facts()
	if err != nil {
		return run, err
	}
	readJestResults(resultPath, &run)
	if scenario.Feature == "skipping" {
		run.Skipping = diagnoseSkipping(run, scenario)
	}
	if run.ExitCode != 0 || run.ResultError != "" || len(run.SuiteErrors) > 0 {
		run.Diagnostic = commandDiagnostic(commandOutput)
	}
	return run, nil
}

// Preserve only the bounded tail where Jest normally prints its final errors,
// including coverage failures which do not appear in its JSON test results.
func commandDiagnostic(output []byte) string {
	text := []rune(failureSignature(string(output)))
	const limit = 1024
	if len(text) > limit {
		const prefix = "[truncated] …"
		return prefix + string(text[len(text)-(limit-len([]rune(prefix))):])
	}
	return string(text)
}

func configureScenarioEnvironment(env map[string]string, feature string) {
	// Coverage requires ITR negotiation; tests_skipping is separately disabled by
	// the reporting-only settings response. No skippable tests are returned there.
	env["DD_CIVISIBILITY_ITR_ENABLED"] = "true"
	env["DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED"] = fmt.Sprint(feature == "early-flake-detection")
	env["DD_TEST_EARLY_FLAKE_DETECTION_RETRY_COUNT"] = "2"
	env["DD_CIVISIBILITY_FLAKY_RETRY_ENABLED"] = fmt.Sprint(feature == "auto-retries")
	env["DD_CIVISIBILITY_FLAKY_RETRY_COUNT"] = "2"
	env["DD_TEST_MANAGEMENT_ENABLED"] = fmt.Sprint(feature == "quarantine" || feature == "disabled" || feature == "attempt-to-fix")
	env["DD_TEST_MANAGEMENT_ATTEMPT_TO_FIX_RETRIES"] = "2"
}

func executedTests(run validationRun) int {
	count := 0
	for _, test := range run.Tests {
		if test.Status == "passed" || test.Status == "failed" {
			count++
		}
	}
	return count
}

func outcomeKeys(run validationRun) []string {
	keys := []string{fmt.Sprintf("exit=%d", run.ExitCode), "result=" + run.ResultError}
	keys = append(keys, run.SuiteErrors...)
	for _, test := range run.Tests {
		keys = append(keys, test.File+" › "+test.Name+": "+test.Status+"\n"+test.Failure)
	}
	slices.Sort(keys)
	return keys
}

func compareJest(baseline, instrumented validationRun) verdict {
	if baseline.ResultError != "" || executedTests(baseline) == 0 {
		return verdict{Status: "inconclusive", Reason: "The uninstrumented run did not produce results for any executed tests; check project setup using the recorded baseline command."}
	}
	if baseline.Facts.TestEventCount > 0 {
		return verdict{Status: "inconclusive", Reason: "The baseline emitted test telemetry despite instrumentation being disabled; a clean comparison was not established."}
	}
	left, right := outcomeKeys(baseline), outcomeKeys(instrumented)
	if !slices.Equal(left, right) {
		differences := []string{}
		counts := make(map[string]int)
		for _, key := range left {
			counts[key]++
		}
		for _, key := range right {
			counts[key]--
		}
		for key, count := range counts {
			if count == 0 {
				continue
			}
			label, _, _ := strings.Cut(key, "\n")
			side := "Without instrumentation only"
			if count < 0 {
				side = "With instrumentation only"
			}
			differences = append(differences, side+": "+label)
		}
		slices.Sort(differences)
		return verdict{Status: "suspected regression", Reason: "Uninstrumented and reporting-only results differ.", Differences: differences}
	}
	if reason := verifyJestTelemetry(instrumented); reason != "" {
		return verdict{Status: "inconclusive", Reason: reason}
	}
	return verdict{Status: "compatible", Reason: "No observed behavioral regression: test identities, outcomes, failure details, and exit code match. Existing test failures are acceptable; reporting telemetry was verified."}
}

func verifyJestTelemetry(run validationRun) string {
	if run.Facts.TestEventCount == 0 {
		return "The instrumented run sent no test events; matching test outcomes do not prove instrumentation worked."
	}
	if len(run.Facts.ConfigurationErrors) > 0 {
		return "The tracer reported configuration errors: " + strings.Join(run.Facts.ConfigurationErrors, ", ")
	}
	expected := make(map[string]int)
	for _, test := range run.Tests {
		status := map[string]string{"passed": "pass", "failed": "fail"}[test.Status]
		if status != "" {
			expected[test.File+"\x00"+test.Name+"\x00"+status]++
		}
	}
	observed := make(map[string]int)
	for _, test := range run.Facts.Tests {
		file := test.SourceFile
		if file == "" {
			file = test.Suite
		}
		if !filepath.IsAbs(file) {
			file = filepath.Join(run.root, file)
		}
		file = filepath.Clean(file)
		for _, attempt := range test.Attempts {
			if attempt.Status == "pass" || attempt.Status == "fail" {
				observed[file+"\x00"+test.Name+"\x00"+attempt.Status]++
			}
			if attempt.Retry {
				return "Unexpected retry telemetry in the reporting-only run."
			}
		}
	}
	if len(expected) != len(observed) {
		return "Reported telemetry does not match the executed Jest tests."
	}
	for key, count := range expected {
		if observed[key] != count {
			return "Reported telemetry does not match the executed Jest tests."
		}
	}
	return ""
}

func (t *Testdrive) runJestValidation(ctx context.Context, output io.Writer, session *Session, preload string, result *validationResult) error {
	baseline, err := t.runJest(ctx, output, session, preload, "baseline", false, intake.Scenario{}, "", "")
	result.Runs = append(result.Runs, baseline.summary())
	if err != nil {
		return err
	}
	instrumented, err := t.runJest(ctx, output, session, preload, "reporting-only", true, intake.Scenario{}, "", "")
	result.Runs = append(result.Runs, instrumented.summary())
	if err != nil {
		return err
	}
	result.Compatibility = compareJest(baseline, instrumented)
	if result.Compatibility.Status == "suspected regression" {
		baselineAgain, err := t.runJest(ctx, output, session, preload, "baseline-repeat", false, intake.Scenario{}, "", "")
		result.Runs = append(result.Runs, baselineAgain.summary())
		if err != nil {
			return err
		}
		instrumentedAgain, err := t.runJest(ctx, output, session, preload, "reporting-only-repeat", true, intake.Scenario{}, "", "")
		result.Runs = append(result.Runs, instrumentedAgain.summary())
		if err != nil {
			return err
		}
		if !slices.Equal(outcomeKeys(baseline), outcomeKeys(baselineAgain)) || !slices.Equal(outcomeKeys(instrumented), outcomeKeys(instrumentedAgain)) {
			result.Compatibility = verdict{Status: "inconclusive", Reason: "Outcomes changed between repeated runs; flakiness or changing setup prevents attributing the difference to instrumentation."}
		}
	}
	if err := t.runJestFeatures(ctx, output, session, preload, result); err != nil {
		return err
	}
	return nil
}
