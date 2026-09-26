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
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/kballard/go-shellquote"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
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
	repositoryRoot string
	framework      framework.Framework
	language       string
	command        string
	args           []string
	tracerLabel    string
	platform       platform.Platform
	tracerVersion  string
	checkOnly      bool
	preflight      func(context.Context, io.Writer, *validationResult) error
	executor       commandExecutor
	startIntake    func(string, intake.Scenario) (localIntake, error)
	nodeVersion    func() string
}

// Prepare detects the repository without running commands or writing files.
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
	command, args, err := framework.TestdriveCommand(repositoryRoot, runner)
	if err != nil {
		return nil, err
	}
	label := map[string]string{"javascript": "dd-trace", "python": "ddtrace", "ruby": "datadog-ci"}[language] + "@" + version
	drive := &Testdrive{repositoryRoot: repositoryRoot, framework: runner, language: language, command: command, args: args, platform: detectedPlatform, tracerLabel: label, tracerVersion: version,
		executor: &ext.DefaultCommandExecutor{}, startIntake: func(directory string, scenario intake.Scenario) (localIntake, error) {
			return intake.StartScenario(directory, scenario)
		},
		nodeVersion: currentNodeVersion}
	drive.checkOnly = len(checkOnly) > 0 && checkOnly[0]
	drive.preflight = drive.checkJestPreflight
	return drive, nil
}

func displayName(name string) string {
	names := map[string]string{"javascript": "JavaScript", "python": "Python", "ruby": "Ruby", "jest": "Jest", "mocha": "Mocha", "cypress": "Cypress", "playwright": "Playwright", "cucumber": "Cucumber", "vitest": "Vitest", "pytest": "pytest", "rspec": "RSpec", "minitest": "Minitest"}
	return names[name]
}

// Preview describes the filesystem and process changes that Run will make.
func (t *Testdrive) Preview(output io.Writer) {
	if t.checkOnly {
		_, _ = fmt.Fprintln(output, "Check configuration only: resolve the tracer, inspect Jest --showConfig, and review static CI runtimes. No tracer installation or tests; update .testoptimization/testdrive.json, retaining the latest paired Jest execution as historical evidence.")
		return
	}
	command, args := t.command, t.args
	reportPath := validationPath(t.repositoryRoot)

	_, _ = fmt.Fprintf(output, "DDTest found %s and %s.\n", displayName(t.language), displayName(t.framework.Name()))
	_, _ = fmt.Fprintln(output)
	_, _ = fmt.Fprintln(output, "It will:")
	_, _ = fmt.Fprintln(output, "  - create a private temporary <session> outside the repository")
	switch t.language {
	case "javascript":
		_, _ = fmt.Fprintln(output, "  - reuse the project tracer; otherwise install the resolved fallback dd-trace@"+t.tracerVersion+" in <session>")
		_, _ = fmt.Fprintln(output, "  - inspect the selected Jest configuration and tracer compatibility before running tests")
	case "python":
		_, _ = fmt.Fprintf(output, "  - reuse the project tracer; if absent, install %s with the test command’s Python interpreter inside <session>/python-packages\n", t.tracerLabel)

	case "ruby":
		_, _ = fmt.Fprintf(output, "  - reuse the project tracer; if unavailable, run bundle add datadog-ci for %s in the project bundle\n", t.tracerLabel)

	}
	if t.framework.Name() == "cypress" {
		_, _ = fmt.Fprintln(output, "  - create Cypress config/support wrappers inside <session>; run with --config-file <session>/cypress.config.cjs and preserve existing hooks")
	}

	_, _ = fmt.Fprintf(output, "  - run: %s\n", shellquote.Join(append([]string{command}, args...)...))
	if t.framework.Name() == "jest" {
		_, _ = fmt.Fprintln(output, "  - check GitHub Actions Node runtimes against the workflow-selected tracer using public action and npm metadata")
		_, _ = fmt.Fprintln(output, "  - compare Jest JSON results without instrumentation and with reporting-only instrumentation")
		_, _ = fmt.Fprintln(output, "  - repeat the pair if outcomes differ; timing and console order are ignored")
		_, _ = fmt.Fprintln(output, "  - discover and remove a temporary probe test in each selectable Jest project; check retries, EFD, skipping, quarantine, disabled tests, and attempt-to-fix per project; wait for completion before running other repository checks")
	} else {
		_, _ = fmt.Fprintln(output, "  - collect reporting-only telemetry; compatibility and features remain unvalidated for this framework")
	}
	_, _ = fmt.Fprintf(output, "  - update the single report at %s; retain the latest paired Jest execution as historical evidence until another pair runs\n", reportPath)
	_, _ = fmt.Fprintln(output, "  - disable coverage thresholds only for isolated probes, preserving coverage collection and the original full-suite thresholds")
	_, _ = fmt.Fprintln(output, "  - keep one JSON report with verdicts, commands, exit codes, counts, and bounded failure diagnostics; remove temporary probes, tracer, traffic, and run files")
	_, _ = fmt.Fprintln(output)
	if t.language == "ruby" {
		_, _ = fmt.Fprintln(output, "If tracer installation is needed, Bundler updates the project Gemfile and lockfile.")
	} else {
		_, _ = fmt.Fprintln(output, "It will not change package.json, Gemfile, Python dependency files, or a lockfile in your project.")
	}
}

