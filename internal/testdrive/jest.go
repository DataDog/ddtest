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
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
	"github.com/kballard/go-shellquote"
)

type jestResults struct {
	TestResults []struct {
		Name             string `json:"name"`
		Message          string `json:"message"`
		Status           string `json:"status"`
		AssertionResults []struct {
			FullName        string   `json:"fullName"`
			Title           *string  `json:"title"`
			AncestorTitles  []string `json:"ancestorTitles"`
			Status          string   `json:"status"`
			FailureMessages []string `json:"failureMessages"`
		} `json:"assertionResults"`
	} `json:"testResults"`
}

func readJestResults(path string, run *validationRun) { readRunnerResults(path, "Jest", run) }

func readRunnerResults(path, label string, run *validationRun) {
	data, err := os.ReadFile(path)
	if err != nil {
		run.ResultError = label + " did not produce its JSON results"
		return
	}
	var result jestResults
	if err := json.Unmarshal(data, &result); err != nil {
		run.ResultError = label + " JSON results could not be decoded"
		return
	}
	if result.TestResults == nil {
		run.ResultError = label + " JSON results are missing the testResults array"
		return
	}
	for _, suite := range result.TestResults {
		if len(suite.AssertionResults) == 0 && suite.Status == "failed" {
			run.SuiteErrors = append(run.SuiteErrors, suite.Name+": "+failureSignature(suite.Message))
		} else if suite.Message != "" && suite.Status == "failed" {
			// The aggregate message also contains hook errors after assertions ran.
			// Compare it without treating the intentional probe assertion as setup failure.
			run.SuiteFailures = append(run.SuiteFailures, suite.Name+": "+failureSignature(suite.Message))
		}
		for _, test := range suite.AssertionResults {
			parsed := jestTest{File: filepath.Clean(suite.Name), Name: test.FullName, Status: test.Status,
				Failure: failureSignature(strings.Join(test.FailureMessages, "\n"))}

			if label == "Vitest" && test.Title != nil && test.AncestorTitles != nil {
				// Vitest's JSON fullName omits an empty test title; dd-trace
				// includes it and skips empty ancestor titles. Preserve the
				// runner's identity for outcome comparison and derive only the
				// expected telemetry name, without trimming meaningful spaces.
				var names []string
				for _, ancestor := range test.AncestorTitles {
					if ancestor != "" {
						names = append(names, ancestor)
					}
				}
				name := strings.Join(append(names, *test.Title), " ")
				parsed.telemetryName = &name
			}
			run.Tests = append(run.Tests, parsed)
		}
	}
	slices.Sort(run.SuiteErrors)
}

func appendJestArgs(command string, args []string, additions ...string) []string {
	return framework.WithFrameworkOptions(command, args, "jest", additions...)
}

func jestSeparator(command string, args []string) int {
	start := 0
	if filepath.Base(command) != "jest" {
		for i, arg := range args {
			if filepath.Base(arg) == "jest" {
				start = i + 1
				break
			}
		}
	}
	index := slices.Index(args[start:], "--")
	if index >= 0 {
		index += start
	}
	// npm consumes its first separator when forwarding script arguments.
	if filepath.Base(command) == "npm" && index >= 0 && index == slices.Index(args, "--") {
		if next := slices.Index(args[index+1:], "--"); next >= 0 {
			return index + 1 + next
		}
		return -1
	}
	return index
}

func withJestProbe(command string, args []string, probe string) []string {
	if index := jestSeparator(command, args); index >= 0 {
		args = args[:index+1]
	}
	return appendJestArgs(command, args, "--runTestsByPath", probe)
}

func probeJestArgs(command string, args []string) []string {
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
	return appendJestArgs(command, result, "--coverageThreshold={}")
}

func (t *Testdrive) runJest(ctx context.Context, output io.Writer, session *workspace.Session, preload string, spec runSpec) (run validationRun, runErr error) {
	name, instrumented, scenario, probePath, probeMode := spec.Name, spec.Instrumented, spec.Scenario, spec.ProbePath, spec.ProbeMode
	run.Role = spec.Role
	if scenario.Feature == "" {
		scenario.Feature = "reporting-only"
	}
	run.Name = name
	run.framework = "jest"
	run.Instrumented = instrumented
	run.ProbeMode = probeMode
	run.root = t.checkoutRoot()
	run.checkoutRoot = run.root
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
	if err := run.ownTemporaryDirectory(session.Directory(), env); err != nil {
		return run, err
	}
	configureScenarioEnvironment(env, scenario.Feature)
	if !instrumented {
		env["NODE_OPTIONS"] = stripDatadogNodeOptions(t.nodeOptions())
		env["DD_CIVISIBILITY_ENABLED"] = "false"
		env["DD_TRACE_ENABLED"] = "false"
	}
	// Package scripts can already set coverageDirectory. Jest treats duplicate
	// string options as arrays, so normalize only our override before parsing.
	coverageDirectory := filepath.Join(directory, "coverage")
	resultPath := filepath.Join(directory, "jest-results.json")
	if err := redirectJestArguments(directory, jestArgumentRequest{Coverage: coverageDirectory, Output: resultPath, Probe: probePath, Project: t.jestProject.DisplayName.Name}, env); err != nil {
		return run, err
	}
	args := appendJestArgs(t.command, t.args, "--json", "--outputFile", resultPath, "--coverageDirectory", coverageDirectory)
	if probePath != "" {
		// A single synthetic test cannot meet whole-project coverage thresholds.
		// Keep coverage collection enabled, including for Test Impact Analysis.
		args = withJestProbe(t.command, args, probePath)
		args = appendJestArgs(t.command, probeJestArgs(t.command, args), "--testNamePattern", "^"+probeName+"$")
		env["DDTEST_PROBE_MODE"] = probeMode
		env["DDTEST_JEST_PROBE"] = "1"
		run.ProbeLintSkipped = t.probeLintSkipped
	}
	_, _ = fmt.Fprintf(output, "Running %s...\n", name)
	run.Command = shellquote.Join(append([]string{t.command}, args...)...)
	commandOutput, commandErr := t.executor.CombinedOutput(ctx, t.command, args, env)
	run.commandOutput, run.commandError = string(commandOutput), commandErr
	run.ExitCode = commandExitCode(commandErr)
	if commandErr != nil && probePath == "" {
		writeCommandFailure(output, displayName(t.framework.Name())+" "+name, commandOutput)
	}
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
	run.ProbeCoverageThresholdsDisabled = probePath != "" && probeThresholdOverrideApplied(directory)
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
func compareJest(baseline, instrumented validationRun) verdict {
	return compareRuns(baseline, instrumented, verifyJestTelemetry)
}

func verifyJestTelemetry(run validationRun) verdict {
	return verifyNativeTelemetry(run, false)
}
