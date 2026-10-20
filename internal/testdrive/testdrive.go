// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package testdrive runs a customer's tests against a local Test Optimization intake.
package testdrive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kballard/go-shellquote"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
)

const testOutputFilename = "test-output.txt"

type commandExecutor interface {
	CombinedOutput(ctx context.Context, name string, args []string, envMap map[string]string) ([]byte, error)
}

type localIntake interface {
	URL() string
	Facts() (intake.Facts, error)
	Close() error
}

// Testdrive is a detected local test run ready for preview and execution.
type Testdrive struct {
	workflowSelect func(context.Context, onboard.ValidationScope, bool) (string, error) // deterministic preflight injection for coordinator tests
	workflowRoot   string
	workflowPlan   *onboard.ExecutionPlan
	workflowScope  *onboard.ValidationScope
	workflowRun    func(context.Context, io.Writer) error // test injection for coordinator failures

	webdriverIOCapability *int
	environmentInfo       environmentInfo
	environmentFailure    *reportCard
	cypress               *onboard.CypressCommand
	cypressCommand        string
	preparationError      error
	isolated              bool
	vitestRunner          bool
	playwrightGuard       bool
	allConfigurations     bool
	reportModels          *[]reportModel
	repositoryRoot        string
	framework             framework.Framework
	language              string
	command               string
	args                  []string
	tracerLabel           string
	platform              platform.Platform
	tracerVersion         string
	projectTracer         string
	session               *Session
	installCommand        string
	installArgs           []string
	checkOnly             bool
	preflight             func(context.Context, io.Writer, *validationResult) error
	executor              commandExecutor
	startIntake           func(string, intake.Scenario) (localIntake, error)
	nodeVersion           func() string
	resolveJSTracer       func(context.Context, string) (platform.JSSelection, error)
}

