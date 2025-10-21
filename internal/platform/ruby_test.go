package platform

import (
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
	output      []byte
	err         error
	onExecution func(cmd *exec.Cmd)
}

func (m *mockCommandExecutor) CombinedOutput(cmd *exec.Cmd) ([]byte, error) {
	if m.onExecution != nil {
		m.onExecution(cmd)
	}
	return m.output, m.err
}

func (m *mockCommandExecutor) StderrOutput(cmd *exec.Cmd) ([]byte, error) {
	if m.onExecution != nil {
		m.onExecution(cmd)
	}
	return m.output, m.err
}

func (m *mockCommandExecutor) Run(cmd *exec.Cmd) error {
	if m.onExecution != nil {
		m.onExecution(cmd)
	}
	return m.err
}

func TestRuby_Name(t *testing.T) {
	ruby := NewRuby()
	expected := "ruby"
	actual := ruby.Name()

	if actual != expected {
		t.Errorf("expected %q, got %q", expected, actual)
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
		err: nil,
		onExecution: func(cmd *exec.Cmd) {
			// Verify the command is correct
			if len(cmd.Args) < 6 {
				t.Errorf("expected at least 6 args, got %d", len(cmd.Args))
				return
			}

			// Check the base command
			expectedArgs := []string{"bundle", "exec", "ruby", "-e"}
			for i, expected := range expectedArgs {
				if cmd.Args[i] != expected {
					t.Errorf("expected arg[%d] to be %q, got %q", i, expected, cmd.Args[i])
				}
			}

			// Verify the script is not empty
			if cmd.Args[4] == "" {
				t.Error("ruby script should not be empty")
			}

			// The last argument should be the temp file path
			tempFile := cmd.Args[5]
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
		output: []byte("bundle: command not found"),
		err:    &exec.ExitError{},
		onExecution: func(cmd *exec.Cmd) {
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
		err: nil,
		onExecution: func(cmd *exec.Cmd) {
			// Get the temp file path from the last argument
			if len(cmd.Args) < 6 {
				t.Errorf("expected at least 6 args, got %d", len(cmd.Args))
				return
			}
			tempFile := cmd.Args[5]

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
	if err != nil {
		t.Fatalf("DetectPlatform failed: %v", err)
	}

	if platform == nil {
		t.Error("expected platform to be non-nil")
	}

	if platform.Name() != "ruby" {
		t.Errorf("expected platform name to be 'ruby', got %q", platform.Name())
	}

	// Verify it's the correct type and has executor
	rubyPlatform, ok := platform.(*Ruby)
	if !ok {
		t.Error("expected platform to be *Ruby")
	} else if rubyPlatform.executor == nil {
		t.Error("expected Ruby platform to have executor")
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