// Run prepares the tracer and executes the detected test suite.
func (t *Testdrive) Run(ctx context.Context, output io.Writer) (runErr error) {
	session, err := NewSession()
	if err != nil {
		return err
	}

	result := validationResult{CheckOnly: t.checkOnly, Session: session.ID(), Framework: t.framework.Name(), Tracer: t.tracerLabel,
		Compatibility: verdict{Status: "inconclusive", Reason: "Validation did not complete."}}
	defer func() {
		runErr = errors.Join(runErr, session.Close())
		if runErr != nil {
			result.Error = runErr.Error()
		}
		runErr = errors.Join(runErr, finishValidation(output, t.repositoryRoot, result))
	}()

	if t.framework.Name() == "jest" {
		check := onboard.CheckCIRuntimes(ctx, t.repositoryRoot)
		result.CIRuntime = &check
		if t.preflight != nil {
			if err := t.preflight(ctx, output, &result); err != nil {
				return err
			}
		}
		if t.checkOnly {
			result.Compatibility = verdict{Status: "not exercised", Reason: "Configuration checks only; run testdrive without --check-only for paired execution."}
			result.Features = []featureResult{{Name: "all", Status: "not exercised", Reason: "Configuration checks only."}}
			return nil
		}
	} else if t.checkOnly {
		return fmt.Errorf("configuration preflight currently supports Jest only")
	}

	_, _ = fmt.Fprintf(output, "\nPreparing %s in %s...\n", t.tracerLabel, session.Directory())
	version := t.tracerVersion
	if result.Selection != nil && result.Selection.Version != "" {
		version = result.Selection.Version
	}
	installation, err := t.platform.InstallTestdriveTracer(ctx, platform.TracerOptions{Directory: session.Directory(), Version: version, Command: t.command, Args: t.args})
	if err != nil {
		return err
	}

	result.TracerSource = "temporary installation"
	if t.language == "ruby" {
		result.TracerSource = "project bundle installation"
	}
	if installation.Project {
		result.TracerSource = "project installation (reused; fallback selector ignored)"
	}
	if t.framework.Name() == "jest" {
		if result.Selection != nil {
			if err := verifyInstalledSelection(installation.Path, &result); err != nil {
				return err
			}
			result.Preflight.Verdict = checkJestSupport(*result.Preflight, *result.Selection)
			if result.Preflight.Verdict.Status != "compatible" {
				return fmt.Errorf("jest preflight: %s", result.Preflight.Verdict.Reason)
			}
		}
		return t.runJestValidation(ctx, output, session, installation.Path, &result)
	}
	server, err := t.startIntake(session.Directory(), intake.Scenario{})
	if err != nil {
		return err
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
			return err
		}
	}
	testOutput, testErr := t.executor.CombinedOutput(ctx, command, args, env)
	result.Runs = []runSummary{(validationRun{Name: "reporting-only", Command: shellquote.Join(append([]string{command}, args...)...),
		Instrumented: true, ExitCode: commandExitCode(testErr)}).summary()}

	testOutputPath := filepath.Join(session.Directory(), testOutputFilename)
	if err := os.WriteFile(testOutputPath, testOutput, 0644); err != nil {
		return fmt.Errorf("save test output: %w", err)
	}

	// Drain the intake before taking the snapshot used by the report.
	closeErr := server.Close()
	intakeClosed = true
	if closeErr != nil {
		return closeErr
	}
	findings, err := server.Facts()
	if err != nil {
		return err
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
	_, _ = fmt.Fprintf(output, "  %s: %s\n", displayName(t.framework.Name()), passedFailed(testErr == nil))
	_, _ = fmt.Fprintf(output, "  Tracer: %s · %s\n", result.Tracer, result.TracerSource)
	result.Compatibility = verdict{Status: "inconclusive", Reason: "This framework has no compatibility adapter yet; telemetry alone does not validate behavior."}
	result.Features = []featureResult{{Name: "all", Status: "unvalidated", Reason: "Feature scenarios currently support Jest only."}}
	result.Runs[0].TestEventCount = findings.TestEventCount
	return nil
}