// Prepare detects the repository and probes the project tracer without writing files.
func Prepare(version string, checkOnly ...bool) (*Testdrive, error) {
	if version == "" {
		version = "latest"
	}
	if version == "git:" {
		return nil, fmt.Errorf("tracer git ref must not be empty")
	}
	repositoryRoot, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("find repository root: %w", err)
	}
	detectedPlatform, err := platform.DetectPlatform()
	if err != nil {
		return nil, err
	}
	runner, err := detectedPlatform.DetectFramework()
	if err != nil {
		return nil, err
	}
	language := detectedPlatform.Name()
	command, args := runner.Command()
	all := len(checkOnly) > 1 && checkOnly[1]
	if all && strings.TrimSpace(settings.GetCommand()) != "" {
		return nil, fmt.Errorf("--all and --command are mutually exclusive")
	}
	if all && !supportsValidationFramework(runner.Name()) {
		return nil, fmt.Errorf("--all currently validates Jest, Vitest, Playwright, Mocha, Cypress, Cucumber and WebdriverIO only")
	}
	if runner.Name() == "vitest" {
		repositoryRoot, err = filepath.EvalSymlinks(repositoryRoot)
		if err != nil {
			return nil, fmt.Errorf("resolve repository root: %w", err)
		}
		if settings.GetVitestConfig() != "" {
			return nil, fmt.Errorf("--vitest-config applies to Vitest plan/run; for testdrive use --all, --command 'vitest run --config <file>', or --command 'ddtest run --vitest-config <file>'")
		}
		// Preserve the execution path selected by the customer. ddtest run uses
		// the framework's Node API; ordinary Vitest commands keep their CLI path.
		command, args = "npx", []string{"vitest", "run"}
		if override := strings.TrimSpace(settings.GetCommand()); override != "" {
			words, err := shellquote.Split(override)
			if err != nil || len(words) == 0 || words[0] == "" {
				return nil, fmt.Errorf("invalid Vitest command: %s", override)
			}
			command, args = words[0], words[1:]
		}
		if onboard.IsDDTestCommand(command) {
			if _, err := onboard.ParseVitestRunnerCommand(args); err != nil {
				return nil, err
			}
		}
	}
	if runner.Name() == "jest" && !all && strings.TrimSpace(settings.GetCommand()) == "" {
		selected, err := onboard.JestValidationCommand(repositoryRoot)
		if err != nil {
			return nil, err
		}
		if selected != "" {
			words, err := shellquote.Split(selected)
			if err != nil {
				return nil, err
			}
			command, args = words[0], words[1:]
		}
	}
	if runner.Name() == "jest" && strings.TrimSpace(settings.GetCommand()) != "" {
		selected, err := onboard.ForwardJestCommand(repositoryRoot, settings.GetCommand())
		if err != nil {
			return nil, err
		}
		words, err := shellquote.Split(selected)
		if err != nil || len(words) == 0 {
			return nil, fmt.Errorf("invalid Jest command: %s", selected)
		}
		command, args = words[0], words[1:]
	}
	var preparationError error
	if (runner.Name() == "mocha" || runner.Name() == "cypress" || runner.Name() == "cucumber" || runner.Name() == "webdriverio") && !all {
		var selected string
		var err error
		if runner.Name() == "cypress" {
			selected, err = onboard.CypressValidationCommand(repositoryRoot, settings.GetCommand())
		} else if runner.Name() == "webdriverio" {
			selected, err = onboard.WebdriverIOValidationCommand(repositoryRoot, settings.GetCommand())
		} else if runner.Name() == "cucumber" {
			selected, err = onboard.CucumberValidationCommand(repositoryRoot, settings.GetCommand())
		} else {
			selected, err = onboard.MochaValidationCommand(repositoryRoot, settings.GetCommand())
		}
		preparationError = err
		if selected != "" {
			words, err := shellquote.Split(selected)
			if err != nil {
				return nil, err
			}
			command, args = words[0], words[1:]
		}
	}
	if runner.Name() == "playwright" && !all {
		selected, err := onboard.PlaywrightValidationCommand(repositoryRoot, settings.GetCommand())
		if err != nil {
			return nil, err
		}
		if selected != "" {
			words, err := shellquote.Split(selected)
			if err != nil {
				return nil, err
			}
			command, args = words[0], words[1:]
		}
	}
	label := map[string]string{"javascript": "dd-trace", "python": "ddtrace", "ruby": "datadog-ci"}[language] + "@" + version
	session := planSession()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var environment environmentInfo
	var environmentFailure *reportCard
	switch language {
	case "python":
		var source string
		command, args, source = pythonTestdriveCommand(repositoryRoot, command, args, strings.TrimSpace(settings.GetCommand()) != "")
		environment, environmentFailure = pythonEnvironmentInfo(ctx, command, args, source)
	case "javascript":
		environment = nodeEnvironmentInfo(ctx, command)
	case "ruby":
		environment = rubyEnvironmentInfo(ctx)
	}
	options := platform.TracerOptions{Directory: session.Directory(), Version: version, Command: command, Args: args}
	if language == "python" && environment.Executable != "" {
		options.Command, options.Args = environment.Executable, nil
	}
	var projectTracer string
	if environmentFailure == nil {
		projectTracer, _ = detectedPlatform.DetectTracer(ctx, options)
	}
	var installCommand string
	var installArgs []string
	if projectTracer == "" {
		installCommand, installArgs, err = detectedPlatform.TracerInstallCommand(options)
		if err != nil {
			return nil, err
		}
	}
	drive := &Testdrive{projectTracer: projectTracer, session: session, installCommand: installCommand, installArgs: installArgs, repositoryRoot: repositoryRoot, framework: runner, language: language, command: command, args: args, platform: detectedPlatform, tracerLabel: label, tracerVersion: version,
		executor: &ext.DefaultCommandExecutor{}, startIntake: func(directory string, scenario intake.Scenario) (localIntake, error) {
			return intake.StartScenario(directory, scenario)
		},
		nodeVersion: currentNodeVersion}
	drive.environmentInfo, drive.environmentFailure = environment, environmentFailure
	drive.preparationError = preparationError
	drive.allConfigurations = all
	if runner.Name() == "cypress" && !all && strings.TrimSpace(settings.GetCommand()) == "" {
		// Use the configuration executor for every discovered CI execution path,
		// preserving its application flags, environment and setup.
		if scope, err := onboard.DiscoverValidationScope(repositoryRoot, "cypress"); err == nil && len(scope.Commands) > 0 {
			drive.allConfigurations = true
		}
	}
	drive.checkOnly = len(checkOnly) > 0 && checkOnly[0]
	drive.bindPreflight()
	if js, ok := detectedPlatform.(*platform.JavaScript); ok {
		drive.resolveJSTracer = js.ResolveRegistryTracer
	}
	return drive, nil
}

// Bind method values after copying a drive so preflight uses its command and workspace.
func (t *Testdrive) bindPreflight() {
	switch t.framework.Name() {
	case "vitest":
		t.preflight = t.checkVitestPreflight
	case "playwright":
		t.preflight = t.checkPlaywrightPreflight
	case "mocha":
		t.preflight = t.checkMochaPreflight
	case "cypress":
		t.preflight = t.checkCypressPreflight
	case "cucumber":
		t.preflight = t.checkCucumberPreflight
	case "webdriverio":
		t.preflight = t.checkWebdriverIOPreflight
	default:
		t.preflight = t.checkJestPreflight
	}
}

func displayName(name string) string {
	names := map[string]string{"javascript": "JavaScript", "webdriverio": "WebdriverIO", "python": "Python", "ruby": "Ruby", "jest": "Jest", "mocha": "Mocha", "cypress": "Cypress", "playwright": "Playwright", "cucumber": "Cucumber", "vitest": "Vitest", "pytest": "pytest", "rspec": "RSpec", "minitest": "Minitest"}
	if label := names[name]; label != "" {
		return label
	}
	return name
}

