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

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
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
		"dd-trace",
		"npm run test",
		validationPath(repositoryRoot),
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
	testdrive.preflight = nil
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
	for _, expected := range []string{"Tracer error:", "2 coverage entries with an empty files list", "Affected payloads were excluded"} {
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
	drive.framework = &framework.Mocha{}
	executor := &fakeTestdriveExecutor{}
	drive.executor = executor
	drive.startIntake = func(string, intake.Scenario) (localIntake, error) {
		return &fakeIntake{url: "http://127.0.0.1:1234", findings: intake.Facts{TestEventCount: 1}}, nil
	}
	var output bytes.Buffer
	if err := drive.Run(t.Context(), &output); err == nil {
		t.Fatal("Mocha remains unvalidated")
	}
	if installer.options.Version != "git:ignored-for-existing-tracer" || installer.options.Command != drive.command {
		t.Fatal(installer.options)
	}
	if !strings.Contains(output.String(), "project installation (reused; fallback selector ignored)") {
		t.Fatal(output.String())
	}
	if !strings.Contains(executor.env["NODE_OPTIONS"], installer.preloadPath) {
		t.Fatal(executor.env)
	}
}
