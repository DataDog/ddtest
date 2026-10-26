package testdrive

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestRubyReviewReadinessUsesWorkingDirectoryAndGemsLocked(t *testing.T) {
	for _, tc := range []struct{ directory, gemfile, lock string }{
		{".", "gems.rb", "gems.locked"},
		{"app", "gems.rb", "app/gems.locked"},
		{"app", "Gemfile", "app/Gemfile.lock"},
		{"app", "../Gemfile", "Gemfile.lock"},
	} {
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, "app"), 0700))
		requireWriteFile(t, filepath.Join(root, tc.directory, tc.gemfile), "source 'https://rubygems.org'\n")
		requireWriteFile(t, filepath.Join(root, tc.lock), "installed bundle\n")
		executor := &rubyReadinessExecutor{}
		drive := Testdrive{repositoryRoot: root, executor: executor}
		scope := onboard.ValidationScope{Commands: []onboard.TestCommand{{Directory: tc.directory, Environment: map[string]string{"BUNDLE_GEMFILE": tc.gemfile}}}}
		commands, err := drive.rubyBundleReadiness(t.Context(), scope)
		require.NoError(t, err)
		require.Empty(t, commands, tc.gemfile)
		require.Len(t, executor.environments, 1)
		copyRoot := executor.directories[0]
		if tc.directory != "." {
			copyRoot = filepath.Dir(copyRoot)
		}
		require.Equal(t, filepath.Clean(filepath.Join(copyRoot, tc.directory, tc.gemfile)), executor.environments[0]["BUNDLE_GEMFILE"])
	}
}

type rubyReviewPreflightExecutor struct{}

func (rubyReviewPreflightExecutor) CombinedOutput(_ context.Context, _ string, args []string, _ map[string]string) ([]byte, error) {
	var jobs []onboard.RuntimeFinding
	if err := json.Unmarshal([]byte(args[len(args)-1]), &jobs); err != nil {
		return nil, err
	}
	for i := range jobs {
		jobs[i].Status = "incompatible"
	}
	return json.Marshal(map[string]any{"version": "5.27.0", "tracer": "1.42.0", "ruby": "3.3.11", "supported": true, "jobs": jobs})
}
func TestRubyReviewNestedPreflightInspectsCheckoutCI(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "app"), 0700))
	requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), `jobs:
  tests:
    steps:
      - uses: ruby/setup-ruby@v1
        with: {ruby-version: '2.6'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: ruby, ruby-tracer-version: '1.42.0'}
      - run: bundle exec rake test
        working-directory: app
`)
	drive := Testdrive{repositoryRoot: filepath.Join(root, "app"), configurationRoot: root, language: "ruby", framework: framework.NewMinitest(platform.NewRuby("")), executor: rubyReviewPreflightExecutor{}, command: "bundle", args: []string{"exec", "rake", "test"}}
	var result validationResult
	require.NoError(t, drive.checkRubyPreflight(t.Context(), io.Discard, &result))
	require.Equal(t, "incompatible", result.CIRuntime.Status)
	require.Len(t, result.CIRuntime.Jobs, 1)
	require.Equal(t, "2.6", result.CIRuntime.Jobs[0].Ruby)
}

func TestRubyReviewRunRetainsCheckoutRoot(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "app")
	require.NoError(t, os.Mkdir(app, 0700))
	session, err := NewSession()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, session.Close()) })
	drive := Testdrive{repositoryRoot: app, configurationRoot: root, framework: framework.NewMinitest(platform.NewRuby("")), executor: &rubyRecordingExecutor{}, command: "bundle", args: []string{"exec", "rake", "test"}, startIntake: func(string, intake.Scenario) (localIntake, error) { return &fakeIntake{}, nil }}
	run, err := drive.runRuby(t.Context(), io.Discard, session, "", "reporting-only", true, intake.Scenario{}, "", "")
	require.NoError(t, err)
	require.Equal(t, root, run.checkoutRoot)
	require.Equal(t, app, run.root)
}

