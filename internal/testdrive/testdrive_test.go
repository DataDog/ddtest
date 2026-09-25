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
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/spf13/viper"
)

type fakeTracer struct {
	platform.Platform
	preloadPath      string
	project          bool
	env              map[string]string
	options          platform.TracerOptions
	sessionDirectory string
	err              error
}

func (f *fakeTracer) InstallTestdriveTracer(_ context.Context, options platform.TracerOptions) (platform.TracerInstallation, error) {
	sessionDirectory := options.Directory
	f.sessionDirectory = sessionDirectory
	f.options = options
	return platform.TracerInstallation{Path: f.preloadPath, Project: f.project, Env: f.env}, f.err
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
	findings    intake.Facts
	findingsErr error
	closeErr    error
	closed      bool
}

func (f *fakeIntake) URL() string { return f.url }
func (f *fakeIntake) Facts() (intake.Facts, error) {
	if !f.closed {
		return intake.Facts{}, errors.New("findings read before intake was drained")
	}
	return f.findings, f.findingsErr
}
func (f *fakeIntake) Close() error { f.closed = true; return f.closeErr }

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

	t.Chdir(repositoryRoot)
	testdrive, err := Prepare("latest")
	if err != nil {
		t.Fatalf("Prepare() unexpected error: %v", err)
	}
	var output bytes.Buffer
	testdrive.Preview(&output)

	for _, expected := range []string{
		"found JavaScript and Jest",
		"dd-trace@",
		"npx jest",
		filepath.Join(repositoryRoot, ".testoptimization", "testdrive"),
		"will not change package.json",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("Preview() output does not contain %q:\n%s", expected, output.String())
		}
	}
}

func TestPrepareRejectsUnsupportedRepository(t *testing.T) {
	t.Chdir(t.TempDir())
	_, err := Prepare("latest")
	if err == nil || !strings.Contains(err.Error(), "package.json") {
		t.Fatalf("Prepare() error = %v, want package.json diagnostic", err)
	}
}

func TestPrepareRejectsJavaScriptWithoutSupportedRunner(t *testing.T) {
	repositoryRoot := t.TempDir()
	requireWriteFile(t, filepath.Join(repositoryRoot, "package.json"), `{"scripts":{"test":"node test.js"}}`)

	t.Chdir(repositoryRoot)
	_, err := Prepare("latest")
	if err == nil || !strings.Contains(err.Error(), "could not detect a supported javascript test framework") {
		t.Fatalf("Prepare() error = %v, want framework diagnostic", err)
	}
}

