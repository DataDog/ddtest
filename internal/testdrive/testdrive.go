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
	"net/url"
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
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/DataDog/ddtest/internal/testdrive/tracer"
)

const testOutputFilename = "test-output.txt"

type commandExecutor interface {
	CombinedOutput(ctx context.Context, name string, args []string, envMap map[string]string) ([]byte, error)
}

type localIntake interface {
	URL() string
	Findings() (intake.Findings, error)
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
	tracer         tracer.Tracer
	executor       commandExecutor
	startIntake    func(string) (localIntake, error)
}

// Prepare detects the repository without running commands or writing files.
func Prepare(repositoryRoot string) (*Testdrive, error) {
	return PrepareWithFramework(repositoryRoot, "")
}

func PrepareWithFramework(repositoryRoot, hint string) (*Testdrive, error) {
	language, runner, err := platform.DetectTestProject(repositoryRoot, hint)
	if err != nil {
		return nil, err
	}
	command, args, err := framework.TestdriveCommand(repositoryRoot, runner)
	if err != nil {
		return nil, err
	}
	var installer tracer.Tracer
	var label string
	switch language {
	case "javascript":
		installer = tracer.NewJavaScript()
		label = "dd-trace@" + tracer.JavaScriptVersion
	case "python":
		interpreter := "python"
		if _, err := exec.LookPath(interpreter); err != nil {
			interpreter = "python3"
		}
		if command == "python" || command == "python3" || strings.HasSuffix(command, "/python") || strings.HasSuffix(command, "/python3") {
			interpreter = command
		}
		installer = tracer.NewPython(interpreter)
		label = "ddtrace==" + tracer.PythonVersion
	case "ruby":
		installer = tracer.NewRuby(repositoryRoot)
		label = "datadog-ci@" + tracer.RubyVersion
	}
	return &Testdrive{repositoryRoot: repositoryRoot, framework: runner, language: language, command: command, args: args, tracer: installer, tracerLabel: label,
		executor: &ext.DefaultCommandExecutor{}, startIntake: func(directory string) (localIntake, error) { return intake.Start(directory) }}, nil
}

func displayName(name string) string {
	names := map[string]string{"javascript": "JavaScript", "python": "Python", "ruby": "Ruby", "jest": "Jest", "mocha": "Mocha", "cypress": "Cypress", "playwright": "Playwright", "cucumber": "Cucumber", "vitest": "Vitest", "pytest": "pytest", "rspec": "RSpec", "minitest": "Minitest"}
	return names[name]
}

// Preview describes the filesystem and process changes that Run will make.
func (t *Testdrive) Preview(output io.Writer) {
	command, args := t.command, t.args
	sessionsDirectory := filepath.Join(t.repositoryRoot, constants.PlanDirectory, "testdrive")

	_, _ = fmt.Fprintf(output, "DDTest found %s and %s.\n", displayName(t.language), displayName(t.framework.Name()))
	_, _ = fmt.Fprintln(output)
	_, _ = fmt.Fprintln(output, "It will:")
	_, _ = fmt.Fprintf(output, "  - create a new <session> under %s\n", sessionsDirectory)
	switch t.language {
	case "javascript":
		_, _ = fmt.Fprintf(output, "  - run: npm install --prefix <session> --no-save --package-lock=false --no-audit --no-fund dd-trace@%s\n", tracer.JavaScriptVersion)
		_, _ = fmt.Fprintln(output, "  - run node once to resolve the installed dd-trace preload")
	case "python":
		_, _ = fmt.Fprintf(output, "  - run: %s -m pip install --disable-pip-version-check --target <session>/python ddtrace==%s\n", t.tracer.(*tracer.Python).Interpreter, tracer.PythonVersion)
	case "ruby":
		_, _ = fmt.Fprintf(output, "  - create an isolated Gemfile including this project's dependencies and datadog-ci %s; run bundle install into <session>/gems\n", tracer.RubyVersion)
	}
	if t.framework.Name() == "cypress" {
		_, _ = fmt.Fprintln(output, "  - create Cypress config/support wrappers inside <session>; run with --config-file <session>/cypress.config.cjs and preserve existing hooks")
	}

	_, _ = fmt.Fprintf(output, "  - run: %s\n", shellquote.Join(append([]string{command}, args...)...))
	_, _ = fmt.Fprintln(output, "  - save a clickable report as <session>/report.html")
	_, _ = fmt.Fprintln(output, "  - save decoded traffic as <session>/intake/*.json and test output as <session>/test-output.txt")
	_, _ = fmt.Fprintln(output)
	_, _ = fmt.Fprintln(output, "It will not change package.json, Gemfile, Python dependency files, or a lockfile in your project.")
}

