package testdrive

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
	"github.com/stretchr/testify/require"
)

func TestRubyNamedRSpecExamplesKeepScopedIdentity(t *testing.T) {
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "results.json"), `{"complete":true,"tests":[{"file":"/repo/spec/one_spec.rb","name":"same description","telemetry_name":"same description","telemetry_scoped_id":"1:1","status":"failed","failure":"same failure"},{"file":"/repo/spec/one_spec.rb","name":"same description","telemetry_name":"same description","telemetry_scoped_id":"1:2","status":"passed"}]}`)
	baseline := validationRun{root: "/repo", framework: "rspec", ExitCode: 1}
	readRubyResults(root, &baseline)
	reporting := baseline
	reporting.Tests = append([]jestTest(nil), baseline.Tests...)
	reporting.Instrumented = true
	reporting.Facts = intake.Facts{TestEventCount: 2, Tests: []intake.Test{
		{Name: "same description", SourceFile: "spec/one_spec.rb", Parameters: `{"metadata":{"scoped_id":"1:1"}}`, Attempts: []intake.TestRun{{Status: "fail"}}},
		{Name: "same description", SourceFile: "spec/one_spec.rb", Parameters: `{"metadata":{"scoped_id":"1:2"}}`, Attempts: []intake.TestRun{{Status: "pass"}}},
	}}
	require.Equal(t, "compatible", compareJest(baseline, reporting).Status)
	reporting.Facts.Tests[0].Attempts[0].Status, reporting.Facts.Tests[1].Attempts[0].Status = "pass", "fail"
	require.Equal(t, "inconclusive", verifyJestTelemetry(reporting).Status, "telemetry must match each scoped example, not description counts")
	reporting.Facts.Tests[0].Attempts[0].Status, reporting.Facts.Tests[1].Attempts[0].Status = "fail", "pass"
	reporting.Tests[0].Status, reporting.Tests[1].Status = "passed", "failed"
	reporting.Tests[0].Failure, reporting.Tests[1].Failure = "", "same failure"
	require.Equal(t, "suspected regression", compareJest(baseline, reporting).Status)
	counts := nativeExecutionCounts(baseline)
	require.NotNil(t, counts.SourceTestIdentities)
	require.Equal(t, 2, *counts.SourceTestIdentities)
}

type rubyBundleRuntimeExecutor struct{ jobs [][]onboard.RuntimeFinding }

func (e *rubyBundleRuntimeExecutor) CombinedOutput(_ context.Context, _ string, args []string, env map[string]string) ([]byte, error) {
	var jobs []onboard.RuntimeFinding
	if err := json.Unmarshal([]byte(args[len(args)-1]), &jobs); err != nil {
		return nil, err
	}
	e.jobs = append(e.jobs, append([]onboard.RuntimeFinding(nil), jobs...))
	legacy := strings.Contains(env["BUNDLE_GEMFILE"], "legacy")
	for i := range jobs {
		jobs[i].Status = "compatible"
		jobs[i].Requirement = "modern dependencies"
		if legacy {
			jobs[i].Requirement = "legacy dependencies"
		}
		if legacy != (jobs[i].Runtime == "2.7") {
			jobs[i].Status = "incompatible"
			jobs[i].Reason = "metadata from another bundle"
		}
	}
	return json.Marshal(map[string]any{"version": "5.27.0", "tracer": "1.42.0", "runtime": "3.3.11", "supported": true, "jobs": jobs})
}

func TestRubyRuntimeChecksUseEachConfigurationsBundle(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gemfiles"), 0700))
	workflow := `jobs:
  tests:
    strategy:
      matrix:
        include:
          - {ruby: '2.7', bundle: legacy}
          - {ruby: '3.3', bundle: modern}
    steps:
      - uses: ruby/setup-ruby@v1
        with: {ruby-version: '${{ matrix.ruby }}'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: ruby, ruby-tracer-version: '1.42.0'}
      - run: bundle exec rake test
        env: {BUNDLE_GEMFILE: 'gemfiles/${{ matrix.bundle }}.gemfile'}
`
	requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), workflow)
	for _, name := range []string{"legacy", "modern"} {
		requireWriteFile(t, filepath.Join(root, "gemfiles/"+name+".gemfile"), "# bundle\n")
		requireWriteFile(t, filepath.Join(root, "gemfiles/"+name+".gemfile.lock"), "installed\n")
	}
	executor := &rubyBundleRuntimeExecutor{}
	drive := Testdrive{repositoryRoot: root, language: "ruby", framework: framework.NewMinitest(platform.NewRuby("")), executor: executor, checkOnly: true, command: "bundle", args: []string{"exec", "rake", "test"}}
	require.NoError(t, drive.runAllConfigurations(t.Context(), io.Discard))
	require.Len(t, executor.jobs, 2)
	for _, jobs := range executor.jobs {
		require.Len(t, jobs, 1, "each configuration checks only its own CI locations")
	}
	data, err := os.ReadFile(filepath.Join(root, ".testoptimization/testdrive.json"))
	require.NoError(t, err)
	var report struct {
		CIRuntime onboard.RuntimeCheck `json:"ci_runtime"`
	}
	require.NoError(t, json.Unmarshal(data, &report))
	require.Equal(t, "compatible", report.CIRuntime.Status)
	require.Len(t, report.CIRuntime.Jobs, 2)
	for _, job := range report.CIRuntime.Jobs {
		require.Equal(t, "compatible", job.Status)
		if job.Runtime == "2.7" {
			require.Equal(t, "legacy dependencies", job.Requirement)
		} else {
			require.Equal(t, "modern dependencies", job.Requirement)
		}
	}
}

