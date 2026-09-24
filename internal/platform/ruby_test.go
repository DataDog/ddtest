package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

type mockCommandExecutor struct {
	runErr            error
	combinedOutput    []byte
	combinedOutputErr error
	combinedOutputCtx []context.Context
	onRun             func(name string, args []string, envMap map[string]string)
	onCombinedOutput  func(name string, args []string, envMap map[string]string)
}

func (m *mockCommandExecutor) CombinedOutput(ctx context.Context, name string, args []string, envMap map[string]string) ([]byte, error) {
	m.combinedOutputCtx = append(m.combinedOutputCtx, ctx)
	if m.onCombinedOutput != nil {
		m.onCombinedOutput(name, args, envMap)
	}
	return m.combinedOutput, m.combinedOutputErr
}

func (m *mockCommandExecutor) Run(ctx context.Context, name string, args []string, envMap map[string]string) error {
	if m.onRun != nil {
		m.onRun(name, args, envMap)
	}
	return m.runErr
}

func newTestRuby() *Ruby {
	return NewRuby(settings.TestSkippingLevelTest)
}

func TestRuby_Name(t *testing.T) {
	ruby := newTestRuby()
	expected := "ruby"
	actual := ruby.Name()

	if actual != expected {
		t.Errorf("expected %q, got %q", expected, actual)
	}
}

