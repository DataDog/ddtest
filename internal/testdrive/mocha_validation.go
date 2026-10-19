// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"context"
	_ "embed"
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

//go:embed scripts/mocha_observer.cjs
var mochaObserver string

func (t *Testdrive) checkMochaPreflight(ctx context.Context, output io.Writer, result *validationResult) error {
	check := &jestPreflight{Node: t.nodeVersion(), Command: shellquote.Join(append([]string{t.command}, t.args...)...), Verdict: verdict{Status: "inconclusive", Reason: "Mocha package was not resolved."}}
	result.Preflight = check
	env := testEnvironment("http://127.0.0.1:1", "mocha-preflight")
	env["NODE_OPTIONS"] = stripDatadogNodeOptions(os.Getenv("NODE_OPTIONS"))
	env["DD_CIVISIBILITY_ENABLED"], env["DD_TRACE_ENABLED"] = "false", "false"
	data, err := t.executor.CombinedOutput(ctx, "node", []string{"-p", "require('mocha/package.json').version"}, env)
	if err != nil {
		return fmt.Errorf("inspect Mocha: %w: %s", err, commandDiagnostic(data))
	}
	check.Version = strings.TrimSpace(string(data))
	installer, ok := t.platform.(*platform.JavaScript)
	if !ok {
		return fmt.Errorf("mocha requires the JavaScript tracer selector")
	}
	selection, err := installer.ResolveTestdriveTracer(ctx, platform.TracerOptions{Version: t.tracerVersion, Command: t.command, Args: t.args})
	result.Selection = &selection
	if err != nil {
		check.Verdict.Reason = "Tracer metadata unavailable: " + reportText(err.Error())
		return err
	}
	result.Tracer, result.TracerSource = "dd-trace@"+selection.Version, selection.Source
	result.CISelection = compareCISelection(*result.CIRuntime, selection.Version)
	check.Verdict = checkMochaSupport(*check, selection)
	_, _ = fmt.Fprintf(output, "Mocha %s; Node %s; tracer %s (%s). Preflight: %s — %s\n", check.Version, check.Node, result.Tracer, selection.Source, check.Verdict.Status, check.Verdict.Reason)
	t.recommendTracer(ctx, output, result)
	if check.Verdict.Status != "compatible" {
		return fmt.Errorf("mocha preflight: %s", check.Verdict.Reason)
	}
	return nil
}

func checkMochaSupport(check jestPreflight, selection platform.JSSelection) verdict {
	major, minor, known := releaseParts(check.Version)
	tracerMajor, _, tracerKnown := releaseParts(selection.Version)
	if !known || !tracerKnown {
		return verdict{Status: "inconclusive", Reason: "Could not resolve the installed Mocha and selected tracer versions."}
	}
	// Instrumentation boundaries, not a validation-version allowlist.
	if known && tracerKnown && ((tracerMajor >= 6 && major < 8) || major < 5 || (major == 5 && minor < 2)) {
		return verdict{Status: "incompatible", Reason: "dd-trace 6 requires Mocha >=8; dd-trace 5 requires Mocha >=5.2. Preserve the test matrix and select a compatible tracer."}
	}
	status, reason := onboard.CompareNodeRequirement(check.Node, selection.Node)
	if status != "compatible" {
		return verdict{Status: status, Reason: reason}
	}
	return verdict{Status: "compatible", Reason: "No known tracer/runtime incompatibility blocks Mocha. Paired execution and feature probes are still required."}
}

