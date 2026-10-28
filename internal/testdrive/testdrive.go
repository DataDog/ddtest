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
	"slices"
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
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
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
	// configurationLocations are the CI locations of the configuration being run.
	configurationLocations []string
	// tracerEnvironment is what a temporary tracer installation adds to every run.
	tracerEnvironment map[string]string
	// runtimeVersion overrides the language runtime identity a coordinated
	// report is bound to; tests set it, everything else asks the runtime.
	runtimeVersion func() string
	// reuseCheckCopy lets one coordinated validation share a single owned
	// repository copy (checkCopy) across its read-only language checks.
	reuseCheckCopy bool
	// selectedSite is the Datadog site a coordinated validation checks CI
	// setup against: --site, otherwise DD_SITE. Empty checks no site.
	selectedSite             string
	checkCopy                string
	selectedPlan             onboard.TestCommand
	cypressPackage           string
	cypressSelected          onboard.TestCommand
	cypressDiscovered        bool // cypressSelected came from CI discovery, not --command.
	configurationSourceRoot  string
	scopeCommand             string
	cucumberCoverage         map[string]string
	workflowSelect           func(context.Context, onboard.ValidationScope, bool) (string, error)
	workflowPlan             *onboard.ExecutionPlan
	workflowReportOnly       []string // Configurations tracer selection left unedited and reported.
	workflowScope            *onboard.ValidationScope
	workflowRun              func(context.Context, io.Writer) error
	workflowSourceCheck      func(context.Context) error
	cucumberSupport          *cucumberSupport
	webdriverIOCapability    *int
	cucumberNode             string
	cucumberExecutable       string
	cypress                  *onboard.CypressCommand
	cypressCommand           string
	cypressCodeCoverage      string // Installed @cypress/code-coverage version, if any.
	playwrightGuard          bool
	playwrightProject        string
	configurationEnvironment map[string]string
	// Leading VAR=value assignments parsed from --command.
	commandAssignments   map[string]string
	allowReviewedBuilds  bool
	commandTimeout       time.Duration
	allConfigurations    bool
	reportModels         *[]reportModel
	workflowRoot         string
	configurationRoot    string
	vitestArguments      []string
	vitestReporters      []string
	vitestProbeArguments []string
	// Inspection may legitimately resolve no options (a bare vitest).
	vitestInspected, vitestProbeInspected bool
	vitestNode                            string
	vitestExecutable                      string
	vitestRunner                          bool
	isolated                              bool
	jestProject                           jestProject
	probeLintSkipped                      bool
	commandReviewReason                   string
	preflightEnvironment                  map[string]string
	discoveryError                        error
	preflight                             func(context.Context, io.Writer, *validationResult) error
	repositoryRoot                        string
	framework                             framework.Framework
	language                              string
	command                               string
	args                                  []string
	tracerLabel                           string
	platform                              platform.Platform
	tracerVersion                         string
	tracerVersionDefaulted                bool // --tracer-version was not given.
	projectTracer                         string
	session                               *workspace.Session
	installCommand                        string
	installArgs                           []string
	executor                              commandExecutor
	startIntake                           func(string, intake.Scenario) (localIntake, error)
	nodeVersion                           func() string
	environmentInfo                       environmentInfo
	environmentFailure                    *reportCard
	checkOnly                             bool
	resolveJSTags                         func(context.Context) (map[string]string, error)
	resolveJSTracer                       func(context.Context, string) (platform.JSSelection, error)

	// acceptSubstituteRuntime completes validation on a compatible local runtime.
	acceptSubstituteRuntime bool
}

// Prepare detects the repository and probes the selected test environment and tracer.
type PrepareOptions struct {
	AllowReviewedBuilds bool
	Timeout             time.Duration
	TracerVersion       string
	// DefaultTracerVersion marks TracerVersion as the flag default, which a
	// version pinned by every instrumented CI job replaces.
	DefaultTracerVersion bool
	CheckOnly            bool
	All                  bool

	// AcceptSubstituteRuntime completes validation on a compatible local runtime.
	AcceptSubstituteRuntime bool
}

func checkTemporaryStorage() error {
	directory, err := os.MkdirTemp("", "ddtest-storage-check-")
	if err != nil {
		return fmt.Errorf("temporary folder %s does not exist or is not writable; set TMPDIR to a writable folder: %w", os.TempDir(), err)
	}
	return os.Remove(directory)
}