func TestRuby_TestSkippingLevel(t *testing.T) {
	tests := []struct {
		name  string
		value settings.TestSkippingLevel
		want  settings.TestSkippingLevel
	}{
		{name: "test", value: "test", want: settings.TestSkippingLevelTest},
		{name: "suite", value: "suite", want: settings.TestSkippingLevelSuite},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewRuby(tt.value).TestSkippingLevel(); got != tt.want {
				t.Fatalf("TestSkippingLevel() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRuby_SanityCheck_Passes(t *testing.T) {
	mockExecutor := &mockCommandExecutor{
		combinedOutput: []byte("  * datadog-ci (1.31.0 9d54a15)\n"),
		onCombinedOutput: func(name string, args []string, envMap map[string]string) {
			if name != "bundle" {
				t.Fatalf("expected command 'bundle', got %q", name)
			}
			if len(args) != 2 || args[0] != "info" || args[1] != requiredGemName {
				t.Fatalf("unexpected args: %v", args)
			}
		},
	}

	ruby := newTestRuby()
	ruby.executor = mockExecutor
	if err := ruby.SanityCheck(context.Background()); err != nil {
		t.Fatalf("SanityCheck() unexpected error: %v", err)
	}
}

func TestRuby_SanityCheck_FailsWhenBundleInfoFails(t *testing.T) {
	mockExecutor := &mockCommandExecutor{
		combinedOutput:    []byte("Could not find gem 'datadog-ci'."),
		combinedOutputErr: &exec.ExitError{},
	}

	ruby := newTestRuby()
	ruby.executor = mockExecutor
	err := ruby.SanityCheck(context.Background())
	if err == nil {
		t.Fatal("SanityCheck() expected error when bundle info fails")
	}

	if !strings.Contains(err.Error(), "Could not find gem") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRuby_SanityCheck_FailsWhenVersionTooLow(t *testing.T) {
	mockExecutor := &mockCommandExecutor{
		combinedOutput: []byte("  * datadog-ci (1.30.9)\n"),
	}

	ruby := newTestRuby()
	ruby.executor = mockExecutor
	err := ruby.SanityCheck(context.Background())
	if err == nil {
		t.Fatal("SanityCheck() expected error for outdated datadog-ci version")
	}

	if !strings.Contains(err.Error(), "1.30.9") {
		t.Fatalf("expected error to mention detected version, got: %v", err)
	}
	if !strings.Contains(err.Error(), "1.31.0") {
		t.Fatalf("expected error to mention required version, got: %v", err)
	}
}

func TestRuby_SanityCheck_FailsWhenVersionNotFound(t *testing.T) {
	mockExecutor := &mockCommandExecutor{
		combinedOutput: []byte("  * datadog-ci\n    Summary: Datadog Test Optimization for your ruby application\n"),
	}

	ruby := newTestRuby()
	ruby.executor = mockExecutor
	err := ruby.SanityCheck(context.Background())
	if err == nil {
		t.Fatal("SanityCheck() expected error when version is not found")
	}

	if !strings.Contains(err.Error(), "unable to find datadog-ci gem version") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRuby_SanityCheck_SucceedsWithDebugLogs(t *testing.T) {
	// Debug logs from datadog tracing contain paths with gem name and parentheses
	// that could confuse the parser if not handled correctly
	output := `D, [2026-02-02T16:50:26.016240 #9457] DEBUG -- datadog: [datadog] (/path/to/datadog-ci-rb/lib/datadog/ci/contrib/instrumentation.rb:27:in 'auto_instrument') Auto instrumenting all integrations...
  * datadog-ci (1.31.2 e11ecfb)
        Summary: Datadog Test Optimization for your ruby application
        Homepage: https://github.com/DataDog/datadog-ci-rb
        Path: /Users/user/.rbenv/versions/3.3.5/lib/ruby/gems/3.3.0/bundler/gems/datadog-ci-rb-e11ecfbf06ad
`
	mockExecutor := &mockCommandExecutor{
		combinedOutput: []byte(output),
	}

	ruby := newTestRuby()
	ruby.executor = mockExecutor
	if err := ruby.SanityCheck(context.Background()); err != nil {
		t.Fatalf("SanityCheck() unexpected error: %v", err)
	}
}

func TestRuby_DetectFramework_RSpec(t *testing.T) {
	viper.Reset()
	viper.Set("framework", "rspec")
	settings.Init()
	t.Cleanup(func() { viper.Reset(); settings.Init() })

	ruby := newTestRuby()
	fw, err := ruby.DetectFramework()

	if err != nil {
		t.Fatalf("DetectFramework failed: %v", err)
	}

	if fw == nil {
		t.Error("expected framework to be non-nil")
	}

	if fw.Name() != "rspec" {
		t.Errorf("expected framework name to be 'rspec', got %q", fw.Name())
	}

	if fw.Name() != "rspec" {
		t.Error("expected framework to be RSpec")
	}
}

func TestRuby_DetectFramework_Minitest(t *testing.T) {
	viper.Reset()
	viper.Set("framework", "minitest")
	settings.Init()
	defer func() {
		viper.Reset()
		settings.Init()
	}()

	ruby := newTestRuby()
	fw, err := ruby.DetectFramework()

	if err != nil {
		t.Fatalf("DetectFramework failed: %v", err)
	}

	if fw == nil {
		t.Error("expected framework to be non-nil")
	}

	if fw.Name() != "minitest" {
		t.Errorf("expected framework name to be 'minitest', got %q", fw.Name())
	}
}

func TestRuby_DetectFramework_Unsupported(t *testing.T) {
	viper.Reset()
	viper.Set("framework", "cucumber")
	settings.Init()
	defer func() {
		viper.Reset()
		settings.Init()
	}()

	ruby := newTestRuby()
	fw, err := ruby.DetectFramework()

	if err == nil {
		t.Errorf("expected error for unsupported framework, but got framework: %v", fw)
		return
	}

	if fw != nil {
		t.Error("expected nil framework for unsupported framework")
	}

	expectedError := "framework 'cucumber' is not supported by platform 'ruby'"
	if err.Error() != expectedError {
		t.Errorf("expected error %q, got %q", expectedError, err.Error())
	}
}

func TestRuby_CreateTagsMap_Success(t *testing.T) {
	testDir := constants.PlanDirectory
	defer func() {
		_ = os.RemoveAll(testDir)
	}()

	expectedRubyTags := map[string]string{
		"os.platform":     "darwin",
		"os.version":      "24.5.0",
		"runtime.name":    "ruby",
		"runtime.version": "3.3.0",
	}

	// Prepare expected JSON output
	expectedOutput, err := json.Marshal(expectedRubyTags)
	if err != nil {
		t.Fatalf("failed to marshal expected tags: %v", err)
	}

	mockExecutor := &mockCommandExecutor{
		onCombinedOutput: func(name string, args []string, envMap map[string]string) {
			// Verify the command is correct
			if name != "bundle" {
				t.Errorf("expected command to be 'bundle', got %q", name)
			}

			if len(args) < 5 {
				t.Errorf("expected at least 5 args, got %d", len(args))
				return
			}

			// Check the base command args
			expectedArgs := []string{"exec", "ruby", "-e"}
			for i, expected := range expectedArgs {
				if args[i] != expected {
					t.Errorf("expected arg[%d] to be %q, got %q", i, expected, args[i])
				}
			}

			// Verify the script is not empty
			if args[3] == "" {
				t.Error("ruby script should not be empty")
			}

			// The last argument should be the temp file path
			tempFile := args[4]
			if tempFile == "" {
				t.Error("temp file path should not be empty")
			}

			// Write the expected output to the temp file
			if err := os.WriteFile(tempFile, expectedOutput, 0644); err != nil {
				t.Errorf("failed to write temp file: %v", err)
			}
		},
	}

	// Create a testable Ruby instance with mocked executor
	ruby := &Ruby{
		executor: mockExecutor,
	}

	tags, err := ruby.CreateTagsMap(context.Background())
	if err != nil {
		t.Fatalf("CreateTagsMap failed: %v", err)
	}

	// Verify basic language tag is set
	if tags["language"] != "ruby" {
		t.Errorf("expected language tag to be 'ruby', got %q", tags["language"])
	}

	// Verify Ruby-specific tags are merged
	for key, expectedValue := range expectedRubyTags {
		if actualValue, exists := tags[key]; !exists {
			t.Errorf("expected tag %q to exist", key)
		} else if actualValue != expectedValue {
			t.Errorf("expected tag %q to be %q, got %q", key, expectedValue, actualValue)
		}
	}
}

func TestRuby_CreateTagsMap_CommandFailure(t *testing.T) {
	defer func() {
		_ = os.RemoveAll(constants.PlanDirectory)
	}()

	probeErr := errors.New("probe failed")
	mockExecutor := &mockCommandExecutor{
		combinedOutput:    []byte(" Bundler setup failed\n"),
		combinedOutputErr: probeErr,
		onCombinedOutput: func(name string, args []string, envMap map[string]string) {
			// Command fails, don't create any file
		},
	}

	ruby := &Ruby{
		executor: mockExecutor,
	}

	tags, err := ruby.CreateTagsMap(context.Background())
	if err == nil {
		t.Error("expected error when ruby command fails")
	}

	if tags != nil {
		t.Error("expected nil tags when command fails")
	}

	expectedErrorMsg := "failed to execute Ruby script"
	if err == nil || len(err.Error()) < len(expectedErrorMsg) || err.Error()[:len(expectedErrorMsg)] != expectedErrorMsg {
		t.Errorf("expected error to start with %q, got %q", expectedErrorMsg, err.Error())
	}
	if !strings.Contains(err.Error(), "Bundler setup failed") {
		t.Errorf("expected error to include probe output, got %q", err.Error())
	}
	if !errors.Is(err, probeErr) {
		t.Errorf("expected error to wrap probe failure, got %v", err)
	}
}

func TestRuby_CreateTagsMap_InvalidJSON(t *testing.T) {
	testDir := constants.PlanDirectory
	defer func() {
		_ = os.RemoveAll(testDir)
	}()

	invalidJSON := `{invalid json}`
	mockExecutor := &mockCommandExecutor{
		onCombinedOutput: func(name string, args []string, envMap map[string]string) {
			// Get the temp file path from the last argument
			if len(args) < 5 {
				t.Errorf("expected at least 5 args, got %d", len(args))
				return
			}
			tempFile := args[4]

			// Write invalid JSON to the temp file
			if err := os.WriteFile(tempFile, []byte(invalidJSON), 0644); err != nil {
				t.Errorf("failed to write temp file: %v", err)
			}
		},
	}

	ruby := &Ruby{
		executor: mockExecutor,
	}

	tags, err := ruby.CreateTagsMap(context.Background())
	if err == nil {
		t.Error("expected error when JSON is invalid")
	}

	if tags != nil {
		t.Error("expected nil tags when JSON parsing fails")
	}

	expectedErrorMsg := "failed to parse runtime tags JSON"
	if err == nil || !strings.Contains(err.Error(), expectedErrorMsg) {
		t.Errorf("expected error to contain %q, got %q", expectedErrorMsg, err.Error())
	}
}

func TestRuby_EmbeddedScript(t *testing.T) {
	// Test that the embedded script is not empty and contains expected content
	if rubyEnvScript == "" {
		t.Error("embedded Ruby script should not be empty")
	}

	// Check for key components that should be in the script
	expectedContent := []string{
		"require \"json\"",
		"tags_map",
		"output_file = ARGV[0]",
		"File.write(output_file, tags_map.to_json)",
	}

	for _, expected := range expectedContent {
		if !strings.Contains(rubyEnvScript, expected) {
			t.Errorf("expected Ruby script to contain %q", expected)
		}
	}
}

func TestDetectPlatform_Ruby(t *testing.T) {
	// Save original settings
	viper.Reset()
	viper.Set("platform", "ruby")
	settings.Init()
	t.Cleanup(func() { viper.Reset(); settings.Init() })

	t.Setenv("PATH", t.TempDir())
	platform, err := DetectPlatform()
	if err != nil {
		t.Fatal(err)
	}
	if platform.Name() != "ruby" {
		t.Fatalf("expected ruby, got %q", platform.Name())
	}

}

func TestDetectPlatform_Unsupported(t *testing.T) {
	viper.Reset()
	viper.Set("platform", "go") // Set BEFORE Init
	settings.Init()             // Re-initialize to set defaults
	defer func() {
		viper.Reset()
		settings.Init()
	}()

	platform, err := DetectPlatform()
	if err == nil {
		t.Errorf("expected error for unsupported platform, but got platform: %v", platform)
		return
	}

	if platform != nil {
		t.Error("expected nil platform for unsupported platform")
	}

	expectedError := "unsupported platform: go"
	if err.Error() != expectedError {
		t.Errorf("expected error %q, got %q", expectedError, err.Error())
	}
}

func TestRuby_GetPlatformEnv_SetsRUBYOPT_WhenNotSet(t *testing.T) {
	// Ensure RUBYOPT is not set
	originalValue, existed := os.LookupEnv("RUBYOPT")
	if existed {
		_ = os.Unsetenv("RUBYOPT")
		defer func() { _ = os.Setenv("RUBYOPT", originalValue) }()
	}

	ruby := newTestRuby()
	envMap := ruby.GetPlatformEnv()

	expectedValue := "-rbundler/setup -rdatadog/ci/auto_instrument"
	if envMap["RUBYOPT"] != expectedValue {
		t.Errorf("expected RUBYOPT to be %q, got %q", expectedValue, envMap["RUBYOPT"])
	}
}

func TestRuby_GetPlatformEnv_DoesNotOverride_WhenAlreadySet(t *testing.T) {
	// Set RUBYOPT to a custom value
	originalValue, existed := os.LookupEnv("RUBYOPT")
	customValue := "-rbundler/setup -rsome_other_require"
	_ = os.Setenv("RUBYOPT", customValue)
	defer func() {
		if existed {
			_ = os.Setenv("RUBYOPT", originalValue)
		} else {
			_ = os.Unsetenv("RUBYOPT")
		}
	}()

	ruby := newTestRuby()
	envMap := ruby.GetPlatformEnv()

	// When RUBYOPT is already set, GetPlatformEnv should not include it
	if _, exists := envMap["RUBYOPT"]; exists {
		t.Error("expected RUBYOPT to not be in envMap when it's already set in environment")
	}
}

func TestRuby_DetectFramework_SetsPlatformEnv(t *testing.T) {
	// Ensure RUBYOPT is not set so we can verify it gets set
	originalValue, existed := os.LookupEnv("RUBYOPT")
	if existed {
		_ = os.Unsetenv("RUBYOPT")
		defer func() { _ = os.Setenv("RUBYOPT", originalValue) }()
	}

	viper.Reset()
	viper.Set("framework", "rspec")
	settings.Init()
	t.Cleanup(func() { viper.Reset(); settings.Init() })

	ruby := newTestRuby()
	fw, err := ruby.DetectFramework()

	if err != nil {
		t.Fatalf("DetectFramework failed: %v", err)
	}

	if fw == nil {
		t.Fatal("expected framework to be non-nil")
	}

	// Verify the framework received the correct platform env
	frameworkPlatformEnv := fw.GetPlatformEnv()
	expectedRubyOpt := "-rbundler/setup -rdatadog/ci/auto_instrument"
	if frameworkPlatformEnv["RUBYOPT"] != expectedRubyOpt {
		t.Errorf("expected framework platformEnv RUBYOPT=%q, got %q", expectedRubyOpt, frameworkPlatformEnv["RUBYOPT"])
	}
}

func (m *mockCommandExecutor) Output(ctx context.Context, name string, args []string, env map[string]string) ([]byte, []byte, error) {
	output, err := m.CombinedOutput(ctx, name, args, env)
	if err != nil {
		return nil, output, err
	}
	return output, nil, nil
}

func TestRubyInstallDoesNotEditCustomerBundle(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	directory := t.TempDir()
	original := "source 'https://rubygems.org'\ngemspec\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, "Gemfile"), []byte(original), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "Gemfile.lock"), []byte("customer lock"), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".bundle"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".bundle", "config"), []byte("BUNDLE_MIRROR__HTTPS://RUBYGEMS__ORG/: https://mirror.example\n"), 0600))
	executor := &fakeCommandExecutor{responses: []commandResponse{{err: errors.New("project tracer unavailable")}, {}}}
	installer := &Ruby{executor: executor}
	path, err := installer.InstallTestdriveTracer(t.Context(), TracerOptions{Directory: directory, Version: "latest"})
	require.NoError(t, err)
	contents, err := os.ReadFile(path.Path)
	require.NoError(t, err)
	require.Contains(t, string(contents), "eval_gemfile")
	require.Contains(t, string(contents), filepath.Join(root, "Gemfile"))
	require.Contains(t, string(contents), "gem 'datadog-ci' unless dependencies.any?")
	require.Equal(t, path.Path, executor.envs[1]["BUNDLE_GEMFILE"])
	require.Equal(t, filepath.Join(directory, "gems"), executor.envs[1]["BUNDLE_PATH"])
	require.Empty(t, executor.envs[1]["BUNDLE_WITHOUT"])
	require.Empty(t, executor.envs[1]["BUNDLE_ONLY"])
	copiedConfig, err := os.ReadFile(filepath.Join(directory, "bundle-config", "config"))
	require.NoError(t, err)
	require.Contains(t, string(copiedConfig), "mirror.example")
	contents, err = os.ReadFile(filepath.Join(root, "Gemfile"))
	require.NoError(t, err)
	require.Equal(t, original, string(contents))
	contents, err = os.ReadFile(filepath.Join(root, "Gemfile.lock"))
	require.NoError(t, err)
	require.Equal(t, "customer lock", string(contents))
	require.NoFileExists(t, filepath.Join(directory, "Gemfile.lock")) // Bundler creates its own lockfile.
}

func TestRubyInstallInPathWithSpacesReportsBuildResult(t *testing.T) {
	for _, fails := range []bool{false, true} {
		t.Run(fmt.Sprintf("build fails=%t", fails), func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "project space")
			require.NoError(t, os.MkdirAll(root, 0755))
			t.Chdir(root)
			require.NoError(t, os.WriteFile("Gemfile", []byte("source 'https://rubygems.org'\n"), 0600))
			directory := filepath.Join(root, "session space")
			require.NoError(t, os.MkdirAll(directory, 0755))
			build := commandResponse{}
			if fails {
				build = commandResponse{output: []byte("compiling crashtracker.c\nclang: error: missing header"), err: errors.New("exit status 5")}
			}
			executor := &fakeCommandExecutor{responses: []commandResponse{{err: errors.New("tracer unavailable")}, build}}
			installer := &Ruby{executor: executor}
			result, err := installer.InstallTestdriveTracer(t.Context(), TracerOptions{Directory: directory})
			if fails {
				require.ErrorContains(t, err, "compiling crashtracker.c\nclang: error: missing header")
				require.ErrorIs(t, err, build.err)
			} else {
				require.NoError(t, err)
				require.Equal(t, filepath.Join(directory, "Gemfile"), result.Path)
			}
			require.Len(t, executor.commands, 2)
			require.Equal(t, command{name: "bundle", args: []string{"install"}}, executor.commands[1])
			require.Equal(t, filepath.Join(directory, "Gemfile"), executor.envs[1]["BUNDLE_GEMFILE"])
			contents, err := os.ReadFile(filepath.Join(directory, "Gemfile"))
			require.NoError(t, err)
			require.Contains(t, string(contents), "eval_gemfile '"+filepath.Join(root, "Gemfile")+"'")
		})
	}
}

func TestRubyTracerVersions(t *testing.T) {
	for _, tt := range []struct{ version, declaration string }{
		{"", "gem 'datadog-ci'\n"}, {"latest", "gem 'datadog-ci'\n"},
		{"1.39.0", "gem 'datadog-ci', '1.39.0'\n"},
		{"1.40.0.pre.1", "gem 'datadog-ci', '1.40.0.pre.1'\n"},
		{"git:abc1234", "gem 'datadog-ci', git: 'https://github.com/DataDog/datadog-ci-rb.git', ref: 'abc1234'\n"},
	} {
		t.Run(tt.version, func(t *testing.T) {
			root, directory := t.TempDir(), t.TempDir()
			t.Chdir(root)
			require.NoError(t, os.WriteFile(filepath.Join(root, "Gemfile"), []byte("source 'https://rubygems.org'\n"), 0600))
			lock := "GEM\n  specs:\n    rake (13.2.1)\n"
			require.NoError(t, os.WriteFile(filepath.Join(root, "Gemfile.lock"), []byte(lock), 0600))
			executor := &fakeCommandExecutor{responses: []commandResponse{{err: errors.New("project tracer unavailable")}, {}}}
			installer := NewRuby(settings.TestSkippingLevelTest)
			installer.executor = executor
			path, err := installer.InstallTestdriveTracer(t.Context(), TracerOptions{Directory: directory, Version: tt.version})
			require.NoError(t, err)
			contents, err := os.ReadFile(path.Path)
			require.NoError(t, err)
			require.Contains(t, string(contents), strings.TrimSuffix(tt.declaration, "\n")+" unless dependencies.any? { |dependency| dependency.name == 'datadog-ci' }")
			require.Equal(t, []string{"install"}, executor.commands[1].args)
			unchanged, err := os.ReadFile(filepath.Join(root, "Gemfile.lock"))
			require.NoError(t, err)
			require.Equal(t, lock, string(unchanged))
		})
	}
	_, err := NewRuby(settings.TestSkippingLevelTest).InstallTestdriveTracer(t.Context(), TracerOptions{Directory: t.TempDir(), Version: "git:"})
	require.ErrorContains(t, err, "git ref must not be empty")
}

func TestRubyReusesProjectTracer(t *testing.T) {
	t.Setenv("BUNDLE_GEMFILE", "/project/custom.gemfile")
	for _, version := range []string{"latest", "1.39.0", "git:abc1234"} {
		t.Run(version, func(t *testing.T) {
			executor := &fakeCommandExecutor{responses: []commandResponse{{output: []byte("  * datadog-ci (1.31.0)\n")}}}
			installer := NewRuby(settings.TestSkippingLevelTest)
			installer.executor = executor
			directory := filepath.Join(t.TempDir(), "session with spaces")
			result, err := installer.InstallTestdriveTracer(t.Context(), TracerOptions{Directory: directory, Version: version})
			require.NoError(t, err)
			require.Equal(t, TracerInstallation{Project: true}, result)
			require.Len(t, executor.commands, 1)
			require.Equal(t, "bundle", executor.commands[0].name)
			require.Equal(t, []string{"info", "datadog-ci"}, executor.commands[0].args)
			require.NotContains(t, executor.envs[0], "BUNDLE_GEMFILE") // Inherit the selected project Gemfile.
			require.NoDirExists(t, directory)
			require.Empty(t, result.Env)
		})
	}
}

func TestRubyProbeFailureAttemptsInstall(t *testing.T) {
	executor := &fakeCommandExecutor{responses: []commandResponse{{err: errors.New("broken project Gemfile")}, {err: errors.New("bundle install failed")}}}
	installer := NewRuby(settings.TestSkippingLevelTest)
	installer.executor = executor
	_, err := installer.InstallTestdriveTracer(t.Context(), TracerOptions{Directory: t.TempDir(), Version: "latest"})
	require.ErrorContains(t, err, "bundle install failed")
	require.Len(t, executor.commands, 2)
	require.Equal(t, "bundle", executor.commands[1].name)
	require.Equal(t, []string{"install"}, executor.commands[1].args)
}

func TestRubyFallbackHonorsSelectedGemfileAndClearsBootstrapPreload(t *testing.T) {
	for _, absolute := range []bool{false, true} {
		t.Run(fmt.Sprintf("absolute=%t", absolute), func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			selected := filepath.Join("config", "Gemfile.test")
			require.NoError(t, os.MkdirAll("config", 0755))
			require.NoError(t, os.WriteFile(selected, []byte("gem 'datadog-ci', '1.39.0'\n"), 0600))
			if absolute {
				selected = filepath.Join(root, selected)
			}
			t.Setenv("BUNDLE_GEMFILE", selected)
			t.Setenv("RUBYOPT", "-rbundler/setup -rdatadog/ci/auto_instrument")
			executor := &fakeCommandExecutor{responses: []commandResponse{{err: errors.New("tracer not installed")}, {}}}
			result, err := (&Ruby{executor: executor}).InstallTestdriveTracer(t.Context(), TracerOptions{Directory: t.TempDir(), Version: "git:does-not-exist"})
			require.NoError(t, err)
			contents, err := os.ReadFile(result.Path)
			require.NoError(t, err)
			require.Contains(t, string(contents), "eval_gemfile '"+filepath.Join(root, "config", "Gemfile.test")+"'")
			require.Contains(t, string(contents), "unless dependencies.any? { |dependency| dependency.name == 'datadog-ci' }")
			for _, env := range executor.envs {
				require.Contains(t, env, "RUBYOPT")
				require.Empty(t, env["RUBYOPT"])
			}
			require.NotContains(t, result.Env, "RUBYOPT") // Keep instrumentation for the test run.
			require.Equal(t, "-rbundler/setup -rdatadog/ci/auto_instrument", os.Getenv("RUBYOPT"))
		})
	}
}