// Preview describes the filesystem and process changes that Run will make.
func (t *Testdrive) Preview(output io.Writer) {
	if t.preparationError != nil {
		_, _ = fmt.Fprintf(output, "Discovery failed: %s\nWith --yes, save an INCOMPLETE JSON report; no tests will execute.\n", t.preparationError)
		return
	}
	if t.allConfigurations {
		var scope onboard.ValidationScope
		var err error
		if t.workflowScope != nil {
			scope = *t.workflowScope
		} else {
			scope, err = onboard.DiscoverValidationScope(t.repositoryRoot, t.framework.Name())
		}
		if err != nil {
			_, _ = fmt.Fprintln(output, err)
		} else {
			onboard.WriteValidationScope(output, scope)
		}
		_, _ = fmt.Fprintln(output, "All configurations will share .testoptimization/testdrive.json and report.html; previous runs do not certify this invocation.")
	}
	if t.checkOnly {
		_, _ = fmt.Fprintln(output, "Check configuration only: resolve the tracer, inspect the selected framework configuration, and review static CI runtimes. No tracer installation or tests; update .testoptimization/testdrive.json, retaining the latest paired execution as historical evidence.")
		return
	}
	command, args := t.executionCommand()
	reportPath := validationPath("")

	_, _ = fmt.Fprintf(output, "DDTest found %s and %s.\n", displayName(t.language), displayName(t.framework.Name()))
	_, _ = fmt.Fprintln(output)
	t.environmentInfo.write(output)
	_, _ = fmt.Fprintln(output, "It will:")
	directory := t.session.Directory()
	if relative, err := filepath.Rel(t.repositoryRoot, directory); err == nil {
		directory = relative
	}
	_, _ = fmt.Fprintf(output, "  - create a private temporary directory outside the repository: %s\n", directory)
	if t.environmentFailure != nil {
		_, _ = fmt.Fprintf(output, "  - save findings and troubleshooting; tests will not run: %s\n", t.environmentFailure.Title)
	} else if t.projectTracer != "" {
		_, _ = fmt.Fprintf(output, "  - reuse installed %s; no installation is needed\n", t.installedTracerLabel(t.projectTracer))
	} else if t.language == "python" {
		_, _ = fmt.Fprintf(output, "  - install %s in a temporary Python environment; remove it after the run\n", t.tracerLabel)
	} else {
		install := append([]string{t.installCommand}, t.installArgs...)
		for i, arg := range install {
			install[i] = strings.ReplaceAll(arg, t.session.Directory(), directory)
		}
		_, _ = fmt.Fprintf(output, "  - install %s: %s\n", t.tracerLabel, shellquote.Join(install...))
	}
	if supportsValidationFramework(t.framework.Name()) {
		_, _ = fmt.Fprintln(output, "  - inspect the selected framework configuration and tracer compatibility before running tests")
	}

	if t.allConfigurations && t.framework.Name() == "cypress" {
		_, _ = fmt.Fprintln(output, "  - run each listed Cypress configuration through its preserved API or CLI path")
	} else {
		_, _ = fmt.Fprintf(output, "  - command: %s\n", shellquote.Join(append([]string{command}, args...)...))
	}
	if supportsValidationFramework(t.framework.Name()) {
		_, _ = fmt.Fprintln(output, "  - check GitHub Actions Node runtimes against the workflow-selected tracer using public action and npm metadata")
		_, _ = fmt.Fprintln(output, "  - compare native JSON results without instrumentation and with reporting-only instrumentation")
		_, _ = fmt.Fprintln(output, "  - repeat the pair if outcomes differ; timing and console order are ignored")
		if t.framework.Name() == "webdriverio" {
			_, _ = fmt.Fprintln(output, "  - preserve WDIO services, capabilities and reporters in a temporary copy; clean owned server/browser processes")
			_, _ = fmt.Fprintln(output, "  - check five supported features per capability; TIA skipping and code coverage are not supported")
		} else if t.framework.Name() == "cypress" {
			_, _ = fmt.Fprintln(output, "  - check automatic retries, early flake detection, quarantine, disabled tests and attempt-to-fix; Cypress TIA skipping and application coverage validation are deferred")
		} else if t.framework.Name() == "playwright" {
			_, _ = fmt.Fprintln(output, "  - run in a temporary repository copy; capture native results and clean owned server processes after every run")
			_, _ = fmt.Fprintln(output, "  - check retries, EFD, quarantine, disabled tests and attempt-to-fix per Playwright project; TIA skipping/coverage are not supported")
		} else {
			_, _ = fmt.Fprintln(output, "  - discover and remove a temporary probe test under the selected project configuration; check retries, EFD, skipping, quarantine, disabled tests, and attempt-to-fix per project; wait for completion before running other repository checks")
		}
	} else {
		_, _ = fmt.Fprintln(output, "  - collect reporting-only telemetry; compatibility and features remain unvalidated for this framework")
	}
	_, _ = fmt.Fprintf(output, "  - update the validation report at %s; retain the latest paired execution as historical evidence until another pair runs\n", reportPath)
	_, _ = fmt.Fprintf(output, "  - save the instrumented suite findings in %s\n", htmlReportPath(""))
	if t.framework.Name() == "jest" || t.framework.Name() == "vitest" || t.framework.Name() == "mocha" || t.framework.Name() == "cucumber" {
		_, _ = fmt.Fprintln(output, "  - disable coverage thresholds only for isolated probes, preserving coverage collection and the original full-suite thresholds")
	}
	_, _ = fmt.Fprintln(output, "  - keep the upstream HTML findings report and compact JSON validation report; remove temporary probes, tracer, traffic, and run files")
	_, _ = fmt.Fprintln(output)
	if t.language == "ruby" && t.projectTracer == "" {
		_, _ = fmt.Fprintln(output, "Bundler updates Gemfile and Gemfile.lock.")
		return
	}
	patterns := map[string][]string{
		"javascript": {"package.json", "package-lock.json", "npm-shrinkwrap.json", "yarn.lock", "pnpm-lock.yaml", "bun.lock", "bun.lockb"},
		"python":     {"pyproject.toml", "setup.py", "setup.cfg", "requirements*.txt", "requirements*.in", "Pipfile", "Pipfile.lock", "poetry.lock", "uv.lock", "pdm.lock", "pylock.toml"},
		"ruby":       {"Gemfile", "Gemfile.lock", "gems.rb", "gems.locked"},
	}[t.language]
	entries, _ := os.ReadDir(t.repositoryRoot)
	var files []string
	for _, pattern := range patterns {
		for _, entry := range entries {
			matched, _ := filepath.Match(pattern, entry.Name())
			if !matched {
				continue
			}
			if info, err := os.Stat(filepath.Join(t.repositoryRoot, entry.Name())); err == nil && !info.IsDir() {
				files = append(files, entry.Name())
			}
		}
	}
	if len(files) > 0 {
		names := files[0]
		if len(files) > 1 {
			names = strings.Join(files[:len(files)-1], ", ") + " or " + files[len(files)-1]
		}
		_, _ = fmt.Fprintf(output, "It will not change %s.\n", names)
	}
}

