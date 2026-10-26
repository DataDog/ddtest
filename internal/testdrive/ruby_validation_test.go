package testdrive

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestRubyRequirementSelectorsUseRubyGemsBoundaries(t *testing.T) {
	ruby, err := exec.LookPath("ruby")
	if err != nil {
		t.Skip("Ruby is required to verify RubyGems requirement semantics")
	}
	// These cases exercise crossings and holes, not just a selector's minimum.
	script := rubyRequirementScript + `
cases = [
  ["3.4", [">= 2.7", "< 4.1.dev"], "compatible"],
  ["3.4.9", [">= 3.4.9", "<= 3.4.9"], "compatible"],
  ["3.4", [">= 3.4.9"], "inconclusive"],
  ["3.4", ["!= 3.4.9"], "inconclusive"],
  ["3.4", ["> 3.4.0"], "inconclusive"],
  ["3.4", ["<= 3.4.9"], "inconclusive"],
  ["3.4", ["~> 3.4.1"], "inconclusive"],
  ["3.4", ["~> 3.4.0"], "compatible"],
  ["3", ["~> 3.4.0"], "inconclusive"],
  ["3.4", ["< 3.4.0.pre"], "incompatible"],
  ["3.4", [">= 3.4.0.pre"], "compatible"],
  ["3.4", ["= 3.4.0.pre"], "incompatible"],
  ["2.6", [">= 2.7"], "incompatible"],
  ["4.1", ["< 4.1.dev"], "incompatible"],
  ["head", [">= 2.7"], "inconclusive"],
  ["jruby-9.4", [">= 2.7"], "inconclusive"]
]
cases.each do |selector, constraints, expected|
  actual = ruby_requirement_status(selector, constraints.map { |c| Gem::Requirement.new(c) })
  abort "#{selector} #{constraints}: #{actual} != #{expected}" unless actual == expected
end
`
	data, err := exec.CommandContext(t.Context(), ruby, "-e", script).CombinedOutput()
	require.NoError(t, err, string(data))
}

func TestRubyCIPreflightChecksTracerDependenciesWithoutLocalApplicationConstraints(t *testing.T) {
	ruby, err := exec.LookPath("ruby")
	if err != nil {
		t.Skip("Ruby is required to inspect RubyGems specifications")
	}
	setup := `
framework = Gem::Specification.new { |s| s.name = "minitest"; s.version = "5.27.0"; s.required_ruby_version = ">= 3.4" }
Gem::Specification.define_singleton_method(:find_by_name) { |*| framework }
Gem.loaded_specs["unrelated-app"] = Gem::Specification.new { |s| s.name = "unrelated-app"; s.version = "1"; s.required_ruby_version = ">= 99" }
Gem.loaded_specs["datadog-ci"] = Gem::Specification.new do |s|
  s.name = "datadog-ci"; s.version = "1.42.0"; s.required_ruby_version = ">= 2.7"
  s.add_runtime_dependency "trace-dependency"
end
Gem.loaded_specs["trace-dependency"] = Gem::Specification.new { |s| s.name = "trace-dependency"; s.version = "1"; s.required_ruby_version = ">= 3.0", "< 4.1.dev" }
`
	jobs, err := json.Marshal([]onboard.RuntimeFinding{
		{Ruby: "3.4", Status: "not checked", TracerRequested: "1.42.0"},
		{Ruby: "2.7", Status: "not checked", TracerRequested: "1.42.0"},
		{Ruby: "2.6", Status: "not checked", TracerRequested: "1.42.0"},
		{Ruby: "3.4", Status: "not checked", TracerRequested: "latest"},
		{Ruby: "head", Status: "excluded"},
	})
	require.NoError(t, err)
	data, err := exec.CommandContext(t.Context(), ruby, "-e", setup+rubyPreflightScript, "minitest", string(jobs)).CombinedOutput()
	require.NoError(t, err, string(data))
	var info struct{ Jobs []onboard.RuntimeFinding }
	require.NoError(t, json.Unmarshal(data, &info))
	for i, status := range []string{"compatible", "inconclusive", "incompatible", "inconclusive", "excluded"} {
		require.Equal(t, status, info.Jobs[i].Status)
	}
	require.NotContains(t, info.Jobs[0].Requirement, "99")
}

