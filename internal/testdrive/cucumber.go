// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/kballard/go-shellquote"
)

func (t *Testdrive) checkCucumberPreflight(ctx context.Context, output io.Writer, result *validationResult) error {
	check := &jestPreflight{Node: t.nodeVersion(), Command: shellquote.Join(append([]string{t.command}, t.args...)...), Verdict: verdict{Status: "inconclusive", Reason: "Cucumber package was not resolved."}}
	result.Preflight = check
	env := testEnvironment("http://127.0.0.1:1", "cucumber-preflight")
	env["NODE_OPTIONS"] = stripDatadogNodeOptions(os.Getenv("NODE_OPTIONS"))
	env["DD_CIVISIBILITY_ENABLED"], env["DD_TRACE_ENABLED"] = "false", "false"
	data, err := t.executor.CombinedOutput(ctx, "node", []string{"-e", "console.log('DDTEST_CUCUMBER_VERSION='+require('@cucumber/cucumber/package.json').version)"}, env)
	if err != nil {
		return fmt.Errorf("inspect Cucumber: %w: %s", err, commandDiagnostic(data))
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "DDTEST_CUCUMBER_VERSION=") {
			check.Version = strings.TrimSpace(strings.TrimPrefix(line, "DDTEST_CUCUMBER_VERSION="))
		}
	}
	installer, ok := t.platform.(*platform.JavaScript)
	if !ok {
		return fmt.Errorf("cucumber requires the JavaScript tracer selector")
	}
	selection, err := installer.ResolveTestdriveTracer(ctx, platform.TracerOptions{Version: t.tracerVersion, Command: t.command, Args: t.args})
	result.Selection = &selection
	if err != nil {
		check.Verdict.Reason = "Tracer metadata unavailable: " + reportText(err.Error())
		return err
	}
	result.Tracer, result.TracerSource = "dd-trace@"+selection.Version, selection.Source
	result.CISelection = compareCISelection(*result.CIRuntime, selection.Version)
	check.Verdict = checkCucumberSupport(*check, selection)
	_, _ = fmt.Fprintf(output, "Cucumber %s; Node %s; tracer %s (%s). Preflight: %s — %s\n", check.Version, check.Node, result.Tracer, selection.Source, check.Verdict.Status, check.Verdict.Reason)
	t.recommendTracer(ctx, output, result)
	if check.Verdict.Status != "compatible" {
		return fmt.Errorf("cucumber preflight: %s", check.Verdict.Reason)
	}
	return nil
}

func checkCucumberSupport(check jestPreflight, selection platform.JSSelection) verdict {
	major, _, known := releaseParts(check.Version)
	if !known {
		return verdict{Status: "inconclusive", Reason: "Could not resolve the installed Cucumber version."}
	}
	if major < 7 {
		return verdict{Status: "incompatible", Reason: "dd-trace instruments @cucumber/cucumber >=7. Preserve the customer's framework and select a supported configuration."}
	}
	status, reason := onboard.CompareNodeRequirement(check.Node, selection.Node)
	if status != "compatible" {
		return verdict{Status: status, Reason: reason}
	}
	return verdict{Status: "compatible", Reason: "No known Cucumber/tracer runtime incompatibility. Paired execution and controlled feature checks are still required."}
}

// Read Cucumber's public message formatter, including native hooks and retries.
// A final envelope is mandatory: partial files and crashes cannot look passed.
func readCucumberResults(filename string, run *validationRun) {
	file, err := os.Open(filename)
	if err != nil {
		run.ResultError = "Cucumber did not produce native runner results"
		return
	}
	defer func() { _ = file.Close() }()
	type pickle struct{ ID, URI, Name string }
	type attempt struct {
		ID, TestCaseID  string
		Status, Failure string
		WillBeRetried   bool
	}
	pickles := map[string]pickle{}
	cases := map[string]string{}
	attempts := map[string]*attempt{}
	var finished []string
	complete := false
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 16<<20)
	for scanner.Scan() {
		var envelope struct {
			Pickle           *pickle
			TestCase         *struct{ ID, PickleID string }
			TestCaseStarted  *struct{ ID, TestCaseID string }
			TestStepFinished *struct {
				TestCaseStartedID string
				TestStepResult    struct{ Status, Message string }
			}
			TestCaseFinished *struct {
				TestCaseStartedID string
				WillBeRetried     bool
			}
			TestRunFinished *struct {
				Success   bool
				Exception *struct{ Message string }
			}
		}
		if err := json.Unmarshal(scanner.Bytes(), &envelope); err != nil {
			run.ResultError = "Cucumber native messages are invalid"
			return
		}
		if p := envelope.Pickle; p != nil {
			pickles[p.ID] = *p
		}
		if c := envelope.TestCase; c != nil {
			cases[c.ID] = c.PickleID
		}
		if a := envelope.TestCaseStarted; a != nil {
			attempts[a.ID] = &attempt{ID: a.ID, TestCaseID: a.TestCaseID, Status: "passed"}
		}
		if step := envelope.TestStepFinished; step != nil {
			if a := attempts[step.TestCaseStartedID]; a != nil {
				switch step.TestStepResult.Status {
				case "FAILED", "AMBIGUOUS", "UNDEFINED", "PENDING":
					a.Status = "failed"
					a.Failure += step.TestStepResult.Status + ": " + step.TestStepResult.Message + "\n"
				case "SKIPPED":
					if a.Status != "failed" {
						a.Status = "skipped"
					}
				}
			}
		}
		if a := envelope.TestCaseFinished; a != nil {
			if value := attempts[a.TestCaseStartedID]; value != nil {
				value.WillBeRetried = a.WillBeRetried
				finished = append(finished, a.TestCaseStartedID)
			}
		}
		if end := envelope.TestRunFinished; end != nil {
			complete = true
			if end.Exception != nil {
				run.SuiteErrors = append(run.SuiteErrors, end.Exception.Message)
			}
		}
	}
	if scanner.Err() != nil || !complete || len(finished) != len(attempts) {
		run.ResultError = "Cucumber native results are incomplete"
		return
	}
	for _, id := range finished {
		a := attempts[id]
		if a.WillBeRetried {
			continue
		}
		p, ok := pickles[cases[a.TestCaseID]]
		if !ok {
			run.ResultError = "Cucumber native results reference an unknown scenario"
			return
		}
		name := p.Name
		run.Tests = append(run.Tests, jestTest{File: filepath.Clean(filepath.Join(run.root, p.URI)), Name: name, Status: a.Status, Failure: failureSignature(a.Failure)})
	}
}

