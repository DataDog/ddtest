package testdrive

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestRubySafetyFallbackClearsOnlyOwnedFrozenSettings(t *testing.T) {
	if _, err := exec.LookPath("ruby"); err != nil {
		t.Skip("Ruby is required")
	}
	root, session := t.TempDir(), t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".bundle"), 0700))
	requireWriteFile(t, filepath.Join(root, "Gemfile"), "source 'https://rubygems.org'\n")
	settings := "---\nBUNDLE_FROZEN: true\nBUNDLE_DEPLOYMENT: true\nBUNDLE_BUILD__FIXTURE: retained\n"
	requireWriteFile(t, filepath.Join(root, ".bundle/config"), settings)
	executor := &rubyBundlerConfigExecutor{root: root}
	drive := Testdrive{executor: executor, platform: platform.NewRuby("")}
	installed, err := drive.installRubyTracer(t.Context(), platform.TracerOptions{Directory: session, Version: "1.42.0", Environment: map[string]string{
		"BUNDLE_GEMFILE": filepath.Join(root, "Gemfile"), "BUNDLE_FROZEN": "true", "BUNDLE_DEPLOYMENT": "true",
	}})
	require.NoError(t, err)
	require.Equal(t, "false", executor.installEnv["BUNDLE_FROZEN"])
	require.Equal(t, "false", installed.Env["BUNDLE_DEPLOYMENT"])
	verify := exec.CommandContext(t.Context(), "ruby", "-rbundler", "-e", `print "#{Bundler.settings[:frozen]} #{Bundler.settings[:deployment]} #{Bundler.settings['build.fixture']}"`)
	verify.Dir, verify.Env = root, append(os.Environ(), "BUNDLE_GEMFILE="+filepath.Join(root, "Gemfile"), "BUNDLE_APP_CONFIG="+installed.Env["BUNDLE_APP_CONFIG"])
	data, err := verify.CombinedOutput()
	require.NoError(t, err, string(data))
	require.Equal(t, "false false retained", string(data))
	original, err := os.ReadFile(filepath.Join(root, ".bundle/config"))
	require.NoError(t, err)
	require.Equal(t, settings, string(original))
}

type rubySafetyBundleExecutor struct{ selected []string }

func (e *rubySafetyBundleExecutor) CombinedOutput(_ context.Context, _ string, args []string, env map[string]string) ([]byte, error) {
	e.selected = append(e.selected, env["BUNDLE_GEMFILE"])
	var jobs []onboard.RuntimeFinding
	if err := json.Unmarshal([]byte(args[len(args)-1]), &jobs); err != nil {
		return nil, err
	}
	for i := range jobs {
		jobs[i].Status = "compatible"
	}
	return json.Marshal(map[string]any{"version": "5.27.0", "tracer": "1.42.0", "ruby": "3.3.11", "supported": true, "jobs": jobs})
}

func TestRubySafetyDiscoveredBundlesIgnoreAmbientGemfile(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	requireWriteFile(t, filepath.Join(root, "Gemfile"), "# CI bundle\n")
	requireWriteFile(t, filepath.Join(root, "Gemfile.lock"), "CI lock\n")
	amb := filepath.Join(t.TempDir(), "Gemfile")
	requireWriteFile(t, amb, "# Appraisal shell bundle\n")
	t.Setenv("BUNDLE_GEMFILE", amb)
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
	requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  tests:\n    steps:\n      - uses: ruby/setup-ruby@v1\n        with: {ruby-version: '3.3'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: ruby, ruby-tracer-version: '1.42.0'}\n      - run: bundle exec rake test\n")
	executor := &rubySafetyBundleExecutor{}
	drive := Testdrive{repositoryRoot: root, language: "ruby", framework: framework.NewMinitest(platform.NewRuby("")), executor: executor, checkOnly: true, command: "bundle", args: []string{"exec", "rake", "test"}}
	require.NoError(t, drive.runAllConfigurations(t.Context(), io.Discard))
	require.Len(t, executor.selected, 1)
	require.NotEqual(t, amb, executor.selected[0])
	require.Equal(t, "Gemfile", filepath.Base(executor.selected[0]))
	scope, err := onboard.DiscoverValidationScope(root, "minitest")
	require.NoError(t, err)
	readiness := &rubyReadinessExecutor{}
	drive.executor = readiness
	missing, err := drive.rubyBundleReadiness(t.Context(), scope)
	require.NoError(t, err)
	require.Empty(t, missing)
	require.Len(t, readiness.environments, 1)
	require.NotEqual(t, amb, readiness.environments[0]["BUNDLE_GEMFILE"])
	_, err = InputFingerprint(t.Context(), root)
	require.NoError(t, err)
}

