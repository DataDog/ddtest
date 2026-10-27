package testdrive

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestRubyRevisionsImplicitManifestLookup(t *testing.T) {
	t.Setenv("BUNDLE_GEMFILE", "")
	for _, tc := range []struct {
		name, directory, expected string
		manifests                 []string
	}{
		{"prefers gems.rb", ".", "gems.rb", []string{"Gemfile", "gems.rb"}},
		{"parent Gemfile", "app/jobs", "Gemfile", []string{"Gemfile"}},
		{"nearest manifest", "app/jobs", "app/Gemfile", []string{"gems.rb", "app/Gemfile"}},
		{"parent gems.rb", "app/jobs", "app/gems.rb", []string{"Gemfile", "app/Gemfile", "app/gems.rb"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(root, tc.directory), 0700))
			for _, name := range tc.manifests {
				require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0700))
				requireWriteFile(t, filepath.Join(root, name), "# manifest\n")
			}
			selected, err := rubyConfigurationGemfile(root, tc.directory, nil)
			require.NoError(t, err)
			require.Equal(t, tc.expected, filepath.ToSlash(selected))
		})
	}
}

func TestRubyRevisionsFocusedReadinessUsesSelectedEnvironment(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "bundles"), 0700))
	requireWriteFile(t, filepath.Join(root, "bundles/integration.rb"), "# selected bundle\n")
	requireWriteFile(t, filepath.Join(root, "bundles/integration.rb.lock"), "installed bundle\n")
	executor := &rubyReadinessExecutor{}
	reachedValidation := false
	drive := Testdrive{repositoryRoot: root, language: "ruby", framework: framework.NewMinitest(platform.NewRuby("")), executor: executor,
		command: "bundle", args: []string{"exec", "rake", "test"},
		configurationEnvironment: map[string]string{"BUNDLE_GEMFILE": "bundles/integration.rb"},
		rubyVersion:              func() string { return "ruby 3.3.11" },
		workflowRun: func(context.Context, io.Writer) error {
			reachedValidation = true
			return errors.New("stop after readiness")
		},
	}
	var output bytes.Buffer
	_ = drive.Workflow(t.Context(), &output, WorkflowOptions{Format: "json", ExplicitVersion: true})
	require.True(t, reachedValidation, output.String())
	require.NotContains(t, output.String(), "ruby_dependencies_missing")
	require.Len(t, executor.environments, 1)
	require.Contains(t, executor.environments[0]["BUNDLE_GEMFILE"], "bundles/integration.rb")
}

func TestRubyRevisionsFingerprintsSelectedCustomLockfiles(t *testing.T) {
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
			data, err := exec.CommandContext(t.Context(), "git", "init", "-q", root).CombinedOutput()
			require.NoError(t, err, string(data))
			data, err = exec.CommandContext(t.Context(), "git", "-C", root, "add", ".").CombinedOutput()
			require.NoError(t, err, string(data))
			lock := filepath.Join(root, "bundles/integration.rb.lock")
			if source == "lock override" {
				t.Setenv("BUNDLE_LOCKFILE", "selected.lock")
				lock = filepath.Join(root, "selected.lock")
			}
			requireWriteFile(t, lock, "first resolution\n")
			before, err := InputFingerprint(t.Context(), root)
			require.NoError(t, err)
			requireWriteFile(t, lock, "second resolution\n")
			after, err := InputFingerprint(t.Context(), root)
			require.NoError(t, err)
			require.NotEqual(t, before, after)
		})
	}
}

func TestRubyRevisionsFingerprintsEffectiveBundlerConfig(t *testing.T) {
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
			data, err := exec.CommandContext(t.Context(), "git", "init", "-q", root).CombinedOutput()
			require.NoError(t, err, string(data))
			data, err = exec.CommandContext(t.Context(), "git", "-C", root, "add", ".").CombinedOutput()
			require.NoError(t, err, string(data))
			before, err := InputFingerprint(t.Context(), root)
			require.NoError(t, err)
			requireWriteFile(t, config, "---\nBUNDLE_WITHOUT: development:test\n")
			after, err := InputFingerprint(t.Context(), root)
			require.NoError(t, err)
			require.NotEqual(t, before, after)
			if source == "custom" {
				other := t.TempDir()
				requireWriteFile(t, filepath.Join(other, "config"), "---\nBUNDLE_WITHOUT: development:test\n")
				t.Setenv("BUNDLE_APP_CONFIG", other)
				changed, err := InputFingerprint(t.Context(), root)
				require.NoError(t, err)
				require.NotEqual(t, after, changed, "the effective config selector is an input too")
			}
		})
	}
}

func TestRubyRevisionsObserverLoadsFromQuotedTemporaryPath(t *testing.T) {
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
	session, err := NewSession()
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