func Prepare(configuration PrepareOptions) (*Testdrive, error) {
	timeout := configuration.Timeout
	if timeout < 0 {
		return nil, fmt.Errorf("--timeout must be positive")
	}
	if timeout == 0 {
		timeout = 10 * time.Minute
	}
	version := configuration.TracerVersion
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
	selectedLanguage := languageNamed(language)
	if selectedLanguage == nil {
		selectedLanguage = unsupportedLanguage{}
	}
	if err := selectedLanguage.prepareStorage(); err != nil {
		return nil, err
	}
	configurationOnly := configuration.CheckOnly
	all := configuration.All
	if all && strings.TrimSpace(settings.GetCommand()) != "" {
		return nil, fmt.Errorf("--all and --command are mutually exclusive")
	}
	if all && !frameworkFeaturePolicies[runner.Name()].Configurations {
		return nil, fmt.Errorf("--all currently validates %s only", frameworkNamesWith(func(policy frameworkPolicy) bool { return policy.Configurations }))
	}
	if all {
		// Every discovered CI configuration is validated and set up on the
		// selected provider, which must support the language.
		if err := onboard.CheckCIProviderSupport(repositoryRoot, language, runner.Name()); err != nil {
			return nil, err
		}
	}

	command, args, selected, discoveryError := selectedLanguage.selectCommand(commandRequest{root: repositoryRoot, runner: runner, override: strings.TrimSpace(settings.GetCommand()), all: all})
	if !selected {
		command, args = runner.Command()
	}
	var selectedPlan onboard.TestCommand
	var preflightEnvironment map[string]string
	if configurationOnly && !frameworkFeaturePolicies[runner.Name()].CheckOnly {
		return nil, fmt.Errorf("configuration preflight currently supports %s only", frameworkNamesWith(func(policy frameworkPolicy) bool { return policy.CheckOnly }))
	}
	if frameworkFeaturePolicies[runner.Name()].Preflight {
		repositoryRoot, err = filepath.EvalSymlinks(repositoryRoot)
		if err != nil {
			return nil, fmt.Errorf("resolve JavaScript repository root: %w", err)
		}
	}
	// Command selection and CI environment matching share one discovery.
	var scope onboard.ValidationScope
	var scopeErr error
	if frameworkFeaturePolicies[runner.Name()].Configurations && !all {
		scope, scopeErr = onboard.DiscoverValidationScope(repositoryRoot, runner.Name())
	}
	if runner.Name() == "jest" && !all {
		var selected string
		if configurationOnly {
			var entry onboard.TestCommand
			entry, err = onboard.JestConfigurationCommand(repositoryRoot, strings.TrimSpace(settings.GetCommand()))
			selected, preflightEnvironment = entry.Command, entry.Environment
		} else if override := strings.TrimSpace(settings.GetCommand()); override != "" {
			selectedPlan, err = onboard.ForwardJestExecution(repositoryRoot, override)
			selected = selectedPlan.Command
		} else {
			selectedPlan, err = onboard.JestExecutionCommand(repositoryRoot)
			selected = selectedPlan.Command
		}
		if err != nil {
			discoveryError = err
		}
		if selected != "" {
			words, err := shellquote.Split(selected)
			if err != nil || len(words) == 0 {
				discoveryError = fmt.Errorf("invalid Jest command: %s", selected)
			} else {
				command, args = words[0], words[1:]
				if command == "npx" && !slices.Contains(args, "--no-install") {
					args = append([]string{"--no-install"}, args...)
				}
			}

		}
	}
	// A failed discovery still runs startup recovery and replaces the earlier
	// report after confirmation, so a stale PASSED result never stays current.
	if runner.Name() == "playwright" && !all {
		selected, resolveErr := onboard.PlaywrightValidationCommandInScope(repositoryRoot, strings.TrimSpace(settings.GetCommand()), scope, scopeErr)
		if resolveErr != nil {
			discoveryError = resolveErr
		} else if selected != "" {
			words, splitErr := shellquote.Split(selected)
			if splitErr != nil || len(words) == 0 {
				discoveryError = fmt.Errorf("invalid Playwright command: %s", selected)
			} else {
				command, args = words[0], words[1:]
			}
		}
	}
	if runner.Name() == "cypress" && !all {
		selectedPlan, err = onboard.CypressExecutionCommandInScope(repositoryRoot, strings.TrimSpace(settings.GetCommand()), scope, scopeErr)
		if err != nil {
			discoveryError = err
		} else if selectedPlan.Cypress != nil {
			command, args = "node", nil
		} else {
			words, splitErr := shellquote.Split(selectedPlan.Command)
			if splitErr != nil || len(words) == 0 {
				discoveryError = fmt.Errorf("invalid Cypress command: %s", selectedPlan.Command)
			} else {
				command, args = words[0], words[1:]
			}
		}
	}
	if runner.Name() == "cucumber" && !all {
		var resolveErr error
		selectedPlan, resolveErr = onboard.CucumberExecutionCommand(repositoryRoot, strings.TrimSpace(settings.GetCommand()))
		selected := selectedPlan.Command
		if resolveErr != nil {
			discoveryError = resolveErr
		} else if selected != "" {
			words, splitErr := shellquote.Split(selected)
			if splitErr != nil || len(words) == 0 {
				discoveryError = fmt.Errorf("invalid Cucumber command: %s", selected)
			} else {
				command, args = words[0], words[1:]
			}
		}
	}
	if runner.Name() == "webdriverio" && !all {
		selected, resolveErr := onboard.WebdriverIOValidationCommand(repositoryRoot, strings.TrimSpace(settings.GetCommand()))
		if resolveErr != nil {
			discoveryError = resolveErr
		} else if selected != "" {
			words, splitErr := shellquote.Split(selected)
			if splitErr != nil || len(words) == 0 {
				discoveryError = fmt.Errorf("invalid WebdriverIO command: %s", selected)
			} else {
				command, args = words[0], words[1:]
			}
		}
	}
	if runner.Name() == "mocha" && !all {
		selected, resolveErr := onboard.MochaValidationCommandInScope(repositoryRoot, strings.TrimSpace(settings.GetCommand()), scope, scopeErr)
		if resolveErr != nil {
			discoveryError = resolveErr
		} else if selected != "" {
			words, splitErr := shellquote.Split(selected)
			if splitErr != nil || len(words) == 0 {
				discoveryError = fmt.Errorf("invalid Mocha command: %s", selected)
			} else {
				command, args = words[0], words[1:]
			}
		}
	}
	label := selectedLanguage.tracerPackage() + "@" + version

	session := workspace.PlanSession()
	if !supportsValidationFramework(runner.Name()) {
		session.Persist(filepath.Join(repositoryRoot, ".testoptimization", "testdrive", session.ID()))
	}
	if runner.Name() == "vitest" && !all {
		selectedPlan, discoveryError = onboard.VitestValidationCommandInScope(repositoryRoot, strings.TrimSpace(settings.GetCommand()), scope, scopeErr)
		if discoveryError == nil {
			words, splitErr := shellquote.Split(selectedPlan.Command)
			if splitErr != nil || len(words) == 0 {
				discoveryError = fmt.Errorf("invalid Vitest command: %s", selectedPlan.Command)
			} else {
				command, args = words[0], words[1:]
			}
			preflightEnvironment = selectedPlan.Environment
		}
	}
	originalCommand := shellquote.Join(append([]string{command}, args...)...)
	if selectedPlan.Cypress != nil {
		originalCommand = selectedPlan.Command
	}
	var commandAssignments map[string]string
	if selectedLanguage.parsesCommandAssignments() && !all && selectedPlan.Cypress == nil {
		parsed, arguments, assignments, parseErr := onboard.ParseValidationCommand(originalCommand)
		if parseErr != nil {
			discoveryError = parseErr
		} else {
			command, args, commandAssignments = parsed, arguments, assignments
			preflightEnvironment = mergeConfigurationEnvironment(preflightEnvironment, assignments)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command, args, environment, preflight := selectedLanguage.inspectEnvironment(ctx, environmentRequest{root: repositoryRoot, command: command, args: args, explicit: strings.TrimSpace(settings.GetCommand()) != "", checkOnly: configurationOnly, discoveryError: discoveryError})
	scopeCommand := selectedPlan.ScopeCommand
	if scopeCommand == "" {
		scopeCommand = originalCommand
	}
	var configurationEnvironment map[string]string
	if frameworkFeaturePolicies[runner.Name()].Configurations && !all {
		// A command matching a discovered CI configuration replays its environment,
		// including the variables GitHub sets on every step.
		matched := matchingConfigurationEnvironment(scope, scopeErr, repositoryRoot, runner.Name(), scopeCommand)
		if matched != nil {
			matched = onboard.WithCIProviderDefaults(matched)
		}
		configurationEnvironment = mergeConfigurationEnvironment(matched, preflightEnvironment)
		configurationEnvironment = mergeConfigurationEnvironment(configurationEnvironment, selectedPlan.Environment)
		configurationEnvironment = mergeConfigurationEnvironment(configurationEnvironment, commandAssignments)
	}
	options := platform.TracerOptions{Directory: session.Directory(), Version: version, Command: command, Args: args, Environment: configurationEnvironment}
	options.Command, options.Args = selectedLanguage.tracerCommand(environment, options.Command, options.Args)
	var projectTracer string
	if preflight == nil && !configurationOnly && discoveryError == nil && selectedLanguage.detectsTracerInPreparation() {
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

	drive := &Testdrive{selectedPlan: selectedPlan, commandAssignments: commandAssignments, probeLintSkipped: selectedPlan.ProbeLintSkipped, commandReviewReason: selectedPlan.ReviewReason, projectTracer: projectTracer, session: session, installCommand: installCommand, installArgs: installArgs, repositoryRoot: repositoryRoot, framework: runner, language: language, command: command, args: args, platform: detectedPlatform, tracerVersion: version, tracerVersionDefaulted: configuration.DefaultTracerVersion, tracerLabel: label,
		executor: &ext.DefaultCommandExecutor{}, startIntake: func(directory string, scenario intake.Scenario) (localIntake, error) {
			return intake.StartScenario(directory, scenario)
		}, nodeVersion: currentNodeVersion, environmentInfo: environment, environmentFailure: preflight, checkOnly: configurationOnly, discoveryError: discoveryError, preflightEnvironment: preflightEnvironment}
	if runner.Name() == "vitest" && !onboard.IsDDTestCommand(drive.command) {
		drive.command = drive.configurationExecutable(drive.command)
	}
	drive.allConfigurations = all
	if runner.Name() == "cypress" {
		drive.cypressSelected = selectedPlan
		drive.cypressDiscovered = strings.TrimSpace(settings.GetCommand()) == "" && discoveryError == nil
		drive.cypress, drive.cypressCommand, drive.cypressPackage = selectedPlan.Cypress, selectedPlan.Command, selectedPlan.CypressPackage
	}

	drive.scopeCommand = scopeCommand
	drive.allowReviewedBuilds = configuration.AllowReviewedBuilds
	drive.acceptSubstituteRuntime = configuration.AcceptSubstituteRuntime
	drive.commandTimeout = timeout
	if supportsValidationFramework(runner.Name()) {
		drive.executor = &ext.DefaultCommandExecutor{Timeout: timeout}
	}
	drive.configurationEnvironment = configurationEnvironment
	if len(configurationEnvironment) > 0 {
		drive.executor = configurationExecutor{drive.executor, configurationEnvironment}
	}
	if frameworkFeaturePolicies[runner.Name()].Preflight {
		drive.preflight = drive.frameworkPreflight()
	}
	if installer, ok := detectedPlatform.(*platform.JavaScript); ok {
		drive.resolveJSTracer = installer.ResolveRegistryTracer
		drive.resolveJSTags = installer.RegistryTracerTags
	}
	return drive, nil
}

func displayName(name string) string {
	for _, language := range validationLanguages {
		if label := language.displayNames()[name]; label != "" {
			return label
		}
	}
	return name
}

// Preview describes the filesystem and process changes that Run will make.
func (t *Testdrive) Preview(output io.Writer) {
	if t.discoveryError != nil {
		_, _ = fmt.Fprintf(output, "Command discovery failed: %s\nWith --yes, save a failed report without running tests.\n", t.discoveryError)
		return
	}
	if t.allConfigurations {
		scope, err := onboard.DiscoverValidationScope(t.repositoryRoot, t.framework.Name())
		if err != nil {
			_, _ = fmt.Fprintln(output, err)
		} else if onboard.RedactsCommandKeys(t.framework.Name()) {
			onboard.WriteValidationScope(output, onboard.RedactCypressScope(scope))
		} else {
			onboard.WriteValidationScope(output, scope)
		}
		_, _ = fmt.Fprintln(output, "Every discovered configuration shares the final JSON/HTML reports. Preparation runs customer code. Package lifecycle scripts and dependency changes can use the network; review-tagged preparation requires --allow-reviewed-builds. Unresolved CI setup stays unvalidated.")
		if !t.checkOnly {
			needsCopy := slices.ContainsFunc(scope.Commands, func(c onboard.TestCommand) bool { return len(c.Prerequisites) > 0 })
			if needsCopy {
				if size, err := configurationCopyEstimate(context.Background(), t.repositoryRoot, skipIgnoredOutput); err == nil {
					_, _ = fmt.Fprintf(output, "Estimated copy per prepared configuration: up to %d bytes; ignored outputs excluded, dependencies and Git metadata retained. Copy-on-write when available.\n", size)
				} else {
					_, _ = fmt.Fprintf(output, "Copy estimate unavailable: %s\n", err)
				}
			}
		}
		_, _ = fmt.Fprintf(output, "Each test or preparation subprocess has a %s timeout (--timeout).\n", t.commandTimeout)
		return

	}

	if t.checkOnly {
		_, _ = fmt.Fprintln(output, "Check configuration only: load the selected framework configuration, resolve tracer metadata and review static CI runtimes. No tracer installation or tests; write .testoptimization/testdrive.json. Framework configuration may execute project JavaScript. CI execution and Datadog backend processing remain untested.")
		return
	}
	command, args := t.executionCommand()
	directory := t.session.Directory()
	if relative, err := filepath.Rel(t.repositoryRoot, directory); err == nil {
		directory = relative
	}

	_, _ = fmt.Fprintf(output, "DDTest found %s and %s.\n", displayName(t.language), displayName(t.framework.Name()))
	_, _ = fmt.Fprintln(output)
	t.environmentInfo.write(output)
	_, _ = fmt.Fprintln(output, "\nRun:")
	if supportsValidationFramework(t.framework.Name()) {
		_, _ = fmt.Fprintln(output, "  - compare the original suite without instrumentation and with reporting-only instrumentation; repeat differing pairs")
		var required []string
		for _, policy := range featurePolicies(t.framework.Name()) {
			if policy.Status == "required" {
				required = append(required, policy.Name)
			}
		}
		_, _ = fmt.Fprintf(output, "  - discover a temporary probe with the original framework configuration; check %s per selected project\n", strings.Join(required, ", "))
		if policy := skippingPolicy(t.framework.Name()); policy.Status == "not supported" {
			_, _ = fmt.Fprintln(output, "  - TIA skipping/per-test coverage: "+policy.Status+"; "+policy.Reason)
		}
		_, _ = fmt.Fprintln(output, "  - keep .testoptimization/testdrive.json and .testoptimization/report.html; remove owned probes, temporary tracer and run files")
	}
	if supportsValidationFramework(t.framework.Name()) {
		_, _ = fmt.Fprintf(output, "  - use private temporary scratch storage: %s (removed after the run)\n", directory)
	} else {
		_, _ = fmt.Fprintf(output, "  - create an output folder: %s\n", directory)
	}
	if t.environmentFailure != nil {
		_, _ = fmt.Fprintf(output, "  - save findings and troubleshooting; tests will not run: %s\n", t.environmentFailure.Title)
	} else if t.projectTracer != "" {
		_, _ = fmt.Fprintf(output, "  - reuse installed %s; no installation is needed\n", t.installedTracerLabel(t.projectTracer))
	} else if preview := t.lang().installPreview(t.tracerLabel); preview != "" {
		_, _ = fmt.Fprintf(output, "  - %s\n", preview)
	} else {
		install := append([]string{t.installCommand}, t.installArgs...)
		for i, arg := range install {
			install[i] = strings.ReplaceAll(arg, t.session.Directory(), directory)
		}
		_, _ = fmt.Fprintf(output, "  - install %s: %s\n", t.tracerLabel, shellquote.Join(install...))
	}
	for _, note := range t.lang().previewNotes() {
		_, _ = fmt.Fprintf(output, "  - %s\n", note)
	}

	preview := shellquote.Join(append([]string{command}, args...)...)
	if onboard.RedactsCommandKeys(t.framework.Name()) {
		preview = onboard.RedactCypressCommand(preview)
	}
	_, _ = fmt.Fprintf(output, "  - command: %s\n", preview)
	if t.framework.Name() == "mocha" {
		for _, entry := range onboard.ValidationCommands(t.repositoryRoot, shellquote.Join(append([]string{command}, args...)...), "mocha") {
			onboard.WriteLifecycleHooks(output, entry)
		}
	}
	_, _ = fmt.Fprintln(output)
	patterns, note := t.lang().dependencyChanges(t.projectTracer)
	if note != "" {
		_, _ = fmt.Fprintln(output, note)
		return
	}
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

// workflowLockKey marks a context whose caller already holds the checkout lock.
type workflowLockKey struct{}

func (t *Testdrive) Run(ctx context.Context, output io.Writer) (runErr error) {
	if t.framework.Name() == "cypress" {
		ctx = onboard.WithCypressMetadataCache(ctx)
	}
	defer func() {
		if runErr != nil && supportsValidationFramework(t.framework.Name()) {
			runErr = validationFailure{runErr}
		}
	}()

	if ctx.Value(workflowLockKey{}) == workspace.CanonicalRoot(t.repositoryRoot) {
		// The coordinator recovered abandoned storage when it took this lock,
		// before creating live storage such as the shared check copy. A second
		// recovery would delete that live storage.
		return t.runLocked(ctx, output, false)
	}
	lock, err := workspace.AcquireLock(t.repositoryRoot)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	return t.runLocked(ctx, output, true)
}

func (t *Testdrive) runLocked(ctx context.Context, output io.Writer, recoverStorage bool) (runErr error) {
	if err := cleanupExpiredCheckpoints(); err != nil {
		return t.finishStartupFailure(output, err)
	}
	if recoverStorage {
		if err := t.recoverStartup(output); err != nil {
			return t.finishStartupFailure(output, err)
		}
	}
	if t.allConfigurations {
		return t.runAllConfigurations(ctx, output)
	}
	if t.checkOnly {
		return t.runConfigurationChecks(ctx, output)
	}
	if t.discoveryError != nil {
		result := validationResult{Framework: t.framework.Name(), Tracer: t.tracerLabel, Error: t.discoveryError.Error(),
			Compatibility: verdict{Status: "not exercised", Reason: "Command discovery failed; no tests were run."}}
		return errors.Join(t.discoveryError, finishValidation(output, t.repositoryRoot, result))
	}

	var result validationResult
	var prepared *configurationResult
	if t.discoveryError != nil {
		result, runErr = t.run(ctx, output)
	} else if t.lang().isolatesConfigurations() || t.framework.Name() == "webdriverio" || t.framework.Name() == "cucumber" || t.framework.Name() == "cypress" || t.framework.Name() == "playwright" || t.framework.Name() == "mocha" || (t.framework.Name() == "vitest" && t.usesVitestRunner()) {
		child := *t
		child.workflowRoot = t.repositoryRoot
		child.preflight = child.frameworkPreflight()
		// The run executes in a disposable copy; publish its report here instead.
		var models []reportModel
		child.reportModels = &models
		entry := child.selectedPlan
		entry.Environment = child.configurationEnvironment
		if child.framework.Name() == "cypress" {
			entry = child.cypressSelected
			entry.Environment = child.configurationEnvironment
		}
		prepared = &configurationResult{TestCommand: entry}
		result, runErr = child.runPreparedConfiguration(ctx, output, prepared)
		runErr = errors.Join(runErr, t.publishCopiedRunReport(output, &result, models))
	} else {
		result, runErr = t.run(ctx, output)
	}
	result.Framework = t.framework.Name()
	result.FocusedCommand = frameworkFeaturePolicies[t.framework.Name()].Configurations && strings.TrimSpace(settings.GetCommand()) != ""
	result.CommandEnvironment = reportedEnvironment(t.commandAssignments)
	if frameworkFeaturePolicies[t.framework.Name()].Configurations {
		prepareValidationResult(t.repositoryRoot, &result)
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
			t.setConfigurationScope(&result, scope)
			if prepared != nil {
				creditPreparation(&result, *prepared)
			}
		}
	}
	if runErr != nil {
		result.Error = runErr.Error()
	}
	return errors.Join(runErr, finishValidation(output, t.repositoryRoot, result))
}

// publishCopiedRunReport writes the last retained report, as a run in place
// would leave it, under the original repository.
func (t *Testdrive) publishCopiedRunReport(output io.Writer, result *validationResult, models []reportModel) error {
	if len(models) == 0 {
		return nil
	}
	if err := writeHTMLReportFile(t.repositoryRoot, models[len(models)-1]); err != nil {
		result.HTMLReportCurrent = false
		return err
	}
	return announceHTMLReport(output, t.repositoryRoot, t.framework.Name())
}

// setConfigurationScope credits this single run to the discovered
// configurations it executed.
func (t *Testdrive) setConfigurationScope(result *validationResult, scope onboard.ValidationScope) {
	if t.cypressDiscovered {
		// The selected CI configuration ran as discovered, including its build,
		// working directory and managed startup; its command text alone may not.
		setSelectedConfigurationScope(result, scope, []onboard.TestCommand{t.cypressSelected}, t.configurationEnvironment)
		return
	}
	if t.framework.Name() == "cucumber" && len(t.selectedPlan.Prerequisites) > 0 {
		// The focused plan ran the CI preparation its command text drops.
		setSelectedConfigurationScope(result, scope, []onboard.TestCommand{t.selectedPlan}, t.configurationEnvironment)
		return
	}
	command := t.scopeCommand
	if command == "" {
		command = shellquote.Join(append([]string{t.command}, t.args...)...)
	}
	setSingleConfigurationScope(t.repositoryRoot, result, scope, command, t.configurationEnvironment)
}

// ambientDatadogEnvironment lists inherited DD_* values that test commands see
// because neither ddtest nor the CI configuration sets them. A local workaround
// like disabling an instrumentation must show up in the report.
// ddtestProcessSettings are the variables main sets on ddtest's own process
// before any command runs (main.go); they are not the user's settings.
var ddtestProcessSettings = map[string]string{
	"DD_CIVISIBILITY_ENABLED":                    "1",
	"DD_TELEMETRY_DEPENDENCY_COLLECTION_ENABLED": "0",
}

func ambientDatadogEnvironment(configured map[string]string) map[string]string {
	managed := testEnvironment("", "")
	maps.Copy(managed, reportingEnvironment("", ""))
	ambient := map[string]string{}
	for _, entry := range os.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(key, "DD_") && !strings.HasPrefix(key, "DATADOG_") {
			continue
		}
		if _, ok := managed[key]; ok {
			continue
		}
		if own, ok := ddtestProcessSettings[key]; ok && own == value {
			continue
		}
		if _, ok := configured[key]; ok {
			continue
		}
		ambient[key] = value
	}
	return ambient
}

// Startup recovery runs under the repository lock, before any new work.
func (t *Testdrive) recoverStartup(output io.Writer) error {
	if err := workspace.RecoverSessions(t.repositoryRoot, output); err != nil {
		return err
	}
	if err := recoverProcessOwners(t.repositoryRoot, output); err != nil {
		return err
	}
	if !probeRecoveryEnabled() {
		return nil
	}
	return recoverProbesWithOutput(t.repositoryRoot, output)
}

func (t *Testdrive) run(ctx context.Context, output io.Writer) (result validationResult, runErr error) {
	session := t.session
	if session == nil {
		session = workspace.PlanSession()
	}
	if !supportsValidationFramework(t.framework.Name()) {
		session.Persist(filepath.Join(t.repositoryRoot, ".testoptimization", "testdrive", session.ID()))
	}
	session.SetOwnerRoot(t.ownershipRoot())
	if err := session.Create(); err != nil {
		return result, err
	}

	t.session = session
	result = validationResult{CheckOnly: t.checkOnly, Session: session.ID(), Framework: t.framework.Name(), Tracer: t.tracerLabel,
		Compatibility: verdict{Status: "inconclusive", Reason: "Validation did not complete."}}
	if !t.checkOnly {
		result.AmbientDatadogEnvironment = reportedEnvironment(ambientDatadogEnvironment(t.configurationEnvironment))
	}
	// Isolated configurations run in a disposable copy; anything else runs in the checkout.
	var entriesBefore map[string]bool
	if !t.isolated {
		entriesBefore = checkoutEntries(t.repositoryRoot)
	}
	defer func() {
		cleanupErr := session.Close()
		var supervision *processSupervisionError
		if errors.As(runErr, &supervision) {
			result.Compatibility = verdict{Status: "not exercised", Code: supervision.Code, Reason: supervision.Error()}
		}
		var processCleanup *processCleanupError
		if errors.As(runErr, &processCleanup) {
			cleanupErr = errors.Join(cleanupErr, processCleanup)
		}
		result.Cleanup = &verdict{Status: "passed", Reason: "Owned session storage removed."}
		if session.Persistent() {
			result.Cleanup = &verdict{Status: "not exercised", Reason: "Legacy diagnostics retained."}
		}
		if cleanupErr != nil {
			result.Cleanup = &verdict{Status: "failed", Reason: reportText(cleanupErr.Error())}
		}
		if entriesBefore != nil {
			// The test command's own outputs are not ddtest's to delete; name them instead of passing silently.
			if leftovers := checkoutLeftovers(entriesBefore, checkoutEntries(t.repositoryRoot)); len(leftovers) > 0 {
				result.Cleanup.Leftovers = leftovers
				result.Cleanup.Reason += " Left in the checkout by commands run there: " + strings.Join(leftovers, ", ") + "."
			}
		}
		runErr = errors.Join(runErr, cleanupErr)
		if runErr != nil {
			result.Error = runErr.Error()
		}
	}()

	if t.discoveryError != nil {
		return result, t.discoveryError
	}
	if t.environmentFailure != nil {
		result.Compatibility = verdict{Status: "not exercised", Reason: t.environmentFailure.Title}
		_, _ = fmt.Fprintf(output, "\nEnvironment check: %s\n%s\n", t.environmentFailure.Title, t.environmentFailure.Context)
		execution := validationRun{Command: shellquote.Join(append([]string{t.command}, t.args...)...), commandOutput: t.environmentFailure.Context}
		if !supportsValidationFramework(t.framework.Name()) {
			path := filepath.Join(session.Directory(), testOutputFilename)
			if err := os.WriteFile(path, []byte(t.environmentFailure.Context), 0600); err != nil {
				return result, err
			}
			model := buildReport(t.repositoryRoot, intake.Facts{}, false, reportRuntime{Environment: t.environmentInfo, SetupFailure: t.environmentFailure})
			findings, err := writeDiagnostics(t.repositoryRoot, session.Directory(), intake.Facts{}, model, false)
			if err != nil {
				return result, err
			}
			writeDiagnosticPaths(output, t.repositoryRoot, findings, path, "")
		}
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
		check := checkConfigurationCIRuntimes(ctx, ciRoot, t.framework.Name())
		if t.workflowRoot != "" {
			check = frameworkCIRuntimes(ctx, ciRoot, t.framework.Name())
		}
		result.CIRuntime = &check
		t.defaultToCITracer(check)
		if t.preflight != nil {
			if err := t.preflight(ctx, output, &result); err != nil {
				return result, err
			}
		}
	}

	if t.projectTracer != "" {
		_, _ = fmt.Fprintf(output, "\nReusing installed %s...\n", t.installedTracerLabel(t.projectTracer))
	} else {
		_, _ = fmt.Fprintf(output, "\nInstalling %s...\n", t.tracerLabel)
	}
	version := t.tracerVersion
	if result.Selection != nil && result.Selection.Version != "" {
		version = result.Selection.Version
	}
	installation := platform.TracerInstallation{Project: t.projectTracer != "", Path: t.lang().projectTracerPath(t.projectTracer)}
	if t.projectTracer == "" {
		directory, cleanup, err := t.lang().installDirectory(session.Directory(), t.repositoryRoot)
		if cleanup != nil {
			defer func() {
				if err := cleanup(); err != nil {
					runErr = errors.Join(runErr, err)
				}
			}()
		}
		if err != nil {
			return result, err
		}
		options := platform.TracerOptions{Directory: directory, Version: version, Command: t.command, Args: t.args, Environment: t.configurationEnvironment}
		options.Command, options.Args = t.lang().tracerCommand(t.environmentInfo, options.Command, options.Args)
		if supportsValidationFramework(t.framework.Name()) {
			// The same --timeout bound as every other validation subprocess.
			options.Timeout = t.commandTimeout
		}
		var handled bool
		installation, handled, err = t.lang().installTracer(t, ctx, options)
		if !handled && err == nil {
			installation, err = t.platform.InstallTestdriveTracer(ctx, options)
		}
		if err != nil {
			return result, err
		}
	}

	result.TracerSource = t.lang().temporaryTracerSource()
	if installation.Project {
		result.Tracer = t.installedTracerLabel(t.projectTracer)
		result.TracerSource = "project installation · reused"
	}
	if supportsValidationFramework(t.framework.Name()) {
		if adapter, ok := pairedFrameworkNamed(t.framework.Name()); ok && adapter.preflightAfterInstall && t.preflight != nil {
			if err := t.preflight(ctx, output, &result); err != nil {
				return result, err
			}
		}
		if result.Selection != nil {
			if err := t.verifyInstalledSelection(ctx, installation.Path, &result); err != nil {
				return result, err
			}
			if t.framework.Name() == "vitest" {
				result.Preflight.Verdict = checkVitestSupport(*result.Preflight, *result.Selection)
			} else if t.framework.Name() == "cypress" {
				result.Preflight.Verdict = checkCypressSupport(*result.Preflight, *result.Selection)
			} else if t.framework.Name() == "playwright" {
				result.Preflight.Verdict = checkPlaywrightSupport(*result.Preflight, *result.Selection)
			} else if t.framework.Name() == "webdriverio" {
				result.Preflight.Verdict = checkWebdriverIOSupport(*result.Preflight, *result.Selection)
			} else if t.framework.Name() == "cucumber" {
				result.Preflight.Verdict = checkCucumberSupport(*result.Preflight, *result.Selection)
			} else if t.framework.Name() == "mocha" {
				result.Preflight.Verdict = checkMochaSupport(*result.Preflight, *result.Selection)
			} else {
				result.Preflight.Verdict = checkJestSupport(*result.Preflight, *result.Selection)
			}
			if result.Preflight.Verdict.Status != "compatible" {
				return result, fmt.Errorf("%s preflight: %s", t.framework.Name(), result.Preflight.Verdict.Reason)
			}
		}
		runErr = t.runFrameworkValidation(ctx, output, session, installation.Path, &result)
		return
	}
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
	execution := validationRun{Role: roleReporting, Name: "reporting-only", Command: shellquote.Join(append([]string{command}, args...)...),
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
	runtime := reportRuntime{Environment: t.environmentInfo, SetupFailure: t.environmentFailure, Framework: displayName(t.framework.Name()), Tracer: tracerDescription(result), Command: execution.Command, Output: string(testOutput)}
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
	t.environmentInfo.write(output)
	diagnosticsPath, err := writeDiagnostics(t.repositoryRoot, session.Directory(), findings, model, testErr != nil)
	if err != nil {
		return result, err
	}
	writeDiagnosticPaths(output, t.repositoryRoot, diagnosticsPath, testOutputPath, filepath.Join(session.Directory(), "intake"))
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
	_, _ = fmt.Fprintf(output, "  Datadog library: %s\n", tracerDescription(result))
	_, _ = fmt.Fprintf(output, "  Command: %s\n", execution.Command)
	result.Compatibility = verdict{Status: "inconclusive", Reason: "This framework has no compatibility adapter yet; telemetry alone does not validate behavior."}
	result.Features = []featureResult{{Name: "all", Status: "unvalidated", Reason: "Controlled feature scenarios are not implemented for " + displayName(t.framework.Name()) + "."}}
	result.Runs[0].TestEventCount = findings.TestEventCount
	execution.Facts = findings
	runErr = t.writeHTMLReport(output, &result, execution)
	if testErr != nil {
		runErr = errors.Join(runErr, fmt.Errorf("%s failed after sending %d test event(s): %w", t.framework.Name(), findings.TestEventCount, testErr))
	} else if findings.TestEventCount == 0 {
		runErr = errors.Join(runErr, fmt.Errorf("%s command exited successfully, but Test Optimization sent no test events", t.framework.Name()))
	}
	return
}

func writeFindings(output io.Writer, findings intake.Facts, cards ...reportCard) {
	writeRunFindings(output, findings, true, cards...)
}

// Validation sessions delete captured traffic with their storage; only legacy
// telemetry-only sessions keep it under .testoptimization for inspection.
func inspectionHint(trafficRetained bool, retained string) string {
	if trafficRetained {
		return retained
	}
	return "Inspect the test output."
}

func writeRunFindings(output io.Writer, findings intake.Facts, trafficRetained bool, cards ...reportCard) {
	if len(findings.ConfigurationErrors) > 0 {
		_, _ = fmt.Fprintf(output, "Tracer configuration errors: %s. %s\n", strings.Join(findings.ConfigurationErrors, ", "), inspectionHint(trafficRetained, "Inspect the captured traffic and test output."))
	}
	count := len(findings.ConfigurationErrors)
	for _, finding := range cards {
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
	if findings.MissingCoverage {
		count++
		context := intake.MissingCoverageContext
		for _, card := range cards {
			if card.Kind == intake.MissingCoverageFinding {
				context = card.Context
			}
		}
		_, _ = fmt.Fprintln(output, context)
		writeTroubleshooting(output, cards, intake.MissingCoverageFinding)
	}
	if findings.BrowserCoverageUnavailable {
		count++
		_, _ = fmt.Fprintf(output, "Info: %s. %s\n", intake.BrowserCoverageTitle, intake.BrowserCoverageContext)
		writeTroubleshooting(output, cards, intake.BrowserCoverageFinding)
	}
	if findings.EmptyCoverageEntryCount > 0 {
		count += findings.EmptyCoverageEntryCount
		_, _ = fmt.Fprintf(output, "Tracer error: received %d coverage entries with an empty files list. Affected payloads were excluded from coverage counts. %s\n", findings.EmptyCoverageEntryCount, inspectionHint(trafficRetained, "Inspect the captured traffic."))
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
		"DD_CIVISIBILITY_GIT_UPLOAD_ENABLED":                          "false",
		"DD_CIVISIBILITY_ITR_ENABLED":                                 "true",
		"DD_CIVISIBILITY_CODE_COVERAGE_REPORT_UPLOAD_ENABLED":         "true",
		"DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED":               "true",
		"DD_TEST_EARLY_FLAKE_DETECTION_RETRY_COUNT":                   "1",
		"DD_CIVISIBILITY_FLAKY_RETRY_ENABLED":                         "true",
		"DD_CIVISIBILITY_FLAKY_RETRY_COUNT":                           "5",
		"DD_CIVISIBILITY_IMPACTED_TESTS_DETECTION_ENABLED":            "true",
		"DD_TEST_FAILED_TEST_REPLAY_ENABLED":                          "true",
		"DD_TEST_MANAGEMENT_ENABLED":                                  "true",
		"DD_TEST_MANAGEMENT_ATTEMPT_TO_FIX_RETRIES":                   "1",
		"DD_INSTRUMENTATION_TELEMETRY_ENABLED":                        "false",
		"DD_TRACE_STARTUP_LOGS":                                       "false",
	}
}

func reportingEnvironment(intakeURL, sessionID string) map[string]string {
	return map[string]string{
		constants.APIKeyEnvironmentVariable:                           "ddtest-testdrive",
		constants.TestOptimizationEnabledEnvironmentVariable:          "true",
		constants.TestOptimizationAgentlessEnabledEnvironmentVariable: "true",
		constants.TestOptimizationAgentlessURLEnvironmentVariable:     intakeURL,
		constants.TestOptimizationTestSessionNameEnvironmentVariable:  "ddtest testdrive " + sessionID,
		"DD_CIVISIBILITY_GIT_UPLOAD_ENABLED":                          "false",
		"DD_CIVISIBILITY_ITR_ENABLED":                                 "true",
		"DD_CIVISIBILITY_CODE_COVERAGE_REPORT_UPLOAD_ENABLED":         "false",
		"DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED":               "false",
		"DD_TEST_EARLY_FLAKE_DETECTION_RETRY_COUNT":                   "1",
		"DD_CIVISIBILITY_FLAKY_RETRY_ENABLED":                         "false",
		"DD_CIVISIBILITY_FLAKY_RETRY_COUNT":                           "2",
		"DD_CIVISIBILITY_IMPACTED_TESTS_DETECTION_ENABLED":            "false",
		"DD_TEST_FAILED_TEST_REPLAY_ENABLED":                          "false",
		"DD_TEST_MANAGEMENT_ENABLED":                                  "false",
		"DD_TEST_MANAGEMENT_ATTEMPT_TO_FIX_RETRIES":                   "1",
		"DD_INSTRUMENTATION_TELEMETRY_ENABLED":                        "false",
		"DD_TRACE_STARTUP_LOGS":                                       "false",
		"DD_TRACE_ENABLED":                                            "true",
		"DD_REMOTE_CONFIG_ENABLED":                                    "false",
		"DD_PROFILING_ENABLED":                                        "false",
		"DD_APPSEC_ENABLED":                                           "false",
		"DD_DYNAMIC_INSTRUMENTATION_ENABLED":                          "false",
		"DD_TRACE_AGENT_URL":                                          intakeURL,
		"DD_CIVISIBILITY_CODE_COVERAGE_ENABLED":                       "true",
	}
}

func (t *Testdrive) environment(path, intakeURL, sessionID string) map[string]string {
	env := testEnvironment(intakeURL, sessionID)
	if t.framework != nil && supportsValidationFramework(t.framework.Name()) {
		env = reportingEnvironment(intakeURL, sessionID)
	}
	maps.Copy(env, t.lang().runEnvironment(t, path))
	return env
}

func (t *Testdrive) installedTracerLabel(detected string) string {
	name := t.lang().tracerPackage()
	version := t.lang().installedTracerVersion(detected)
	if version != "" {
		return name + "@" + version
	}
	return name
}

// defaultToCITracer validates the tracer every instrumented CI job selects
// when --tracer-version was not given. A project-installed tracer still wins.
func (t *Testdrive) defaultToCITracer(check onboard.RuntimeCheck) {
	if !t.tracerVersionDefaulted || !t.lang().defaultsToCITracer() {
		return
	}
	pinned := ""
	for _, job := range check.Jobs {
		if job.Status == onboard.StatusExcluded {
			continue
		}
		version := strings.TrimPrefix(job.Tracer, "dd-trace@")
		if version == "" || (pinned != "" && version != pinned) {
			return
		}
		pinned = version
	}
	if pinned != "" {
		t.tracerVersion, t.tracerLabel = pinned, "dd-trace@"+pinned
	}
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

func (t *Testdrive) finishStartupFailure(output io.Writer, err error) error {
	result := validationResult{Framework: t.framework.Name(), CheckOnly: t.checkOnly, Error: err.Error(), Compatibility: verdict{Status: "inconclusive", Code: "recovery_failed", Reason: "Owned startup recovery did not finish."}}
	if t.checkOnly {
		result.Preflight = &jestPreflight{Verdict: verdict{Status: "inconclusive", Code: "recovery_failed", Reason: err.Error()}}
	}
	return errors.Join(err, finishValidation(output, t.repositoryRoot, result))
}

// An explicit CI value, including empty, overrides the caller's shell value.
func (t *Testdrive) nodeOptions() string {
	if value, exists := t.configurationEnvironment["NODE_OPTIONS"]; exists {
		return value
	}
	return os.Getenv("NODE_OPTIONS")
}

// Validation has a stable CLI exit code; tool-specific codes remain in JSON.
type validationFailure struct{ error }

func (e validationFailure) Unwrap() error { return e.error }
func (e validationFailure) ExitCode() int { return 1 }
func (t *Testdrive) ownershipRoot() string {
	if t.workflowRoot != "" {
		return t.workflowRoot
	}
	return t.repositoryRoot
}
