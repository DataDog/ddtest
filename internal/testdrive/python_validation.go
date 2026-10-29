package testdrive

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
	"github.com/kballard/go-shellquote"
)

//go:embed scripts/pytest_observer.py
var pytestObserver string

const pytestProbeName = "test_ddtest_validation_probe"
const pytestProbeSource = `import os
from pathlib import Path

def test_ddtest_validation_probe():
    path = Path(os.environ["DDTEST_PYTEST_EXECUTIONS"])
    with path.open("a") as output:
        output.write("attempt\n")
    attempts = len(path.read_text().splitlines())
    mode = os.environ.get("DDTEST_PROBE_MODE")
    assert not (mode == "fail" or (mode == "fail-once" and attempts == 1)), "ddtest intentional failure"

test_ddtest_validation_probe.__test__ = True
`

func (t *Testdrive) checkPytestPreflight(ctx context.Context, output io.Writer, result *validationResult) error {
	check := &preflightCheck{Runtime: t.environmentInfo.Version, Command: shellquote.Join(append([]string{t.command}, t.args...)...), Verdict: verdict{Status: "inconclusive", Reason: "Python tracer selection was not resolved."}}
	result.Preflight = check
	if t.environmentFailure != nil {
		check.Verdict = verdict{Status: "incompatible", Reason: t.environmentFailure.Context}
		return fmt.Errorf("pytest preflight: %s", t.environmentFailure.Context)
	}
	addopts := os.Getenv("PYTEST_ADDOPTS")
	if configured, ok := t.configurationEnvironment["PYTEST_ADDOPTS"]; ok {
		addopts = configured
	}
	words := append([]string{t.command}, t.args...)
	if err := checkPytestPluginOptions(addopts, words, nil); err != nil && (t.checkOnly || !errors.Is(err, onboard.ErrPytestRuntimeValidation)) {
		status := "incompatible"
		if errors.Is(err, onboard.ErrPytestRuntimeValidation) {
			status = "inconclusive"
		}
		check.Verdict = verdict{Status: status, Reason: err.Error()}
		return fmt.Errorf("pytest preflight: %w", err)
	}
	command, prefix, err := pythonProbeCommand(t.command, t.args)
	if err != nil {
		return err
	}
	var metadata struct{ Pytest, Python, Tracer, RequiresPython, Executable, Directory string }
	const script = `import importlib.metadata as m, json, os, pathlib, platform, sys
import pytest
result = {"pytest": m.version("pytest"), "python": platform.python_version(), "executable": sys.executable, "directory": os.getcwd()}
try:
    result.update(tracer=m.version("ddtrace"), requiresPython=m.metadata("ddtrace").get("Requires-Python", ""))
except m.PackageNotFoundError:
    pass
pathlib.Path(sys.argv[1]).write_text(json.dumps(result))
`
	env := maps.Clone(t.configurationEnvironment)
	if env == nil {
		env = map[string]string{}
	}
	maps.Copy(env, testEnvironment("http://127.0.0.1:1", "pytest-preflight"))
	if err := probeEnvironment(ctx, command, append(prefix, "-c", script), env, &metadata, t.commandTimeout); err != nil {
		return fmt.Errorf("inspect pytest: %w", err)
	}
	check.Version, check.Runtime = metadata.Pytest, metadata.Python
	directory := metadata.Directory
	if directory == "" {
		directory = t.repositoryRoot
	}
	defaults, configErr := onboard.PytestConfigOptions(t.checkoutRoot(), directory, addopts, words)
	if configErr != nil && t.checkOnly {
		check.Verdict = verdict{Status: "inconclusive", Reason: configErr.Error()}
		return fmt.Errorf("pytest configuration: %w", configErr)
	}
	// Full execution resolves ambiguous configuration through pytest itself.
	// Static discovery limitations must not block a selected local comparison.
	if err := checkPytestPluginOptions(addopts, words, defaults); err != nil && (t.checkOnly || !errors.Is(err, onboard.ErrPytestRuntimeValidation)) {
		status := "incompatible"
		if errors.Is(err, onboard.ErrPytestRuntimeValidation) {
			status = "inconclusive"
		}
		check.Verdict = verdict{Status: status, Reason: err.Error()}
		return fmt.Errorf("pytest preflight: %w", err)
	}
	t.projectTracer = metadata.Tracer
	t.environmentInfo.Executable, t.environmentInfo.Version = metadata.Executable, metadata.Python
	selection := onboard.PythonRelease{Version: metadata.Tracer, RequiresPython: metadata.RequiresPython}
	if metadata.Tracer == "" {
		if strings.HasPrefix(t.tracerVersion, "git:") {
			return fmt.Errorf("pytest validation needs an exact release; install the git tracer in the selected environment first")
		}
		selection, err = onboard.ResolvePythonRelease(ctx, t.tracerVersion)
		if err != nil {
			return err
		}
	}
	t.tracerVersion = selection.Version
	result.Tracer = "ddtrace@" + selection.Version
	result.TracerSource = "temporary installation"
	if metadata.Tracer != "" {
		result.TracerSource = "project installation (reused; fallback selector ignored)"
	}
	major, minor, known := releaseParts(selection.Version)
	pytestMajor, pytestMinor, pytestKnown := releaseParts(metadata.Pytest)
	if !known || !pytestKnown || major < 4 || major == 4 && minor < 11 || pytestMajor < 7 || pytestMajor == 7 && pytestMinor < 2 {
		check.Verdict = verdict{Status: "incompatible", Reason: "pytest validation requires ddtrace >=4.11 and pytest >=7.2. Preserve the suite and select compatible dependencies."}
	} else {
		check.Verdict.Status, check.Verdict.Reason = onboard.ComparePythonRequirement(metadata.Python, selection.RequiresPython)
	}
	ci := onboard.CheckPythonCIRuntimes(ctx, t.checkoutRoot(), selection.Version)
	if previous := result.CIRuntime; previous != nil && slices.ContainsFunc(previous.Review, func(finding onboard.RuntimeFinding) bool { return finding.Code == onboard.CodeNestedCIScope }) {
		ci.Review = append(ci.Review, previous.Review...)
		if ci.Status == onboard.StatusCompatible || ci.Status == onboard.StatusNotApplicable {
			ci.Status, ci.Reason = previous.Status, previous.Reason
		}
	}
	result.CIRuntime = &ci
	_, _ = fmt.Fprintf(output, "pytest %s; Python %s; tracer %s (%s). Preflight: %s — %s\n", check.Version, check.Runtime, result.Tracer, result.TracerSource, check.Verdict.Status, check.Verdict.Reason)
	if check.Verdict.Status != "compatible" {
		return fmt.Errorf("pytest preflight: %s", check.Verdict.Reason)
	}
	return nil
}