// Run installs an isolated tracer and executes the detected test suite.
func (t *Testdrive) Run(ctx context.Context, output io.Writer) (runErr error) {
	session, err := NewSession(t.repositoryRoot)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(output, "\nPreparing %s in %s...\n", t.tracerLabel, session.Directory())
	ciInitPath, err := t.tracer.Install(ctx, session.Directory())
	if err != nil {
		return err
	}

	server, err := t.startIntake(session.Directory())
	if err != nil {
		return err
	}
	defer func() {
		runErr = errors.Join(runErr, server.Close())
	}()

	command, args := t.command, t.args
	_, _ = fmt.Fprintf(output, "Running %s...\n", shellquote.Join(append([]string{command}, args...)...))
	env := t.environment(ciInitPath, server.URL(), session.ID())
	if t.framework.Name() == "cypress" {
		if command == "npm" && !slices.Contains(args, "--") {
			args = append(slices.Clone(args), "--")
		}
		args, err = prepareCypress(t.repositoryRoot, session.Directory(), ciInitPath, args)
		if err != nil {
			return err
		}
	}
	testOutput, testErr := t.executor.CombinedOutput(ctx, command, args, env)

	testOutputPath := filepath.Join(session.Directory(), testOutputFilename)
	if err := os.WriteFile(testOutputPath, testOutput, 0644); err != nil {
		return fmt.Errorf("save test output: %w", err)
	}

	findings, err := server.Findings()
	if err != nil {
		return err
	}
	reportPath, err := writeReport(t.repositoryRoot, session.Directory(), findings, testErr != nil, reportRuntime{Framework: displayName(t.framework.Name()), Tracer: t.tracerLabel})
	if err != nil {
		return err
	}
	reportURL, err := fileURL(reportPath)
	if err != nil {
		return fmt.Errorf("create report link: %w", err)
	}

	_, _ = fmt.Fprintln(output)
	if findings.TestEventCount > 0 {
		_, _ = fmt.Fprintln(output, "Test Optimization is ready.")
	} else {
		_, _ = fmt.Fprintln(output, "No test events received.")
	}
	writeFindings(output, findings)
	_, _ = fmt.Fprintln(output, "\nRun details:")
	_, _ = fmt.Fprintf(output, "  Test events: %d\n", findings.TestEventCount)
	_, _ = fmt.Fprintf(output, "  Tests with coverage: %d / %d\n", findings.CoveredTestCount, findings.TestCount)
	if findings.CoveredTestCount == 0 {
		_, _ = fmt.Fprintln(output, "  Coverage was not reported by this run.")
	}
	_, _ = fmt.Fprintf(output, "  %s: %s\n", displayName(t.framework.Name()), passedFailed(testErr == nil))
	_, _ = fmt.Fprintf(output, "  Tracer: %s · isolated\n", t.tracerLabel)
	_, _ = fmt.Fprintf(output, "\nOpen report: %s\n", terminalLink(reportURL))

	if testErr != nil {
		return fmt.Errorf("%s failed after sending %d test event(s): %w", t.framework.Name(), findings.TestEventCount, testErr)
	}
	if findings.TestEventCount == 0 {
		return fmt.Errorf("%s passed, but Test Optimization sent no test events", t.framework.Name())
	}
	return nil
}

func writeFindings(output io.Writer, findings intake.Findings) {
	count := 0
	for _, size := range []int{
		len(findings.FailedTests), len(findings.FlakyTests), len(findings.SlowTests), len(findings.BroadCoverage),
	} {
		if size > 0 {
			count++
		}
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

func writeTestFindings(output io.Writer, title string, findings []intake.TestFinding) {
	if len(findings) == 0 {
		return
	}
	_, _ = fmt.Fprintf(output, "\n%s (%d):\n", title, len(findings))
	writeTestFindingRows(output, findings)
}

func writeTestFindingRows(output io.Writer, findings []intake.TestFinding) {
	for _, finding := range findings {
		status, _ := testDisplayStatus(finding)
		_, _ = fmt.Fprintf(
			output, "  - %s · %s · %s\n",
			testFindingLabel(finding), status, formatDuration(findingDuration(finding)),
		)
	}
}

func testFindingLabel(finding intake.TestFinding) string {
	if finding.Suite == "" {
		return finding.Name
	}
	return finding.Suite + " › " + finding.Name
}

func testEnvironment(ciInitPath, intakeURL, sessionID string) map[string]string {
	nodeOptions := "-r " + strconv.Quote(ciInitPath)
	if current := strings.TrimSpace(os.Getenv("NODE_OPTIONS")); current != "" {
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
	env := testEnvironment(path, intakeURL, sessionID)
	switch t.language {
	case "javascript":
		// ESM instrumentation is needed by Vitest and by ESM test/config files.
		register := url.URL{Scheme: "file", Path: filepath.ToSlash(filepath.Join(filepath.Dir(filepath.Dir(path)), "register.js"))}
		env["NODE_OPTIONS"] += " --import " + strconv.Quote(register.String())
		// dd-trace 6.15.0 impacted-test detection dereferences scenario.id on
		// Background/Rule nodes. Basic Cucumber reporting works with it off.
		if t.framework.Name() == "cucumber" {
			env["DD_CIVISIBILITY_IMPACTED_TESTS_DETECTION_ENABLED"] = "false"
		}
	case "python":
		delete(env, "NODE_OPTIONS")
		env["PYTHONPATH"] = path
		if existing := os.Getenv("PYTHONPATH"); existing != "" {
			env["PYTHONPATH"] += string(os.PathListSeparator) + existing
		}
		env["PYTEST_ADDOPTS"] = strings.TrimSpace(os.Getenv("PYTEST_ADDOPTS") + " --ddtrace")
	case "ruby":
		delete(env, "NODE_OPTIONS")
		maps.Copy(env, tracer.RubyEnvironment(path))
		env["RUBYOPT"] = strings.TrimSpace(os.Getenv("RUBYOPT") + " -rbundler/setup -rdatadog/ci/auto_instrument")
	}
	return env
}