func writeFindings(output io.Writer, findings intake.Facts) {
	if len(findings.ConfigurationErrors) > 0 {
		_, _ = fmt.Fprintf(output, "Tracer configuration errors: %s.\n", strings.Join(findings.ConfigurationErrors, ", "))
	}
	count := 0
	if findings.EmptyCoverageEntryCount > 0 {
		count += findings.EmptyCoverageEntryCount
		_, _ = fmt.Fprintf(output, "Tracer error: received %d coverage entries with an empty files list. Affected payloads were excluded from coverage counts.\n", findings.EmptyCoverageEntryCount)
	}
	for _, size := range []int{
		len(findings.FailedTests), len(findings.FlakyTests), len(findings.SlowTests), len(findings.BroadCoverage),
	} {
		count += size
	}
	if count == 0 {
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

func testEnvironment(ciInitPath, intakeURL, sessionID string) map[string]string {
	nodeOptions := "-r " + strconv.Quote(ciInitPath)
	if current := stripDatadogNodeOptions(os.Getenv("NODE_OPTIONS")); current != "" {
		nodeOptions += " " + current
	}

	return map[string]string{
		"NODE_OPTIONS":                                                nodeOptions,
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

func stripDatadogNodeOptions(value string) string {
	fields, err := shellquote.Split(value)
	if err != nil {
		return value
	}
	kept := make([]string, 0, len(fields))
	for index := 0; index < len(fields); index++ {
		field := fields[index]
		if field == "-r" || field == "--require" || field == "--import" {
			if index+1 < len(fields) && isDatadogNodePreload(fields[index+1]) {
				index++
				continue
			}
		}
		if strings.HasPrefix(field, "--require=") && isDatadogNodePreload(strings.TrimPrefix(field, "--require=")) {
			continue
		}
		if strings.HasPrefix(field, "--import=") && isDatadogNodePreload(strings.TrimPrefix(field, "--import=")) {
			continue
		}
		if strings.HasPrefix(field, "-r") && isDatadogNodePreload(strings.TrimPrefix(field, "-r")) {
			continue
		}
		if strings.ContainsAny(field, " \t\r\n\"") {
			field = strconv.Quote(field)
		}
		kept = append(kept, field)
	}
	return strings.Join(kept, " ")
}

func isDatadogNodePreload(value string) bool {
	value = strings.Trim(value, `"'`)
	return value == "dd-trace/ci/init" || value == "dd-trace/register.js" || strings.HasSuffix(filepath.ToSlash(value), "/dd-trace/ci/init.js") ||
		strings.HasSuffix(filepath.ToSlash(value), "/dd-trace/register.js")
}

func (t *Testdrive) environment(path, intakeURL, sessionID string) map[string]string {
	env := testEnvironment(path, intakeURL, sessionID)
	switch t.language {
	case "javascript":
		// ESM instrumentation is needed by Vitest and by ESM test/config files.
		version := ""
		if t.nodeVersion != nil {
			version = t.nodeVersion()
		}
		if supportsNodeImport(version) {
			register := absoluteFileURL(filepath.Join(filepath.Dir(filepath.Dir(path)), "register.js"))
			env["NODE_OPTIONS"] += " --import " + strconv.Quote(register)
		}
	case "python":
		delete(env, "NODE_OPTIONS")
		if path != "" {
			env["PYTHONPATH"] = path
			if existing := os.Getenv("PYTHONPATH"); existing != "" {
				env["PYTHONPATH"] += string(os.PathListSeparator) + existing
			}
		}
		env["PYTEST_ADDOPTS"] = strings.TrimSpace(os.Getenv("PYTEST_ADDOPTS") + " --ddtrace")
	case "ruby":
		delete(env, "NODE_OPTIONS")
		env["RUBYOPT"] = strings.TrimSpace(os.Getenv("RUBYOPT") + " -rbundler/setup -rdatadog/ci/auto_instrument")
	}
	return env
}

func currentNodeVersion() string {
	output, err := exec.Command("node", "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func supportsNodeImport(version string) bool {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return false
	}
	major, majorErr := strconv.Atoi(parts[0])
	minor, minorErr := strconv.Atoi(parts[1])
	if majorErr != nil || minorErr != nil {
		return false
	}
	return major > 18 || major == 18 && minor >= 18
}
