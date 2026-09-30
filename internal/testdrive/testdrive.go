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
	allConfigurations bool
	reportModels      *[]reportModel
	repositoryRoot    string
	framework         framework.Framework
	language          string
	command           string
	args              []string
	tracerLabel       string
	platform          platform.Platform
	tracerVersion     string
	projectTracer     string
	session           *Session
	installCommand    string
	installArgs       []string
	checkOnly         bool
	preflight         func(context.Context, io.Writer, *validationResult) error
	executor          commandExecutor
	startIntake       func(string, intake.Scenario) (localIntake, error)
	nodeVersion       func() string
	resolveJSTracer   func(context.Context, string) (platform.JSSelection, error)
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
	if all && runner.Name() != "jest" && runner.Name() != "vitest" {
		return nil, fmt.Errorf("--all currently validates Jest and Vitest only")
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
	label := map[string]string{"javascript": "dd-trace", "python": "ddtrace", "ruby": "datadog-ci"}[language] + "@" + version
	session := planSession()
	options := platform.TracerOptions{Directory: session.Directory(), Version: version, Command: command, Args: args}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	projectTracer, probeErr := detectedPlatform.DetectTracer(ctx, options)
	if probeErr != nil {
		projectTracer = ""
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
	drive.allConfigurations = all
	drive.checkOnly = len(checkOnly) > 0 && checkOnly[0]
	drive.preflight = drive.checkJestPreflight
	if runner.Name() == "vitest" {
		drive.preflight = drive.checkVitestPreflight
	}
	if js, ok := detectedPlatform.(*platform.JavaScript); ok {
		drive.resolveJSTracer = js.ResolveRegistryTracer
	}
	return drive, nil
}

func displayName(name string) string {
	names := map[string]string{"javascript": "JavaScript", "python": "Python", "ruby": "Ruby", "jest": "Jest", "mocha": "Mocha", "cypress": "Cypress", "playwright": "Playwright", "cucumber": "Cucumber", "vitest": "Vitest", "pytest": "pytest", "rspec": "RSpec", "minitest": "Minitest"}
	return names[name]
}

// Preview describes the filesystem and process changes that Run will make.
func (t *Testdrive) Preview(output io.Writer) {
	if t.allConfigurations {
		scope, err := onboard.DiscoverValidationScope(t.repositoryRoot, t.framework.Name())
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
	command, args := t.command, t.args
	reportPath := validationPath("")

	_, _ = fmt.Fprintf(output, "DDTest found %s and %s.\n", displayName(t.language), displayName(t.framework.Name()))
	_, _ = fmt.Fprintln(output)
	_, _ = fmt.Fprintln(output, "It will:")
	directory := t.session.Directory()
	if relative, err := filepath.Rel(t.repositoryRoot, directory); err == nil {
		directory = relative
	}
	_, _ = fmt.Fprintf(output, "  - create a private temporary directory outside the repository: %s\n", directory)
	if t.projectTracer != "" {
		_, _ = fmt.Fprintf(output, "  - reuse installed %s; no installation is needed\n", t.installedTracerLabel(t.projectTracer))
	} else {
		install := append([]string{t.installCommand}, t.installArgs...)
		for i, arg := range install {
			install[i] = strings.ReplaceAll(arg, t.session.Directory(), directory)
		}
		_, _ = fmt.Fprintf(output, "  - install %s: %s\n", t.tracerLabel, shellquote.Join(install...))
	}
	if t.framework.Name() == "jest" || t.framework.Name() == "vitest" {
		_, _ = fmt.Fprintln(output, "  - inspect the selected framework configuration and tracer compatibility before running tests")
	}
	if t.framework.Name() == "cypress" {
		_, _ = fmt.Fprintf(output, "  - create Cypress config/support wrappers in %s and preserve existing hooks\n", directory)
	}

	_, _ = fmt.Fprintf(output, "  - run: %s\n", shellquote.Join(append([]string{command}, args...)...))
	if t.framework.Name() == "jest" || t.framework.Name() == "vitest" {
		_, _ = fmt.Fprintln(output, "  - check GitHub Actions Node runtimes against the workflow-selected tracer using public action and npm metadata")
		_, _ = fmt.Fprintln(output, "  - compare native JSON results without instrumentation and with reporting-only instrumentation")
		_, _ = fmt.Fprintln(output, "  - repeat the pair if outcomes differ; timing and console order are ignored")
		_, _ = fmt.Fprintln(output, "  - discover and remove a temporary probe test under the selected project configuration; check retries, EFD, skipping, quarantine, disabled tests, and attempt-to-fix per project; wait for completion before running other repository checks")
	} else {
		_, _ = fmt.Fprintln(output, "  - collect reporting-only telemetry; compatibility and features remain unvalidated for this framework")
	}
	_, _ = fmt.Fprintf(output, "  - update the validation report at %s; retain the latest paired execution as historical evidence until another pair runs\n", reportPath)
	_, _ = fmt.Fprintf(output, "  - save the instrumented suite findings in %s\n", htmlReportPath(""))
	_, _ = fmt.Fprintln(output, "  - disable coverage thresholds only for isolated probes, preserving coverage collection and the original full-suite thresholds")
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
	if t.allConfigurations {
		return t.runAllConfigurations(ctx, output)
	}
	result, runErr := t.run(ctx, output)
	if t.framework.Name() == "jest" || t.framework.Name() == "vitest" {
		scope, err := onboard.DiscoverValidationScope(t.repositoryRoot, t.framework.Name())
		if err != nil {
			runErr = errors.Join(runErr, err)
			result.Error = runErr.Error()
		} else {
			setSingleConfigurationScope(t.repositoryRoot, &result, scope)
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

	result = validationResult{CheckOnly: t.checkOnly, Session: session.ID(), Framework: t.framework.Name(), Tracer: t.tracerLabel,
		Compatibility: verdict{Status: "inconclusive", Reason: "Validation did not complete."}}
	defer func() {
		cleanupErr := session.Close()
		result.Cleanup = &verdict{Status: "passed", Reason: "ddtest temporary session storage removed."}
		if cleanupErr != nil {
			result.Cleanup = &verdict{Status: "failed", Reason: reportText(cleanupErr.Error())}
		}
		runErr = errors.Join(runErr, cleanupErr)
		if runErr != nil {
			result.Error = runErr.Error()
		}
	}()

	if t.framework.Name() == "jest" || t.framework.Name() == "vitest" {
		check := onboard.CheckCIRuntimes(ctx, t.repositoryRoot, t.framework.Name())
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
		return result, fmt.Errorf("configuration preflight currently supports Jest and Vitest only")
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
		installation, err = t.platform.InstallTestdriveTracer(ctx, platform.TracerOptions{Directory: session.Directory(), Version: version, Command: t.command, Args: t.args})
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
	if t.framework.Name() == "vitest" {
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

	command, args := t.command, t.args
	_, _ = fmt.Fprintf(output, "Running %s...\n", shellquote.Join(append([]string{command}, args...)...))
	env := t.environment(installation.Path, server.URL(), session.ID())
	maps.Copy(env, installation.Env)
	if t.framework.Name() == "cypress" {
		if command == "npm" && !slices.Contains(args, "--") {
			args = append(slices.Clone(args), "--")
		}
		args, err = prepareCypress(t.repositoryRoot, session.Directory(), installation.Path, command, args)
		if err != nil {
			return result, err
		}
	}
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

	_, _ = fmt.Fprintln(output)
	if findings.TestEventCount > 0 {
		_, _ = fmt.Fprintln(output, "Test events received.")
	} else {
		_, _ = fmt.Fprintln(output, "No test events received.")
	}
	writeFindings(output, findings)
	_, _ = fmt.Fprintln(output, "\nRun details:")
	_, _ = fmt.Fprintf(output, "  Test events: %d\n", findings.TestEventCount)
	_, _ = fmt.Fprintf(output, "  Tests with coverage: %d / %d\n", findings.CoveredTestCount, findings.TestCount)
	if findings.CoveredTestCount == 0 {
		if findings.EmptyCoverageEntryCount > 0 {
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
	result.Compatibility = verdict{Status: "inconclusive", Reason: "This framework has no compatibility adapter yet; telemetry alone does not validate behavior."}
	result.Features = []featureResult{{Name: "all", Status: "unvalidated", Reason: "Controlled feature scenarios are not implemented for " + displayName(t.framework.Name()) + "."}}
	result.Runs[0].TestEventCount = findings.TestEventCount
	execution.Facts = findings
	runErr = t.writeHTMLReport(output, &result, execution)
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

func writeFindings(output io.Writer, findings intake.Facts) {
	if len(findings.ConfigurationErrors) > 0 {
		_, _ = fmt.Fprintf(output, "Tracer configuration errors: %s.\n", strings.Join(findings.ConfigurationErrors, ", "))
	}
	count := len(findings.ConfigurationErrors)
	if findings.MissingCoverage {
		count++
		_, _ = fmt.Fprintln(output, "Coverage not reported: test events arrived, but no code coverage was reported. Test Impact Analysis cannot map these tests to changed files.")
	}
	if findings.EmptyCoverageEntryCount > 0 {
		count += findings.EmptyCoverageEntryCount
		_, _ = fmt.Fprintf(output, "Tracer error: received %d coverage entries with an empty files list. Affected payloads were excluded from coverage counts.\n", findings.EmptyCoverageEntryCount)
	}
	for _, size := range []int{
		len(findings.FailedTests), len(findings.FlakyTests), len(findings.SlowTests), len(findings.SlowSuites), len(findings.BroadCoverage),
	} {
		count += size
	}
	if count == 0 {
		if findings.TestEventCount == 0 {
			_, _ = fmt.Fprintln(output, "Test findings unavailable: no test events were received.")
			return
		}
		_, _ = fmt.Fprintln(output, "No findings.")
		return
	}

	_, _ = fmt.Fprintf(output, "%d %s.\n", count, plural(count, "finding", "findings"))
	writeTestFindings(output, "Failed tests", findings.FailedTests)
	writeTestFindings(output, "Flaky tests", findings.FlakyTests)
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
		}
	}
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
	}
}

func testFindingLabel(finding intake.Test) string {
	if finding.Suite == "" {
		return finding.Name
	}
	return finding.Suite + " › " + finding.Name
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