func (t *Testdrive) runCucumber(ctx context.Context, output io.Writer, session *Session, preload, name string, instrumented bool, scenario intake.Scenario, probePath, probeMode string) (run validationRun, runErr error) {
	run.framework, run.root, run.Name, run.Instrumented, run.ProbeMode = "cucumber", t.repositoryRoot, name, instrumented, probeMode
	directory := filepath.Join(session.Directory(), name)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return run, err
	}
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
	env["CUCUMBER_PUBLISH_ENABLED"] = "false"
	if !instrumented {
		env["NODE_OPTIONS"] = stripDatadogNodeOptions(os.Getenv("NODE_OPTIONS"))
		env["DD_CIVISIBILITY_ENABLED"], env["DD_TRACE_ENABLED"] = "false", "false"
	}
	loader := filepath.Join(t.repositoryRoot, "node_modules/@istanbuljs/esm-loader-hook/index.js")
	if info, err := os.Stat(loader); err == nil && info.Mode().IsRegular() && !strings.Contains(env["NODE_OPTIONS"], "esm-loader-hook") {
		// NYC's require hook cannot instrument native ESM. Use the installed
		// application loader equally in both modes; never install one silently.
		env["NODE_OPTIONS"] += " --experimental-loader " + strconv.Quote(loader)
	}
	command, args := t.command, append([]string{}, t.args...)
	if probePath != "" {
		command, args, err = t.cucumberProbeCommand(probePath)
		if err != nil {
			return run, err
		}
		env["DDTEST_PROBE_MODE"], env["DDTEST_PROBE_COUNTER"] = probeMode, filepath.Join(directory, "probe-bodies")
		env["NYC_CHECK_COVERAGE"], env["C8_CHECK_COVERAGE"] = "false", "false"
		run.ProbeCoverageThresholdsDisabled = true
	}
	results := filepath.Join(directory, "native.ndjson")
	args = appendCucumberOptions(command, args, "--format", "message:"+results)
	selected := *t
	selected.command, selected.args = command, args
	command, args = selected.executionCommand()
	env["PATH"] = filepath.Join(t.repositoryRoot, "node_modules/.bin") + string(os.PathListSeparator) + os.Getenv("PATH")
	run.Command = shellquote.Join(append([]string{command}, args...)...)
	_, _ = fmt.Fprintf(output, "Running %s...\n", name)
	data, commandErr := t.executor.CombinedOutput(ctx, command, args, env)
	run.commandOutput, run.commandError, run.ExitCode = string(data), commandErr, commandExitCode(commandErr)
	if err := server.Close(); err != nil {
		return run, err
	}
	closed = true
	if err := ctx.Err(); err != nil {
		return run, err
	}
	run.Facts, err = server.Facts()
	if err != nil {
		return run, err
	}
	readCucumberResults(results, &run)
	if instrumented && probePath == "" {
		run.Coverage = cucumberSourceCoverage(t.repositoryRoot, run.Facts, run.Tests)
	}
	if probePath != "" {
		data, _ := os.ReadFile(env["DDTEST_PROBE_COUNTER"])
		count := strings.Count(string(data), "\n")
		run.ProbeExecutions = &count
	}
	if scenario.Feature == "skipping" {
		run.Skipping = diagnoseSkipping(run, scenario)
	}
	if run.ExitCode != 0 || run.ResultError != "" {
		run.Diagnostic = commandDiagnostic(data)
	}
	return run, nil
}
