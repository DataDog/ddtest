package testdrive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"github.com/stretchr/testify/require"
)

type rubyReadinessExecutor struct {
	directories  []string
	environments []map[string]string
}

func (e *rubyReadinessExecutor) CombinedOutput(context.Context, string, []string, map[string]string) ([]byte, error) {
	return nil, nil
}

func (e *rubyReadinessExecutor) CombinedOutputInDirectory(_ context.Context, root, _ string, _ []string, env map[string]string) ([]byte, error) {
	e.directories = append(e.directories, root)
	e.environments = append(e.environments, maps.Clone(env))
	return []byte("dependencies satisfied"), nil
}

func gitInit(t *testing.T, root string) {
	t.Helper()
	data, err := exec.CommandContext(t.Context(), "git", "init", "-q", root).CombinedOutput()
	require.NoError(t, err, string(data))
	data, err = exec.CommandContext(t.Context(), "git", "-C", root, "add", ".").CombinedOutput()
	require.NoError(t, err, string(data))
}

func TestRubyReadinessInventoriesMissingBundlesWithoutChangingLocks(t *testing.T) {
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "Gemfile"), "source 'https://rubygems.org'\n")
	requireWriteFile(t, filepath.Join(root, "Gemfile.lock"), "installed lock\n")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gemfiles"), 0700))
	requireWriteFile(t, filepath.Join(root, "gemfiles", "rails.gemfile"), "source 'https://rubygems.org'\n")
	executor := &rubyReadinessExecutor{}
	drive := &Testdrive{repositoryRoot: root, executor: executor}
	scope := onboard.ValidationScope{Commands: []onboard.TestCommand{{Directory: "."}, {Directory: ".", Environment: map[string]string{"BUNDLE_GEMFILE": "gemfiles/rails.gemfile"}}}}
	commands, err := drive.rubyBundleReadiness(t.Context(), scope)
	require.NoError(t, err)
	require.Equal(t, [][]string{{"bundle", "install", "--gemfile", "gemfiles/rails.gemfile"}}, commands)
	require.Len(t, executor.directories, 1)
	require.NotEqual(t, root, executor.directories[0])
	require.NoDirExists(t, executor.directories[0])
	require.Contains(t, executor.environments[0]["BUNDLE_GEMFILE"], "checkout")
	require.NoFileExists(t, filepath.Join(root, "gemfiles/rails.gemfile.lock"))
	data, err := os.ReadFile(filepath.Join(root, "Gemfile.lock"))
	require.NoError(t, err)
	require.Equal(t, "installed lock\n", string(data))
}

func TestRubyReadinessUsesWorkingDirectoryAndGemsLocked(t *testing.T) {
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

func TestRubyReadinessHonorsBundleLockfile(t *testing.T) {
	for _, lock := range []string{"locks/ci.lock", "ABSOLUTE"} {
		root := t.TempDir()
		requireWriteFile(t, filepath.Join(root, "Gemfile"), "source 'https://rubygems.org'\n")
		require.NoError(t, os.MkdirAll(filepath.Join(root, "locks"), 0700))
		requireWriteFile(t, filepath.Join(root, "locks/ci.lock"), "installed bundle\n")
		if lock == "ABSOLUTE" {
			lock = filepath.Join(root, "locks/ci.lock")
		}
		executor := &rubyReadinessExecutor{}
		drive := Testdrive{repositoryRoot: root, executor: executor}
		scope := onboard.ValidationScope{Commands: []onboard.TestCommand{{Directory: ".", Command: "bundle exec rake test", Environment: map[string]string{"BUNDLE_LOCKFILE": lock}, Locations: []string{".github/workflows/test.yml / tests"}}}}
		missing, err := drive.rubyBundleReadiness(t.Context(), scope)
		require.NoError(t, err)
		require.Empty(t, missing, "the configured lockfile exists; the default Gemfile.lock does not")
		require.Len(t, executor.environments, 1)
		selected := executor.environments[0]["BUNDLE_LOCKFILE"]
		require.Equal(t, filepath.Join(executor.directories[0], "locks/ci.lock"), selected, "the check reads the copy's lockfile")
	}
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "Gemfile"), "source 'https://rubygems.org'\n")
	drive := Testdrive{repositoryRoot: root, executor: &rubyReadinessExecutor{}}
	_, err := drive.rubyBundleReadiness(t.Context(), onboard.ValidationScope{Commands: []onboard.TestCommand{{Directory: ".", Environment: map[string]string{"BUNDLE_LOCKFILE": "../elsewhere.lock"}, Locations: []string{"ci"}}}})
	require.ErrorContains(t, err, "BUNDLE_LOCKFILE is outside the repository")
}

