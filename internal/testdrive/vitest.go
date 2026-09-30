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

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
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
	base := filepath.Base(command)
	switch base {
	case "vitest", "vitest.mjs":
		return slices.Clone(args), nil
	case "pnpm", "yarn", "npx":
		if len(args) > 0 && args[0] == "exec" {
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

func (t *Testdrive) inspectVitest(ctx context.Context, directory string) (string, []vitestProject, []string, error) {
	args, err := vitestArguments(t.command, t.args)
	if err != nil {
		return "", nil, nil, err
	}
	if len(args) > 0 && args[0] == "bench" {
		return "", nil, nil, fmt.Errorf("vitest benchmark mode is not validated by the test adapter; validate the ordinary test command separately and leave benchmarks unvalidated")
	}
	encoded, _ := json.Marshal(args)
	env := testEnvironment("http://127.0.0.1:1", "vitest-preflight")
	env["NODE_OPTIONS"] = stripDatadogNodeOptions(os.Getenv("NODE_OPTIONS"))
	env["DD_CIVISIBILITY_ENABLED"], env["DD_TRACE_ENABLED"] = "false", "false"
	data, err := t.executor.CombinedOutput(ctx, "node", []string{"--input-type=module", "--eval", vitestConfigScript, string(encoded), filepath.Join(directory, "vite-cache")}, env)
	if err != nil {
		return "", nil, nil, fmt.Errorf("inspect Vitest configuration: %w: %s", err, commandDiagnostic(data))
	}
	const marker = "__DDTEST_VITEST_CONFIG__"
	start := strings.LastIndex(string(data), marker)
	if start < 0 {
		return "", nil, nil, fmt.Errorf("vitest configuration did not return its discovery result")
	}
	var config struct {
		Version  string
		Projects []vitestProject
		Files    []string
	}
	if err := json.NewDecoder(strings.NewReader(string(data[start+len(marker):]))).Decode(&config); err != nil {
		return "", nil, nil, err
	}
	return config.Version, config.Projects, config.Files, nil
}

func (t *Testdrive) checkVitestPreflight(ctx context.Context, output io.Writer, result *validationResult) error {
	check := &jestPreflight{Node: t.nodeVersion(), Command: shellquote.Join(append([]string{t.command}, t.args...)...), Verdict: verdict{Status: "inconclusive", Reason: "Vitest configuration was not resolved."}}
	result.Preflight = check
	directory, err := os.MkdirTemp("", "ddtest-vitest-config-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(directory) }()
	check.Version, check.VitestProjects, _, err = t.inspectVitest(ctx, directory)
	if err != nil {
		check.Verdict.Reason = "ddtest could not inspect this Vitest configuration: " + reportText(err.Error())
		return err
	}
	installer, ok := t.platform.(*platform.JavaScript)
	if !ok {
		return fmt.Errorf("vitest requires the JavaScript tracer selector")
	}
	selection, err := installer.ResolveTestdriveTracer(ctx, platform.TracerOptions{Version: t.tracerVersion, Command: t.command, Args: t.args})
	result.Selection = &selection
	if err != nil {
		check.Verdict.Reason = "Vitest configuration resolved, but tracer metadata is unavailable: " + reportText(err.Error())
		return err
	}
	result.Tracer, result.TracerSource = "dd-trace@"+selection.Version, selection.Source
	if selection.Source == "project" {
		_, _ = fmt.Fprintln(output, "Using the existing project tracer; --tracer-version applies only when it is absent. Review any dependency upgrade explicitly.")
	}
	result.CISelection = compareCISelection(*result.CIRuntime, selection.Version)
	check.Verdict = checkVitestSupport(*check, selection)
	_, _ = fmt.Fprintf(output, "Vitest %s; Node %s; tracer %s (%s). Preflight: %s — %s\n", check.Version, check.Node, result.Tracer, selection.Source, check.Verdict.Status, check.Verdict.Reason)
	t.recommendTracer(ctx, output, result)
	if check.Verdict.Status != "compatible" {
		return fmt.Errorf("vitest preflight: %s", check.Verdict.Reason)
	}
	return nil
}

func checkVitestSupport(check jestPreflight, selection platform.JSSelection) verdict {
	major, minor, known := releaseParts(check.Version)
	tracerMajor, tracerMinor, tracerKnown := releaseParts(selection.Version)
	// Reject known tracer incompatibilities, not versions absent from an adapter
	// allowlist. Configuration discovery and live checks establish validation.
	// https://docs.datadoghq.com/tests/setup/javascript/#compatibility
	if known && (major < 1 || (major == 1 && minor < 6)) {
		return verdict{Status: "incompatible", Reason: "dd-trace Vitest instrumentation requires Vitest >=1.6.0."}
	}
	if len(check.VitestProjects) != 1 || check.VitestProjects[0].Browser || check.VitestProjects[0].Typecheck || !slices.Contains([]string{"forks", "threads"}, check.VitestProjects[0].Pool) {
		return verdict{Status: "inconclusive", Reason: "Vitest validation currently requires one Node project using forks or threads, without Vitest typechecking; preserve other configurations and report them as unvalidated."}
	}
	status, reason := onboard.CompareNodeRequirement(check.Node, selection.Node)
	if status != "compatible" {
		return verdict{Status: status, Reason: reason}
	}
	// Vitest TIA shipped in these releases (PR #9604); this is a capability
	// boundary, not an installation pin. Later releases still require live probes.
	// https://github.com/DataDog/dd-trace-js/releases/tag/v5.120.0
	// https://github.com/DataDog/dd-trace-js/releases/tag/v6.9.0
	if tracerKnown && (tracerMajor < 5 || (tracerMajor == 5 && tracerMinor < 120) || (tracerMajor == 6 && tracerMinor < 9)) {
		return verdict{Status: "incompatible", Reason: fmt.Sprintf("dd-trace %s does not support Vitest test skipping. This feature requires dd-trace >=5.120.0 in major 5 or >=6.9.0 in major 6. Resolve a newer Node-compatible release with --tracer-version 5 or 6, then pin its exact version locally and in CI. An installed project tracer takes precedence and requires an explicit dependency upgrade; no dependency was changed.", selection.Version)}
	}
	// https://github.com/DataDog/dd-trace-js/releases/tag/v5.125.0
	// https://github.com/DataDog/dd-trace-js/releases/tag/v6.14.0
	if known && major == 5 && tracerKnown && ((tracerMajor == 5 && tracerMinor < 125) || (tracerMajor == 6 && tracerMinor < 14)) {
		return verdict{Status: "incompatible", Reason: fmt.Sprintf("dd-trace %s predates Vitest 5 support. Use dd-trace >=5.125.0 in major 5 or >=6.14.0 in major 6, aligning local and CI selections. An installed project tracer requires an explicit dependency upgrade; no dependency was changed.", selection.Version)}
	}
	return verdict{Status: "compatible", Reason: "No known tracer/runtime incompatibility blocks this configuration. Paired execution and feature checks are still required."}
}

func (t *Testdrive) runVitest(ctx context.Context, output io.Writer, session *Session, preload, name string, instrumented bool, scenario intake.Scenario, probePath, probeMode string) (run validationRun, runErr error) {
	run.framework = "vitest"
	run.Name, run.Instrumented, run.ProbeMode, run.root = name, instrumented, probeMode, t.repositoryRoot
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
	configureScenarioEnvironment(env, scenario.Feature)
	if !instrumented {
		env["NODE_OPTIONS"] = stripDatadogNodeOptions(os.Getenv("NODE_OPTIONS"))
		env["DD_CIVISIBILITY_ENABLED"], env["DD_TRACE_ENABLED"] = "false", "false"
	}
	resultPath := filepath.Join(directory, "vitest-results.json")
	// Vitest's JSON reporter can omit unhandled errors even when they cause exit 1.
	args := append(slices.Clone(t.args), "--run", "--no-cache", "--reporter=default", "--reporter=json", "--outputFile="+resultPath, "--coverage.reportsDirectory="+filepath.Join(directory, "coverage"))
	if probePath != "" {
		args = append(args, probePath, "--testNamePattern=^"+probeName+"$")
		// Only probe thresholds are relaxed. Collection and full-suite thresholds stay intact.
		for _, metric := range []string{"lines", "branches", "functions", "statements"} {
			args = append(args, "--coverage.thresholds."+metric+"=0")
		}
		run.ProbeCoverageThresholdsDisabled = true
		env["DDTEST_PROBE_MODE"] = probeMode
	}
	run.Command = shellquote.Join(append([]string{t.command}, args...)...)
	_, _ = fmt.Fprintf(output, "Running %s...\n", name)
	data, commandErr := t.executor.CombinedOutput(ctx, t.command, args, env)
	run.commandOutput, run.commandError = string(data), commandErr
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
	readRunnerResults(resultPath, "Vitest", &run)
	if scenario.Feature == "skipping" {
		run.Skipping = diagnoseSkipping(run, scenario)
	}
	if run.ExitCode != 0 || run.ResultError != "" || len(run.SuiteErrors) > 0 {
		run.Diagnostic = vitestDiagnostic(data)
	}
	return run, nil
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

func (t *Testdrive) runVitestFeatures(ctx context.Context, output io.Writer, session *Session, preload string, baseline validationRun, result *validationResult) (runErr error) {
	probePath, err := createProbe(t.repositoryRoot, baseline, "import { test } from 'vitest';\n"+probeSource)
	if err != nil {
		unavailableFeatures(result, err.Error())
		return nil
	}
	defer func() { runErr = errors.Join(runErr, os.Remove(probePath)) }()
	runner := *t
	runner.args = append(slices.Clone(t.args), probePath)
	_, projects, files, err := runner.inspectVitest(ctx, session.Directory())
	if err != nil || len(projects) != 1 || len(files) != 1 || filepath.Clean(files[0]) != filepath.Clean(probePath) {
		unavailableFeatures(result, fmt.Sprintf("The temporary probe was not uniquely discovered under the original Vitest configuration: %v", err))
		return nil
	}
	run := func(name string, scenario intake.Scenario, mode string) (validationRun, error) {
		return t.runVitest(ctx, output, session, preload, name, true, scenario, probePath, mode)
	}
	return runProbeFeatures(run, result)
}