// Run prepares the tracer and executes the detected test suite.
func (t *Testdrive) Run(ctx context.Context, output io.Writer) error {
	if t.preparationError != nil {
		result := validationResult{Session: planSession().ID(), Framework: t.framework.Name(), Tracer: t.tracerLabel, CheckOnly: t.checkOnly, Error: t.preparationError.Error(), Compatibility: verdict{Status: "not exercised", Reason: "Command resolution failed; no tests were executed."}}
		return errors.Join(t.preparationError, finishValidation(output, t.repositoryRoot, result))
	}
	if t.allConfigurations {
		return t.runAllConfigurations(ctx, output)
	}
	var result validationResult
	var runErr error
	if (t.framework.Name() == "vitest" && onboard.IsDDTestCommand(t.command) || t.framework.Name() == "playwright" || t.framework.Name() == "mocha" || t.framework.Name() == "cypress" || t.framework.Name() == "cucumber" || t.framework.Name() == "webdriverio") && !t.checkOnly {
		child := *t
		var models []reportModel
		child.reportModels = &models
		child.bindPreflight()
		result, runErr = child.runPreparedConfiguration(ctx, output, &configurationResult{})
		if result.Preflight != nil {
			result.Preflight.Command = shellquote.Join(append([]string{t.command}, t.args...)...)
		}
		if len(models) > 0 {
			err := writeConfigurationHTML(t.repositoryRoot, models[len(models)-1:])
			runErr = errors.Join(runErr, err)
			result.HTMLReportCurrent = err == nil
		}
	} else {
		result, runErr = t.run(ctx, output)
	}
	result.Framework = t.framework.Name()
	result.FocusedCommand = supportsValidationFramework(t.framework.Name()) && strings.TrimSpace(settings.GetCommand()) != ""
	if supportsValidationFramework(t.framework.Name()) {
		var scope onboard.ValidationScope
		var err error
		if t.workflowScope != nil {
			scope = *t.workflowScope
		} else {
			scope, err = onboard.DiscoverValidationScope(t.repositoryRoot, t.framework.Name())
		}
		if err != nil {
			runErr = errors.Join(runErr, err)
		} else {
			setSingleConfigurationScope(t.repositoryRoot, &result, scope)
		}
	}
	if runErr != nil {
		result.Error = runErr.Error()
		if result.Compatibility.Status == "" {
			result.Compatibility = verdict{Status: "not exercised", Reason: "Validation preparation failed; tests were not started."}
		}
	}
	return errors.Join(runErr, finishValidation(output, t.repositoryRoot, result))
}

