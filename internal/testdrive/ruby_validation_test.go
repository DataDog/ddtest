package testdrive

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestRubyNativeResultsPreserveDuplicatesFailuresAndTelemetryNames(t *testing.T) {
	directory := t.TempDir()
	requireWriteFile(t, filepath.Join(directory, "1.json"), `{"complete":true,"tests":[{"file":"/repo/spec/one_spec.rb","name":"group nested example","telemetry_name":"nested example","status":"passed"},{"file":"/repo/spec/one_spec.rb","name":"group nested example","telemetry_name":"nested example","status":"failed","failure":"RuntimeError: customer failure"}],"errors":["before suite failed"]}`)
	var run validationRun
	readRubyResults(directory, &run)
	require.Empty(t, run.ResultError)
	require.Len(t, run.Tests, 2)
	require.Equal(t, "nested example", *run.Tests[0].telemetryName)
	require.Equal(t, "RuntimeError: customer failure", run.Tests[1].Failure)
	require.Equal(t, []string{"before suite failed"}, run.SuiteErrors)
	for _, content := range []string{`{"complete":false,"tests":[]}`, `{"complete":true}`, `{"complete":true,"tests":null}`, `invalid`} {
		requireWriteFile(t, filepath.Join(directory, "2.json"), content)
		run = validationRun{}
		readRubyResults(directory, &run)
		require.NotEmpty(t, run.ResultError, content)
	}
	run = validationRun{}
	readRubyResults(t.TempDir(), &run)
	require.NotEmpty(t, run.ResultError)
}

func TestRubyBaselineOptionsKeepCustomerPreloads(t *testing.T) {
	for input, expected := range map[string]string{
		"-W0 -rcustomer/setup -rdatadog/ci/auto_instrument": "-W0 -rcustomer/setup",
		"-r datadog/ci/auto_instrument -w":                  "-w",
		"--require=datadog/ci/auto_instrument -Ilib":        "-Ilib",
		"--require datadog/ci/auto_instrument -rcustom":     "-rcustom",
	} {
		require.Equal(t, expected, stripDatadogRubyOptions(input))
	}
}

type rubyRecordingExecutor struct {
	command string
	args    []string
	env     map[string]string
}

func (e *rubyRecordingExecutor) CombinedOutput(_ context.Context, command string, args []string, env map[string]string) ([]byte, error) {
	e.command, e.args, e.env = command, args, env
	if directory := env["DDTEST_RUBY_RESULTS"]; directory != "" {
		if err := os.MkdirAll(directory, 0700); err != nil {
			return nil, err
		}
		return nil, os.WriteFile(filepath.Join(directory, "1.json"), []byte(`{"complete":true,"tests":[{"file":"/repo/one_test.rb","name":"test_one","status":"passed"}],"errors":[]}`), 0600)
	}
	return []byte("installed"), nil
}

func TestRubyRunRetainsCommandAndSeparatesBaselineInstrumentation(t *testing.T) {
	t.Setenv("RUBYOPT", "-W0 -rcustomer -rdatadog/ci/auto_instrument")
	for _, instrumented := range []bool{false, true} {
		session, err := NewSession()
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, session.Close()) })
		executor := &rubyRecordingExecutor{}
		drive := &Testdrive{language: "ruby", framework: framework.NewMinitest(platform.NewRuby("")), repositoryRoot: t.TempDir(), command: "bundle", args: []string{"exec", "rake", "test", "TESTOPTS=-v"}, executor: executor,
			startIntake: func(string, intake.Scenario) (localIntake, error) { return &fakeIntake{}, nil }}
		run, err := drive.runRuby(t.Context(), io.Discard, session, "", "reporting-only", instrumented, intake.Scenario{}, "", "")
		require.NoError(t, err)
		require.Empty(t, run.ResultError)
		require.Equal(t, "bundle", executor.command)
		require.Equal(t, drive.args, executor.args)
		require.Contains(t, executor.env["RUBYOPT"], "-W0 -rcustomer")
		require.Contains(t, executor.env["RUBYOPT"], "ruby_observer.rb")
		if instrumented {
			require.Contains(t, executor.env["RUBYOPT"], "datadog/ci/auto_instrument")
		} else {
			require.NotContains(t, executor.env["RUBYOPT"], "datadog/ci/auto_instrument")
			require.Equal(t, "false", executor.env["DD_CIVISIBILITY_ENABLED"])
		}
		require.Equal(t, "false", executor.env["DD_CIVISIBILITY_FLAKY_RETRY_ENABLED"])
		require.Equal(t, "false", executor.env["DD_TEST_MANAGEMENT_ENABLED"])
		require.Equal(t, filepath.Clean(os.TempDir()), filepath.Dir(run.temporaryRoot), "DRb scratch is directly under the supplied TMPDIR")
		require.NoDirExists(t, run.temporaryRoot)
	}
}