func TestRubyReadinessAndSetupUseGitHubActionsDefaults(t *testing.T) {
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "Gemfile"), "source 'https://rubygems.org'\n")
	requireWriteFile(t, filepath.Join(root, "Gemfile.lock"), "installed bundle\n")
	executor := &rubyReadinessExecutor{}
	drive := Testdrive{repositoryRoot: root, executor: executor}
	ci := onboard.TestCommand{Directory: ".", Command: "bundle exec rake test", Locations: []string{".github/workflows/test.yml / tests"}}
	_, err := drive.rubyBundleReadiness(t.Context(), onboard.ValidationScope{Commands: []onboard.TestCommand{ci}})
	require.NoError(t, err)
	require.Len(t, executor.environments, 1)
	require.Equal(t, "true", executor.environments[0]["CI"])
	require.Equal(t, "true", executor.environments[0]["GITHUB_ACTIONS"])
	command, err := rubyBundleSetupCommand(ci, "Gemfile")
	require.NoError(t, err)
	require.Contains(t, command, "CI=true")
	require.Contains(t, command, "GITHUB_ACTIONS=true")
	ci.Environment = map[string]string{"CI": "spec"}
	command, err = rubyBundleSetupCommand(ci, "Gemfile")
	require.NoError(t, err)
	require.Contains(t, command, "CI=spec", "an explicit CI value wins")
	command, err = rubyBundleSetupCommand(onboard.TestCommand{Directory: "."}, "Gemfile")
	require.NoError(t, err)
	require.Equal(t, []string{"bundle", "install", "--gemfile", "Gemfile"}, command, "a local command keeps the shell environment")
}

func TestRubyReadinessIgnoresTheShellGemfileForCIConsumers(t *testing.T) {
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "Gemfile"), "# CI bundle\n")
	requireWriteFile(t, filepath.Join(root, "Gemfile.lock"), "CI lock\n")
	ambient := filepath.Join(t.TempDir(), "Gemfile")
	requireWriteFile(t, ambient, "# Appraisal shell bundle\n")
	t.Setenv("BUNDLE_GEMFILE", ambient)
	readiness := &rubyReadinessExecutor{}
	drive := Testdrive{repositoryRoot: root, executor: readiness}
	scope := onboard.ValidationScope{Commands: []onboard.TestCommand{{Directory: ".", Command: "bundle exec rake test", Locations: []string{".github/workflows/test.yml / tests"}}}}
	missing, err := drive.rubyBundleReadiness(t.Context(), scope)
	require.NoError(t, err)
	require.Empty(t, missing)
	require.Len(t, readiness.environments, 1)
	require.NotEqual(t, ambient, readiness.environments[0]["BUNDLE_GEMFILE"])
	require.Equal(t, "Gemfile", filepath.Base(readiness.environments[0]["BUNDLE_GEMFILE"]))
}

func TestRubySetupCommandPreservesEnvironmentAndDirectory(t *testing.T) {
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
	resolved, err := filepath.EvalSymlinks(filepath.Join(root, "app"))
	require.NoError(t, err)
	cwd, err := filepath.EvalSymlinks(actual.Cwd)
	require.NoError(t, err)
	require.Equal(t, resolved, cwd)
	require.Equal(t, "vendor/ci-bundle", actual.Path)
	require.Equal(t, "config", actual.Config)
	require.Equal(t, "selected", actual.Profile)
	require.Equal(t, []string{"install", "--gemfile", "gems.rb"}, actual.Args)
}

func TestRubySetupCommandInstallsSelectedBundle(t *testing.T) {
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
	require.Empty(t, missing, "executing the supplied setup command must satisfy the same bundle check")
}

