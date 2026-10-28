package testdrive

import (
	"context"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/stretchr/testify/require"
)

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

func TestRubyPreservesEffectiveBundlerConfiguration(t *testing.T) {
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

func TestRubyRefreshesTracerForEachBundle(t *testing.T) {
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
	drive.lang().refreshProjectTracer(&drive, t.Context())
	require.Empty(t, drive.projectTracer)
	drive.configurationEnvironment["BUNDLE_GEMFILE"] = filepath.Join(root, "traced/Gemfile")
	drive.lang().refreshProjectTracer(&drive, t.Context())
	require.Contains(t, drive.projectTracer, "1.42.0")
}

func TestRubyTracerDiscoveryHonorsTheSubprocessTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake bundle")
	}
	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "bundle"), []byte("#!/bin/sh\nexec sleep 30\n"), 0700))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	drive := Testdrive{platform: platform.NewRuby(""), language: "ruby", commandTimeout: 200 * time.Millisecond, configurationEnvironment: map[string]string{}}
	started := time.Now()
	drive.lang().refreshProjectTracer(&drive, t.Context())
	require.Less(t, time.Since(started), 10*time.Second)
	require.Empty(t, drive.projectTracer)
}

func TestRubyFocusedCheckOnlyLoadsOnlyOwnedCopy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake bundle")
	}
	root := t.TempDir()
	bin := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "Gemfile"), "# Gemfile whose evaluation writes a generated file\n")
	requireWriteFile(t, filepath.Join(root, "Gemfile.lock"), "installed bundle\n")
	script := `#!/bin/sh
printf loaded > gemfile-generated
echo "bundle diagnostics"
printf '%s' '{"version":"5.27.0","tracer":"1.42.0","runtime":"3.3.11","supported":true,"jobs":[]}' > "$DDTEST_RUBY_PREFLIGHT_RESULT"
`
	require.NoError(t, os.WriteFile(filepath.Join(bin, "bundle"), []byte(script), 0700))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Chdir(root)
	drive := Testdrive{repositoryRoot: root, language: "ruby", framework: framework.NewMinitest(platform.NewRuby("")), executor: &ext.DefaultCommandExecutor{}, command: "bundle", args: []string{"exec", "rake", "test"}, checkOnly: true}
	require.NoError(t, drive.runConfigurationChecks(t.Context(), io.Discard))
	require.NoFileExists(t, filepath.Join(root, "gemfile-generated"))
}

func TestRubyImplicitManifestLookup(t *testing.T) {
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
			selected, err := rubyConfigurationGemfile(root, tc.directory, nil, true)
			require.NoError(t, err)
			require.Equal(t, tc.expected, filepath.ToSlash(selected))
		})
	}
}

func TestRubyFallbackUnfreezesOnlyTheCopiedBundle(t *testing.T) {
	if _, err := exec.LookPath("ruby"); err != nil {
		t.Skip("Ruby is required")
	}
	root, session := t.TempDir(), t.TempDir()
	t.Setenv("BUNDLE_APP_CONFIG", "")
	require.NoError(t, os.Unsetenv("BUNDLE_APP_CONFIG"))
	requireWriteFile(t, filepath.Join(root, "Gemfile"), "source 'https://rubygems.org'\n")
	settings := "---\nBUNDLE_FROZEN: \"true\"\nBUNDLE_DEPLOYMENT: \"true\"\n"
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".bundle"), 0700))
	requireWriteFile(t, filepath.Join(root, ".bundle/config"), settings)
	executor := &rubyBundlerConfigExecutor{root: root}
	drive := Testdrive{executor: executor, platform: platform.NewRuby("")}
	environment := map[string]string{"BUNDLE_GEMFILE": filepath.Join(root, "Gemfile"), "BUNDLE_FROZEN": "true"}
	installed, err := drive.installRubyTracer(t.Context(), platform.TracerOptions{Directory: session, Version: "1.42.0", Environment: environment})
	require.NoError(t, err)
	require.Equal(t, "false", executor.installEnv["BUNDLE_FROZEN"], "the CI environment must not refreeze the copy")
	require.Equal(t, "false", executor.installEnv["BUNDLE_DEPLOYMENT"])
	verify := exec.CommandContext(t.Context(), "ruby", "-rbundler", "-e", `print [Bundler.settings[:frozen], Bundler.settings[:deployment]].inspect`)
	verify.Dir, verify.Env = root, append(os.Environ(), "BUNDLE_GEMFILE="+environment["BUNDLE_GEMFILE"], "BUNDLE_APP_CONFIG="+installed.Env["BUNDLE_APP_CONFIG"])
	selected, err := verify.CombinedOutput()
	require.NoError(t, err, string(selected))
	require.Equal(t, "[false, false]", string(selected))
	original, err := os.ReadFile(filepath.Join(root, ".bundle/config"))
	require.NoError(t, err)
	require.Equal(t, settings, string(original), "the customer's settings stay frozen")
}