func TestRubyCIPreflightUsesOnlyMatchingPortablePlatformMetadata(t *testing.T) {
	ruby, err := exec.LookPath("ruby")
	if err != nil {
		t.Skip("Ruby is required")
	}
	for _, scenario := range []string{"matching", "different version", "different dependencies", "still incompatible", "offline"} {
		t.Run(scenario, func(t *testing.T) {
			setup := `require "rubygems/source"
framework = Gem::Specification.new { |s| s.name="minitest"; s.version="5.27.0" }
Gem::Specification.define_singleton_method(:find_by_name) { |*| framework }
Gem.loaded_specs["datadog-ci"] = Gem::Specification.new { |s| s.name="datadog-ci"; s.version="1.42.0"; s.required_ruby_version=">= 2.7"; s.add_runtime_dependency "native-dependency" }
Gem.loaded_specs["native-dependency"] = Gem::Specification.new { |s| s.name="native-dependency"; s.version="1.0"; s.platform="aarch64-linux"; s.required_ruby_version=">= 3.0" }
scenario = ARGV.shift
portable = Gem::Specification.new { |s| s.name="native-dependency"; s.version=scenario == "different version" ? "2.0" : "1.0"; s.required_ruby_version=scenario == "still incompatible" ? ">= 3.0" : ">= 2.5"; s.add_runtime_dependency "unresolved" if scenario == "different dependencies" }
source = Object.new
source.define_singleton_method(:fetch_spec) do |tuple|
  raise "offline" if scenario == "offline"
  raise "unexpected requested platform" unless tuple.platform.to_s == "ruby"
  portable
end
Gem::Source.define_singleton_method(:new) { |*| source }
`
			jobs, err := json.Marshal([]onboard.RuntimeFinding{{Ruby: "2.7", Status: "not checked", TracerRequested: "1.42.0"}})
			require.NoError(t, err)
			data, err := exec.CommandContext(t.Context(), ruby, "-e", setup+rubyPreflightScript, scenario, "minitest", string(jobs)).CombinedOutput()
			require.NoError(t, err, string(data))
			var info struct{ Jobs []onboard.RuntimeFinding }
			require.NoError(t, json.Unmarshal(data, &info))
			if scenario == "matching" {
				require.Equal(t, "compatible", info.Jobs[0].Status)
				require.Contains(t, info.Jobs[0].Reason, "same-version portable gem metadata")
				require.Contains(t, info.Jobs[0].Requirement, ">= 2.5")
			} else {
				require.Equal(t, "inconclusive", info.Jobs[0].Status)
			}
		})
	}
}

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
			rubyTracerEnv: map[string]string{"BUNDLE_PATH": filepath.Join(session.Directory(), "bundle"), "BUNDLE_APP_CONFIG": filepath.Join(session.Directory(), "bundle-config")},
			startIntake:   func(string, intake.Scenario) (localIntake, error) { return &fakeIntake{}, nil }}
		run, err := drive.runRuby(t.Context(), io.Discard, session, "", "reporting-only", instrumented, intake.Scenario{}, "", "")
		require.NoError(t, err)
		require.Empty(t, run.ResultError)
		require.Equal(t, "bundle", executor.command)
		require.Equal(t, drive.args, executor.args)
		for key, value := range drive.rubyTracerEnv {
			require.Equal(t, value, executor.env[key], "fallback bundle must reach every native and instrumented subprocess")
		}
		require.Contains(t, executor.env["RUBYOPT"], "-W0 -rcustomer")
		require.Contains(t, executor.env["RUBYOPT"], "ruby_observer.rb")
		if instrumented {
			require.Contains(t, executor.env["RUBYOPT"], "datadog/ci/auto_instrument")
		} else {
			require.NotContains(t, executor.env["RUBYOPT"], "datadog/ci/auto_instrument")
			require.Equal(t, "false", executor.env["DD_CIVISIBILITY_ENABLED"])
		}
		require.Equal(t, "false", executor.env["DD_CIVISIBILITY_FLAKY_RETRY_ENABLED"])
		require.Equal(t, "false", executor.env["DD_CIVISIBILITY_GIT_METADATA_UPLOAD_ENABLED"])
		require.Equal(t, "false", executor.env["DD_TEST_MANAGEMENT_ENABLED"])
		require.Equal(t, filepath.Clean(os.TempDir()), filepath.Dir(run.temporaryRoot), "DRb scratch is directly under the supplied TMPDIR")
		require.NoDirExists(t, run.temporaryRoot)
	}
}

func TestRubyRunKeepsCIConfigurationAfterTracerPreload(t *testing.T) {
	t.Setenv("RUBYOPT", "-W0 -rbundler/setup -rdatadog/ci/auto_instrument -r./ci_datadog.rb")
	session, err := NewSession()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, session.Close()) })
	executor := &rubyRecordingExecutor{}
	drive := &Testdrive{language: "ruby", framework: framework.NewMinitest(platform.NewRuby("")), repositoryRoot: t.TempDir(), command: "bundle", args: []string{"exec", "rake", "test"}, executor: executor,
		startIntake: func(string, intake.Scenario) (localIntake, error) { return &fakeIntake{}, nil }}
	_, err = drive.runRuby(t.Context(), io.Discard, session, "", "reporting-only", true, intake.Scenario{}, "", "")
	require.NoError(t, err)
	options := executor.env["RUBYOPT"]
	require.Less(t, strings.Index(options, "datadog/ci/auto_instrument"), strings.Index(options, "ci_datadog.rb"), "the CI initializer must run after the tracer, as it does in CI")
}