func TestRubyRuntimeAggregationKeepsEarlierFailure(t *testing.T) {
	current := &onboard.RuntimeCheck{Jobs: []onboard.RuntimeFinding{
		{Location: "legacy", Runtime: "2.7", Status: "incompatible", Requirement: "legacy dependencies", Reason: "legacy target rejected"},
		{Location: "modern", Runtime: "3.3", Status: "not checked"},
	}}
	next := &onboard.RuntimeCheck{Jobs: []onboard.RuntimeFinding{{Location: "modern", Runtime: "3.3", Status: "compatible", Requirement: "modern dependencies"}}}
	actual := mergeRubyRuntimeChecks(current, next)
	require.Equal(t, "incompatible", actual.Status)
	require.Equal(t, "legacy target rejected", actual.Reason)
	require.Equal(t, "legacy dependencies", actual.Jobs[0].Requirement)
	require.Equal(t, "modern dependencies", actual.Jobs[1].Requirement)
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
	return json.Marshal(map[string]any{"version": "5.27.0", "tracer": "1.42.0", "runtime": "3.3.11", "supported": true, "jobs": jobs})
}

func TestRubyNestedPreflightInspectsCheckoutCI(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "app"), 0700))
	requireWriteFile(t, filepath.Join(root, "app/Gemfile"), "# selected app bundle\n")
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
	require.Equal(t, "2.6", result.CIRuntime.Jobs[0].Runtime)
}

func TestRubyRunRetainsCheckoutRoot(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "app")
	require.NoError(t, os.Mkdir(app, 0700))
	session, err := workspace.NewSession()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, session.Close()) })
	drive := Testdrive{repositoryRoot: app, configurationRoot: root, framework: framework.NewMinitest(platform.NewRuby("")), executor: &rubyRecordingExecutor{}, command: "bundle", args: []string{"exec", "rake", "test"}, startIntake: func(string, intake.Scenario) (localIntake, error) { return &fakeIntake{}, nil }}
	run, err := drive.runRuby(t.Context(), io.Discard, session, "", "reporting-only", true, intake.Scenario{}, "", "")
	require.NoError(t, err)
	require.Equal(t, root, run.checkoutRoot)
	require.Equal(t, app, run.root)
}

func TestRubyMinitestClassChangesCannotPassComparison(t *testing.T) {
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

func TestRubyHelpersCannotCountAsApplicationCoverage(t *testing.T) {
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

func TestRubyObserverLoadsFromQuotedTemporaryPath(t *testing.T) {
	if _, err := exec.CommandContext(t.Context(), "ruby", "-rminitest", "-e", "0").CombinedOutput(); err != nil {
		t.Skip("Ruby and Minitest are required")
	}
	t.Setenv("RUBYOPT", "")
	root := t.TempDir()
	t.Chdir(root)
	temporary := filepath.Join(t.TempDir(), "space and quote's")
	require.NoError(t, os.Mkdir(temporary, 0700))
	t.Setenv("TMPDIR", temporary)
	library := t.TempDir()
	requireWriteFile(t, filepath.Join(library, "customer.rb"), "CUSTOMER_LOADED = true\n")
	t.Setenv("RUBYLIB", "ignored-ambient-path")
	session, err := workspace.NewSession()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, session.Close()) })
	drive := Testdrive{repositoryRoot: root, framework: framework.NewMinitest(platform.NewRuby("")), executor: &ext.DefaultCommandExecutor{},
		configurationEnvironment: map[string]string{"RUBYLIB": library}, command: "ruby", args: []string{"-rminitest/autorun", "-e", "require 'customer'; class FixtureTest < Minitest::Test; def test_one; assert CUSTOMER_LOADED; end; end"},
		startIntake: func(string, intake.Scenario) (localIntake, error) { return &fakeIntake{}, nil },
	}
	run, err := drive.runRuby(t.Context(), io.Discard, session, "", "baseline", false, intake.Scenario{}, "", "")
	require.NoError(t, err)
	require.Zero(t, run.ExitCode, run.commandOutput)
	require.Empty(t, run.ResultError)
	require.Len(t, run.Tests, 1)
}
