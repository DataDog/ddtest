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
	TestEventCount() (int, error)
	CoveredTestCount() (int, error)
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
	_, _ = fmt.Fprintf(output, "Running %s...\n\n", strings.Join(append([]string{command}, args...), " "))
	testOutput, testErr := t.executor.CombinedOutput(ctx, command, args, testEnvironment(ciInitPath, server.URL(), session.ID()))
	if len(testOutput) > 0 {
		_, _ = output.Write(testOutput)
		if testOutput[len(testOutput)-1] != '\n' {
			_, _ = fmt.Fprintln(output)
		}
	}

	testOutputPath := filepath.Join(session.Directory(), testOutputFilename)
	if err := os.WriteFile(testOutputPath, testOutput, 0644); err != nil {
		return fmt.Errorf("save Jest output: %w", err)
	}

	testCount, err := server.TestEventCount()
	if err != nil {
		return err
	}
	coveredTestCount, err := server.CoveredTestCount()
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintln(output)
	if testCount > 0 {
		_, _ = fmt.Fprintf(output, "Test Optimization is working: received %d test event(s).\n", testCount)
	} else {
		_, _ = fmt.Fprintln(output, "Test Optimization did not send any test events.")
	}
	if coveredTestCount > 0 {
		_, _ = fmt.Fprintf(output, "Coverage is working: %d of %d reported test(s) have coverage.\n", coveredTestCount, testCount)
	} else {
		_, _ = fmt.Fprintf(output, "Coverage was not reported for the %d observed test(s).\n", testCount)
	}
	_, _ = fmt.Fprintf(output, "Decoded traffic: %s\n", filepath.Join(session.Directory(), "intake"))
	_, _ = fmt.Fprintf(output, "Test output: %s\n", testOutputPath)

	if testErr != nil {
		return fmt.Errorf("jest failed after sending %d test event(s): %w", testCount, testErr)
	}
	if testCount == 0 {
		return fmt.Errorf("jest passed, but Test Optimization sent no test events")
	}
	return nil
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
		"DD_INSTRUMENTATION_TELEMETRY_ENABLED":                        "false",
		"DD_TRACE_STARTUP_LOGS":                                       "false",
	}
}