func TestRubyReviewMinitestClassChangesCannotPassComparison(t *testing.T) {
	directory := t.TempDir()
	content := `{"complete":true,"tests":[{"file":"/repo/test/one_test.rb","class_name":"FirstTest","name":"test_same","telemetry_name":"test_same","status":"failed","failure":"same failure"},{"file":"/repo/test/one_test.rb","class_name":"SecondTest","name":"test_same","telemetry_name":"test_same","status":"passed"}]}`
	requireWriteFile(t, filepath.Join(directory, "1.json"), content)
	baseline := validationRun{root: "/repo", framework: "minitest", ExitCode: 1}
	readRubyResults(directory, &baseline)
	require.Equal(t, "FirstTest#test_same", baseline.Tests[0].Name)
	reporting := baseline
	reporting.Instrumented = true
	reporting.Tests = append([]jestTest(nil), baseline.Tests...)
	reporting.Facts = intake.Facts{TestEventCount: 2, Tests: []intake.Test{
		{Name: "test_same", Suite: "FirstTest at test/one_test.rb", SourceFile: "test/one_test.rb", Status: "fail", Attempts: []intake.TestRun{{Status: "fail"}}},
		{Name: "test_same", Suite: "SecondTest at test/one_test.rb", SourceFile: "test/one_test.rb", Status: "pass", Attempts: []intake.TestRun{{Status: "pass"}}},
	}}
	require.Equal(t, "compatible", compareJest(baseline, reporting).Status)
	reporting.Tests[0].Status, reporting.Tests[1].Status = "passed", "failed"
	reporting.Tests[0].Failure, reporting.Tests[1].Failure = "", "same failure"
	require.Equal(t, "suspected regression", compareJest(baseline, reporting).Status)
	reporting.Tests = baseline.Tests
	reporting.Facts.Tests[0].Suite, reporting.Facts.Tests[1].Suite = reporting.Facts.Tests[1].Suite, reporting.Facts.Tests[0].Suite
	require.Equal(t, "inconclusive", compareJest(baseline, reporting).Status, "telemetry from a different class cannot match by method name alone")
}

func TestRubyReviewHelpersCannotCountAsApplicationCoverage(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"test/support/assertions.rb", "spec/support/setup.rb", "packages/app/test/test_helper.rb", "spec_helper.rb", "lib/customer.rb"} {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0700))
		requireWriteFile(t, filepath.Join(root, name), "# source\n")
		facts := intake.Facts{TestCount: 1, Tests: []intake.Test{{CoveredFiles: []string{name}}}}
		want := "not observed"
		if name == "lib/customer.rb" {
			want = "observed"
		}
		require.Equal(t, want, rubySourceCoverage(root, facts, nil).Status, name)
		facts.Tests[0].CoveredFiles = nil
		facts.SuiteCoverages = []intake.SuiteCoverage{{Files: []string{name}}}
		require.Equal(t, want, rubySourceCoverage(root, facts, nil).Status, name+" suite coverage")
	}
}

func TestRubyReviewRefreshesTracerForEachBundle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake bundle")
	}
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	require.NoError(t, os.Mkdir(bin, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(bin, "bundle"), []byte("#!/bin/sh\ncase \"$BUNDLE_GEMFILE\" in */traced/Gemfile) echo '  * datadog-ci (1.42.0)' ;; *) exit 1 ;; esac\n"), 0700))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	drive := Testdrive{platform: platform.NewRuby(""), language: "ruby", projectTracer: "cached root tracer"}
	drive.configurationEnvironment = map[string]string{"BUNDLE_GEMFILE": filepath.Join(root, "untraced/Gemfile")}
	drive.refreshConfigurationTracer(t.Context())
	require.Empty(t, drive.projectTracer)
	drive.configurationEnvironment["BUNDLE_GEMFILE"] = filepath.Join(root, "traced/Gemfile")
	drive.refreshConfigurationTracer(t.Context())
	require.Contains(t, drive.projectTracer, "1.42.0")
}

func TestRubyReviewFocusedCheckOnlyLoadsOnlyOwnedCopy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake bundle")
	}
	root := t.TempDir()
	bin := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "Gemfile"), "# Gemfile whose evaluation writes a generated file\n")
	requireWriteFile(t, filepath.Join(root, "Gemfile.lock"), "installed bundle\n")
	script := `#!/bin/sh
printf loaded > gemfile-generated
printf '%s' '{"version":"5.27.0","tracer":"1.42.0","ruby":"3.3.11","supported":true,"jobs":[]}'
`
	require.NoError(t, os.WriteFile(filepath.Join(bin, "bundle"), []byte(script), 0700))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Chdir(root)
	drive := Testdrive{repositoryRoot: root, language: "ruby", framework: framework.NewMinitest(platform.NewRuby("")), executor: &ext.DefaultCommandExecutor{}, command: "bundle", args: []string{"exec", "rake", "test"}, checkOnly: true}
	require.NoError(t, drive.runConfigurationChecks(t.Context(), io.Discard))
	require.NoFileExists(t, filepath.Join(root, "gemfile-generated"))
}