func TestRubyWorkflowAgentModesReportMissingBundles(t *testing.T) {
	for _, runner := range []framework.Framework{framework.NewRSpec(platform.NewRuby("")), framework.NewMinitest(platform.NewRuby(""))} {
		for _, format := range []string{"agent", "json"} {
			t.Run(runner.Name()+"/"+format, func(t *testing.T) {
				root := t.TempDir()
				t.Chdir(root)
				require.NoError(t, writeValidation(validationPath(root), []byte(`{"success":false}`)))
				executor := &fakeTestdriveExecutor{}
				drive := &Testdrive{repositoryRoot: root, framework: runner, language: "ruby", executor: executor, runtimeVersion: func() string { return "ruby 3.3.11 arm64" }}
				var output bytes.Buffer
				require.ErrorContains(t, drive.Workflow(t.Context(), &output, WorkflowOptions{Format: format}), "project_dependencies_missing")
				var result onboard.AgentResult
				require.NoError(t, json.Unmarshal(output.Bytes(), &result))
				require.Equal(t, "needs_input", result.Status)
				require.Equal(t, "run_command", result.NextAction)
				require.Equal(t, []string{"bundle", "install", "--gemfile", "Gemfile"}, result.NextCommand)
				require.Equal(t, [][]string{result.NextCommand}, result.SetupCommands)
				require.Contains(t, result.Needs, "setup_commands")
				require.Contains(t, result.ResumeCommand, "--resume")
				require.Empty(t, result.FinalResponse, "a dependency handoff continues the workflow")
				require.Empty(t, executor.command)
				current, err := os.ReadFile(validationPath(root))
				require.NoError(t, err)
				require.NotContains(t, string(current), "COMPLETE LOCALLY")
			})
		}
	}
}

func TestRubyWorkflowFocusedReadinessUsesSelectedEnvironment(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "bundles"), 0700))
	requireWriteFile(t, filepath.Join(root, "bundles/integration.rb"), "# selected bundle\n")
	requireWriteFile(t, filepath.Join(root, "bundles/integration.rb.lock"), "installed bundle\n")
	executor := &rubyReadinessExecutor{}
	reachedValidation := false
	drive := Testdrive{repositoryRoot: root, language: "ruby", framework: framework.NewMinitest(platform.NewRuby("")), executor: executor,
		command: "bundle", args: []string{"exec", "rake", "test"},
		configurationEnvironment: map[string]string{"BUNDLE_GEMFILE": "bundles/integration.rb"},
		runtimeVersion:           func() string { return "ruby 3.3.11" },
		workflowRun: func(context.Context, io.Writer) error {
			reachedValidation = true
			return errors.New("stop after readiness")
		},
	}
	var output bytes.Buffer
	_ = drive.Workflow(t.Context(), &output, WorkflowOptions{Format: "json", ExplicitVersion: true})
	require.True(t, reachedValidation, output.String())
	require.NotContains(t, output.String(), "project_dependencies_missing")
	require.Len(t, executor.environments, 1)
	require.Contains(t, executor.environments[0]["BUNDLE_GEMFILE"], "bundles/integration.rb")
}

func TestRubyWorkflowPlanBindsTheRubyRuntime(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	drive := Testdrive{repositoryRoot: root, language: "ruby", framework: framework.NewRSpec(platform.NewRuby("")), executor: &rubyReadinessExecutor{}, runtimeVersion: func() string { return "" }}
	var output bytes.Buffer
	require.ErrorContains(t, drive.Workflow(t.Context(), &output, WorkflowOptions{Format: "json"}), "runtime_unavailable")
	var result onboard.AgentResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	require.Equal(t, "blocked_environment", result.Status)
	require.Equal(t, "runtime_unavailable", result.Code)
	language := rubyLanguage{}
	require.Equal(t, "bundler", language.packageManager(root))
	require.Equal(t, "1.42.0", language.ciPinnedTracer(onboard.RuntimeFinding{Status: "not checked", TracerRequested: "1.42.0"}))
	require.Empty(t, language.ciPinnedTracer(onboard.RuntimeFinding{Status: "excluded", TracerRequested: "1.42.0"}), "an excluded row installs no tracer")
	require.Contains(t, workflowInitialization("minitest"), "datadog-ci")
}

