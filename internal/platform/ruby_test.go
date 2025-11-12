package platform

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/viper"
)

type mockCommandExecutor struct {
	runErr            error
	combinedOutput    []byte
	combinedOutputErr error
	onRun             func(name string, args []string, envMap map[string]string)
	onCombinedOutput  func(name string, args []string, envMap map[string]string)
}

func (m *mockCommandExecutor) CombinedOutput(ctx context.Context, name string, args []string, envMap map[string]string) ([]byte, error) {
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

func TestRuby_Name(t *testing.T) {
	ruby := NewRuby()
	expected := "ruby"
	actual := ruby.Name()

	if actual != expected {
		t.Errorf("expected %q, got %q", expected, actual)
	}
}

func TestRuby_SanityCheck_Passes(t *testing.T) {
	mockExecutor := &mockCommandExecutor{
		combinedOutput: []byte("  * datadog-ci (1.23.1 9d54a15)\n"),
		onCombinedOutput: func(name string, args []string, envMap map[string]string) {
			if name != "bundle" {
				t.Fatalf("expected command 'bundle', got %q", name)
			}
			if len(args) != 2 || args[0] != "info" || args[1] != "datadog-ci" {
				t.Fatalf("unexpected args: %v", args)
			}
		},
	}

	ruby := NewRuby()
	ruby.executor = mockExecutor
	if err := ruby.SanityCheck(); err != nil {
		t.Fatalf("SanityCheck() unexpected error: %v", err)
	}
}

func TestRuby_SanityCheck_FailsWhenBundleInfoFails(t *testing.T) {
	mockExecutor := &mockCommandExecutor{
		combinedOutput:    []byte("Could not find gem 'datadog-ci'."),
		combinedOutputErr: &exec.ExitError{},
	}

	ruby := NewRuby()
	ruby.executor = mockExecutor
	err := ruby.SanityCheck()
	if err == nil {
		t.Fatal("SanityCheck() expected error when bundle info fails")
	}

	if !strings.Contains(err.Error(), "Could not find gem") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRuby_SanityCheck_FailsWhenVersionTooLow(t *testing.T) {
	mockExecutor := &mockCommandExecutor{
		combinedOutput: []byte("  * datadog-ci (1.22.5)\n"),
	}

	ruby := NewRuby()
	ruby.executor = mockExecutor
	err := ruby.SanityCheck()
	if err == nil {
		t.Fatal("SanityCheck() expected error for outdated datadog-ci version")
	}

	if !strings.Contains(err.Error(), "1.22.5") {
		t.Fatalf("expected error to mention detected version, got: %v", err)
	}
}

func TestRuby_SanityCheck_FailsWhenVersionNotFound(t *testing.T) {
	mockExecutor := &mockCommandExecutor{
		combinedOutput: []byte("  * datadog-ci\n    Summary: Datadog Test Optimization for your ruby application\n"),
	}

	ruby := NewRuby()
	ruby.executor = mockExecutor
	err := ruby.SanityCheck()
	if err == nil {
		t.Fatal("SanityCheck() expected error when version is not found")
	}

	if !strings.Contains(err.Error(), "unable to find datadog-ci gem version") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRuby_DetectFramework_RSpec(t *testing.T) {
	viper.Reset()
	viper.Set("framework", "rspec")
	defer viper.Reset()

	ruby := NewRuby()
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

	ruby := NewRuby()
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

	ruby := NewRuby()
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
		onRun: func(name string, args []string, envMap map[string]string) {
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

	tags, err := ruby.CreateTagsMap()
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

	mockExecutor := &mockCommandExecutor{
		runErr: &exec.ExitError{},
		onRun: func(name string, args []string, envMap map[string]string) {
			// Command fails, don't create any file
		},
	}

	ruby := &Ruby{
		executor: mockExecutor,
	}

	tags, err := ruby.CreateTagsMap()
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
}

func TestRuby_CreateTagsMap_InvalidJSON(t *testing.T) {
	testDir := constants.PlanDirectory
	defer func() {
		_ = os.RemoveAll(testDir)
	}()

	invalidJSON := `{invalid json}`
	mockExecutor := &mockCommandExecutor{
		onRun: func(name string, args []string, envMap map[string]string) {
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

	tags, err := ruby.CreateTagsMap()
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

	platform, err := DetectPlatform()
	if err == nil {
		t.Errorf("expected error for SanityCheck failure, but got platform: %v", platform)
	} else if platform != nil {
		t.Errorf("expected nil platform for SanityCheck failure, but got platform: %v", platform)
	}

	expectedError := "sanity check failed for platform ruby: bundle info datadog-ci command failed: Could not locate Gemfile"
	if err.Error() != expectedError {
		t.Errorf("expected error %q, got %q", expectedError, err.Error())
	}
}

func TestDetectPlatform_Unsupported(t *testing.T) {
	viper.Reset()
	viper.Set("platform", "python") // Set BEFORE Init
	settings.Init()                 // Re-initialize to set defaults
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

	expectedError := "unsupported platform: python"
	if err.Error() != expectedError {
		t.Errorf("expected error %q, got %q", expectedError, err.Error())
	}
}