func (t *Testdrive) run(ctx context.Context, output io.Writer) (result validationResult, runErr error) {
	session := t.session
	if session == nil {
		session = planSession()
	}
	if err := session.create(); err != nil {
		return result, err
	}

	t.session = session
	result = validationResult{CheckOnly: t.checkOnly, Session: session.ID(), Framework: t.framework.Name(), Tracer: t.tracerLabel,
		Compatibility: verdict{Status: "inconclusive", Reason: "Validation did not complete."}}
	defer func() {
		cleanupErr := session.Close()
		var supervision *processSupervisionError
		if errors.As(runErr, &supervision) {
			result.Compatibility = verdict{Status: "not exercised", Reason: supervision.Error()}
		}
		var processCleanup *processCleanupError
		if errors.As(runErr, &processCleanup) {
			cleanupErr = errors.Join(cleanupErr, processCleanup)
		}
		result.Cleanup = &verdict{Status: "passed", Reason: "Owned session storage and framework daemons removed."}
		if cleanupErr != nil {
			result.Cleanup = &verdict{Status: "failed", Reason: reportText(cleanupErr.Error())}
		}
		runErr = errors.Join(runErr, cleanupErr)
		if runErr != nil {
			result.Error = runErr.Error()
		}
	}()

	if t.environmentFailure != nil {
		result.Compatibility = verdict{Status: "not exercised", Reason: t.environmentFailure.Title}
		_, _ = fmt.Fprintf(output, "\nEnvironment check: %s\n%s\n", t.environmentFailure.Title, t.environmentFailure.Context)
		execution := validationRun{Command: shellquote.Join(append([]string{t.command}, t.args...)...), commandOutput: t.environmentFailure.Context}
		if err := t.writeHTMLReport(output, &result, execution); err != nil {
			return result, err
		}
		return result, errors.New(t.environmentFailure.Title)
	}

	if supportsValidationFramework(t.framework.Name()) {
		ciRoot := t.repositoryRoot
		if t.workflowRoot != "" {
			ciRoot = t.workflowRoot
		}
		check := onboard.CheckCIRuntimes(ctx, ciRoot, t.framework.Name())
		result.CIRuntime = &check
		if t.preflight != nil {
			if err := t.preflight(ctx, output, &result); err != nil {
				return result, err
			}
		}
		if t.checkOnly {
			result.Compatibility = verdict{Status: "not exercised", Reason: "Configuration checks only; run testdrive without --check-only for paired execution."}
			result.Features = []featureResult{{Name: "all", Status: "not exercised", Reason: "Configuration checks only."}}
			return result, nil
		}
	} else if t.checkOnly {
		return result, fmt.Errorf("configuration preflight currently supports Jest, Vitest, Playwright, Mocha, Cypress, Cucumber and WebdriverIO only")
	}

	_, _ = fmt.Fprintf(output, "\nPreparing %s in %s...\n", t.tracerLabel, session.Directory())
	version := t.tracerVersion
	if result.Selection != nil && result.Selection.Version != "" {
		version = result.Selection.Version
	}
	installation := platform.TracerInstallation{Project: t.projectTracer != ""}
	if t.language == "javascript" {
		installation.Path = t.projectTracer
	}
	if t.projectTracer == "" {
		var err error
		directory := session.Directory()
		if t.language == "python" {
			directory, err = os.MkdirTemp("", "ddtest-python-*")
			if err != nil {
				return result, err
			}
			defer func() {
				if err := os.RemoveAll(directory); err != nil {
					runErr = errors.Join(runErr, err)
				}
			}()
		}
		options := platform.TracerOptions{Directory: directory, Version: version, Command: t.command, Args: t.args}
		if t.language == "python" && t.environmentInfo.Executable != "" {
			options.Command, options.Args = t.environmentInfo.Executable, nil
		}
		installation, err = t.platform.InstallTestdriveTracer(ctx, options)
		if err != nil {
			return result, err
		}
	}

	result.TracerSource = "temporary installation"
	if t.language == "ruby" {
		result.TracerSource = "project bundle installation"
	}
	if installation.Project {
		result.Tracer = t.installedTracerLabel(t.projectTracer)
		result.TracerSource = "project installation (reused; fallback selector ignored)"
	}
	if t.framework.Name() == "vitest" || t.framework.Name() == "playwright" || t.framework.Name() == "mocha" || t.framework.Name() == "cypress" || t.framework.Name() == "cucumber" || t.framework.Name() == "webdriverio" {
		if err := verifyInstalledSelection(installation.Path, &result); err != nil {
			return result, err
		}
		runErr = t.runJavaScriptValidation(ctx, output, session, installation.Path, &result)
		return
	}
	if t.framework.Name() == "jest" {
		if result.Selection != nil {
			if err := verifyInstalledSelection(installation.Path, &result); err != nil {
				return result, err
			}
			result.Preflight.Verdict = checkJestSupport(*result.Preflight, *result.Selection)
			if result.Preflight.Verdict.Status != "compatible" {
				return result, fmt.Errorf("jest preflight: %s", result.Preflight.Verdict.Reason)
			}
		}
		runErr = t.runJavaScriptValidation(ctx, output, session, installation.Path, &result)
		return
	}
	result.TelemetryOnly = t.language == "python" || t.language == "ruby"
	server, err := t.startIntake(session.Directory(), intake.Scenario{})
	if err != nil {
		return result, err
	}
	intakeClosed := false
	defer func() {
		if !intakeClosed {
			runErr = errors.Join(runErr, server.Close())
		}
	}()

	command, args := t.executionCommand()
	_, _ = fmt.Fprintf(output, "Running %s...\n", shellquote.Join(append([]string{command}, args...)...))
	env := t.environment(installation.Path, server.URL(), session.ID())
	maps.Copy(env, installation.Env)

	testOutput, testErr := t.executor.CombinedOutput(ctx, command, args, env)
	execution := validationRun{Name: "reporting-only", Command: shellquote.Join(append([]string{command}, args...)...),
		Instrumented: true, ExitCode: commandExitCode(testErr), commandOutput: string(testOutput), commandError: testErr}
	result.Runs = []runSummary{execution.summary()}

	testOutputPath := filepath.Join(session.Directory(), testOutputFilename)
	if err := os.WriteFile(testOutputPath, testOutput, 0644); err != nil {
		return result, fmt.Errorf("save test output: %w", err)
	}
	if testErr != nil {
		result.Runs[0].Diagnostic = commandDiagnostic(testOutput)
		writeCommandFailure(output, displayName(t.framework.Name()), testOutput)
	}

	// Drain the intake before taking the snapshot used by the report.
	closeErr := server.Close()
	intakeClosed = true
	if closeErr != nil {
		return result, closeErr
	}
	findings, err := server.Facts()
	if err != nil {
		return result, err
	}
	runtime := reportRuntime{Environment: t.environmentInfo, SetupFailure: t.environmentFailure, Framework: displayName(t.framework.Name()), Tracer: result.Tracer + " · " + result.TracerSource, Command: execution.Command, Output: string(testOutput)}
	if testErr != nil {
		runtime.Error = testErr.Error()
	}
	model := buildReport(t.repositoryRoot, findings, testErr != nil, runtime)

	_, _ = fmt.Fprintln(output)
	if findings.TestEventCount > 0 {
		_, _ = fmt.Fprintln(output, "Test events received.")
	} else {
		_, _ = fmt.Fprintln(output, "No test events received.")
	}
	writeFindings(output, findings, model.Cards...)
	_, _ = fmt.Fprintln(output, "\nRun details:")
	_, _ = fmt.Fprintf(output, "  Test events: %d\n", findings.TestEventCount)
	_, _ = fmt.Fprintf(output, "  Tests: %d · Suites: %d\n", findings.TestCount, len(model.Suites))
	coverageLabel := "Tests with coverage"
	covered, total := findings.CoveredTestCount, findings.TestCount
	if findings.CoverageLevel == "suite" {
		coverageLabel = "Suites with coverage"
		covered, total = 0, len(model.Suites)
		for _, suite := range model.Suites {
			if suite.CoveredCount > 0 {
				covered++
			}
		}
	}
	_, _ = fmt.Fprintf(output, "  %s: %d / %d\n", coverageLabel, covered, total)
	if findings.CoverageLevel != "" {
		_, _ = fmt.Fprintf(output, "  Coverage level: %s\n", findings.CoverageLevel)
	}
	if covered == 0 {
		if findings.BrowserCoverageUnavailable && !findings.MissingCoverage && findings.EmptyCoverageEntryCount == 0 {
			_, _ = fmt.Fprintln(output, "  Coverage unavailable for runs containing Vitest browser projects.")
		} else if findings.EmptyCoverageEntryCount > 0 {
			_, _ = fmt.Fprintln(output, "  No valid coverage was reported by this run.")
		} else {
			_, _ = fmt.Fprintln(output, "  Coverage was not reported by this run.")
		}
	}
	status := passedFailed(testErr == nil)
	if findings.TestEventCount == 0 {
		status = "No test results received"
	}
	_, _ = fmt.Fprintf(output, "  %s: %s\n", displayName(t.framework.Name()), status)
	_, _ = fmt.Fprintf(output, "  Datadog library: %s · %s\n", result.Tracer, result.TracerSource)
	_, _ = fmt.Fprintf(output, "  Command: %s\n", execution.Command)
	result.Compatibility = verdict{Status: "inconclusive", Reason: "This framework has no compatibility adapter yet; telemetry alone does not validate behavior."}
	result.Features = []featureResult{{Name: "all", Status: "unvalidated", Reason: "Controlled feature scenarios are not implemented for " + displayName(t.framework.Name()) + "."}}
	result.Runs[0].TestEventCount = findings.TestEventCount
	execution.Facts = findings
	runErr = t.writeHTMLReport(output, &result, execution)
	if result.TelemetryOnly {
		if testErr != nil {
			runErr = errors.Join(runErr, fmt.Errorf("%s failed after sending %d test event(s): %w", t.framework.Name(), findings.TestEventCount, testErr))
		} else if findings.TestEventCount == 0 {
			runErr = errors.Join(runErr, fmt.Errorf("%s command exited successfully, but Test Optimization sent no test events", t.framework.Name()))
		}
	}
	return
}