func TestRubySafetyRecoveryPreservesEnvironmentAndDirectory(t *testing.T) {
	if _, err := exec.LookPath("ruby"); err != nil {
		t.Skip("Ruby is required")
	}
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "app"), 0700))
	requireWriteFile(t, filepath.Join(root, "app/gems.rb"), "# bundle\n")
	drive := Testdrive{repositoryRoot: root, executor: &rubyReadinessExecutor{}}
	commands, err := drive.rubyBundleReadiness(t.Context(), onboard.ValidationScope{Commands: []onboard.TestCommand{{Directory: "app", Environment: map[string]string{"BUNDLE_PATH": "vendor/ci-bundle", "BUNDLE_APP_CONFIG": "config", "APP_PROFILE": "selected"}}}})
	require.NoError(t, err)
	require.Len(t, commands, 1)
	require.Contains(t, commands[0], "BUNDLE_PATH=vendor/ci-bundle")
	bin := t.TempDir()
	rubyPath, err := exec.CommandContext(t.Context(), "ruby", "-rrbconfig", "-e", "print RbConfig.ruby").Output()
	require.NoError(t, err)
	require.NoError(t, os.Symlink(string(rubyPath), filepath.Join(bin, "ruby")))
	requireWriteFile(t, filepath.Join(bin, "bundle"), "require 'json'\nFile.write(ENV.fetch('CAPTURE'),JSON.generate(cwd:Dir.pwd,path:ENV['BUNDLE_PATH'],config:ENV['BUNDLE_APP_CONFIG'],profile:ENV['APP_PROFILE'],args:ARGV))\n")
	require.NoError(t, os.Chmod(filepath.Join(bin, "bundle"), 0700))
	capture := filepath.Join(t.TempDir(), "capture.json")
	cmd := exec.CommandContext(t.Context(), commands[0][0], commands[0][1:]...)
	cmd.Dir, cmd.Env = root, append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "CAPTURE="+capture)
	data, err := cmd.CombinedOutput()
	require.NoError(t, err, string(data))
	data, err = os.ReadFile(capture)
	require.NoError(t, err)
	var actual struct {
		Cwd, Path, Config, Profile string
		Args                       []string
	}
	require.NoError(t, json.Unmarshal(data, &actual))
	require.Equal(t, canonicalWorkflowRoot(filepath.Join(root, "app")), canonicalWorkflowRoot(actual.Cwd))
	require.Equal(t, "vendor/ci-bundle", actual.Path)
	require.Equal(t, "config", actual.Config)
	require.Equal(t, "selected", actual.Profile)
	require.Equal(t, []string{"install", "--gemfile", "gems.rb"}, actual.Args)
}

func TestRubySafetyRecoveryInstallsSelectedBundle(t *testing.T) {
	if _, err := exec.CommandContext(t.Context(), "ruby", "-rbundler", "-e", "0").CombinedOutput(); err != nil {
		t.Skip("Ruby and Bundler are required")
	}
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "app"), 0700))
	requireWriteFile(t, filepath.Join(root, "app/gems.rb"), "source 'https://rubygems.org'\n")
	drive := Testdrive{repositoryRoot: root, executor: &ext.DefaultCommandExecutor{}}
	scope := onboard.ValidationScope{Commands: []onboard.TestCommand{{Directory: "app", Environment: map[string]string{"BUNDLE_PATH": "vendor/ci-bundle", "BUNDLE_APP_CONFIG": "config"}}}}
	missing, err := drive.rubyBundleReadiness(t.Context(), scope)
	require.NoError(t, err)
	require.Len(t, missing, 1)
	command := exec.CommandContext(t.Context(), missing[0][0], append(missing[0][1:], "--local")...)
	command.Dir = root
	data, err := command.CombinedOutput()
	require.NoError(t, err, string(data))
	require.FileExists(t, filepath.Join(root, "app/gems.locked"))
	missing, err = drive.rubyBundleReadiness(t.Context(), scope)
	require.NoError(t, err)
	require.Empty(t, missing, "executing the supplied recovery must satisfy the same bundle check")
}

type rubySafetyProbeExecutor struct {
	rubyRecordingExecutor
	probes []string
}

func (e *rubySafetyProbeExecutor) CombinedOutput(ctx context.Context, name string, args []string, env map[string]string) ([]byte, error) {
	e.probes = append(e.probes, env["DDTEST_RUBY_PROBE"])
	return e.rubyRecordingExecutor.CombinedOutput(ctx, name, args, env)
}
func TestRubySafetyProbesStayInOwnedCheckout(t *testing.T) {
	root, shared := t.TempDir(), t.TempDir()
	external := filepath.Join(shared, "shared_test.rb")
	requireWriteFile(t, external, "# installed shared test\n")
	session, err := NewSession()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, session.Close()) })
	executor := &rubySafetyProbeExecutor{}
	drive := Testdrive{repositoryRoot: root, framework: framework.NewMinitest(platform.NewRuby("")), executor: executor, startIntake: func(string, intake.Scenario) (localIntake, error) { return &fakeIntake{}, nil }}
	var result validationResult
	require.NoError(t, drive.runRubyFeatures(t.Context(), io.Discard, session, validationRun{Tests: []jestTest{{File: external}}}, &result))
	require.NotEmpty(t, executor.probes)
	for _, probe := range executor.probes {
		require.Equal(t, root, filepath.Dir(probe))
		require.NoFileExists(t, probe)
	}
	entries, err := os.ReadDir(shared)
	require.NoError(t, err)
	require.Len(t, entries, 1)
}
