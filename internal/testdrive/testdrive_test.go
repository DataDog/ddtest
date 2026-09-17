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
	err              error
}

func (f *fakeTracer) Install(_ context.Context, sessionDirectory string) (string, error) {
	f.sessionDirectory = sessionDirectory
	return f.preloadPath, f.err
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
	url         string
	findings    intake.Findings
	findingsErr error
	closeErr    error
	closed      bool
}

func (f *fakeIntake) URL() string                        { return f.url }
func (f *fakeIntake) Findings() (intake.Findings, error) { return f.findings, f.findingsErr }
func (f *fakeIntake) Close() error                       { f.closed = true; return f.closeErr }

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

func TestPrepareRejectsJavaScriptWithoutJest(t *testing.T) {
	repositoryRoot := t.TempDir()
	requireWriteFile(t, filepath.Join(repositoryRoot, "package.json"), `{"scripts":{"test":"mocha"}}`)

	_, err := Prepare(repositoryRoot)
	if err == nil || !strings.Contains(err.Error(), "could not find a Jest test script") {
		t.Fatalf("Prepare() error = %v, want Jest diagnostic", err)
	}
}

func TestPrepareReportsMalformedManifest(t *testing.T) {
	repositoryRoot := t.TempDir()
	requireWriteFile(t, filepath.Join(repositoryRoot, "package.json"), "{")

	_, err := Prepare(repositoryRoot)
	if err == nil || !strings.Contains(err.Error(), "package.json") {
		t.Fatalf("Prepare() error = %v, want package.json parse error", err)
	}
}