func TestPrepareReportsMalformedManifest(t *testing.T) {
	repositoryRoot := t.TempDir()
	requireWriteFile(t, filepath.Join(repositoryRoot, "package.json"), "{")

	t.Chdir(repositoryRoot)
	_, err := Prepare("latest")
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
	t.Chdir(repositoryRoot)
	testdrive, err := Prepare("latest")
	if err != nil {
		t.Fatal(err)
	}

	installer := &fakeTracer{preloadPath: "/tmp/dd-trace/ci/init.js"}
	executor := &fakeTestdriveExecutor{output: []byte("PASS one.test.js\n")}
	server := &fakeIntake{
		url: "http://127.0.0.1:1234",
		findings: intake.Facts{
			TestCount:          2,
			TestEventCount:     2,
			CoveredTestCount:   2,
			TestDurationMedian: time.Second,
			Tests: []intake.Test{
				{Name: "fast test", Suite: "one.test.js", SourceFile: "one.test.js", SourceStart: 1, Status: "pass", Duration: time.Millisecond, Attempts: []intake.TestRun{{Status: "pass", Duration: time.Millisecond}}},
				{Name: "slow test", Suite: "one.test.js", SourceFile: "one.test.js", SourceStart: 1, Status: "pass", Duration: 2 * time.Second, Attempts: []intake.TestRun{{Status: "pass", Duration: 2 * time.Second}}},
			},
			SlowTests: []intake.Test{
				{
					Name: "slow test", Suite: "one.test.js", SourceFile: "one.test.js", SourceStart: 1, Duration: 2 * time.Second,
					Attempts: []intake.TestRun{
						{Status: "pass", Duration: 1500 * time.Millisecond},
						{Status: "pass", Duration: 2 * time.Second, Retry: true, RetryReason: "early_flake_detection"},
					},
				},
			},
		},
	}
	testdrive.platform = installer
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
	if executor.command != "npx" || strings.Join(executor.args, " ") != "jest" {
		t.Fatalf("executed %s %v, want npx jest", executor.command, executor.args)
	}
	if executor.env["DD_API_KEY"] != "ddtest-testdrive" {
		t.Fatalf("DD_API_KEY = %q", executor.env["DD_API_KEY"])
	}
	if executor.env["DD_CIVISIBILITY_AGENTLESS_URL"] != server.url {
		t.Fatalf("agentless URL = %q", executor.env["DD_CIVISIBILITY_AGENTLESS_URL"])
	}
	if !strings.HasPrefix(executor.env["NODE_OPTIONS"], "-r "+strconv.Quote(installer.preloadPath)) {
		t.Fatalf("NODE_OPTIONS = %q", executor.env["NODE_OPTIONS"])
	}
	for _, expected := range []string{
		"Test events received.",
		"1 finding.",
		"Tests slower than the others (1):",
		"Median test time: 1s",
		"one.test.js › slow test · Pass · 2s",
		"Run details:",
		"Test events: 2",
		"Tests with coverage: 2 / 2",
		"Jest: Passed",
		"Tracer: dd-trace@latest · isolated",
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
	t.Chdir(repositoryRoot)
	testdrive, err := Prepare("latest")
	if err != nil {
		t.Fatal(err)
	}

	testdrive.platform = &fakeTracer{preloadPath: "/tmp/dd-trace/ci/init.js"}
	testdrive.executor = &fakeTestdriveExecutor{output: []byte("FAIL one.test.js\n"), err: errors.New("exit status 1")}
	testdrive.startIntake = func(string) (localIntake, error) {
		return &fakeIntake{
			url: "http://127.0.0.1:1234",
			findings: intake.Facts{
				TestCount:      1,
				TestEventCount: 1,
				FailedTests: []intake.Test{{
					Name: "fails", Suite: "one.test.js", Status: "fail",
					Attempts: []intake.TestRun{{Status: "fail", Duration: time.Millisecond}},
				}},
			},
		}, nil
	}

	var output bytes.Buffer
	err = testdrive.Run(t.Context(), &output)
	if err == nil || !strings.Contains(err.Error(), "jest failed after sending 1 test event") {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(output.String(), "Test events received.") {
		t.Fatalf("Run() did not report working instrumentation:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "Failed tests (1):") || !strings.Contains(output.String(), "one.test.js › fails · Fail · 1ms") || !strings.Contains(output.String(), "Jest: Failed") {
		t.Fatalf("Run() did not report the failure and report link:\n%s", output.String())
	}
}

func TestWriteFindingsIncludesOnlyPresentCategories(t *testing.T) {
	var output bytes.Buffer
	writeFindings(&output, intake.Facts{
		ConfigurationErrors: []string{"skippable_tests"},
		FlakyTests: []intake.Test{{
			Name: "sometimes works", Suite: "flaky.test.js", Duration: 5 * time.Millisecond,
			Attempts: []intake.TestRun{
				{Status: "fail", Duration: 5 * time.Millisecond},
				{Status: "pass", Duration: 7 * time.Millisecond, Retry: true},
			},
		}},
		BroadCoverage: []intake.CoverageFact{{
			Name: "broad.test.js", Level: "suite", FileCount: 12,
		}},
		CoveredFilesMedian: 3,
	})

	for _, expected := range []string{
		"2 findings.",
		"Flaky tests (1):",
		"flaky.test.js › sometimes works · Flaky · 5ms",
		"Unusually broad coverage (1):",
		"Tracer configuration errors: skippable_tests.",
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

func TestWriteFindingsCountsIndividualFindings(t *testing.T) {
	var output bytes.Buffer
	writeFindings(&output, intake.Facts{FailedTests: []intake.Test{{Name: "one"}, {Name: "two"}, {Name: "three"}}})
	if !strings.Contains(output.String(), "3 findings.") {
		t.Fatalf("writeFindings() did not count individual findings:\n%s", output.String())
	}
}

func TestRunReportsSetupAndCollectionErrors(t *testing.T) {
	t.Run("tracer install", func(t *testing.T) {
		testdrive := preparedTestdrive(t)
		testdrive.platform = &fakeTracer{err: errors.New("npm unavailable")}

		err := testdrive.Run(t.Context(), &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "npm unavailable") {
			t.Fatalf("Run() error = %v", err)
		}
	})

	t.Run("intake start", func(t *testing.T) {
		testdrive := preparedTestdrive(t)
		testdrive.platform = &fakeTracer{preloadPath: "/tmp/dd-trace/ci/init.js"}
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
		testdrive.platform = &fakeTracer{preloadPath: "/tmp/dd-trace/ci/init.js"}
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
			findings: intake.Facts{TestCount: 1, TestEventCount: 1},
			closeErr: errors.New("shutdown failed"),
		}
		testdrive.platform = &fakeTracer{preloadPath: "/tmp/dd-trace/ci/init.js"}
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
	testdrive.platform = &fakeTracer{preloadPath: "/tmp/dd-trace/ci/init.js"}
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
	testdrive.platform = &fakeTracer{preloadPath: "/tmp/dd-trace/ci/init.js"}
	testdrive.executor = &fakeTestdriveExecutor{output: []byte("PASS\n")}
	testdrive.startIntake = func(sessionDirectory string) (localIntake, error) {
		if err := os.RemoveAll(sessionDirectory); err != nil {
			t.Fatal(err)
		}
		return &fakeIntake{url: "http://127.0.0.1:1234"}, nil
	}

	err := testdrive.Run(t.Context(), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "save test output") {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestTestEnvironmentPreservesExistingNodeOptions(t *testing.T) {
	t.Setenv("NODE_OPTIONS", "--require dd-trace/ci/init --max-old-space-size=4096 --import=/tmp/dd-trace/register.js")
	environment := testEnvironment("/tmp/dd-trace/ci/init.js", "http://127.0.0.1:1234", "session")
	if environment["NODE_OPTIONS"] != `-r "/tmp/dd-trace/ci/init.js" --max-old-space-size=4096` {
		t.Fatalf("NODE_OPTIONS = %q", environment["NODE_OPTIONS"])
	}
}

func preparedTestdrive(t *testing.T) *Testdrive {
	t.Helper()
	repositoryRoot := t.TempDir()
	writeJestManifest(t, repositoryRoot)
	t.Chdir(repositoryRoot)
	testdrive, err := Prepare("latest")
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

func TestWriteFindingsReportsEmptyCoverageAsTracerError(t *testing.T) {
	var output bytes.Buffer
	writeFindings(&output, intake.Facts{EmptyCoverageEntryCount: 2})
	for _, expected := range []string{"Tracer error:", "2 coverage entries with an empty files list", "Affected payloads were excluded", "Inspect the captured traffic"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("missing %q in output: %s", expected, output.String())
		}
	}
	if strings.Contains(output.String(), "No findings.") {
		t.Fatalf("empty coverage was hidden as no findings: %s", output.String())
	}
}

func TestPreparePreviewsSelectedTracer(t *testing.T) {
	root := t.TempDir()
	writeJestManifest(t, root)
	t.Chdir(root)
	for _, version := range []string{"6.15.0", "git:abc1234"} {
		drive, err := Prepare(version)
		if err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		drive.Preview(&output)
		if !strings.Contains(output.String(), "dd-trace@"+version) {
			t.Fatal(output.String())
		}
	}
	if _, err := Prepare("git:"); err == nil {
		t.Fatal("accepted empty Git ref")
	}
}

func TestRunReportsProjectTracer(t *testing.T) {
	root := t.TempDir()
	writeJestManifest(t, root)
	t.Chdir(root)
	drive, err := Prepare("git:ignored-for-existing-tracer")
	if err != nil {
		t.Fatal(err)
	}
	installer := &fakeTracer{preloadPath: "/project/node_modules/dd-trace/ci/init.js", project: true}
	drive.platform = installer
	executor := &fakeTestdriveExecutor{}
	drive.executor = executor
	drive.startIntake = func(string) (localIntake, error) {
		return &fakeIntake{url: "http://127.0.0.1:1234", findings: intake.Facts{TestEventCount: 1}}, nil
	}
	var output bytes.Buffer
	if err := drive.Run(t.Context(), &output); err != nil {
		t.Fatal(err)
	}
	if installer.options.Version != "git:ignored-for-existing-tracer" || installer.options.Command != drive.command {
		t.Fatal(installer.options)
	}
	if !strings.Contains(output.String(), "Tracer: project tracer · reused") {
		t.Fatal(output.String())
	}
	if !strings.Contains(executor.env["NODE_OPTIONS"], installer.preloadPath) {
		t.Fatal(executor.env)
	}
}

type commandFramework struct{ framework.Framework }

func (commandFramework) Command() (string, []string) { return "custom-runner", []string{"suite"} }

func TestTestCommand(t *testing.T) {
	t.Cleanup(func() { viper.Reset(); settings.Init() })
	for _, tc := range []struct {
		name, override, command string
		args                    []string
		wantError               bool
	}{
		{name: "framework dispatch", command: "custom-runner", args: []string{"suite"}},
		{name: "explicit override", override: "node 'path with spaces.js'", command: "node", args: []string{"path with spaces.js"}},
		{name: "invalid quoting", override: "node '", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			viper.Reset()
			viper.Set("command", tc.override)
			settings.Init()
			command, args, err := testCommand(commandFramework{})
			if (err != nil) != tc.wantError {
				t.Fatalf("testCommand() error = %v", err)
			}
			if !tc.wantError && (command != tc.command || !slices.Equal(args, tc.args)) {
				t.Fatalf("testCommand() = %q %q, want %q %q", command, args, tc.command, tc.args)
			}
		})
	}
}
