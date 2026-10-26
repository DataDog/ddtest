package testdrive

import (
	"context"
	"encoding/json"
	"io"
	"maps"
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
	"github.com/stretchr/testify/require"
)

func TestRubyFollowupFingerprintsIgnoredGemsLocked(t *testing.T) {
	for _, directory := range []string{".", "app"} {
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, directory), 0700))
		requireWriteFile(t, filepath.Join(root, directory, "gems.rb"), "source 'https://rubygems.org'\n")
		requireWriteFile(t, filepath.Join(root, ".gitignore"), "gems.locked\n")
		data, err := exec.CommandContext(t.Context(), "git", "init", "-q", root).CombinedOutput()
		require.NoError(t, err, string(data))
		data, err = exec.CommandContext(t.Context(), "git", "-C", root, "add", ".").CombinedOutput()
		require.NoError(t, err, string(data))
		lock := filepath.Join(root, directory, "gems.locked")
		requireWriteFile(t, lock, "first resolution\n")
		before, err := InputFingerprint(t.Context(), root)
		require.NoError(t, err)
		requireWriteFile(t, lock, "second resolution\n")
		after, err := InputFingerprint(t.Context(), root)
		require.NoError(t, err)
		require.NotEqual(t, before, after, directory)
	}
}

func TestRubyFollowupNamedRSpecExamplesKeepScopedIdentity(t *testing.T) {
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

type rubyBundlerConfigExecutor struct {
	root       string
	installEnv map[string]string
}

func (e *rubyBundlerConfigExecutor) CombinedOutput(ctx context.Context, name string, args []string, env map[string]string) ([]byte, error) {
	if name != "ruby" {
		e.installEnv = maps.Clone(env)
		return nil, nil
	}
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = e.root
	command.Env = os.Environ()
	for key, value := range env {
		command.Env = append(command.Env, key+"="+value)
	}
	return command.CombinedOutput()
}
func TestRubyFollowupPreservesEffectiveBundlerConfiguration(t *testing.T) {
	if _, err := exec.LookPath("ruby"); err != nil {
		t.Skip("Ruby is required")
	}
	for _, custom := range []bool{false, true} {
		root, session := t.TempDir(), t.TempDir()
		source := filepath.Join(root, ".bundle")
		t.Setenv("BUNDLE_APP_CONFIG", "")
		require.NoError(t, os.Unsetenv("BUNDLE_APP_CONFIG"))
		environment := map[string]string{"BUNDLE_GEMFILE": filepath.Join(root, "Gemfile")}
		if custom {
			source = filepath.Join(root, "custom-config")
			environment["BUNDLE_APP_CONFIG"] = source
		}
		require.NoError(t, os.MkdirAll(source, 0700))
		requireWriteFile(t, filepath.Join(root, "Gemfile"), "source 'https://rubygems.org'\n")
		settings := "---\nBUNDLE_BUILD__FFI: --enable-system-libffi\nBUNDLE_MIRROR__HTTPS://RUBYGEMS__ORG/: https://fixture.invalid/gems\nBUNDLE_FIXTURE__INVALID: private-fixture-token\nBUNDLE_PATH: /unowned-fixture-bundle\nBUNDLE_PATH__SYSTEM: true\nBUNDLE_DISABLE_SHARED_GEMS: false\n"
		requireWriteFile(t, filepath.Join(source, "config"), settings)
		executor := &rubyBundlerConfigExecutor{root: root}
		drive := Testdrive{executor: executor, platform: platform.NewRuby("")}
		installed, err := drive.installRubyTracer(t.Context(), platform.TracerOptions{Directory: session, Version: "1.42.0", Environment: environment})
		require.NoError(t, err)
		copied, err := os.ReadFile(filepath.Join(installed.Env["BUNDLE_APP_CONFIG"], "config"))
		require.NoError(t, err)
		for _, setting := range []string{"--enable-system-libffi", "https://fixture.invalid/gems", "private-fixture-token"} {
			require.Contains(t, string(copied), setting)
		}
		require.NotContains(t, string(copied), "/unowned-fixture-bundle")
		verify := exec.CommandContext(t.Context(), "ruby", "-rbundler", "-e", `print Bundler.settings.path.base_path`)
		verify.Dir, verify.Env = root, append(os.Environ(), "BUNDLE_GEMFILE="+environment["BUNDLE_GEMFILE"], "BUNDLE_APP_CONFIG="+installed.Env["BUNDLE_APP_CONFIG"])
		selected, err := verify.CombinedOutput()
		require.NoError(t, err, string(selected))
		require.Equal(t, filepath.Join(session, "bundle"), string(selected))
		original, err := os.ReadFile(filepath.Join(source, "config"))
		require.NoError(t, err)
		require.Equal(t, settings, string(original))
		info, err := os.Stat(filepath.Join(installed.Env["BUNDLE_APP_CONFIG"], "config"))
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0600), info.Mode().Perm())
		require.Equal(t, filepath.Join(session, "bundle"), executor.installEnv["BUNDLE_PATH"])
	}
}