func TestRubyFallbackInstallsOnlyInSessionBundle(t *testing.T) {
	executor := &rubyRecordingExecutor{}
	drive := &Testdrive{executor: executor, platform: platform.NewRuby("")}
	directory := t.TempDir()
	installation, err := drive.installRubyTracer(t.Context(), platform.TracerOptions{Directory: directory, Version: "1.40.0"})
	require.NoError(t, err)
	require.Equal(t, "bundle", executor.command)
	require.Equal(t, []string{"add", "datadog-ci", "--version", "1.40.0"}, executor.args)
	require.Equal(t, filepath.Join(directory, "bundle"), executor.env["BUNDLE_PATH"])
	require.Equal(t, filepath.Join(directory, "bundle-config"), executor.env["BUNDLE_APP_CONFIG"])
	require.Equal(t, drive.rubyTracerEnv, installation.Env)
}

func TestRubySkippingNeedsIndependentProofOfNoBodyExecution(t *testing.T) {
	for _, name := range []string{"rspec", "minitest"} {
		count := 0
		identity := intake.Test{Module: name, Suite: "probe suite", Name: "probe", SourceFile: "probe.rb"}
		run := validationRun{framework: name, ProbeExecutions: &count, Tests: []jestTest{{Name: "probe", Status: "pending"}}, Facts: intake.Facts{TestEventCount: 1, Events: []intake.Event{{Type: "test", Tags: map[string]string{"test.module": name, "test.suite": "probe suite", "test.name": "probe", "test.status": "skip", "test.skipped_by_itr": "true"}}}}}
		require.Equal(t, "passed", evaluateFeature("skipping", run, identity).Status)
		count = 1
		require.Equal(t, "failed", evaluateFeature("skipping", run, identity).Status)
	}
}

func TestRubyIncompleteEvidenceCannotPass(t *testing.T) {
	result := validationResult{Framework: "rspec", Compatibility: verdict{Status: "compatible"}, Runs: []runSummary{{Name: "reporting-only", Instrumented: true}}, Features: []featureResult{{Name: "auto-retries", Status: "passed"}}}
	prepareValidationResult(t.TempDir(), &result)
	require.False(t, result.Success)
	require.False(t, result.LocalSuccess)
}

func TestRubyAnonymousRSpecTelemetryUsesIndependentScopedIdentity(t *testing.T) {
	id := "1:2"
	run := validationRun{framework: "rspec", root: "/repo", Tests: []jestTest{{File: "/repo/spec/one_spec.rb", Name: "native generated description", Status: "passed", telemetryScopedID: id}},
		Facts: intake.Facts{TestEventCount: 1, Tests: []intake.Test{{Name: "stable anonymous description", SourceFile: "spec/one_spec.rb", Parameters: `{"arguments":{},"metadata":{"scoped_id":"1:2"}}`, Attempts: []intake.TestRun{{Status: "pass"}}}}}}
	require.Empty(t, verifyJestTelemetry(run).Status)
	run.Facts.Tests[0].Parameters = `{"metadata":{"scoped_id":"1:3"}}`
	require.Equal(t, "inconclusive", verifyJestTelemetry(run).Status)
	run.Facts.Tests[0].Parameters = `{"metadata":{"scoped_id":"1:2"}}`
	run.Facts.Tests[0].Attempts[0].Status = "fail"
	require.Equal(t, "inconclusive", verifyJestTelemetry(run).Status)
}

func TestRubyCoverageRequiresExistingApplicationSource(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "lib/customer.rb")
	spec := filepath.Join(root, "spec/one_spec.rb")
	require.NoError(t, os.MkdirAll(filepath.Dir(app), 0755))
	require.NoError(t, os.MkdirAll(filepath.Dir(spec), 0755))
	requireWriteFile(t, app, "module Customer; end\n")
	requireWriteFile(t, spec, "RSpec.describe Customer\n")
	native := []jestTest{{File: spec, Name: "test", Status: "passed"}}
	facts := intake.Facts{TestCount: 1, Tests: []intake.Test{{SourceFile: "spec/one_spec.rb", CoveredFiles: []string{"spec/one_spec.rb"}}}}
	require.Equal(t, "not observed", rubySourceCoverage(root, facts, native).Status)
	facts.Tests[0].CoveredFiles = append(facts.Tests[0].CoveredFiles, "lib/customer.rb")
	require.Equal(t, "observed", rubySourceCoverage(root, facts, native).Status)
	facts.Tests[0].CoveredFiles = append(facts.Tests[0].CoveredFiles, "missing.rb")
	require.Equal(t, "invalid", rubySourceCoverage(root, facts, native).Status)
}