func TestRubyReportsIgnoreTheHostNode(t *testing.T) {
	current := currentRubyVersion(t.TempDir())
	if current == "" {
		t.Skip("Ruby is required")
	}
	evidence := WorkflowEvidence{Node: "v0.0.0-not-this-host", Runtime: current}
	require.True(t, rubyLanguage{}.reportRuntimeCurrent(t.TempDir(), evidence), "a Node change cannot make a Ruby report stale")
	evidence.Runtime = "ruby 0.0.0 elsewhere"
	require.False(t, rubyLanguage{}.reportRuntimeCurrent(t.TempDir(), evidence))
	require.False(t, rubyLanguage{}.reportRuntimeCurrent(t.TempDir(), WorkflowEvidence{}), "a report without its Ruby is never current")
	require.False(t, javascriptLanguage{}.reportRuntimeCurrent(t.TempDir(), WorkflowEvidence{Node: "v0.0.0-not-this-host"}))
}

func TestRubyFingerprintsIgnoredLockfiles(t *testing.T) {
	for _, directory := range []string{".", "app"} {
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, directory), 0700))
		requireWriteFile(t, filepath.Join(root, directory, "gems.rb"), "source 'https://rubygems.org'\n")
		requireWriteFile(t, filepath.Join(root, ".gitignore"), "gems.locked\n")
		gitInit(t, root)
		lock := filepath.Join(root, directory, "gems.locked")
		requireWriteFile(t, lock, "first resolution\n")
		before, err := workflowFingerprint(t.Context(), root, "minitest")
		require.NoError(t, err)
		requireWriteFile(t, lock, "second resolution\n")
		after, err := workflowFingerprint(t.Context(), root, "minitest")
		require.NoError(t, err)
		require.NotEqual(t, before, after, directory)
	}
}

func TestRubyFingerprintsSelectedCustomLockfiles(t *testing.T) {
	for _, source := range []string{"environment", "CI", "lock override"} {
		t.Run(source, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(root, "bundles"), 0700))
			requireWriteFile(t, filepath.Join(root, "bundles/integration.rb"), "# custom manifest\n")
			requireWriteFile(t, filepath.Join(root, ".gitignore"), "*.lock\n")
			if source != "CI" {
				t.Setenv("BUNDLE_GEMFILE", "bundles/integration.rb")
			} else {
				require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
				requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  test:\n    steps:\n      - run: bundle exec rake test\n        env: {BUNDLE_GEMFILE: bundles/integration.rb}\n")
			}
			gitInit(t, root)
			lock := filepath.Join(root, "bundles/integration.rb.lock")
			if source == "lock override" {
				t.Setenv("BUNDLE_LOCKFILE", "selected.lock")
				lock = filepath.Join(root, "selected.lock")
			}
			requireWriteFile(t, lock, "first resolution\n")
			before, err := workflowFingerprint(t.Context(), root, "minitest")
			require.NoError(t, err)
			requireWriteFile(t, lock, "second resolution\n")
			after, err := workflowFingerprint(t.Context(), root, "minitest")
			require.NoError(t, err)
			require.NotEqual(t, before, after)
		})
	}
}

func TestRubyFingerprintsEffectiveBundlerConfig(t *testing.T) {
	for _, source := range []string{"local", "custom", "global", "global-legacy", "CI"} {
		t.Run(source, func(t *testing.T) {
			root := t.TempDir()
			requireWriteFile(t, filepath.Join(root, "Gemfile"), "# bundle\n")
			requireWriteFile(t, filepath.Join(root, ".gitignore"), ".bundle/\n")
			config := filepath.Join(root, ".bundle/config")
			switch source {
			case "custom":
				config = filepath.Join(t.TempDir(), "config")
				t.Setenv("BUNDLE_APP_CONFIG", filepath.Dir(config))
			case "global":
				config = filepath.Join(t.TempDir(), "config")
				t.Setenv("BUNDLE_USER_CONFIG", config)
			case "global-legacy":
				config = filepath.Join(t.TempDir(), "config")
				t.Setenv("BUNDLE_CONFIG", config)
			case "CI":
				config = filepath.Join(root, ".bundle/custom/config")
				require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
				requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  test:\n    steps:\n      - run: bundle exec rake test\n        env: {BUNDLE_APP_CONFIG: .bundle/custom}\n")
			}
			require.NoError(t, os.MkdirAll(filepath.Dir(config), 0700))
			requireWriteFile(t, config, "---\nBUNDLE_WITHOUT: development\n")
			gitInit(t, root)
			before, err := workflowFingerprint(t.Context(), root, "minitest")
			require.NoError(t, err)
			requireWriteFile(t, config, "---\nBUNDLE_WITHOUT: development:test\n")
			after, err := workflowFingerprint(t.Context(), root, "minitest")
			require.NoError(t, err)
			require.NotEqual(t, before, after)
			if source == "custom" {
				other := t.TempDir()
				requireWriteFile(t, filepath.Join(other, "config"), "---\nBUNDLE_WITHOUT: development:test\n")
				t.Setenv("BUNDLE_APP_CONFIG", other)
				changed, err := workflowFingerprint(t.Context(), root, "minitest")
				require.NoError(t, err)
				require.NotEqual(t, after, changed, "the effective config selector is an input too")
			}
		})
	}
}