func TestRubyFallbackInstallsOnlyInSessionBundle(t *testing.T) {
	t.Setenv("RUBYOPT", "-W0 -r./ci/datadog_test_optimization.rb")
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
	require.Empty(t, executor.env["RUBYOPT"], "native extension compilation must not inherit test preloads tied to the repository working directory")
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

func TestRubyReportsRequirePairedExecutionAndFeatureValidation(t *testing.T) {
	for _, name := range []string{"rspec", "minitest"} {
		for _, complete := range []bool{false, true} {
			t.Run(name+map[bool]string{false: "/reporting-only", true: "/complete"}[complete], func(t *testing.T) {
				root := t.TempDir()
				result := completeValidationFixture(name)
				if !complete {
					result.Runs = result.Runs[1:]
				}
				var output bytes.Buffer
				err := finishValidation(&output, root, result)
				require.Equal(t, complete, err == nil)
				report := readValidationReport(t, root)
				require.Equal(t, complete, report.Success)
				require.Equal(t, map[bool]string{false: "INCOMPLETE", true: "PASSED"}[complete], report.Summary.Status)
			})
		}
	}
}

func TestRubyCIScopeKeepsInstrumentedAndExcludedVersions(t *testing.T) {
	check := &onboard.RuntimeCheck{Jobs: []onboard.RuntimeFinding{{Ruby: "3.4", Status: "compatible"}, {Ruby: "head", Status: "excluded"}, {Ruby: "jruby-9.4", Status: "inconclusive"}}}
	scope := summarizeCIScope(check)
	require.Equal(t, []string{"Ruby 3.4"}, scope.Instrumented)
	require.Equal(t, []string{"Ruby head"}, scope.Excluded)
	require.Equal(t, []string{"Ruby jruby-9.4"}, scope.Unverified)
}

func TestRubyMissingCoverageReportsRubySetupRemediation(t *testing.T) {
	for _, name := range []string{"rspec", "minitest"} {
		result := validationResult{Framework: name, Runs: []runSummary{{Name: "reporting-only", Coverage: &coverageSummary{Status: "not observed"}}}}
		summary := summarizeValidation(result)
		blockers := strings.Join(summary.BlockingChecks, "\n")
		require.Contains(t, blockers, "Ruby application coverage was not observed")
		require.Contains(t, blockers, "Gemfile/runtime")
		require.NotContains(t, blockers, "Mocha")
		require.NotContains(t, blockers, "NYC")
	}
}

func TestRubyAnonymousRSpecTelemetryUsesIndependentScopedIdentity(t *testing.T) {
	id := "1:2"
	run := validationRun{framework: "rspec", root: "/repo", Tests: []jestTest{{File: "/repo/spec/one_spec.rb", Name: "native generated description", Status: "passed", RSpecScopedID: id}},
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

func TestRubyTelemetryMismatchReportsExecutionCounts(t *testing.T) {
	run := validationRun{framework: "minitest", root: "/repo", Tests: []jestTest{{File: "/repo/test/one_test.rb", Name: "test_one", Status: "passed"}, {File: "/repo/test/one_test.rb", Name: "test_two", Status: "passed"}}, Facts: intake.Facts{TestEventCount: 1, Tests: []intake.Test{{Name: "test_one", SourceFile: "test/one_test.rb", Attempts: []intake.TestRun{{Status: "pass"}}}}}}
	result := verifyJestTelemetry(run)
	require.Equal(t, "inconclusive", result.Status)
	require.Equal(t, "ruby_telemetry_mismatch", result.Code)
	require.Contains(t, result.Reason, "2 native executions, 1 reporting executions")
	require.Contains(t, strings.Join(result.Differences, " "), "test_two")
}

func TestRubyHandoffKeepsExistingFailuresVisible(t *testing.T) {
	result := completeValidationFixture("minitest")
	result.Runs[1].TestCounts = testCounts{Passed: 174, Failed: 1}
	result.Summary = summarizeValidation(result)
	require.Contains(t, validationHandoff(result), "174 passed, 1 failed, 0 skipped")
}

func TestRubyInstrumentedOptionsPreserveEveryCIRequireForm(t *testing.T) {
	for _, preload := range []string{"-rdatadog/ci/auto_instrument", "-r datadog/ci/auto_instrument", "--require=datadog/ci/auto_instrument", "--require datadog/ci/auto_instrument.rb"} {
		original := "-W0 " + preload + " -r./ci_custom.rb"
		options := rubyRunOptions(original, true)
		require.Contains(t, options, original)
		require.Equal(t, 1, strings.Count(options, "datadog/ci/auto_instrument"))
	}
	options := rubyRunOptions("-W0 -r./ci_custom.rb", true)
	require.Less(t, strings.Index(options, "datadog/ci/auto_instrument"), strings.Index(options, "ci_custom.rb"))
	baseline := rubyRunOptions("-rdatadog/ci/auto_instrument -r./ci_custom.rb", false)
	require.NotContains(t, baseline, "datadog/ci")
	require.Contains(t, baseline, "ci_custom.rb")
}