func checkPytestPluginOptions(addopts string, words, defaults []string) error {
	options, iniAddopts, err := onboard.PytestCommandOptions(addopts, words, defaults)
	if err != nil {
		return err
	}
	if slices.Contains(iniAddopts, "--no-ddtrace") {
		return fmt.Errorf("pytest addopts disables Datadog instrumentation; remove --no-ddtrace from the configuration or effective -o/--override-ini addopts before controlled validation")
	}
	plugins := []string{"ddtrace", "ddtrace.contrib.pytest.plugin", "ddtrace.contrib.internal.pytest.plugin", "ddtrace.testing.internal.pytest.entry_point"}
	blocked := map[string]bool{}
	for i := 0; i < len(options); i++ {
		option := options[i]
		plugin := strings.TrimPrefix(option, "-p")
		if option == "-p" && i+1 < len(options) {
			i++
			plugin = options[i]
		} else if !strings.HasPrefix(option, "-p") {
			continue
		}
		name := strings.TrimPrefix(plugin, "no:")
		if slices.Contains(plugins, name) {
			blocked[name] = strings.HasPrefix(plugin, "no:")
		}
	}
	if slices.ContainsFunc(plugins, func(name string) bool { return blocked[name] }) {
		return fmt.Errorf("pytest options block the Datadog plugin. Remove that blocker from PYTEST_ADDOPTS or the selected command before controlled validation; preserve other test options")
	}
	return nil
}

func (t *Testdrive) verifyPytestTracer(ctx context.Context, path, expected string) error {
	command, prefix, err := pythonProbeCommand(t.command, t.args)
	if err != nil {
		return err
	}
	var result struct{ Version string }
	script := `import ddtrace, json, pathlib, sys; pathlib.Path(sys.argv[1]).write_text(json.dumps({"version": ddtrace.__version__}))`
	if path != "" {
		script = fmt.Sprintf("import sys; sys.path.insert(0, %q)\n", path) + script
	}
	env := maps.Clone(t.configurationEnvironment)
	if env == nil {
		env = map[string]string{}
	}
	maps.Copy(env, t.environment(path, "http://127.0.0.1:1", "pytest-tracer-check"))
	if configured, ok := t.configurationEnvironment["PYTHONPATH"]; ok && path != "" && configured != "" {
		env["PYTHONPATH"] = path + string(os.PathListSeparator) + configured
	}
	if err := probeEnvironment(ctx, command, append(prefix, "-c", script), env, &result, t.commandTimeout); err != nil {
		return err
	}
	if result.Version != expected {
		return fmt.Errorf("selected ddtrace@%s but the test environment loads ddtrace@%s", expected, result.Version)
	}
	return nil
}