func TestJavaScriptFingerprintIgnoresRubyBundles(t *testing.T) {
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"jest"}}`)
	requireWriteFile(t, filepath.Join(root, "Gemfile"), "gem 'fastlane'\n")
	requireWriteFile(t, filepath.Join(root, ".gitignore"), "Gemfile.lock\n")
	gitInit(t, root)
	requireWriteFile(t, filepath.Join(root, "Gemfile.lock"), "first resolution\n")
	shared, err := InputFingerprint(t.Context(), root)
	require.NoError(t, err)
	before, err := workflowFingerprint(t.Context(), root, "jest")
	require.NoError(t, err)
	require.Equal(t, shared, before, "JavaScript receipts keep the shared snapshot")
	requireWriteFile(t, filepath.Join(root, "Gemfile.lock"), "second resolution\n")
	after, err := workflowFingerprint(t.Context(), root, "jest")
	require.NoError(t, err)
	require.Equal(t, before, after, "an ignored Ruby lockfile is not a JavaScript input")
}

func TestRubyGemPackagingUsesCopyAndChecksReportMembership(t *testing.T) {
	if _, err := exec.LookPath("ruby"); err != nil {
		t.Skip("Ruby is required")
	}
	for _, glob := range []string{"lib/**/*", "{lib,.testoptimization}/**/*", "git-index"} {
		t.Run(glob, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(root, "lib"), 0700))
			requireWriteFile(t, filepath.Join(root, "lib/fixture.rb"), "module Fixture;end\n")
			data, err := exec.CommandContext(t.Context(), "git", "init", "--quiet", root).CombinedOutput()
			require.NoError(t, err, string(data))
			filesExpression := "Dir['" + glob + "']"
			if glob == "git-index" {
				filesExpression = "`git ls-files -z`.split(\"\\x00\")"
			}
			requireWriteFile(t, filepath.Join(root, "fixture.gemspec"), "Gem::Specification.new do |s|\n s.name='fixture';s.version='1.0';s.files="+filesExpression+";File.write('gemspec-loaded.txt',Dir.pwd)\nend\n")
			// Real clones have an index; a brand-new git init alone misses this case.
			data, err = exec.CommandContext(t.Context(), "git", "-C", root, "add", "lib/fixture.rb", "fixture.gemspec").CombinedOutput()
			require.NoError(t, err, string(data))
			indexPath := filepath.Join(root, ".git/index")
			if glob == "git-index" {
				// Linked worktrees copy a .git file pointing outside the checkout.
				metadata := filepath.Join(t.TempDir(), "metadata")
				require.NoError(t, os.Rename(filepath.Join(root, ".git"), metadata))
				requireWriteFile(t, filepath.Join(root, ".git"), "gitdir: "+metadata+"\n")
				indexPath = filepath.Join(metadata, "index")
			}
			indexBefore, err := os.ReadFile(indexPath)
			require.NoError(t, err)
			drive := &Testdrive{repositoryRoot: root, language: "ruby", executor: &ext.DefaultCommandExecutor{}}
			result := drive.rubyPackageExclusions(t.Context())
			if glob != "{lib,.testoptimization}/**/*" {
				require.Equal(t, "passed", result.Status, result.Reason)
			} else {
				require.Equal(t, "failed", result.Status, result.Reason)
				require.Contains(t, result.Reason, "includes local validation reports")
			}
			indexAfter, err := os.ReadFile(indexPath)
			require.NoError(t, err)
			require.Equal(t, indexBefore, indexAfter, "the original Git index must be unchanged")
			require.NoFileExists(t, filepath.Join(root, "gemspec-loaded.txt"))
			require.NoDirExists(t, filepath.Join(root, ".testoptimization"))
		})
	}
}

func TestRubyPackagingInspectsNestedGemspecsInOwnedCopies(t *testing.T) {
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
			gitInit(t, root)
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

func TestRubyChecksShareOneOwnedCopyPerValidation(t *testing.T) {
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "Gemfile"), "# bundle\n")
	drive := Testdrive{repositoryRoot: root, reuseCheckCopy: true}
	var copies []string
	for range 2 {
		require.NoError(t, drive.inRubyWorkflowCopy(t.Context(), func(copyRoot string) error {
			copies = append(copies, copyRoot)
			return nil
		}))
	}
	require.Equal(t, copies[0], copies[1])
	require.DirExists(t, copies[0])
	drive.releaseCheckCopy()
	require.NoDirExists(t, copies[0])
	require.NoDirExists(t, filepath.Dir(copies[0]))
	single := Testdrive{repositoryRoot: root}
	require.NoError(t, single.inRubyWorkflowCopy(t.Context(), func(copyRoot string) error {
		copies = append(copies, copyRoot)
		return nil
	}))
	require.NoDirExists(t, copies[2], "outside a coordinated validation, each check removes its own copy")
	require.True(t, strings.HasSuffix(copies[2], "checkout"))
}

// installedBundleExecutor reports every bundle as installed and runs other
// commands, such as the gemspec inspection, for real.
type installedBundleExecutor struct{ ext.DefaultCommandExecutor }

func (e *installedBundleExecutor) CombinedOutputInDirectory(ctx context.Context, directory, name string, args []string, env map[string]string) ([]byte, error) {
	if name == "bundle" {
		return []byte("The Gemfile's dependencies are satisfied"), nil
	}
	return e.DefaultCommandExecutor.CombinedOutputInDirectory(ctx, directory, name, args, env)
}

// The coordinated path shares one check copy between dependency readiness and
// the packaging check; the validation run in between must not recover it as
// abandoned storage.
func TestRubyCoordinatedWorkflowKeepsTheSharedCheckCopyForGemspecs(t *testing.T) {
	if _, err := exec.LookPath("ruby"); err != nil {
		t.Skip("Ruby is required")
	}
	t.Setenv("TMPDIR", t.TempDir())
	root := t.TempDir()
	t.Chdir(root)
	requireWriteFile(t, filepath.Join(root, "Gemfile"), "source 'https://rubygems.org'\n")
	requireWriteFile(t, filepath.Join(root, "Gemfile.lock"), "installed bundle\n")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "lib"), 0700))
	requireWriteFile(t, filepath.Join(root, "lib/fixture.rb"), "module Fixture;end\n")
	requireWriteFile(t, filepath.Join(root, "fixture.gemspec"), "Gem::Specification.new do |s|\n s.name='fixture';s.version='1.0';s.files=Dir['lib/**/*']\nend\n")
	gitInit(t, root)
	drive := &Testdrive{repositoryRoot: root, language: "ruby", framework: framework.NewRSpec(platform.NewRuby("")), executor: &installedBundleExecutor{},
		runtimeVersion: func() string { return "ruby 3.3.11 arm64" },
		// Run's own startup still executes; it then stops before any test.
		discoveryError: errors.New("stop after startup"),
	}
	drive.workflowRun = func(ctx context.Context, output io.Writer) error { return drive.Run(ctx, output) }
	var output bytes.Buffer
	_ = drive.Workflow(t.Context(), &output, WorkflowOptions{Format: "json", ExplicitVersion: true})
	var result onboard.AgentResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result), output.String())
	var packaging *onboard.WorkflowCheck
	for i := range result.Checks {
		if result.Checks[i].Name == "package_report_exclusions" {
			packaging = &result.Checks[i]
		}
	}
	require.NotNil(t, packaging, output.String())
	require.Equal(t, "passed", packaging.Status, packaging.Reason)
	matches, err := filepath.Glob(filepath.Join(os.TempDir(), "ddtest-ruby-check-*"))
	require.NoError(t, err)
	require.Empty(t, matches, "the shared copy is removed when the validation ends")
}

func TestRubyCheckOnlyPreflightHonorsBundleLockfile(t *testing.T) {
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "Gemfile"), "source 'https://rubygems.org'\n")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "locks"), 0700))
	requireWriteFile(t, filepath.Join(root, "locks/ci.lock"), "installed bundle\n")
	executor := &fakeTestdriveExecutor{err: errors.New("bundle inspected")}
	drive := Testdrive{repositoryRoot: root, checkOnly: true, framework: framework.NewRSpec(platform.NewRuby("")), executor: executor,
		command: "bundle", args: []string{"exec", "rspec"}, configurationEnvironment: map[string]string{"BUNDLE_LOCKFILE": "locks/ci.lock"}}
	var result validationResult
	err := drive.checkRubyPreflight(t.Context(), io.Discard, &result)
	require.ErrorContains(t, err, "bundle inspected", "the configured lockfile exists, so the bundle is inspected")
	require.NotContains(t, result.Preflight.Verdict.Reason, "lockfile is unavailable")
}

func gitFixtureCommand(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.CommandContext(t.Context(), "git", append([]string{"-C", root, "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false", "-c", "user.name=fixture", "-c", "user.email=fixture@example.com"}, args...)...)
	data, err := command.CombinedOutput()
	require.NoError(t, err, string(data))
	return string(data)
}

// Release gemspecs read history and tags; they also print diagnostics.
func TestRubyPackagingLoadsGemspecsThatReadGitHistory(t *testing.T) {
	if _, err := exec.LookPath("ruby"); err != nil {
		t.Skip("Ruby is required")
	}
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "lib"), 0700))
	requireWriteFile(t, filepath.Join(root, "lib/fixture.rb"), "module Fixture;end\n")
	requireWriteFile(t, filepath.Join(root, "fixture.gemspec"), `version = `+"`git describe --tags --abbrev=0`"+`.strip