func TestRubyFollowupInspectsNestedGemspecsInOwnedCopies(t *testing.T) {
	if _, err := exec.LookPath("ruby"); err != nil {
		t.Skip("Ruby is required")
	}
	for _, mode := range []string{"safe", "checkout-root", "absolute-root", "git-root"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			specDir := filepath.Join(root, "packages/foo")
			require.NoError(t, os.MkdirAll(filepath.Join(specDir, "lib"), 0700))
			requireWriteFile(t, filepath.Join(specDir, "lib/fixture.rb"), "module Fixture;end\n")
			files := "Dir['lib/**/*']"
			switch mode {
			case "checkout-root":
				files = "Dir['.testoptimization/**/*']"
			case "absolute-root":
				files = "Dir[File.expand_path('../../.testoptimization/**/*', __dir__)]"
			case "git-root":
				files = "Dir.chdir(File.expand_path('../..', __dir__)) { `git ls-files --cached --others -z`.split(\"\\x00\") }"
			}
			requireWriteFile(t, filepath.Join(specDir, "fixture.gemspec"), "Gem::Specification.new do |s|\n s.name='fixture';s.version='1.0';s.files="+files+";File.write('gemspec-generated',Dir.pwd)\nend\n")
			data, err := exec.CommandContext(t.Context(), "git", "init", "-q", root).CombinedOutput()
			require.NoError(t, err, string(data))
			data, err = exec.CommandContext(t.Context(), "git", "-C", root, "add", ".").CombinedOutput()
			require.NoError(t, err, string(data))
			t.Chdir(root)
			drive := Testdrive{repositoryRoot: root, language: "ruby", executor: &ext.DefaultCommandExecutor{}}
			check := drive.rubyPackageExclusions(t.Context())
			if mode == "safe" {
				require.Equal(t, "passed", check.Status, check.Reason)
			} else {
				require.Equal(t, "failed", check.Status, check.Reason)
				require.Contains(t, check.Reason, "local validation reports")
			}
			require.NoFileExists(t, filepath.Join(specDir, "gemspec-generated"))
			require.NoFileExists(t, filepath.Join(root, "gemspec-generated"))
		})
	}
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
		if legacy != (jobs[i].Ruby == "2.7") {
			jobs[i].Status = "incompatible"
			jobs[i].Reason = "metadata from another bundle"
		}
	}
	return json.Marshal(map[string]any{"version": "5.27.0", "tracer": "1.42.0", "ruby": "3.3.11", "supported": true, "jobs": jobs})
}
func TestRubyFollowupRuntimeChecksUseEachConfigurationsBundle(t *testing.T) {
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
		if job.Ruby == "2.7" {
			require.Equal(t, "legacy dependencies", job.Requirement)
		} else {
			require.Equal(t, "modern dependencies", job.Requirement)
		}
	}
}

func TestRubyFollowupRuntimeAggregationKeepsEarlierFailure(t *testing.T) {
	current := &onboard.RuntimeCheck{Jobs: []onboard.RuntimeFinding{
		{Location: "legacy", Ruby: "2.7", Status: "incompatible", Requirement: "legacy dependencies", Reason: "legacy target rejected"},
		{Location: "modern", Ruby: "3.3", Status: "not checked"},
	}}
	next := &onboard.RuntimeCheck{Jobs: []onboard.RuntimeFinding{{Location: "modern", Ruby: "3.3", Status: "compatible", Requirement: "modern dependencies"}}}
	actual := mergeRubyRuntimeChecks(current, next)
	require.Equal(t, "incompatible", actual.Status)
	require.Equal(t, "legacy target rejected", actual.Reason)
	require.Equal(t, "legacy dependencies", actual.Jobs[0].Requirement)
	require.Equal(t, "modern dependencies", actual.Jobs[1].Requirement)
}