func stripPytestInstrumentation(value string) (string, error) {
	words, err := shellquote.Split(value)
	if err != nil {
		return "", fmt.Errorf("cannot parse PYTEST_ADDOPTS: %w", err)
	}
	return shellquote.Join(stripPytestArguments(words)...), nil
}

func stripPytestArguments(words []string) []string {
	kept := make([]string, 0, len(words))
	for i := 0; i < len(words); i++ {
		word := words[i]
		if word == "--" {
			return append(kept, words[i:]...)
		}
		if word == "--ddtrace" || word == "--no-ddtrace" || word == "--ddtrace-patch-all" {
			continue
		}
		kept = append(kept, word)
		if slices.Contains([]string{"-k", "-m", "-p", "-o", "--override-ini", "-c", "--config-file", "--rootdir"}, word) && i+1 < len(words) {
			i++
			kept = append(kept, words[i])
		}
	}
	return kept
}

func (t *Testdrive) runPytest(ctx context.Context, output io.Writer, session *workspace.Session, preload string, spec runSpec) (run validationRun, runErr error) {
	name, instrumented, scenario, probePath, probeMode := spec.Name, spec.Instrumented, spec.Scenario, spec.ProbePath, spec.ProbeMode
	run = validationRun{Role: spec.Role, Name: name, Instrumented: instrumented, ProbeMode: probeMode, framework: "pytest", root: t.repositoryRoot, checkoutRoot: t.checkoutRoot()}
	if root, err := filepath.EvalSymlinks(run.root); err == nil {
		run.root = root
	}
	if root, err := filepath.EvalSymlinks(run.checkoutRoot); err == nil {
		run.checkoutRoot = root
	}
	directory := filepath.Join(session.Directory(), name)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return run, err
	}
	observer := filepath.Join(directory, "ddtest_pytest_observer.py")
	if err := os.WriteFile(observer, []byte(pytestObserver), 0600); err != nil {
		return run, err
	}
	scenario.KnownModules = append(scenario.KnownModules, "pytest")
	scenario.SkippingLevel = "test"
	if scenario.Module != "" {
		scenario.SkippableConfigurations = map[string]string{"test.bundle": scenario.Module}
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
	env := maps.Clone(t.configurationEnvironment)
	if env == nil {
		env = map[string]string{}
	}
	maps.Copy(env, t.environment(preload, server.URL(), session.ID()+" "+name))
	configureScenarioEnvironment(env, scenario.Feature)
	if err := run.ownTemporaryDirectory(directory, env); err != nil {
		return run, err
	}
	// The observer imports through PYTHONPATH, even with plugin autoload disabled.
	path := os.Getenv("PYTHONPATH")
	if configured, ok := t.configurationEnvironment["PYTHONPATH"]; ok {
		path = configured
	}
	if preload != "" {
		path = preload + string(os.PathListSeparator) + path
	}
	env["PYTHONPATH"] = directory
	if path != "" {
		env["PYTHONPATH"] += string(os.PathListSeparator) + path
	}
	addopts := os.Getenv("PYTEST_ADDOPTS")
	if configured, ok := t.configurationEnvironment["PYTEST_ADDOPTS"]; ok {
		addopts = configured
	}
	options, err := stripPytestInstrumentation(addopts)
	if err != nil {
		return run, err
	}
	flag := "--no-ddtrace"
	if instrumented {
		flag = "--ddtrace"
	} else {
		env["DD_CIVISIBILITY_ENABLED"], env["DD_TRACE_ENABLED"] = "false", "false"
	}
	optionWords, err := shellquote.Split(options)
	if err != nil {
		return run, err
	}
	env["PYTEST_ADDOPTS"] = shellquote.Join(framework.WithFrameworkOptions("pytest", optionWords, "pytest", "-p", "ddtest_pytest_observer", flag)...)
	env["PYTHONDONTWRITEBYTECODE"] = "1"
	env["DDTEST_PYTEST_RESULTS"] = filepath.Join(directory, "results.json")
	env["DDTEST_PYTEST_PROBE"], env["DDTEST_PROBE_MODE"] = probePath, probeMode
	env["DDTEST_PYTEST_EXECUTIONS"] = filepath.Join(directory, "executions.txt")
	args := slices.Clone(t.args)
	if filepath.Base(t.command) == "uv" && len(args) > 0 && args[0] == "run" && !slices.Contains(args, "--no-sync") {
		args = slices.Insert(args, 1, "--no-sync")
	}
	// Strip explicit tracer enable/disable flags in either mode; append the
	// controlled flag after ini/environment defaults so config addopts agree.
	words := append([]string{t.command}, args...)
	pytestArgs, ok := onboard.PytestArguments(words)
	if !ok {
		return run, fmt.Errorf("cannot identify pytest execution arguments")
	}
	prefix := words[:len(words)-len(pytestArgs)]
	words = append(slices.Clone(prefix), stripPytestArguments(pytestArgs)...)
	args = words[1:]
	args = framework.WithFrameworkOptions(t.command, args, "pytest", flag)
	args = pytestObserverArguments(t.command, args, directory, preload)
	run.Command = shellquote.Join(append([]string{t.command}, args...)...)
	_, _ = fmt.Fprintf(output, "Running %s...\n", name)
	data, commandErr := t.executor.CombinedOutput(ctx, t.command, args, env)
	run.commandOutput, run.commandError, run.ExitCode = string(data), commandErr, commandExitCode(commandErr)
	if commandErr != nil && probePath == "" {
		writeCommandFailure(output, "pytest "+name, data)
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
	readPytestResults(env["DDTEST_PYTEST_RESULTS"], &run)
	if probePath != "" {
		data, err := os.ReadFile(env["DDTEST_PYTEST_EXECUTIONS"])
		count := 0
		if err == nil {
			count = len(strings.Fields(string(data)))
		} else if !os.IsNotExist(err) {
			return run, err
		}
		run.ProbeExecutions = &count
	}
	if instrumented && probePath == "" && run.Facts.TestCount > 0 {
		run.Coverage = pytestSourceCoverage(t.checkoutRoot(), run.Facts, run.Tests)
	}
	if run.ExitCode != 0 || run.ResultError != "" {
		run.Diagnostic = commandDiagnostic(data)
	}
	return run, nil
}

func pytestSourceCoverage(root string, facts intake.Facts, native []nativeTest) *coverageSummary {
	if canonical, err := filepath.EvalSymlinks(root); err == nil {
		root = canonical
	}
	applicationFiles := func(files []string) []string {
		// Python sends checkout-relative coverage paths with a leading slash.
		files = slices.Clone(files)
		for i := range files {
			files[i] = strings.TrimPrefix(filepath.ToSlash(files[i]), "/")
		}
		return slices.DeleteFunc(files, func(file string) bool {
			local, valid := repositoryCoverageFile(root, file)
			if !valid {
				return false // Preserve invalid paths for the shared coverage check.
			}
			parts := strings.Split(filepath.ToSlash(local), "/")
			base := filepath.Base(local)
			return slices.ContainsFunc(parts, func(part string) bool {
				return slices.Contains([]string{"test", "tests", ".venv", "venv", "site-packages"}, part)
			}) || base == "conftest.py" || strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py")
		})
	}
	facts.Tests = slices.Clone(facts.Tests)
	for i := range facts.Tests {
		facts.Tests[i].CoveredFiles = applicationFiles(facts.Tests[i].CoveredFiles)
	}
	facts.SuiteCoverages = slices.Clone(facts.SuiteCoverages)
	for i := range facts.SuiteCoverages {
		facts.SuiteCoverages[i].Files = applicationFiles(facts.SuiteCoverages[i].Files)
	}
	coverage := applicationSourceCoverage(root, facts, native)
	switch coverage.Status {
	case "observed":
		coverage.Reason = "pytest coverage includes existing repository application files."
	case "invalid":
		coverage.Reason = "pytest coverage names missing or outside-repository files."
	default:
		coverage.Reason = "No repository application source coverage observed. Test files, fixtures and dependencies alone are insufficient."
	}
	return coverage
}

func readPytestResults(path string, run *validationRun) {
	var result struct {
		Complete                  bool
		Tests                     []nativeTest
		Errors                    []string
		CoverageThresholdDisabled bool `json:"coverage_threshold_disabled"`
	}
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &result) != nil || !result.Complete || result.Tests == nil {
		run.ResultError = "pytest native results are missing, invalid or incomplete"
		return
	}
	for _, test := range result.Tests {
		test.Failure = failureSignature(test.Failure)
		test.probe = isPytestProbe(test.Name)
		run.Tests = append(run.Tests, test)
	}
	run.SuiteErrors = result.Errors
	run.ProbeCoverageThresholdsDisabled = result.CoverageThresholdDisabled
}

