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
	"os"
	"path/filepath"
	"strings"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/DataDog/ddtest/internal/testdrive/tracer"
)

const testOutputFilename = "jest-output.txt"

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
	framework      *framework.Jest
	tracer         tracer.Tracer
	executor       commandExecutor
	startIntake    func(string) (localIntake, error)
}

// Prepare detects the first supported public testdrive path: JavaScript with Jest.
func Prepare(repositoryRoot string) (*Testdrive, error) {
	javascript := platform.NewJavaScript()
	detected, err := javascript.Detect(repositoryRoot)
	if err != nil {
		return nil, err
	}
	if !detected {
		return nil, fmt.Errorf("testdrive currently supports JavaScript projects with a package.json")
	}

	jest := framework.NewJest()
	detected, err = jest.Detect(repositoryRoot)
	if err != nil {
		return nil, err
	}
	if !detected {
		return nil, fmt.Errorf("testdrive could not find a Jest test script in package.json")
	}

	return &Testdrive{
		repositoryRoot: repositoryRoot,
		framework:      jest,
		tracer:         tracer.NewJavaScript(),
		executor:       &ext.DefaultCommandExecutor{},
		startIntake: func(sessionDirectory string) (localIntake, error) {
			return intake.Start(sessionDirectory)
		},
	}, nil
}

// Preview describes the filesystem and process changes that Run will make.
func (t *Testdrive) Preview(output io.Writer) {
	command, args := t.framework.TestCommand(nil)
	sessionsDirectory := filepath.Join(t.repositoryRoot, constants.PlanDirectory, "testdrive")

	_, _ = fmt.Fprintln(output, "DDTest found JavaScript and Jest.")
	_, _ = fmt.Fprintln(output)
	_, _ = fmt.Fprintln(output, "It will:")
	_, _ = fmt.Fprintf(output, "  - create a new <session> under %s\n", sessionsDirectory)
	_, _ = fmt.Fprintf(output, "  - run: npm install --prefix <session> --no-save --package-lock=false --no-audit --no-fund dd-trace@%s\n", tracer.JavaScriptVersion)
	_, _ = fmt.Fprintln(output, "  - run node once to resolve the installed dd-trace preload")
	_, _ = fmt.Fprintf(output, "  - run: %s\n", strings.Join(append([]string{command}, args...), " "))
	_, _ = fmt.Fprintln(output, "  - save a clickable report as <session>/report.html")
	_, _ = fmt.Fprintln(output, "  - save decoded traffic as <session>/intake/*.json and test output as <session>/jest-output.txt")
	_, _ = fmt.Fprintln(output)
	_, _ = fmt.Fprintln(output, "It will not change package.json or a lockfile in your project.")
}

// Run installs the tracer in an isolated session and executes the detected Jest suite.
func (t *Testdrive) Run(ctx context.Context, output io.Writer) (runErr error) {
	session, err := NewSession(t.repositoryRoot)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(output, "\nPreparing dd-trace@%s in %s...\n", tracer.JavaScriptVersion, session.Directory())
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

	command, args := t.framework.TestCommand(nil)
	_, _ = fmt.Fprintf(output, "Running %s...\n", strings.Join(append([]string{command}, args...), " "))
	testOutput, testErr := t.executor.CombinedOutput(ctx, command, args, testEnvironment(ciInitPath, server.URL(), session.ID()))

	testOutputPath := filepath.Join(session.Directory(), testOutputFilename)
	if err := os.WriteFile(testOutputPath, testOutput, 0644); err != nil {
		return fmt.Errorf("save Jest output: %w", err)
	}

	findings, err := server.Findings()
	if err != nil {
		return err
	}
	reportPath, err := writeReport(t.repositoryRoot, session.Directory(), findings, testErr != nil)
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
	_, _ = fmt.Fprintf(output, "  Jest: %s\n", passedFailed(testErr == nil))
	_, _ = fmt.Fprintf(output, "  Tracer: dd-trace@%s · isolated\n", tracer.JavaScriptVersion)
	_, _ = fmt.Fprintf(output, "\nOpen report: %s\n", terminalLink(reportURL))

	if testErr != nil {
		return fmt.Errorf("jest failed after sending %d test event(s): %w", findings.TestEventCount, testErr)
	}
	if findings.TestEventCount == 0 {
		return fmt.Errorf("jest passed, but Test Optimization sent no test events")
	}
	return nil
}

func writeFindings(output io.Writer, findings intake.Findings) {
	count := 0
	for _, size := range []int{
		len(findings.FailedTests), len(findings.PassedOnRetry), len(findings.SlowTests), len(findings.BroadCoverage),
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
	writeTestFindings(output, "Flaky tests", findings.PassedOnRetry)
	writeTestFindings(output, "Tests slower than the others", findings.SlowTests)
	if len(findings.BroadCoverage) > 0 {
		_, _ = fmt.Fprintf(output, "\nUnusually broad coverage (%d):\n", len(findings.BroadCoverage))
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
	for _, finding := range findings {
		status, _ := testDisplayStatus(finding)
		_, _ = fmt.Fprintf(
			output, "  - %s · %s · %s\n",
			testFindingLabel(finding), status, formatDuration(totalAttemptDuration(finding)),
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
	nodeOptions := "-r " + ciInitPath
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
