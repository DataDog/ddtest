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
	repositoryRoot string
	framework      framework.Framework
	language       string
	command        string
	args           []string
	tracerLabel    string
	platform       platform.Platform
	tracerVersion  string
	projectTracer  string
	session        *Session
	installCommand string
	installArgs    []string
	executor       commandExecutor
	startIntake    func(string) (localIntake, error)
}

// Prepare detects the repository and probes the project tracer without writing files.
func Prepare(version string) (*Testdrive, error) {
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
	switch runner.Name() {
	case "jest":
	default:
		return nil, fmt.Errorf("testdrive does not yet support %s", runner.Name())
	}
	command, args, err := testCommand(runner)
	if err != nil {
		return nil, err
	}
	label := map[string]string{"javascript": "dd-trace", "python": "ddtrace", "ruby": "datadog-ci"}[language] + "@" + version

	session := planSession(repositoryRoot)
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

	return &Testdrive{projectTracer: projectTracer, session: session, installCommand: installCommand, installArgs: installArgs, repositoryRoot: repositoryRoot, framework: runner, language: language, command: command, args: args, platform: detectedPlatform, tracerVersion: version, tracerLabel: label,
		executor: &ext.DefaultCommandExecutor{}, startIntake: func(directory string) (localIntake, error) { return intake.Start(directory) }}, nil
}

func displayName(name string) string {
	names := map[string]string{"javascript": "JavaScript", "python": "Python", "ruby": "Ruby", "jest": "Jest", "mocha": "Mocha", "cypress": "Cypress", "playwright": "Playwright", "cucumber": "Cucumber", "vitest": "Vitest", "pytest": "pytest", "rspec": "RSpec", "minitest": "Minitest"}
	return names[name]
}

// Preview describes the filesystem and process changes that Run will make.
func (t *Testdrive) Preview(output io.Writer) {
	command, args := t.command, t.args
	directory := t.session.Directory()

	_, _ = fmt.Fprintf(output, "DDTest found %s and %s.\n", displayName(t.language), displayName(t.framework.Name()))
	_, _ = fmt.Fprintln(output)
	_, _ = fmt.Fprintln(output, "It will:")
	_, _ = fmt.Fprintf(output, "  - create an output folder: %s\n", directory)
	if t.projectTracer != "" {
		_, _ = fmt.Fprintln(output, "  - reuse the installed project tracer; no installation is needed")
	} else {
		_, _ = fmt.Fprintf(output, "  - install %s: %s\n", t.tracerLabel, shellquote.Join(append([]string{t.installCommand}, t.installArgs...)...))
	}

	_, _ = fmt.Fprintf(output, "  - run: %s\n", shellquote.Join(append([]string{command}, args...)...))
	_, _ = fmt.Fprintf(output, "  - save captured traffic in %s and test output in %s\n", filepath.Join(directory, "intake"), filepath.Join(directory, testOutputFilename))
	_, _ = fmt.Fprintln(output)
	_, _ = fmt.Fprintln(output, "It will not change package.json, Gemfile, Python dependency files, or a lockfile in your project.")
}

// Run prepares the tracer and executes the detected test suite.
func (t *Testdrive) Run(ctx context.Context, output io.Writer) (runErr error) {
	session := t.session
	if session == nil {
		session = planSession(t.repositoryRoot)
	}
	if err := session.create(); err != nil {
		return err
	}
	installation := platform.TracerInstallation{Project: t.projectTracer != ""}
	if t.language == "javascript" {
		installation.Path = t.projectTracer
	}
	if t.projectTracer == "" {
		_, _ = fmt.Fprintf(output, "\nInstalling %s...\n", t.tracerLabel)
		var err error
		installation, err = t.platform.InstallTestdriveTracer(ctx, platform.TracerOptions{Directory: session.Directory(), Version: t.tracerVersion, Command: t.command, Args: t.args})
		if err != nil {
			return err
		}
	}

	tracerLabel := t.tracerLabel + " · isolated"
	if installation.Project {
		tracerLabel = "project tracer · reused"
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

	command, args := t.command, t.args
	_, _ = fmt.Fprintf(output, "Running %s...\n", shellquote.Join(append([]string{command}, args...)...))
	env := t.environment(installation.Path, server.URL(), session.ID())
	maps.Copy(env, installation.Env)

	testOutput, testErr := t.executor.CombinedOutput(ctx, command, args, env)

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
	_, _ = fmt.Fprintf(output, "  Tracer: %s\n", tracerLabel)
	_, _ = fmt.Fprintf(output, "\nRun artifacts: %s\n", session.Directory())

	if testErr != nil {
		return fmt.Errorf("%s failed after sending %d test event(s): %w", t.framework.Name(), findings.TestEventCount, testErr)
	}
	if findings.TestEventCount == 0 {
		return fmt.Errorf("%s passed, but Test Optimization sent no test events", t.framework.Name())
	}
	return nil
}

func writeFindings(output io.Writer, findings intake.Facts) {
	if len(findings.ConfigurationErrors) > 0 {
		_, _ = fmt.Fprintf(output, "Tracer configuration errors: %s. Inspect the captured traffic and test output.\n", strings.Join(findings.ConfigurationErrors, ", "))
	}
	count := 0
	if findings.EmptyCoverageEntryCount > 0 {
		count += findings.EmptyCoverageEntryCount
		_, _ = fmt.Fprintf(output, "Tracer error: received %d coverage entries with an empty files list. Affected payloads were excluded from coverage counts. Inspect the captured traffic.\n", findings.EmptyCoverageEntryCount)
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
		status, _ := testDisplayStatus(finding)
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
	if t.language == "javascript" {
		maps.Copy(env, javascriptEnvironment(path))
	}
	return env
}

func testCommand(runner framework.Framework) (string, []string, error) {
	if command := strings.TrimSpace(settings.GetCommand()); command != "" {
		parts, err := shellquote.Split(command)
		if err != nil {
			return "", nil, fmt.Errorf("parse testdrive --command: %w", err)
		}
		if len(parts) == 0 {
			return "", nil, fmt.Errorf("testdrive --command is empty")
		}
		return parts[0], parts[1:], nil
	}
	command, args := runner.Command()
	return command, args, nil
}
