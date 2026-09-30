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
	"runtime"
	"slices"
	"strconv"
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
	repositoryRoot := filepath.Join(t.TempDir(), "project space")
	if err := os.Mkdir(repositoryRoot, 0755); err != nil {
		t.Fatal(err)
	}
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
		validationPath(""),
		"will not change package.json",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("Preview() output does not contain %q:\n%s", expected, output.String())
		}
	}
	for _, unwanted := range []string{repositoryRoot, "save a clickable report", "save captured traffic"} {
		if strings.Contains(output.String(), unwanted) {
			t.Errorf("Preview() contains %q:\n%s", unwanted, output.String())
		}
	}
}

func TestPreviewNamesOnlyDiscoveredDependencyFiles(t *testing.T) {
	for _, tc := range []struct {
		name, language string
		framework      framework.Framework
		files          []string
		want           string
	}{
		{"manifest only", "javascript", framework.NewJest(), []string{"package.json"}, "It will not change package.json."},
		{"npm", "javascript", framework.NewJest(), []string{"package.json", "package-lock.json"}, "It will not change package.json or package-lock.json."},
		{"pnpm with other languages", "javascript", framework.NewJest(), []string{"package.json", "pnpm-lock.yaml", "Gemfile", "pyproject.toml"}, "It will not change package.json or pnpm-lock.yaml."},
		{"yarn", "javascript", framework.NewJest(), []string{"package.json", "yarn.lock"}, "It will not change package.json or yarn.lock."},
		{"bun", "javascript", framework.NewJest(), []string{"package.json", "bun.lock"}, "It will not change package.json or bun.lock."},
		{"uv", "python", framework.NewPytest(), []string{"pyproject.toml", "uv.lock", "package.json"}, "It will not change pyproject.toml or uv.lock."},
		{"pip", "python", framework.NewPytest(), []string{"requirements.txt", "requirements-dev.txt"}, "It will not change requirements-dev.txt or requirements.txt."},
		{"poetry", "python", framework.NewPytest(), []string{"pyproject.toml", "poetry.lock"}, "It will not change pyproject.toml or poetry.lock."},
		{"ruby reused", "ruby", framework.NewRSpec(), []string{"Gemfile", "Gemfile.lock", "package.json"}, "It will not change Gemfile or Gemfile.lock."},
		{"multiple locks", "javascript", framework.NewJest(), []string{"package.json", "package-lock.json", "yarn.lock"}, "It will not change package.json, package-lock.json or yarn.lock."},
		{"no dependency files", "python", framework.NewPytest(), []string{"pytest.ini"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "project [space]")
			if err := os.Mkdir(root, 0755); err != nil {
				t.Fatal(err)
			}
			for _, name := range tc.files {
				requireWriteFile(t, filepath.Join(root, name), "")
			}
			// A directory with a dependency filename is not a discovered file.
			if err := os.Mkdir(filepath.Join(root, "bun.lockb"), 0755); err != nil {
				t.Fatal(err)
			}
			drive := &Testdrive{repositoryRoot: root, language: tc.language, framework: tc.framework, session: planSession(), projectTracer: "installed"}
			var output bytes.Buffer
			drive.Preview(&output)
			var notice string
			for line := range strings.SplitSeq(output.String(), "\n") {
				if strings.HasPrefix(line, "It will not change") {
					notice = line
				}
			}
			if notice != tc.want {
				t.Fatalf("dependency notice = %q, want %q", notice, tc.want)
			}
		})
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
		"3 findings.",
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
	environment := javascriptEnvironment("/tmp/dd-trace/ci/init.js")
	if environment["NODE_OPTIONS"] != `--max-old-space-size=4096 -r "/tmp/dd-trace/ci/init.js"` {
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

func TestEnvironmentAppliesOnlySelectedPlatform(t *testing.T) {
	for _, language := range []string{"javascript", "python", "ruby"} {
		drive := &Testdrive{language: language, framework: framework.NewJest()}
		env := drive.environment("/tracer/init.js", "http://127.0.0.1:1234", "run")
		if (env["NODE_OPTIONS"] != "") != (language == "javascript") {
			t.Fatalf("%s NODE_OPTIONS = %q", language, env["NODE_OPTIONS"])
		}
		if env["DD_CIVISIBILITY_AGENTLESS_URL"] != "http://127.0.0.1:1234" {
			t.Fatal(env)
		}
	}
}

func TestInstalledTracerLabel(t *testing.T) {
	root := t.TempDir()
	preload := filepath.Join(root, "ci", "init.js")
	drive := &Testdrive{language: "javascript", tracerLabel: "dd-trace@git:not-installed"}
	if got := drive.installedTracerLabel(preload); got != "dd-trace" {
		t.Fatal(got)
	}
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"version":"6.15.0"}`)
	if got := drive.installedTracerLabel(preload); got != "dd-trace@6.15.0" {
		t.Fatal(got)
	}
	requireWriteFile(t, filepath.Join(root, "package.json"), `{invalid`)
	if got := drive.installedTracerLabel(preload); got != "dd-trace" {
		t.Fatal(got)
	}
	drive.language = "python"
	if got := drive.installedTracerLabel("3.12.0"); got != "ddtrace@3.12.0" {
		t.Fatal(got)
	}
	drive.language = "ruby"
	if got := drive.installedTracerLabel("  * datadog-ci (1.24.0)\n\tPath: /bundle"); got != "datadog-ci@1.24.0" {
		t.Fatal(got)
	}
	if got := drive.installedTracerLabel(""); got != "datadog-ci" {
		t.Fatal(got)
	}
}

func TestWriteFindingsConfigurationErrorIsAFinding(t *testing.T) {
	var output bytes.Buffer
	writeFindings(&output, intake.Facts{TestEventCount: 1, ConfigurationErrors: []string{"skippable_tests"}})
	for _, want := range []string{"Tracer configuration errors: skippable_tests", "1 finding."} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing %q: %s", want, output.String())
		}
	}
	if strings.Contains(output.String(), "No findings.") {
		t.Fatal(output.String())
	}
}

func TestPreviewChoosesTracerBeforeConfirmation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	for _, installed := range []bool{false, true} {
		t.Run(strconv.FormatBool(installed), func(t *testing.T) {
			root := t.TempDir()
			writeJestManifest(t, root)
			t.Chdir(root)
			bin := t.TempDir()
			script := "#!/bin/sh\nexit 1\n"
			if installed {
				script = "#!/bin/sh\nprintf /project/node_modules/dd-trace/ci/init.js > \"$4\"\n"
			}
			requireWriteFile(t, filepath.Join(bin, "node"), script)
			if err := os.Chmod(filepath.Join(bin, "node"), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin)
			drive, err := Prepare("6.15.0")
			if err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			drive.Preview(&output)
			preview := output.String()
			if strings.Contains(preview, "<session>") || strings.Contains(preview, "if absent") {
				t.Fatal(preview)
			}
			if _, err := os.Stat(drive.session.Directory()); !os.IsNotExist(err) {
				t.Fatalf("preview created output folder: %v", err)
			}
			if installed {
				if !strings.Contains(preview, "reuse installed dd-trace") || strings.Contains(preview, "npm install") {
					t.Fatal(preview)
				}
				drive.framework = &framework.Mocha{}
				drive.platform = &fakeTracer{err: errors.New("must not install")}
				drive.executor = &fakeTestdriveExecutor{}
				drive.startIntake = func(string, intake.Scenario) (localIntake, error) {
					return &fakeIntake{findings: intake.Facts{TestEventCount: 1}}, nil
				}
				if err := drive.Run(t.Context(), &output); err == nil || !strings.Contains(err.Error(), "validation is incomplete") {
					t.Fatal(err)
				}
			} else {
				directory, err := filepath.Rel(root, drive.session.Directory())
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(preview, "npm install --prefix "+directory+" --global=false --no-save --package-lock=false --no-audit --no-fund dd-trace@6.15.0") {
					t.Fatal(preview)
				}
				if !slices.Contains(drive.installArgs, drive.session.Directory()) {
					t.Fatalf("preview changed the actual installation path: %v", drive.installArgs)
				}
			}
		})
	}
}
