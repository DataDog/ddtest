// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/testdrive/intake"
)

type fakeTracer struct {
	preloadPath      string
	sessionDirectory string
}

func (f *fakeTracer) Install(_ context.Context, sessionDirectory string) (string, error) {
	f.sessionDirectory = sessionDirectory
	return f.preloadPath, nil
}

type fakeTestdriveExecutor struct {
	command string
	args    []string
	env     map[string]string
	output  []byte
	err     error
}

func (f *fakeTestdriveExecutor) CombinedOutput(_ context.Context, command string, args []string, env map[string]string) ([]byte, error) {
	f.command = command
	f.args = args
	f.env = env
	return f.output, f.err
}

type fakeIntake struct {
	url      string
	findings intake.Findings
	closed   bool
}

func (f *fakeIntake) URL() string                        { return f.url }
func (f *fakeIntake) Findings() (intake.Findings, error) { return f.findings, nil }
func (f *fakeIntake) Close() error                       { f.closed = true; return nil }

func writeJestManifest(t *testing.T, repositoryRoot string) {
	t.Helper()
	manifest := `{"scripts":{"test":"jest"},"devDependencies":{"jest":"latest"}}`
	if err := os.WriteFile(filepath.Join(repositoryRoot, "package.json"), []byte(manifest), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareAndPreviewJest(t *testing.T) {
	repositoryRoot := t.TempDir()
	writeJestManifest(t, repositoryRoot)

	testdrive, err := Prepare(repositoryRoot)
	if err != nil {
		t.Fatalf("Prepare() unexpected error: %v", err)
	}
	var output bytes.Buffer
	testdrive.Preview(&output)

	for _, expected := range []string{
		"found JavaScript and Jest",
		"dd-trace@",
		"npm test -- --runInBand",
		filepath.Join(repositoryRoot, ".testoptimization", "testdrive"),
		"will not change package.json",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("Preview() output does not contain %q:\n%s", expected, output.String())
		}
	}
}

func TestPrepareRejectsUnsupportedRepository(t *testing.T) {
	_, err := Prepare(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "package.json") {
		t.Fatalf("Prepare() error = %v, want package.json diagnostic", err)
	}
}

func TestRunReportsCapturedTestsAndCoverage(t *testing.T) {
	repositoryRoot := t.TempDir()
	writeJestManifest(t, repositoryRoot)
	testdrive, err := Prepare(repositoryRoot)
	if err != nil {
		t.Fatal(err)
	}

	installer := &fakeTracer{preloadPath: "/tmp/dd-trace/ci/init.js"}
	executor := &fakeTestdriveExecutor{output: []byte("PASS one.test.js\n")}
	server := &fakeIntake{
		url: "http://127.0.0.1:1234",
		findings: intake.Findings{
			TestCount:        2,
			TestEventCount:   2,
			CoveredTestCount: 2,
			SlowTests: []intake.TestFinding{
				{Name: "slow test", Suite: "one.test.js", Duration: 2 * time.Second},
			},
		},
	}
	testdrive.tracer = installer
	testdrive.executor = executor
	testdrive.startIntake = func(sessionDirectory string) (localIntake, error) {
		if sessionDirectory != installer.sessionDirectory {
			t.Fatalf("intake session = %q, tracer session = %q", sessionDirectory, installer.sessionDirectory)
		}
		return server, nil
	}

	var output bytes.Buffer
	if err := testdrive.Run(t.Context(), &output); err != nil {
		t.Fatalf("Run() unexpected error: %v", err)
	}
	if !server.closed {
		t.Fatal("Run() did not close the intake")
	}
	if executor.command != "npm" || strings.Join(executor.args, " ") != "test -- --runInBand" {
		t.Fatalf("executed %s %v, want npm test -- --runInBand", executor.command, executor.args)
	}
	if executor.env["DD_API_KEY"] != "ddtest-testdrive" {
		t.Fatalf("DD_API_KEY = %q", executor.env["DD_API_KEY"])
	}
	if executor.env["DD_CIVISIBILITY_AGENTLESS_URL"] != server.url {
		t.Fatalf("agentless URL = %q", executor.env["DD_CIVISIBILITY_AGENTLESS_URL"])
	}
	if !strings.HasPrefix(executor.env["NODE_OPTIONS"], "-r "+installer.preloadPath) {
		t.Fatalf("NODE_OPTIONS = %q", executor.env["NODE_OPTIONS"])
	}
	for _, expected := range []string{
		"Test Optimization working: yes",
		"Tests failed: no",
		"Tests passed on retry: no",
		"Tests slower than others: yes (1)",
		"Tests covering unusually many files: no",
		"\x1b]8;;file://",
		"report.html",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("Run() output does not contain %q:\n%s", expected, output.String())
		}
	}
	contents, err := os.ReadFile(filepath.Join(installer.sessionDirectory, testOutputFilename))
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "PASS one.test.js\n" {
		t.Fatalf("saved output = %q", contents)
	}
	if strings.Contains(output.String(), "PASS one.test.js") {
		t.Fatalf("Run() leaked detailed Jest output:\n%s", output.String())
	}
	report, err := os.ReadFile(filepath.Join(installer.sessionDirectory, reportFilename))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Test Optimization is working", "Any tests failed?", "slow test", "2s"} {
		if !strings.Contains(string(report), expected) {
			t.Errorf("report does not contain %q", expected)
		}
	}
	for _, environmentVariable := range []string{
		"DD_CIVISIBILITY_ITR_ENABLED",
		"DD_CIVISIBILITY_CODE_COVERAGE_REPORT_UPLOAD_ENABLED",
		"DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED",
		"DD_CIVISIBILITY_FLAKY_RETRY_ENABLED",
		"DD_CIVISIBILITY_IMPACTED_TESTS_DETECTION_ENABLED",
		"DD_TEST_FAILED_TEST_REPLAY_ENABLED",
		"DD_TEST_MANAGEMENT_ENABLED",
	} {
		if executor.env[environmentVariable] != "true" {
			t.Errorf("%s = %q, want true", environmentVariable, executor.env[environmentVariable])
		}
	}
}

func TestRunStillReportsEventsWhenJestFails(t *testing.T) {
	repositoryRoot := t.TempDir()
	writeJestManifest(t, repositoryRoot)
	testdrive, err := Prepare(repositoryRoot)
	if err != nil {
		t.Fatal(err)
	}

	testdrive.tracer = &fakeTracer{preloadPath: "/tmp/dd-trace/ci/init.js"}
	testdrive.executor = &fakeTestdriveExecutor{output: []byte("FAIL one.test.js\n"), err: errors.New("exit status 1")}
	testdrive.startIntake = func(string) (localIntake, error) {
		return &fakeIntake{
			url: "http://127.0.0.1:1234",
			findings: intake.Findings{
				TestCount:      1,
				TestEventCount: 1,
				FailedTests:    []intake.TestFinding{{Name: "fails", Suite: "one.test.js"}},
			},
		}, nil
	}

	var output bytes.Buffer
	err = testdrive.Run(t.Context(), &output)
	if err == nil || !strings.Contains(err.Error(), "jest failed after sending 1 test event") {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(output.String(), "Test Optimization working: yes") {
		t.Fatalf("Run() did not report working instrumentation:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "Tests failed: yes (1)") || !strings.Contains(output.String(), "file://") {
		t.Fatalf("Run() did not report the failure and report link:\n%s", output.String())
	}
}