func TestRubyCIConfigurationsIgnoreTheShellGemfile(t *testing.T) {
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "Gemfile"), "source 'https://rubygems.org'\n")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gemfiles"), 0700))
	requireWriteFile(t, filepath.Join(root, "gemfiles/local.gemfile"), "source 'https://rubygems.org'\n")
	t.Setenv("BUNDLE_GEMFILE", filepath.Join(root, "gemfiles/local.gemfile"))
	for _, tc := range []struct {
		locations []string
		want      string
	}{
		{[]string{".github/workflows/test.yml / tests / step 1 (bundle exec rspec)"}, "Gemfile"},
		{nil, "gemfiles/local.gemfile"},
	} {
		drive := Testdrive{framework: framework.NewRSpec(nil), configurationEnvironment: map[string]string{}}
		item := configurationResult{TestCommand: onboard.TestCommand{Directory: ".", Locations: tc.locations}}
		workspace := t.TempDir()
		require.NoError(t, rubyLanguage{}.prepareConfiguration(&drive, &item, root, workspace))
		require.Equal(t, filepath.Join(workspace, tc.want), drive.configurationEnvironment["BUNDLE_GEMFILE"])
	}
	// A plain run that selects the sole CI command is a CI configuration too.
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
	requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  tests:\n    steps:\n      - run: bundle exec rspec\n")
	drive := Testdrive{framework: framework.NewRSpec(nil), configurationEnvironment: map[string]string{}}
	item := configurationResult{TestCommand: onboard.TestCommand{Directory: "."}}
	workspace := t.TempDir()
	require.NoError(t, rubyLanguage{}.prepareConfiguration(&drive, &item, root, workspace))
	require.Equal(t, filepath.Join(workspace, "Gemfile"), drive.configurationEnvironment["BUNDLE_GEMFILE"])
}

func TestRubyProbeNeverLandsInAnInstalledGem(t *testing.T) {
	root := t.TempDir()
	gem := filepath.Join(root, "vendor/bundle/ruby/3.4.0/gems/rake-13.4.2/test")
	custom := filepath.Join(root, "bundle/ruby/3.4.0/gems/minitest-5.27.0/test")
	application := filepath.Join(root, "test")
	for _, directory := range []string{gem, custom, application} {
		require.NoError(t, os.MkdirAll(directory, 0700))
	}
	drive := Testdrive{repositoryRoot: root, configurationEnvironment: map[string]string{"BUNDLE_PATH": filepath.Join(root, "bundle")}}
	tests := []nativeTest{{File: filepath.Join(gem, "rake_test.rb")}, {File: filepath.Join(custom, "minitest_test.rb")}, {File: filepath.Join(application, "one_test.rb")}}
	directory, ok := drive.rubyProbeDirectory(tests)
	require.True(t, ok)
	want, err := filepath.EvalSymlinks(application)
	require.NoError(t, err)
	require.Equal(t, want, directory)
	_, ok = drive.rubyProbeDirectory(tests[:2])
	require.False(t, ok, "only installed gems ran; the probe has no repository test directory")
}