// Show upstream's bounded failure output without linking temporary logs that
// session cleanup removes. The compact report retains a bounded diagnostic.
func writeCommandFailure(output io.Writer, label string, data []byte) {
	_, _ = fmt.Fprintf(output, "\n%s command output:\n", label)
	captured := strings.TrimSpace(string(data))
	lines := strings.Split(captured, "\n")
	switch {
	case captured == "":
		_, _ = fmt.Fprintln(output, "The command produced no output.")
	case len(lines) > 80:
		_, _ = fmt.Fprintln(output, strings.Join(lines[:40], "\n"))
		_, _ = fmt.Fprintf(output, "\n... %d %s omitted ...\n\n", len(lines)-80, plural(len(lines)-80, "line", "lines"))
		_, _ = fmt.Fprintln(output, strings.Join(lines[len(lines)-40:], "\n"))
	default:
		_, _ = fmt.Fprintln(output, captured)
	}
	_, _ = fmt.Fprintf(output, "\nBounded diagnostic: %s\n", validationPath(""))
}

func writeFindings(output io.Writer, findings intake.Facts, cards ...reportCard) {
	if len(findings.ConfigurationErrors) > 0 {
		_, _ = fmt.Fprintf(output, "Tracer configuration errors: %s.\n", strings.Join(findings.ConfigurationErrors, ", "))
	}
	count := len(findings.ConfigurationErrors)
	coverageDeferred := false
	for _, finding := range cards {
		if finding.Kind == "cypress-tia-deferred" {
			coverageDeferred = true
			_, _ = fmt.Fprintln(output, "Info: "+finding.Context)
		}
		if finding.Scope != runFindingScope {
			continue
		}
		count += finding.Count
		_, _ = fmt.Fprintf(output, "%s: %s\n", finding.Title, finding.Context)
		writeTroubleshooting(output, cards, finding.Kind)
	}
	if findings.TestEventCount == 0 {
		_, _ = fmt.Fprintln(output, "Test findings unavailable: no test events were received.")
	}
	if findings.MissingCoverage && !coverageDeferred {
		count++
		_, _ = fmt.Fprintln(output, intake.MissingCoverageContext)
		writeTroubleshooting(output, cards, intake.MissingCoverageFinding)
	}
	if findings.BrowserCoverageUnavailable {
		count++
		_, _ = fmt.Fprintf(output, "Info: %s. %s\n", intake.BrowserCoverageTitle, intake.BrowserCoverageContext)
		writeTroubleshooting(output, cards, intake.BrowserCoverageFinding)
	}
	if findings.EmptyCoverageEntryCount > 0 {
		count += findings.EmptyCoverageEntryCount
		_, _ = fmt.Fprintf(output, "Tracer error: received %d coverage entries with an empty files list. Affected payloads were excluded from coverage counts.\n", findings.EmptyCoverageEntryCount)
		writeTroubleshooting(output, cards, "error")
	}
	for _, size := range []int{
		len(findings.FailedTests), len(findings.FlakyTests), len(findings.SlowTests), len(findings.SlowSuites), len(findings.BroadCoverage),
	} {
		count += size
	}
	if count == 0 {
		if findings.TestEventCount == 0 {
			return
		}
		_, _ = fmt.Fprintln(output, "No findings.")
		return
	}

	writeTestFindings(output, "Failed tests", findings.FailedTests)
	writeTroubleshooting(output, cards, "failed")
	writeTestFindings(output, "Flaky tests", findings.FlakyTests)
	writeTroubleshooting(output, cards, "flaky")
	if len(findings.SlowTests) > 0 {
		_, _ = fmt.Fprintf(output, "\nTests slower than the others (%d):\n", len(findings.SlowTests))
		_, _ = fmt.Fprintf(output, "  Median test time: %s\n", formatDuration(findings.TestDurationMedian))
		writeTestFindingRows(output, findings.SlowTests)
	}
	if len(findings.SlowSuites) > 0 {
		_, _ = fmt.Fprintf(output, "\nSuites slower than the others (%d):\n", len(findings.SlowSuites))
		_, _ = fmt.Fprintf(output, "  Median suite time: %s\n", formatDuration(findings.SuiteDurationMedian))
		for _, suite := range findings.SlowSuites {
			name := suite.Suite
			if name == "" {
				name = "Unknown suite"
			}
			if suite.Module != "" {
				name = suite.Module + " › " + name
			}
			_, _ = fmt.Fprintf(output, "  - %s · %s\n", name, formatDuration(suite.Duration))
		}
	}
	if len(findings.BroadCoverage) > 0 {
		_, _ = fmt.Fprintf(output, "\nUnusually broad coverage (%d):\n", len(findings.BroadCoverage))
		_, _ = fmt.Fprintf(output, "  Median covered files: %d\n", findings.CoveredFilesMedian)
		for _, finding := range findings.BroadCoverage {
			_, _ = fmt.Fprintf(
				output, "  - %s · %d %s · %s level\n",
				finding.Name, finding.FileCount, plural(finding.FileCount, "file", "files"), finding.Level,
			)
			writeSourceLocation(output, finding.SourceFile, finding.SourceStart, finding.SourceEnd)
		}
	}
	writeTroubleshooting(output, cards, "slow")
	writeTroubleshooting(output, cards, "coverage")
}

