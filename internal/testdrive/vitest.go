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
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/Masterminds/semver/v3"
	"github.com/kballard/go-shellquote"
)

//go:embed scripts/vitest_config.mjs
var vitestConfigScript string

type vitestProject struct {
	Name      string `json:"name"`
	Root      string `json:"root"`
	Browser   bool   `json:"browser"`
	Pool      string `json:"pool"`
	Typecheck bool   `json:"typecheck"`
}

// Start with direct CLI invocations. Do not silently discard package-script setup,
// environment assignments, or wrapper flags while loading the effective config.
func vitestArguments(command string, args []string) ([]string, error) {
	if onboard.IsDDTestCommand(command) {
		parsed, err := onboard.ParseVitestRunnerCommand(args)
		if err != nil {
			return nil, err
		}
		options := []string{"run"}
		if parsed.Config != "" {
			options = append(options, "--config", parsed.Config)
		}
		// This inspection only checks the configuration. File selection is
		// verified against the real runner's plan and worker results.
		return options, nil
	}
	base := filepath.Base(command)
	switch base {
	case "vitest", "vitest.mjs":
		return slices.Clone(args), nil
	case "pnpm", "yarn", "npx":
		if base == "npx" && len(args) > 0 && args[0] == "--no-install" {
			args = args[1:]
		}
		if (base == "pnpm" || base == "yarn") && len(args) > 0 && args[0] == "vitest" {
			data, err := os.ReadFile("package.json")
			if err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("inspect package scripts: %w", err)
			}
			if err == nil {
				var manifest struct{ Scripts map[string]string }
				if err := json.Unmarshal(data, &manifest); err != nil {
					return nil, fmt.Errorf("inspect package scripts: %w", err)
				}
				if _, exists := manifest.Scripts["vitest"]; exists {
					return nil, fmt.Errorf("%s vitest selects a package script; use testdrive --all to resolve its setup and configuration, or an explicit %s exec vitest command after reviewing required setup", base, base)
				}
			}
		}
		if base != "npx" && len(args) > 0 && args[0] == "exec" {
			args = args[1:]
		}
		if len(args) > 0 && args[0] == "vitest" {
			return slices.Clone(args[1:]), nil
		}
	case "node":
		if len(args) > 0 && filepath.Base(args[0]) == "vitest.mjs" {
			return slices.Clone(args[1:]), nil
		}
	}
	return nil, fmt.Errorf("vitest validation needs the actual Vitest executable and options in --command; review and run required setup first, preserving CI's config, selection and coverage options")
}

func (t *Testdrive) inspectVitest(ctx context.Context, directory string, probe ...string) (string, []vitestProject, []string, error) {
	command := t.command
	if t.usesVitestRunner() {
		command = "ddtest"
	}
	args, err := vitestArguments(command, t.args)
	if err != nil {
		return "", nil, nil, err
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		return "", nil, nil, err
	}
	env := testEnvironment("http://127.0.0.1:1", "vitest-preflight")
	env["NODE_OPTIONS"] = stripDatadogNodeOptions(t.nodeOptions())
	env["DD_CIVISIBILITY_ENABLED"], env["DD_TRACE_ENABLED"] = "false", "false"
	selectedProbe := ""
	if len(probe) > 0 {
		selectedProbe = probe[0]
	}
	node := "node"
	if filepath.Base(t.command) == "node" || filepath.Base(t.command) == "node.exe" {
		node = t.command
	}
	data, err := t.executor.CombinedOutput(ctx, node, []string{"--input-type=module", "--eval", framework.VitestValidationScript + "\n" + vitestConfigScript, string(encoded), filepath.Join(directory, "vite-cache"), selectedProbe}, env)
	if err != nil {
		return "", nil, nil, fmt.Errorf("inspect Vitest configuration: %w: %s", err, commandDiagnostic(data))
	}
	const marker = "__DDTEST_VITEST_CONFIG__"
	start := strings.LastIndex(string(data), marker)
	if start < 0 {
		return "", nil, nil, fmt.Errorf("vitest configuration did not return its discovery result")
	}
	var config struct {
		Version    string
		Node       string
		Executable string
		Arguments  []string
		Reporters  []any
		Projects   []vitestProject
		Files      []string
	}
	if err := json.NewDecoder(strings.NewReader(string(data[start+len(marker):]))).Decode(&config); err != nil {
		return "", nil, nil, err
	}
	t.vitestNode, t.vitestExecutable = config.Node, config.Executable
	if t.vitestNode != "" {
		version := t.vitestNode
		t.nodeVersion = func() string { return version }
		t.environmentInfo.Version, t.environmentInfo.Executable = version, t.vitestExecutable
	}
	if !t.usesVitestRunner() {
		resolved := append(slices.Clone(t.args[:len(t.args)-len(args)]), config.Arguments...)
		if selectedProbe != "" {
			t.vitestProbeArguments = resolved
		} else {
			t.vitestArguments = resolved
		}
	}
	t.vitestReporters = nil
	for _, reporter := range config.Reporters {
		if tuple, ok := reporter.([]any); ok && len(tuple) > 0 {
			reporter = tuple[0]
		}
		t.vitestReporters = append(t.vitestReporters, fmt.Sprint(reporter))
	}
	return config.Version, config.Projects, config.Files, nil
}

