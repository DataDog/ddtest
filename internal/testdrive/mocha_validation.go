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

//go:embed scripts/mocha_nested_guard.cjs
var mochaNestedGuard string

func (t *Testdrive) checkMochaPreflight(ctx context.Context, output io.Writer, result *validationResult) error {
	check := &jestPreflight{Node: t.nodeVersion(), Command: shellquote.Join(append([]string{t.command}, t.args...)...), Verdict: verdict{Status: "inconclusive", Reason: "Mocha package was not resolved."}}
	result.Preflight = check
	env := testEnvironment("http://127.0.0.1:1", "mocha-preflight")
	// Load .mocharc with the script's own assignments, such as MODE=unit.
	for key, value := range onboard.MochaProbePlan(t.repositoryRoot, check.Command).Environment {
		env[key] = value
	}
	env["NODE_OPTIONS"] = stripDatadogNodeOptions(t.nodeOptions())
	env["DD_CIVISIBILITY_ENABLED"], env["DD_TRACE_ENABLED"] = "false", "false"
	directory := onboard.MochaRunnerDirectory(t.repositoryRoot, check.Command)
	args, _ := json.Marshal(onboard.MochaRunnerArguments(t.repositoryRoot, check.Command))
	inspect := `const path = require('node:path')
const fs = require('node:fs')
const manifest = require.resolve('mocha/package.json')
const options = path.join(path.dirname(manifest), 'lib/cli/options')
const config = require(fs.existsSync(options + '.js') ? options + '.js' : options + '.cjs')
  .loadOptions(JSON.parse(process.argv[1]) || [])
console.log(JSON.stringify({version: require(manifest).version, watch: !!(config && config.watch)}))`
	executor, ok := t.executor.(directoryExecutor)
	if !ok {
		return fmt.Errorf("mocha preflight requires a directory-aware executor")
	}
	data, err := executor.CombinedOutputInDirectory(ctx, directory, "node", []string{"-e", inspect, string(args)}, env)
	if err != nil {
		return fmt.Errorf("inspect Mocha: %w: %s", err, commandDiagnostic(data))
	}
	var metadata struct {
		Version string
		Watch   bool
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		return fmt.Errorf("inspect Mocha options: %w", err)
	}
	check.Version = metadata.Version
	if metadata.Watch {
		return fmt.Errorf("mocha watch mode is not supported; select a finite test command")
	}
	installer, ok := t.platform.(*platform.JavaScript)
	if !ok {
		return fmt.Errorf("mocha requires the JavaScript tracer selector")
	}
	selection, err := installer.ResolveTestdriveTracer(ctx, platform.TracerOptions{Version: t.tracerVersion, Command: t.command, Args: t.args, Environment: t.configurationEnvironment})
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
	// A git: fallback has no version until installation; Run checks it again then.
	if check.Verdict.Status != "compatible" && (selection.Version != "" || !strings.HasPrefix(selection.Requested, "git:") || t.checkOnly) {
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
	if (tracerMajor >= 6 && major < 8) || major < 5 || (major == 5 && minor < 2) {
		return verdict{Status: "incompatible", Reason: "dd-trace 6 requires Mocha >=8; dd-trace 5 requires Mocha >=5.2. Preserve the test matrix and select a compatible tracer."}
	}
	status, reason := onboard.CompareNodeRequirement(check.Node, selection.Node)
	if status != "compatible" {
		return verdict{Status: status, Reason: reason}
	}
	return verdict{Status: "compatible", Reason: "No known tracer/runtime incompatibility blocks Mocha. Paired execution and feature probes are still required."}
}

func (t *Testdrive) runMocha(ctx context.Context, output io.Writer, session *Session, preload string, spec runSpec) (run validationRun, runErr error) {
	name, instrumented, scenario, probePath, probeMode := spec.Name, spec.Instrumented, spec.Scenario, spec.ProbePath, spec.ProbeMode
	run.Role = spec.Role
	run.framework, run.root = "mocha", t.checkoutRoot()
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
	guard := filepath.Join(directory, "mocha-nested-guard.cjs")
	if err := os.WriteFile(guard, []byte(mochaNestedGuard), 0600); err != nil {
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
	// Companion tools such as Standard use XDG caches outside the checkout.
	// Keep their scratch inside the same disposable run as Mocha.
	env["XDG_CACHE_HOME"] = filepath.Join(run.temporaryRoot, "cache")
	if !instrumented {
		env["NODE_OPTIONS"] = stripDatadogNodeOptions(t.nodeOptions())
		env["DD_CIVISIBILITY_ENABLED"], env["DD_TRACE_ENABLED"] = "false", "false"
	}
	// The guard must run before the tracer preload; the observer after it.
	env["NODE_OPTIONS"] = "--require=" + strconv.Quote(guard) + " " + env["NODE_OPTIONS"] + " --require=" + strconv.Quote(observer)
	env["DDTEST_MOCHA_RESULTS"] = results
	env["DDTEST_MOCHA_PROBE"], env["DDTEST_PROBE_MODE"] = probePath, probeMode
	command, args := t.executionCommand()
	directoryRoot := t.repositoryRoot
	if probePath == "" && instrumented {
		run.coverageCause = mochaCoverageCause(onboard.MochaCoverageSetup(t.repositoryRoot, shellquote.Join(append([]string{command}, args...)...)))
	}
	if probePath != "" {
		original := shellquote.Join(append([]string{command}, args...)...)
		plan := onboard.MochaProbePlan(t.repositoryRoot, original)
		words, err := shellquote.Split(plan.Command)
		if err != nil || len(words) == 0 {
			return run, fmt.Errorf("invalid Mocha probe command: %s", plan.Command)
		}
		command, args = words[0], words[1:]
		directoryRoot = filepath.Join(t.repositoryRoot, plan.Directory)
		for key, value := range plan.Environment {
			env[key] = value
		}
		run.ProbeCoverageThresholdsDisabled = plan.ProbeCoverageThresholdsDisabled
		run.ProbeLintSkipped = plan.Command != original
		if run.ProbeLintSkipped {
			run.Diagnostic = "Synthetic probe uses the resolved Mocha entry point; companion checks and lifecycle hooks ran only in the full-suite comparison."
		}
	}
	if filepath.Base(command) == command {
		local := filepath.Join(directoryRoot, "node_modules", ".bin", command)
		if info, err := os.Stat(local); err == nil && info.Mode().IsRegular() && info.Mode()&0111 != 0 {
			command = local
		}
	}
	// Keep wrapper executables available exactly as in an npm script.
	// A literal CI PATH stays in effect, as it does in the selected configuration.
	path := os.Getenv("PATH")
	if value, ok := t.configurationEnvironment["PATH"]; ok {
		path = value
	}
	env["PATH"] = filepath.Join(directoryRoot, "node_modules", ".bin") + string(os.PathListSeparator) + path
	run.Command = shellquote.Join(append([]string{command}, args...)...)
	_, _ = fmt.Fprintf(output, "Running %s...\n", name)
	var data []byte
	var commandErr error
	if directoryRoot != t.repositoryRoot {
		executor, ok := t.executor.(directoryExecutor)
		if !ok {
			return run, fmt.Errorf("mocha probes require a directory-aware executor")
		}
		data, commandErr = executor.CombinedOutputInDirectory(ctx, directoryRoot, command, args, env)
		run.Command = "cd " + shellquote.Join(directoryRoot) + " && " + run.Command
	} else {
		data, commandErr = t.executor.CombinedOutput(ctx, command, args, env)
	}
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
	if errors.Is(commandErr, context.DeadlineExceeded) {
		run.ResultError = "Mocha command exceeded its per-command timeout; review delayed/setup commands or adjust --timeout before retrying."
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
			Delayed  bool
			Tests    []jestTest
			Errors   []string
		}
		data, err := os.ReadFile(path)
		if err == nil && json.Unmarshal(data, &result) == nil && !result.Complete && result.Delayed {
			run.ResultError = "Mocha delay mode waits for a global run() call, which the suite never made; call run() once the tests are defined, or remove delay"
			return
		}
		if err != nil || json.Unmarshal(data, &result) != nil || !result.Complete || result.Tests == nil {
			run.ResultError = "Mocha native runner results are missing, invalid or incomplete"
			return
		}
		for _, test := range result.Tests {
			if test.File == "" {
				run.ResultError = "Mocha did not report a source file for a native test; identity comparison is inconclusive"
			}

			test.Failure = failureSignature(test.Failure)
			run.Tests = append(run.Tests, test)
		}
		run.SuiteErrors = append(run.SuiteErrors, result.Errors...)
	}
}

func (t *Testdrive) runMochaFeatures(ctx context.Context, output io.Writer, session *Session, preload string, baseline validationRun, result *validationResult) (runErr error) {
	source := mochaProbeSource
	probe, err := createProbe(t.repositoryRoot, baseline, source)
	if err != nil {
		unavailableFeatures(result, err.Error())
		return nil
	}
	defer func() { runErr = errors.Join(runErr, removeProbe(t.repositoryRoot, probe)) }()
	return runProbeFeatures(func(name string, scenario intake.Scenario, mode string) (validationRun, error) {
		role := roleProbeFeature
		if scenario.Feature == "" {
			role = roleProbeControl
		}
		// dd-trace's Mocha plugin matches skippable suites by the suite path,
		// relative to Mocha's working directory, not the checkout-relative
		// source file. They differ when Mocha runs in a subpackage.
		if scenario.Feature == "skipping" && scenario.Suite != "" {
			scenario.SourceFile = scenario.Suite
		}
		return t.runMocha(ctx, output, session, preload, runSpec{Name: name, Role: role, Instrumented: true, Scenario: scenario, ProbePath: probe, ProbeMode: mode})
	}, result)
}

// Keep this independent of Jest APIs and verify its title with real Mocha.
const mochaProbeSource = `const ddtestSuite = typeof describe === 'function' ? describe : globalThis.suite;
const ddtestTest = typeof it === 'function' ? it : globalThis.test;
ddtestSuite('ddtest', () => {
  let attempts = 0;
  ddtestTest('validation probe', () => {
    attempts++;
    if (process.env.DDTEST_PROBE_MODE === 'fail' ||
        (process.env.DDTEST_PROBE_MODE === 'fail-once' && attempts === 1)) {
      throw new Error('ddtest intentional failure');
    }
  });
});
`

const mochaESMCoverageCause = "Native ESM coverage is not supported by NYC's require hook. Tracing imports do not instrument source coverage; review a separate ESM instrumentation step."

// Name the likely cause of missing per-test coverage for this command, so the
// report does not suggest installing or adding an nyc that is already there.
func mochaCoverageCause(wrapper string, parallel, esm bool) string {
	switch {
	case parallel:
		return "Mocha parallel mode reports no per-test coverage: dd-trace sends none from parallel workers. Check coverage with a command that does not use --parallel."
	case wrapper == "c8":
		return "c8 collects V8 coverage, which dd-trace does not report per test. Use nyc for Mocha per-test coverage."
	case esm:
		return mochaESMCoverageCause
	case wrapper == "":
		return "The command does not run under nyc, so no Istanbul coverage was collected. Wrap the Mocha command with npx --no-install nyc --."
	}
	return "nyc ran, but no test reported coverage. Check nyc's include/exclude rules, file extensions and instrument setting."
}