func (t *Testdrive) runMocha(ctx context.Context, output io.Writer, session *Session, preload, name string, instrumented bool, scenario intake.Scenario, probePath, probeMode string) (run validationRun, runErr error) {
	run.framework, run.root = "mocha", t.repositoryRoot
	run.Name, run.Instrumented, run.ProbeMode = name, instrumented, probeMode
	directory := filepath.Join(session.Directory(), name)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return run, err
	}
	results := filepath.Join(directory, "native-results")
	if err := os.MkdirAll(results, 0700); err != nil {
		return run, err
	}
	observer := filepath.Join(directory, "mocha-observer.cjs")
	if err := os.WriteFile(observer, []byte(mochaObserver), 0600); err != nil {
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
	if err := run.ownTemporaryDirectory(directory, env); err != nil {
		return run, err
	}
	configureScenarioEnvironment(env, scenario.Feature)
	// Companion tools such as Standard use XDG caches outside the checkout.
	// Keep their scratch inside the same disposable run as Mocha.
	env["XDG_CACHE_HOME"] = filepath.Join(run.temporaryRoot, "cache")
	if !instrumented {
		env["NODE_OPTIONS"] = stripDatadogNodeOptions(os.Getenv("NODE_OPTIONS"))
		env["DD_CIVISIBILITY_ENABLED"], env["DD_TRACE_ENABLED"] = "false", "false"
	}
	env["NODE_OPTIONS"] += " --require=" + strconv.Quote(observer)
	env["DDTEST_MOCHA_RESULTS"] = results
	env["DDTEST_MOCHA_PROBE"], env["DDTEST_PROBE_MODE"] = probePath, probeMode
	if probePath != "" {
		// Partial probes cannot meet full-suite coverage thresholds. Baseline and
		// reporting-only executions retain every original threshold.
		env["NYC_CHECK_COVERAGE"], env["C8_CHECK_COVERAGE"] = "false", "false"
		run.ProbeCoverageThresholdsDisabled = true
	}
	command, args := t.executionCommand()
	if probePath != "" {
		original := shellquote.Join(append([]string{command}, args...)...)
		probeCommand := onboard.MochaProbeCommand(t.repositoryRoot, original)
		if probeCommand != original {
			words, err := shellquote.Split(probeCommand)
			if err != nil || len(words) == 0 {
				return run, fmt.Errorf("invalid Mocha probe command: %s", probeCommand)
			}
			command, args = words[0], words[1:]
			run.ProbeLintSkipped = true
			run.Diagnostic = "Synthetic probe uses the resolved Mocha entry point; companion lint and auxiliary checks ran only in the full-suite comparison."
		}
	}
	if filepath.Base(command) == command {
		local := filepath.Join(t.repositoryRoot, "node_modules", ".bin", command)
		if info, err := os.Stat(local); err == nil && info.Mode().IsRegular() && info.Mode()&0111 != 0 {
			command = local
		}
	}
	// Keep wrapper executables available exactly as in an npm script.
	env["PATH"] = filepath.Join(t.repositoryRoot, "node_modules", ".bin") + string(os.PathListSeparator) + os.Getenv("PATH")
	run.Command = shellquote.Join(append([]string{command}, args...)...)
	_, _ = fmt.Fprintf(output, "Running %s...\n", name)
	data, commandErr := t.executor.CombinedOutput(ctx, command, args, env)
	run.commandOutput, run.commandError, run.ExitCode = string(data), commandErr, commandExitCode(commandErr)
	if commandErr != nil && probePath == "" {
		writeCommandFailure(output, "Mocha "+name, data)
	}
	err = server.Close()
	closed = true
	if err != nil {
		return run, err
	}
	if err := ctx.Err(); err != nil {
		return run, err
	}
	run.Facts, err = server.Facts()
	if err != nil {
		return run, err
	}
	readMochaResults(results, &run)
	if scenario.Feature == "skipping" {
		run.Skipping = diagnoseSkipping(run, scenario)
	}
	if run.ExitCode != 0 || run.ResultError != "" || len(run.SuiteErrors) > 0 {
		run.Diagnostic = commandDiagnostic(data)
	}
	return run, nil
}

func readMochaResults(directory string, run *validationRun) {
	paths, err := filepath.Glob(filepath.Join(directory, "*.json"))
	if err != nil || len(paths) == 0 {
		run.ResultError = "Mocha did not produce native runner results"
		return
	}
	for _, path := range paths {
		var result struct {
			Complete bool
			Tests    []jestTest
			Errors   []string
		}
		data, err := os.ReadFile(path)
		if err != nil || json.Unmarshal(data, &result) != nil || !result.Complete || result.Tests == nil {
			run.ResultError = "Mocha native runner results are missing, invalid or incomplete"
			return
		}
		for _, test := range result.Tests {
			test.Failure = failureSignature(test.Failure)
			run.Tests = append(run.Tests, test)
		}
		run.SuiteErrors = append(run.SuiteErrors, result.Errors...)
	}
}

func (t *Testdrive) runMochaFeatures(ctx context.Context, output io.Writer, session *Session, preload string, baseline validationRun, result *validationResult) (runErr error) {
	// Mocha TIA selects suites. Keep the shared full test name while placing
	// the test in an ordinary suite, including under the TDD interface.
	source := "const suite = typeof describe === 'function' ? describe : globalThis.suite;\n" +
		"suite('ddtest', () => {\nconst test = typeof it === 'function' ? it : globalThis.test;\n" +
		strings.Replace(probeSource, "test('ddtest validation probe'", "test('validation probe'", 1) + "\n});\n"
	probe, err := createProbe(t.repositoryRoot, baseline, source)
	if err != nil {
		unavailableFeatures(result, err.Error())
		return nil
	}
	defer func() { runErr = errors.Join(runErr, removeProbe(t.repositoryRoot, probe)) }()
	return runProbeFeatures(func(name string, scenario intake.Scenario, mode string) (validationRun, error) {
		return t.runMocha(ctx, output, session, preload, name, true, scenario, probe, mode)
	}, result)
}