func writeTestFindings(output io.Writer, title string, findings []intake.Test) {
	if len(findings) == 0 {
		return
	}
	_, _ = fmt.Fprintf(output, "\n%s (%d):\n", title, len(findings))
	writeTestFindingRows(output, findings)
}

func writeTestFindingRows(output io.Writer, findings []intake.Test) {
	for _, finding := range findings {
		status := testDisplayStatus(finding)
		_, _ = fmt.Fprintf(
			output, "  - %s · %s · %s\n",
			testFindingLabel(finding), status, formatDuration(findingDuration(finding)),
		)
		writeSourceLocation(output, finding.SourceFile, finding.SourceStart, finding.SourceEnd)
		for index, attempt := range finding.Attempts {
			kind := "Initial run"
			if attempt.Retry {
				kind = "Retry"
				if attempt.RetryReason != "" {
					kind += " · " + strings.ReplaceAll(attempt.RetryReason, "_", " ")
				}
			}
			_, _ = fmt.Fprintf(output, "    Run %d · %s · %s · %s\n", index+1, kind, displayStatus(attempt.Status), formatDuration(attempt.Duration))
			errorText := attempt.ErrorMessage
			if attempt.ErrorType != "" {
				if errorText != "" {
					errorText = attempt.ErrorType + ": " + errorText
				} else {
					errorText = attempt.ErrorType
				}
			}
			if errorText == "" && attempt.ErrorStack != "" {
				errorText, _, _ = strings.Cut(attempt.ErrorStack, "\n")
			}
			if errorText != "" {
				_, _ = fmt.Fprintf(output, "      %s\n", strings.ReplaceAll(errorText, "\n", "\n      "))
			}
		}
	}
}