func (t *Testdrive) runPytestFeatures(ctx context.Context, output io.Writer, session *workspace.Session, preload string, baseline validationRun, result *validationResult) (runErr error) {
	root, err := filepath.EvalSymlinks(t.repositoryRoot)
	if err != nil {
		return err
	}
	var directory string
	for _, test := range baseline.Tests {
		if test.Status != "passed" && test.Status != "failed" {
			continue
		}
		file, err := filepath.EvalSymlinks(test.File)
		if err != nil {
			continue
		}
		relative, err := filepath.Rel(root, file)
		if err == nil && filepath.IsLocal(relative) {
			directory = filepath.Dir(file)
			break
		}
	}
	if directory == "" {
		unavailableFeatures(result, "No executed pytest source inside the repository is available for controlled probes.")
		return nil
	}
	owned, err := os.MkdirTemp(directory, "ddtest-pytest-")
	if err != nil {
		return err
	}
	defer func() {
		if err := os.Remove(owned); !os.IsNotExist(err) {
			runErr = errors.Join(runErr, err)
		}
	}()
	probe := filepath.Join(owned, "test_ddtest_probe.py")
	if err := createOwnedProbe(root, probe, []byte(pytestProbeSource)); err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, removeProbe(root, probe)) }()
	inventory, err := t.runPytest(ctx, output, session, preload, runSpec{Name: "probe-inventory", Role: roleProbeControl, Instrumented: true, ProbePath: probe, ProbeMode: "pass"})
	result.Runs = append(result.Runs, inventory.summary())
	if err != nil {
		return err
	}
	if inventory.ExitCode != 0 || inventory.ResultError != "" || len(inventory.SuiteErrors) != 0 || len(inventory.Tests) == 0 {
		unavailableFeatures(result, "The temporary pytest probe could not enumerate its fixture variants under the project configuration.")
		return nil
	}
	for _, test := range inventory.Tests {
		if !isPytestProbe(test.Name) {
			unavailableFeatures(result, "The pytest probe inventory included an unexpected test identity.")
			return nil
		}
	}
	// Autouse parametrized fixtures apply to the probe too. Exercise every
	// collected variant independently without disabling the project's fixtures.
	for index, test := range inventory.Tests {
		featureStart, runStart := len(result.Features), len(result.Runs)
		err := runProbeFeatures(func(name string, scenario intake.Scenario, mode string) (validationRun, error) {
			run, err := t.runPytest(ctx, output, session, preload, runSpec{Name: fmt.Sprintf("probe-%d/%s", index, name), Role: roleProbeFeature, Instrumented: true, Scenario: scenario, ProbePath: probe + "::" + test.Name, ProbeMode: mode})
			run.Name = name
			return run, err
		}, result)
		if strings.HasPrefix(test.Name, pytestProbeName+"[") {
			for i := featureStart; i < len(result.Features); i++ {
				feature := &result.Features[i]
				if feature.Name == "early-flake-detection" && feature.Code == tracerControlNotApplied {
					feature.Status, feature.ObservedCode, feature.Code = "known limitation", feature.Code, tracerLimitation
					feature.Reason = "The selected Python tracer did not apply early flake detection to the parameterized probe. Python tracer versions that exclude parameterized tests from EFD cannot validate that control under these fixtures. The feature remains unvalidated. Observed: " + feature.Reason
				}
			}
		}
		if len(inventory.Tests) > 1 {
			for i := featureStart; i < len(result.Features); i++ {
				result.Features[i].Project = test.Name
			}
			for i := runStart; i < len(result.Runs); i++ {
				result.Runs[i].Project = test.Name
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func isPytestProbe(name string) bool {
	return name == pytestProbeName || strings.HasPrefix(name, pytestProbeName+"[")
}

func verifyPytestTelemetry(run validationRun) verdict {
	return verifyNativeTelemetry(run, true)
}
