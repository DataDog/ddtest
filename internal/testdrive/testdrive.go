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
	repositoryRoot  string
	framework       framework.Framework
	language        string
	command         string
	args            []string
	tracerLabel     string
	platform        platform.Platform
	tracerVersion   string
	projectTracer   string
	session         *Session
	installCommand  string
	installArgs     []string
	executor        commandExecutor
	startIntake     func(string) (localIntake, error)
	nodeVersion     func() string
	environmentInfo environmentInfo
	preflight       *reportCard
	checkOnly       bool
	resolveJSTracer func(context.Context, string) (platform.JSSelection, error)
}

// Prepare detects the repository and probes the selected test environment and tracer.
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
	configurationOnly := len(checkOnly) > 0 && checkOnly[0]
	if configurationOnly {
		if runner.Name() != "jest" {
			return nil, fmt.Errorf("configuration preflight currently supports Jest only")
		}
		var selected string
		if override := strings.TrimSpace(settings.GetCommand()); override != "" {
			selected, err = onboard.ForwardJestCommand(repositoryRoot, override)
		} else {
			selected, err = onboard.JestValidationCommand(repositoryRoot)
		}
		if err != nil {
			return nil, err
		}
		if selected != "" {
			words, err := shellquote.Split(selected)
			if err != nil || len(words) == 0 {
				return nil, fmt.Errorf("invalid Jest command: %s", selected)
			}
			command, args = words[0], words[1:]
		}
	}
	label := map[string]string{"javascript": "dd-trace", "python": "ddtrace", "ruby": "datadog-ci"}[language] + "@" + version

	session := planSession(repositoryRoot)
	if runner.Name() == "vitest" {
		command, args, err = vitestTestdriveCommand(session.Directory())
		if err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var environment environmentInfo
	var preflight *reportCard
	switch language {
	case "python":
		var source string
		command, args, source = pythonTestdriveCommand(repositoryRoot, command, args, strings.TrimSpace(settings.GetCommand()) != "")
		environment, preflight = pythonEnvironmentInfo(ctx, command, args, source)
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
	if preflight == nil {
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

	drive := &Testdrive{projectTracer: projectTracer, session: session, installCommand: installCommand, installArgs: installArgs, repositoryRoot: repositoryRoot, framework: runner, language: language, command: command, args: args, platform: detectedPlatform, tracerVersion: version, tracerLabel: label,
		executor: &ext.DefaultCommandExecutor{}, startIntake: func(directory string) (localIntake, error) { return intake.Start(directory) }, nodeVersion: currentNodeVersion, environmentInfo: environment, preflight: preflight, checkOnly: configurationOnly}
	if installer, ok := detectedPlatform.(*platform.JavaScript); ok {
		drive.resolveJSTracer = installer.ResolveRegistryTracer
	}
	return drive, nil
}

func displayName(name string) string {
	names := map[string]string{"javascript": "JavaScript", "python": "Python", "ruby": "Ruby", "jest": "Jest", "mocha": "Mocha", "cypress": "Cypress", "playwright": "Playwright", "cucumber": "Cucumber", "vitest": "Vitest", "pytest": "pytest", "rspec": "RSpec", "minitest": "Minitest"}
	return names[name]
}

// Preview describes the filesystem and process changes that Run will make.
func (t *Testdrive) Preview(output io.Writer) {
	if t.checkOnly {
		_, _ = fmt.Fprintln(output, "Check configuration only: load Jest configuration with --showConfig, resolve tracer metadata and review static CI runtimes. No tracer installation or tests; write .testoptimization/testdrive.json. Jest configuration may execute project JavaScript. CI execution and Datadog backend processing remain untested.")
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
	_, _ = fmt.Fprintf(output, "  - create an output folder: %s\n", directory)
	if t.preflight != nil {
		_, _ = fmt.Fprintf(output, "  - save findings and troubleshooting; tests will not run: %s\n", t.preflight.Title)
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

	_, _ = fmt.Fprintf(output, "  - command: %s\n", shellquote.Join(append([]string{command}, args...)...))
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
func (t *Testdrive) Run(ctx context.Context, output io.Writer) (runErr error) {
	if t.checkOnly {
		return t.runConfigurationChecks(ctx, output)
	}
	session := t.session
	if session == nil {
		session = planSession(t.repositoryRoot)
	}
	if err := session.create(); err != nil {
		return err
	}
	if t.preflight != nil {
		return t.finishRun(output, session, intake.Facts{}, []byte(t.preflight.Context), errors.New(t.preflight.Title), nil, "Not installed")
	}
	installation := platform.TracerInstallation{Project: t.projectTracer != ""}
	if t.language == "javascript" {
		installation.Path = t.projectTracer
	}
	if t.projectTracer == "" {
		_, _ = fmt.Fprintf(output, "\nInstalling %s...\n", t.tracerLabel)
		directory := session.Directory()
		var err error
		if t.language == "python" {
			directory, err = os.MkdirTemp("", "ddtest-python-*")
			if err != nil {
				return fmt.Errorf("create temporary Python environment: %w", err)
			}
			defer func() {
				if err := os.RemoveAll(directory); err != nil {
					runErr = errors.Join(runErr, fmt.Errorf("remove temporary Python environment: %w", err))
				}
			}()
		}
		options := platform.TracerOptions{Directory: directory, Version: t.tracerVersion, Command: t.command, Args: t.args}
		if t.language == "python" && t.environmentInfo.Executable != "" {
			options.Command, options.Args = t.environmentInfo.Executable, nil
		}
		installation, err = t.platform.InstallTestdriveTracer(ctx, options)
		if err != nil {
			return err
		}
	}

	tracerLabel := t.tracerLabel
	if t.language == "ruby" {
		// Bundler owns the project dependency selection.
		tracerLabel = "datadog-ci · installed in project"
	}
	if installation.Project {
		tracerLabel = t.installedTracerLabel(t.projectTracer) + " · reused"
	}
	server, err := t.startIntake(session.Directory())
	if err != nil {
		return err
	}
	intakeClosed := false
	defer func() {
		if !intakeClosed {
			runErr = errors.Join(runErr, server.Close())
		}
	}()

	command, args := t.executionCommand()
	if t.framework.Name() == "vitest" {
		if err := writeVitestEntrypoint(session.Directory()); err != nil {
			return err
		}
	}
	_, _ = fmt.Fprintf(output, "Running %s...\n", shellquote.Join(append([]string{command}, args...)...))
	env := t.environment(installation.Path, server.URL(), session.ID())
	maps.Copy(env, installation.Env)

	testOutput, testErr := t.executor.CombinedOutput(ctx, command, args, env)

	// Drain the intake before taking the snapshot used by the report.
	closeErr := server.Close()
	intakeClosed = true
	var findings intake.Facts
	if closeErr == nil {
		findings, closeErr = server.Facts()
	}
	return t.finishRun(output, session, findings, testOutput, testErr, closeErr, tracerLabel)
}

func (t *Testdrive) finishRun(output io.Writer, session *Session, findings intake.Facts, testOutput []byte, testErr, analysisErr error, tracerLabel string) error {
	command, args := t.executionCommand()
	testOutputPath := filepath.Join(session.Directory(), testOutputFilename)
	if err := os.WriteFile(testOutputPath, testOutput, 0644); err != nil {
		return fmt.Errorf("save test output: %w", err)
	}
	var diagnosticsPath string
	defer func() {
		trafficPath := ""
		if t.preflight == nil {
			trafficPath = filepath.Join(session.Directory(), "intake")
		}
		writeDiagnosticPaths(output, t.repositoryRoot, diagnosticsPath, testOutputPath, trafficPath)
	}()
	if testErr != nil {
		if t.preflight != nil {
			_, _ = fmt.Fprintln(output, "\nEnvironment check:")
		} else {
			_, _ = fmt.Fprintf(output, "\n%s command output:\n", displayName(t.framework.Name()))
		}
		captured := strings.TrimSpace(string(testOutput))
		lines := strings.Split(captured, "\n")
		switch {
		case captured == "":
			_, _ = fmt.Fprintln(output, "The command produced no output.")
		case len(lines) > 80:
			_, _ = fmt.Fprintln(output, strings.Join(lines[:40], "\n"))
			_, _ = fmt.Fprintf(output, "\n... %d %s omitted; see the full test output below ...\n\n", len(lines)-80, plural(len(lines)-80, "line", "lines"))
			_, _ = fmt.Fprintln(output, strings.Join(lines[len(lines)-40:], "\n"))
		default:
			_, _ = fmt.Fprintln(output, captured)
		}
	}

	if analysisErr != nil {
		return analysisErr
	}
	runtime := reportRuntime{
		Framework: displayName(t.framework.Name()), Tracer: tracerLabel, Environment: t.environmentInfo, SetupFailure: t.preflight,
		Command: shellquote.Join(append([]string{command}, args...)...), Output: string(testOutput),
	}
	if testErr != nil {
		runtime.Error = testErr.Error()
	}
	commandFailed := testErr != nil && t.preflight == nil
	model := buildReport(t.repositoryRoot, findings, commandFailed, runtime)
	var err error
	diagnosticsPath, err = writeDiagnostics(t.repositoryRoot, session.Directory(), findings, model, commandFailed)
	if err != nil {
		return err
	}
	model.Artifacts = append(model.Artifacts, reportArtifact{Title: "Findings JSON", Href: diagnosticsFilename})
	reportPath, err := writeReportModel(session.Directory(), model)
	if err != nil {
		return err
	}
	reportURL, err := fileURL(reportPath)
	if err != nil {
		return fmt.Errorf("create report link: %w", err)
	}

	_, _ = fmt.Fprintln(output)
	if findings.TestEventCount > 0 {
		_, _ = fmt.Fprintln(output, "Test events received.")
	} else {
		_, _ = fmt.Fprintln(output, "No test events received.")
	}
	writeFindings(output, findings, model.Cards...)
	t.environmentInfo.write(output)
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
	if t.preflight != nil {
		status = "Not run (environment check failed)"
	}
	_, _ = fmt.Fprintf(output, "  %s: %s\n", displayName(t.framework.Name()), status)
	_, _ = fmt.Fprintf(output, "  Datadog library: %s\n", tracerLabel)
	_, _ = fmt.Fprintf(output, "  Command: %s\n", runtime.Command)
	reportLabel, err := filepath.Rel(t.repositoryRoot, reportPath)
	if err != nil {
		reportLabel = reportPath
	}
	_, _ = fmt.Fprintf(output, "\nOpen report: %s\n", terminalLink(reportURL, reportLabel))

	if t.preflight != nil {
		return testErr
	}
	if testErr != nil {
		return fmt.Errorf("%s failed after sending %d test event(s): %w", t.framework.Name(), findings.TestEventCount, testErr)
	}
	if findings.TestEventCount == 0 {
		return fmt.Errorf("%s command exited successfully, but Test Optimization sent no test events", t.framework.Name())
	}
	return nil
}

func writeFindings(output io.Writer, findings intake.Facts, cards ...reportCard) {
	if len(findings.ConfigurationErrors) > 0 {
		_, _ = fmt.Fprintf(output, "Tracer configuration errors: %s. Inspect the captured traffic and test output.\n", strings.Join(findings.ConfigurationErrors, ", "))
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
		_, _ = fmt.Fprintf(output, "Tracer error: received %d coverage entries with an empty files list. Affected payloads were excluded from coverage counts. Inspect the captured traffic.\n", findings.EmptyCoverageEntryCount)
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