raise "no tag: #{version.inspect}" unless version == "v1.0"
raise "no history" if `+"`git log -1 --format=%H`"+`.strip.empty?
files = `+"`git ls-tree -r --name-only HEAD`"+`.split("\n")
puts "gemspec diagnostic on stdout"
warn "gemspec diagnostic on stderr"
Gem::Specification.new do |s|
  s.name = 'fixture'; s.version = version.delete('v'); s.files = files
end
`)
	gitFixtureCommand(t, root, "init", "-q")
	gitFixtureCommand(t, root, "add", ".")
	gitFixtureCommand(t, root, "commit", "-q", "-m", "fixture")
	gitFixtureCommand(t, root, "tag", "v1.0")
	refsBefore := gitFixtureCommand(t, root, "for-each-ref")
	objectsBefore := gitFixtureCommand(t, root, "count-objects", "-v")
	drive := &Testdrive{repositoryRoot: root, language: "ruby", executor: &ext.DefaultCommandExecutor{}}
	check := drive.rubyPackageExclusions(t.Context())
	require.Equal(t, "passed", check.Status, check.Reason)
	require.Equal(t, refsBefore, gitFixtureCommand(t, root, "for-each-ref"), "the customer repository gains no refs")
	require.Equal(t, objectsBefore, gitFixtureCommand(t, root, "count-objects", "-v"), "the customer repository gains no objects")
	require.Empty(t, strings.TrimSpace(gitFixtureCommand(t, root, "remote")), "no remote points back from the customer repository")
}

func TestRubyPackagingIgnoresGemspecDiagnostics(t *testing.T) {
	if _, err := exec.LookPath("ruby"); err != nil {
		t.Skip("Ruby is required")
	}
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "lib"), 0700))
	requireWriteFile(t, filepath.Join(root, "lib/fixture.rb"), "module Fixture;end\n")
	requireWriteFile(t, filepath.Join(root, "fixture.gemspec"), "puts 'loading fixture'\nwarn 'deprecated option'\nGem::Specification.new do |s|\n s.name='fixture';s.version='1.0';s.files=Dir['lib/**/*']\nend\n")
	gitInit(t, root)
	drive := &Testdrive{repositoryRoot: root, language: "ruby", executor: &ext.DefaultCommandExecutor{}}
	check := drive.rubyPackageExclusions(t.Context())
	require.Equal(t, "passed", check.Status, check.Reason)
}

func TestRubyCoordinatedValidationEnforcesTheSelectedSite(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
	requireWriteFile(t, filepath.Join(root, "Gemfile"), "source 'https://rubygems.org'\n")
	requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  tests:\n    steps:\n      - uses: ruby/setup-ruby@v1\n        with: {ruby-version: '3.3'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: ruby, api_key: '${{ secrets.DD_API_KEY }}', ruby-tracer-version: '1.42.0'}\n      - run: bundle exec rspec\n")
	drive := &Testdrive{repositoryRoot: root, framework: framework.NewRSpec(platform.NewRuby("")), language: "ruby", selectedSite: "datadoghq.eu"}
	ci := rubyLanguage{}.configurationsCIRuntime(drive, onboard.ValidationScope{Commands: []onboard.TestCommand{{Command: "bundle exec rspec"}}})
	require.Len(t, ci.Jobs, 1)
	require.Equal(t, onboard.CodeIncompleteRubySetup, ci.Jobs[0].Code, "the action reports to the default site, not the selected one")
	drive.selectedSite = ""
	ci = rubyLanguage{}.configurationsCIRuntime(drive, onboard.ValidationScope{Commands: []onboard.TestCommand{{Command: "bundle exec rspec"}}})
	require.Equal(t, "not checked", ci.Jobs[0].Status)

	// The coordinator defaults the site to DD_SITE, as onboard does.
	t.Setenv("DD_SITE", "datadoghq.eu")
	t.Chdir(root)
	drive = &Testdrive{repositoryRoot: root, framework: framework.NewRSpec(platform.NewRuby("")), language: "ruby", executor: &rubyReadinessExecutor{}, runtimeVersion: func() string { return "" }}
	_ = drive.Workflow(t.Context(), io.Discard, WorkflowOptions{Format: "json"})
	require.Equal(t, "datadoghq.eu", drive.selectedSite)
}

// Gems installed inside the repository are not packaging targets, wherever
// Bundler put them.
func TestRubyPackagingSkipsInstalledGems(t *testing.T) {
	if _, err := exec.LookPath("ruby"); err != nil {
		t.Skip("Ruby is required")
	}
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "lib"), 0700))
	requireWriteFile(t, filepath.Join(root, "lib/fixture.rb"), "module Fixture;end\n")
	requireWriteFile(t, filepath.Join(root, "fixture.gemspec"), "Gem::Specification.new do |s|\n s.name='fixture';s.version='1.0';s.files=Dir['lib/**/*']\nend\n")
	installed := "Gem::Specification.new do |s|\n s.name='rake';s.version='13.4.2';s.files=[]\nend\n"
	for _, path := range []string{
		".gems/ruby/3.4.0/specifications/rake-13.4.2.gemspec",
		".gems/ruby/3.4.0/gems/rake-13.4.2/rake.gemspec",
		"bundle/ruby/3.4.0/specifications/rake-13.4.2.gemspec",
		"app/vendor/bundle/ruby/3.4.0/specifications/rake-13.4.2.gemspec",
		"third_party/vendor/rake.gemspec",
		"ci-gems/rake.gemspec",
	} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, filepath.Dir(path)), 0700))
		requireWriteFile(t, filepath.Join(root, path), installed)
	}
	require.NoError(t, os.MkdirAll(filepath.Join(root, "bundle/ruby/3.4.0/gems"), 0700))
	gitInit(t, root)
	scope := onboard.ValidationScope{Commands: []onboard.TestCommand{{Directory: ".", Environment: map[string]string{"BUNDLE_PATH": "ci-gems"}}}}
	drive := &Testdrive{repositoryRoot: root, language: "ruby", executor: &ext.DefaultCommandExecutor{}, workflowScope: &scope}
	check := drive.rubyPackageExclusions(t.Context())
	require.Equal(t, "passed", check.Status, check.Reason)
}