func testFindingLabel(finding intake.Test) string {
	name := finding.Name
	if finding.Parameters != "" {
		name += " " + finding.Parameters
	}
	if finding.Suite != "" {
		name = finding.Suite + " › " + name
	}
	if finding.Module != "" {
		name = finding.Module + " › " + name
	}
	return name
}

func testEnvironment(intakeURL, sessionID string) map[string]string {
	return map[string]string{
		constants.APIKeyEnvironmentVariable:                           "ddtest-testdrive",
		constants.TestOptimizationEnabledEnvironmentVariable:          "true",
		constants.TestOptimizationAgentlessEnabledEnvironmentVariable: "true",
		constants.TestOptimizationAgentlessURLEnvironmentVariable:     intakeURL,
		constants.TestOptimizationTestSessionNameEnvironmentVariable:  "ddtest testdrive " + sessionID,
		"DDTEST_JEST_PROBE":                                   "0",
		"DD_CIVISIBILITY_GIT_UPLOAD_ENABLED":                  "false",
		"DD_CIVISIBILITY_ITR_ENABLED":                         "true",
		"DD_CIVISIBILITY_CODE_COVERAGE_REPORT_UPLOAD_ENABLED": "false",
		"DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED":       "false",
		"DD_TEST_EARLY_FLAKE_DETECTION_RETRY_COUNT":           "1",
		"DD_CIVISIBILITY_FLAKY_RETRY_ENABLED":                 "false",
		"DD_CIVISIBILITY_FLAKY_RETRY_COUNT":                   "2",
		"DD_CIVISIBILITY_IMPACTED_TESTS_DETECTION_ENABLED":    "false",
		"DD_TEST_FAILED_TEST_REPLAY_ENABLED":                  "false",
		"DD_TEST_MANAGEMENT_ENABLED":                          "false",
		"DD_TEST_MANAGEMENT_ATTEMPT_TO_FIX_RETRIES":           "1",
		"DD_INSTRUMENTATION_TELEMETRY_ENABLED":                "false",
		"DD_TRACE_STARTUP_LOGS":                               "false",
		"DD_TRACE_ENABLED":                                    "true",
		"DD_REMOTE_CONFIG_ENABLED":                            "false",
		"DD_PROFILING_ENABLED":                                "false",
		"DD_APPSEC_ENABLED":                                   "false",
		"DD_DYNAMIC_INSTRUMENTATION_ENABLED":                  "false",
		"DD_TRACE_AGENT_URL":                                  intakeURL,
		"DD_CIVISIBILITY_CODE_COVERAGE_ENABLED":               "true",
	}
}

func (t *Testdrive) environment(path, intakeURL, sessionID string) map[string]string {
	env := testEnvironment(intakeURL, sessionID)
	switch t.language {
	case "javascript":
		maps.Copy(env, t.javascriptEnvironment(path))
	case "python":
		maps.Copy(env, pythonEnvironment(path))
	case "ruby":
		maps.Copy(env, rubyEnvironment(path))
	}
	return env
}

func (t *Testdrive) installedTracerLabel(detected string) string {
	name := map[string]string{"javascript": "dd-trace", "python": "ddtrace", "ruby": "datadog-ci"}[t.language]
	version := ""
	switch t.language {
	case "javascript":
		version = javascriptTracerVersion(detected)
	case "python":
		version = detected
	case "ruby":
		if info, ok := strings.CutPrefix(strings.TrimSpace(detected), "* datadog-ci ("); ok {
			version, _, _ = strings.Cut(info, ")")
		}
	}
	if version != "" {
		return name + "@" + version
	}
	return name
}