func (t *Testdrive) checkVitestPreflight(ctx context.Context, output io.Writer, result *validationResult) error {
	check := &jestPreflight{Node: t.nodeVersion(), Command: shellquote.Join(append([]string{t.command}, t.args...)...), Verdict: verdict{Status: "inconclusive", Reason: "Vitest configuration was not resolved."}}
	result.Preflight = check
	if t.discoveryError != nil {
		check.Verdict.Reason = t.discoveryError.Error()
		return t.discoveryError
	}
	check.ExecutionPath = t.vitestExecutionPath()
	if err := t.resolveVitestSettings(); err != nil {
		check.Verdict.Reason = err.Error()
		return err
	}
	check.Command = shellquote.Join(append([]string{t.command}, t.args...)...)
	if t.usesVitestRunner() {
		binary := t.inspectVitestRunnerBinary(ctx)
		check.RunnerBinary = &binary
		for _, warning := range binary.Warnings {
			_, _ = fmt.Fprintln(output, warning)
		}
	}

	directory, err := os.MkdirTemp("", "ddtest-vitest-config-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(directory) }()
	check.Version, check.VitestProjects, _, err = t.inspectVitest(ctx, directory)
	if t.vitestNode != "" {
		check.Node, check.Executable = t.vitestNode, t.vitestExecutable
	}
	if err != nil {
		check.Verdict.Reason = "ddtest could not inspect this Vitest configuration: " + reportText(err.Error())
		return err
	}
	installer, ok := t.platform.(*platform.JavaScript)
	if !ok {
		return fmt.Errorf("vitest requires the JavaScript tracer selector")
	}
	selection, err := installer.ResolveTestdriveTracer(ctx, platform.TracerOptions{Version: t.tracerVersion, Command: t.command, Args: t.args, Environment: t.configurationEnvironment})
	result.Selection = &selection
	if err != nil {
		check.Verdict.Reason = "Vitest configuration resolved, but tracer metadata is unavailable: " + reportText(err.Error())
		return err
	}
	result.Tracer, result.TracerSource = "dd-trace@"+selection.Version, selection.Source
	if selection.Source == "project" {
		_, _ = fmt.Fprintln(output, "Using the existing project tracer; --tracer-version applies only when it is absent. Review any dependency upgrade explicitly.")
	}
	if result.CIRuntime != nil {
		result.CISelection = compareCISelection(*result.CIRuntime, selection.Version)
	}
	check.Verdict = checkVitestSupport(*check, selection)
	_, _ = fmt.Fprintf(output, "Vitest %s; Node %s; tracer %s (%s). Preflight: %s — %s\n", check.Version, check.Node, result.Tracer, selection.Source, check.Verdict.Status, check.Verdict.Reason)
	t.recommendTracer(ctx, output, result)
	if check.Verdict.Status != "compatible" && (check.Verdict.Status == "incompatible" || !strings.HasPrefix(selection.Requested, "git:") || t.checkOnly) {
		return fmt.Errorf("vitest preflight: %s", check.Verdict.Reason)
	}
	return nil
}

func checkVitestSupport(check jestPreflight, selection platform.JSSelection) verdict {
	major, minor, known := releaseParts(check.Version)
	tracerMajor, _, tracerKnown := releaseParts(selection.Version)
	// Reject known tracer incompatibilities, not versions absent from an adapter
	// allowlist. Configuration discovery and live checks establish validation.
	// https://docs.datadoghq.com/tests/setup/javascript/#compatibility
	if known && (major < 1 || (major == 1 && minor < 6)) {
		return verdict{Status: "incompatible", Reason: "dd-trace Vitest instrumentation requires Vitest >=1.6.0."}
	}
	if len(check.VitestProjects) == 1 && check.VitestProjects[0].Typecheck {
		return verdict{Status: "inconclusive", Reason: "Vitest typecheck mode is not supported by testdrive; validate the ordinary test command separately."}
	}
	if len(check.VitestProjects) != 1 || check.VitestProjects[0].Browser || !slices.Contains([]string{"forks", "threads"}, check.VitestProjects[0].Pool) {
		return verdict{Status: "inconclusive", Reason: "Vitest validation currently requires one Node project using forks or threads, without Vitest typechecking; preserve other configurations and report them as unvalidated."}
	}
	status, reason := onboard.CompareNodeRequirement(check.Node, selection.Node)
	if status != "compatible" {
		return verdict{Status: status, Reason: reason}
	}
	if tracerKnown && tracerMajor < 5 {
		return verdict{Status: "incompatible", Reason: "This tracer does not support Vitest test skipping; an explicit dependency upgrade is required."}
	}
	tracer, parseErr := semver.StrictNewVersion(selection.Version)
	if parseErr == nil && tracer.Prerelease() == "" {
		for _, requirement := range vitestTracerMinimums {
			if requirement.VitestMajor != 0 && (!known || major != requirement.VitestMajor) || requirement.ExecutionPath != "" && check.ExecutionPath != requirement.ExecutionPath {
				continue
			}
			minimum, ok := requirement.Tracers[tracerMajor]
			if !ok {
				continue
			}
			floor, _ := semver.StrictNewVersion(minimum)
			if tracer.LessThan(floor) {
				return verdict{Status: "incompatible", Reason: fmt.Sprintf("dd-trace %s does not support Vitest test skipping and validation for Vitest %s on %s. This requires dd-trace >=%s. An installed project tracer takes precedence and requires an explicit dependency upgrade; no dependency was changed.", selection.Version, check.Version, check.ExecutionPath, minimum)}
			}
		}
	}
	return verdict{Status: "compatible", Reason: "No known tracer/runtime incompatibility blocks this configuration. Paired execution and feature checks are still required."}
}

func (t *Testdrive) runVitest(ctx context.Context, output io.Writer, session *Session, preload string, spec runSpec) (run validationRun, runErr error) {
	name, instrumented, scenario, probePath, probeMode := spec.Name, spec.Instrumented, spec.Scenario, spec.ProbePath, spec.ProbeMode
	run.Role = spec.Role
	run.framework = "vitest"
	run.ExecutionPath = t.vitestExecutionPath()
	run.Name, run.Instrumented, run.ProbeMode, run.root = name, instrumented, probeMode, t.checkoutRoot()
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
	if !instrumented {
		env["NODE_OPTIONS"] = stripDatadogNodeOptions(t.nodeOptions())
		env["DD_CIVISIBILITY_ENABLED"], env["DD_TRACE_ENABLED"] = "false", "false"
	}
	resultPath := filepath.Join(directory, "vitest-results.json")
	// Vitest's JSON reporter can omit unhandled errors even when they cause exit 1.
	args := slices.Clone(t.vitestArguments)
	if probePath != "" {
		args = slices.Clone(t.vitestProbeArguments)
	}
	if !t.usesVitestRunner() && len(args) == 0 {
		return run, fmt.Errorf("vitest arguments have not been inspected")
	}
	run.ReplacedReporters = slices.Clone(t.vitestReporters)
	validationArgs := []string{"--run", "--no-cache", "--reporter=default", "--reporter=json", "--outputFile=" + resultPath, "--coverage.reportsDirectory=" + filepath.Join(directory, "coverage"), "--coverage.thresholds.autoUpdate=false"}
	if probePath != "" {
		validationArgs = append(validationArgs, "--testNamePattern=^"+probeName+"$", "--no-coverage.thresholds", "--retry=0")
		env["DDTEST_PROBE_MODE"] = probeMode
	}
	args = framework.WithFrameworkOptions(t.command, args, "vitest", validationArgs...)
	if t.usesVitestRunner() {
		args, err = t.vitestRunnerArgs(directory, preload, probePath, instrumented, env)
		if err != nil {
			return run, err
		}
	}
	run.ProbeCoverageThresholdsDisabled = probePath != ""
	run.Command = shellquote.Join(append([]string{t.command}, args...)...)
	_, _ = fmt.Fprintf(output, "Running %s...\n", name)
	data, commandErr := t.executor.CombinedOutput(ctx, t.command, args, env)
	run.commandOutput, run.commandError = ansiEscape.ReplaceAllString(string(data), ""), commandErr
	run.ExitCode = commandExitCode(commandErr)
	if commandErr != nil && probePath == "" {
		writeCommandFailure(output, "Vitest "+name, data)
	}
	err = server.Close()
	closed = true
	if err != nil {
		return run, err
	}
	if err = ctx.Err(); err != nil {
		return run, err
	}
	run.Facts, err = server.Facts()
	if err != nil {
		return run, err
	}
	// The worker collector needs the intake diagnosis to recognize an ITR-omitted
	// suite; refresh it below after the native results have also been collected.
	if scenario.Feature == "skipping" {
		run.Skipping = diagnoseSkipping(run, scenario)
	}
	if t.usesVitestRunner() {
		run.PlannerSkipped = vitestPlannerSkippedProbe(directory, t.repositoryRoot, probePath, scenario, run)
		if !run.PlannerSkipped {
			readVitestWorkerResults(directory, t.repositoryRoot, &run)
		}
	} else {
		readRunnerResults(resultPath, "Vitest", &run)
	}
	if scenario.Feature == "skipping" {
		run.Skipping = diagnoseSkipping(run, scenario)
	}
	if signature := vitestUnhandledErrors(data); signature != "" {
		run.SuiteErrors = append(run.SuiteErrors, signature)
	}
	if run.ExitCode != 0 || run.ResultError != "" || len(run.SuiteErrors) > 0 {
		run.Diagnostic = vitestDiagnostic(data)
	}
	return run, nil
}

func vitestUnhandledErrors(output []byte) string {
	text := failureSignature(string(output))
	start := strings.Index(text, "Unhandled Errors")
	if start < 0 {
		return ""
	}
	text = text[start:]
	for _, end := range []string{"\nTest Files", "\nStart at", "\nDuration", "\n% Coverage"} {
		if i := strings.Index(text, end); i >= 0 {
			text = text[:i]
		}
	}
	return text
}

func vitestDiagnostic(output []byte) string {
	text := failureSignature(string(output))
	if start := strings.Index(text, "Unhandled Errors"); start >= 0 {
		// Coverage tables follow these errors and can push them out of the bounded tail.
		text = text[start:]
		if end := strings.Index(text, "\nTest Files"); end >= 0 {
			text = text[:end]
		}
		runes := []rune(text)
		const limit, suffix = 1024, " …[truncated]"
		if len(runes) > limit {
			return string(runes[:limit-len([]rune(suffix))]) + suffix
		}
		return text
	}
	return commandDiagnostic(output)
}

// The native CLI/Node API overrides isolate retry settings for this probe;
// the full-suite configuration and Jest's retryTimes API are untouched.
const vitestProbeSource = `// @ts-nocheck
import {test} from 'vitest';
let ddtestAttempts = 0;
test('ddtest validation probe', () => {
  ddtestAttempts++;
  if (globalThis.process.env.DDTEST_PROBE_MODE === 'fail' ||
      (globalThis.process.env.DDTEST_PROBE_MODE === 'fail-once' && ddtestAttempts === 1)) {
    throw new Error('ddtest intentional failure');
  }
});
`

func (t *Testdrive) runVitestFeatures(ctx context.Context, output io.Writer, session *Session, preload string, baseline validationRun, result *validationResult) (runErr error) {
	probePath, err := createProbe(t.repositoryRoot, baseline, vitestProbeSource)
	if err != nil {
		unavailableFeatures(result, err.Error())
		return nil
	}
	defer func() { runErr = errors.Join(runErr, removeProbe(t.repositoryRoot, probePath)) }()
	runner := *t
	_, projects, files, err := runner.inspectVitest(ctx, session.Directory(), probePath)
	if t.usesVitestRunner() {
		// Native discovery checks inclusion; the actual plan/worker assertions
		// below enforce exact selection without CLI substring filters.
		files = slices.DeleteFunc(files, func(file string) bool { return filepath.Clean(file) != filepath.Clean(probePath) })
	}
	if err != nil || len(projects) != 1 || len(files) != 1 || filepath.Clean(files[0]) != filepath.Clean(probePath) {
		unavailableFeatures(result, fmt.Sprintf("The temporary probe was not uniquely discovered under the original Vitest configuration: %v", err))
		return nil
	}
	run := func(name string, scenario intake.Scenario, mode string) (validationRun, error) {
		role := roleProbeFeature
		if scenario.Feature == "" || scenario.Feature == "reporting-only" {
			role = roleProbeControl
		}
		return runner.runVitest(ctx, output, session, preload, runSpec{Name: name, Role: role, Instrumented: true, Scenario: scenario, ProbePath: probePath, ProbeMode: mode})
	}
	return runProbeFeatures(run, result)
}
