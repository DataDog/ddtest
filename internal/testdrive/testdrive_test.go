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
	url          string
	tests        int
	coveredTests int
	closed       bool
}

func (f *fakeIntake) URL() string                    { return f.url }
func (f *fakeIntake) TestEventCount() (int, error)   { return f.tests, nil }
func (f *fakeIntake) CoveredTestCount() (int, error) { return f.coveredTests, nil }
func (f *fakeIntake) Close() error                   { f.closed = true; return nil }

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
	server := &fakeIntake{url: "http://127.0.0.1:1234", tests: 2, coveredTests: 2}
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
		"PASS one.test.js",
		"received 2 test event(s)",
		"2 of 2 reported test(s) have coverage",
		filepath.Join(installer.sessionDirectory, "intake"),
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
		return &fakeIntake{url: "http://127.0.0.1:1234", tests: 1}, nil
	}

	var output bytes.Buffer
	err = testdrive.Run(t.Context(), &output)
	if err == nil || !strings.Contains(err.Error(), "jest failed after sending 1 test event") {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(output.String(), "Test Optimization is working") {
		t.Fatalf("Run() did not report working instrumentation:\n%s", output.String())
	}
}