func TestRunReportsCapturedTestsAndCoverage(t *testing.T) {
	repositoryRoot := t.TempDir()
	writeJestManifest(t, repositoryRoot)
	if err := os.WriteFile(filepath.Join(repositoryRoot, "one.test.js"), []byte("test('slow test', () => expect(true).toBe(true));\n"), 0644); err != nil {
		t.Fatal(err)
	}
	testdrive, err := Prepare(repositoryRoot)
	if err != nil {
		t.Fatal(err)
	}

	installer := &fakeTracer{preloadPath: "/tmp/dd-trace/ci/init.js"}
	executor := &fakeTestdriveExecutor{output: []byte("PASS one.test.js\n")}
	server := &fakeIntake{
		url: "http://127.0.0.1:1234",
		findings: intake.Findings{
			TestCount:          2,
			TestEventCount:     2,
			CoveredTestCount:   2,
			TestDurationMedian: time.Second,
			Tests: []intake.TestFinding{
				{Name: "fast test", Suite: "one.test.js", SourceFile: "one.test.js", SourceStart: 1, Status: "pass", Duration: time.Millisecond, Attempts: []intake.TestAttempt{{Status: "pass", Duration: time.Millisecond}}},
				{Name: "slow test", Suite: "one.test.js", SourceFile: "one.test.js", SourceStart: 1, Status: "pass", Duration: 2 * time.Second, Attempts: []intake.TestAttempt{{Status: "pass", Duration: 2 * time.Second}}},
			},
			SlowTests: []intake.TestFinding{
				{
					Name: "slow test", Suite: "one.test.js", SourceFile: "one.test.js", SourceStart: 1, Duration: 2 * time.Second,
					Attempts: []intake.TestAttempt{
						{Status: "pass", Duration: 1500 * time.Millisecond},
						{Status: "pass", Duration: 2 * time.Second, Retry: true, RetryReason: "early_flake_detection"},
					},
				},
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
		"Test Optimization is ready.",
		"1 finding.",
		"Tests slower than the others (1):",
		"Median test time: 1s",
		"one.test.js › slow test · Pass · 2s",
		"Run details:",
		"Test events: 2",
		"Tests with coverage: 2 / 2",
		"Jest: Passed",
		"Tracer: dd-trace@6.15.0 · isolated",
		"\x1b]8;;file://",
		"report.html",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("Run() output does not contain %q:\n%s", expected, output.String())
		}
	}
	for _, absent := range []string{"Failed tests", "Flaky tests", "Unusually broad coverage"} {
		if strings.Contains(output.String(), absent) {
			t.Errorf("Run() output contains absent finding %q:\n%s", absent, output.String())
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
	for _, expected := range []string{
		"Test Optimization is ready",
		"Any tests slower than the others?",
		"Median test time · 1s",
		"slow test",
		"Run 2 · Retry · early flake detection",
		"Source · lines 1–1",
		"Run details",
		"Tests with coverage",
		"2 / 2",
		"dd-trace@",
		`href="intake/"`,
		`href="jest-output.txt"`,
		`data-tab="suites"`,
		`data-tab="tests"`,
		`<article class="problem-card">`,
	} {
		if !strings.Contains(string(report), expected) {
			t.Errorf("report does not contain %q", expected)
		}
	}
	for _, hiddenCard := range []string{"Any tests failed?", "Any flaky tests?", "Any unusually broad test coverage?"} {
		if strings.Contains(string(report), hiddenCard) {
			t.Errorf("report contains no-problem card %q", hiddenCard)
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
				FailedTests: []intake.TestFinding{{
					Name: "fails", Suite: "one.test.js", Status: "fail",
					Attempts: []intake.TestAttempt{{Status: "fail", Duration: time.Millisecond}},
				}},
			},
		}, nil
	}

	var output bytes.Buffer
	err = testdrive.Run(t.Context(), &output)
	if err == nil || !strings.Contains(err.Error(), "jest failed after sending 1 test event") {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(output.String(), "Test Optimization is ready.") {
		t.Fatalf("Run() did not report working instrumentation:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "Failed tests (1):") || !strings.Contains(output.String(), "one.test.js › fails · Fail · 1ms") || !strings.Contains(output.String(), "Jest: Failed") || !strings.Contains(output.String(), "file://") {
		t.Fatalf("Run() did not report the failure and report link:\n%s", output.String())
	}
}

func TestWriteFindingsIncludesOnlyPresentCategories(t *testing.T) {
	var output bytes.Buffer
	writeFindings(&output, intake.Findings{
		FlakyTests: []intake.TestFinding{{
			Name: "sometimes works", Suite: "flaky.test.js", Duration: 5 * time.Millisecond,
			Attempts: []intake.TestAttempt{
				{Status: "fail", Duration: 5 * time.Millisecond},
				{Status: "pass", Duration: 7 * time.Millisecond, Retry: true},
			},
		}},
		BroadCoverage: []intake.CoverageFinding{{
			Name: "broad.test.js", Level: "suite", FileCount: 12,
		}},
		CoveredFilesMedian: 3,
	})

	for _, expected := range []string{
		"2 findings.",
		"Flaky tests (1):",
		"flaky.test.js › sometimes works · Flaky · 5ms",
		"Unusually broad coverage (1):",
		"Median covered files: 3",
		"broad.test.js · 12 files · suite level",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("writeFindings() output does not contain %q:\n%s", expected, output.String())
		}
	}
	for _, absent := range []string{"Failed tests", "Tests slower than the others"} {
		if strings.Contains(output.String(), absent) {
			t.Errorf("writeFindings() output contains absent finding %q:\n%s", absent, output.String())
		}
	}
}

func TestRunReportsSetupAndCollectionErrors(t *testing.T) {
	t.Run("tracer install", func(t *testing.T) {
		testdrive := preparedTestdrive(t)
		testdrive.tracer = &fakeTracer{err: errors.New("npm unavailable")}

		err := testdrive.Run(t.Context(), &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "npm unavailable") {
			t.Fatalf("Run() error = %v", err)
		}
	})

	t.Run("intake start", func(t *testing.T) {
		testdrive := preparedTestdrive(t)
		testdrive.tracer = &fakeTracer{preloadPath: "/tmp/dd-trace/ci/init.js"}
		testdrive.startIntake = func(string) (localIntake, error) {
			return nil, errors.New("listener unavailable")
		}

		err := testdrive.Run(t.Context(), &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "listener unavailable") {
			t.Fatalf("Run() error = %v", err)
		}
	})

	t.Run("findings", func(t *testing.T) {
		testdrive := preparedTestdrive(t)
		server := &fakeIntake{url: "http://127.0.0.1:1234", findingsErr: errors.New("invalid event payload")}
		testdrive.tracer = &fakeTracer{preloadPath: "/tmp/dd-trace/ci/init.js"}
		testdrive.executor = &fakeTestdriveExecutor{}
		testdrive.startIntake = func(string) (localIntake, error) { return server, nil }

		err := testdrive.Run(t.Context(), &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "invalid event payload") || !server.closed {
			t.Fatalf("Run() error = %v, intake closed = %v", err, server.closed)
		}
	})

	t.Run("close", func(t *testing.T) {
		testdrive := preparedTestdrive(t)
		server := &fakeIntake{
			url:      "http://127.0.0.1:1234",
			findings: intake.Findings{TestCount: 1, TestEventCount: 1},
			closeErr: errors.New("shutdown failed"),
		}
		testdrive.tracer = &fakeTracer{preloadPath: "/tmp/dd-trace/ci/init.js"}
		testdrive.executor = &fakeTestdriveExecutor{}
		testdrive.startIntake = func(string) (localIntake, error) { return server, nil }

		err := testdrive.Run(t.Context(), &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "shutdown failed") || !server.closed {
			t.Fatalf("Run() error = %v, intake closed = %v", err, server.closed)
		}
	})
}

func TestRunReportsPassingSuiteWithoutEvents(t *testing.T) {
	testdrive := preparedTestdrive(t)
	testdrive.tracer = &fakeTracer{preloadPath: "/tmp/dd-trace/ci/init.js"}
	testdrive.executor = &fakeTestdriveExecutor{}
	testdrive.startIntake = func(string) (localIntake, error) {
		return &fakeIntake{url: "http://127.0.0.1:1234"}, nil
	}

	var output bytes.Buffer
	err := testdrive.Run(t.Context(), &output)
	if err == nil || !strings.Contains(err.Error(), "sent no test events") {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(output.String(), "No test events received.") || !strings.Contains(output.String(), "No findings.") {
		t.Fatalf("Run() output = %s", output.String())
	}
}

func TestRunReportsTestOutputWriteFailure(t *testing.T) {
	testdrive := preparedTestdrive(t)
	testdrive.tracer = &fakeTracer{preloadPath: "/tmp/dd-trace/ci/init.js"}
	testdrive.executor = &fakeTestdriveExecutor{output: []byte("PASS\n")}
	testdrive.startIntake = func(sessionDirectory string) (localIntake, error) {
		if err := os.RemoveAll(sessionDirectory); err != nil {
			t.Fatal(err)
		}
		return &fakeIntake{url: "http://127.0.0.1:1234"}, nil
	}

	err := testdrive.Run(t.Context(), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "save Jest output") {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestTestEnvironmentPreservesExistingNodeOptions(t *testing.T) {
	t.Setenv("NODE_OPTIONS", "--max-old-space-size=4096")
	environment := testEnvironment("/tmp/dd-trace/ci/init.js", "http://127.0.0.1:1234", "session")
	if environment["NODE_OPTIONS"] != "-r /tmp/dd-trace/ci/init.js --max-old-space-size=4096" {
		t.Fatalf("NODE_OPTIONS = %q", environment["NODE_OPTIONS"])
	}
}

func preparedTestdrive(t *testing.T) *Testdrive {
	t.Helper()
	repositoryRoot := t.TempDir()
	writeJestManifest(t, repositoryRoot)
	testdrive, err := Prepare(repositoryRoot)
	if err != nil {
		t.Fatal(err)
	}
	return testdrive
}

func requireWriteFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
}
