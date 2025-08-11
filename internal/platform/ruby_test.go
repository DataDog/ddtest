package platform

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/datadog-test-runner/internal/settings"
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

func TestRuby_DetectFramework_Unsupported(t *testing.T) {
	viper.Reset()
	viper.Set("framework", "minitest")
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

	expectedError := "framework 'minitest' is not supported by platform 'ruby'"
	if err.Error() != expectedError {
		t.Errorf("expected error %q, got %q", expectedError, err.Error())
	}
}

func TestRuby_CreateTagsMap_Success(t *testing.T) {
	testDir := ".dd"
	defer os.RemoveAll(testDir)

	expectedRubyTags := map[string]string{
		"os.platform":     "darwin",
		"os.version":      "24.5.0",
		"runtime.name":    "ruby",
		"runtime.version": "3.3.0",
	}

	mockExecutor := &mockCommandExecutor{
		output: []byte("Tags written successfully"),
		err:    nil,
		onExecution: func(cmd *exec.Cmd) {
			// Verify the command is correct
			expectedArgs := []string{"bundle", "exec", "ruby", "-e", rubyEnvScript}
			if len(cmd.Args) != len(expectedArgs) {
				t.Errorf("expected %d args, got %d", len(expectedArgs), len(cmd.Args))
			}

			for i, expected := range expectedArgs {
				if i >= len(cmd.Args) {
					t.Errorf("missing arg at index %d", i)
					continue
				}
				// For the script argument, just verify it's not empty
				if i == len(expectedArgs)-1 {
					if cmd.Args[i] == "" {
						t.Error("ruby script should not be empty")
					}
				} else if cmd.Args[i] != expected {
					t.Errorf("expected arg[%d] to be %q, got %q", i, expected, cmd.Args[i])
				}
			}

			// Create the output file that the Ruby script would create
			if err := os.MkdirAll(testDir, 0755); err != nil {
				t.Fatalf("failed to create test directory: %v", err)
			}

			data, err := json.Marshal(expectedRubyTags)
			if err != nil {
				t.Fatalf("failed to marshal test data: %v", err)
			}

			if err := os.WriteFile(filepath.Join(testDir, "runtime_tags.json"), data, 0644); err != nil {
				t.Fatalf("failed to write test file: %v", err)
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
	defer os.RemoveAll(".dd")

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

func TestRuby_CreateTagsMap_FileReadError(t *testing.T) {
	defer os.RemoveAll(".dd")

	mockExecutor := &mockCommandExecutor{
		output: []byte("Tags written successfully"),
		err:    nil,
		onExecution: func(cmd *exec.Cmd) {
			// Command succeeds but doesn't create the file
		},
	}

	ruby := &Ruby{
		executor: mockExecutor,
	}

	tags, err := ruby.CreateTagsMap()
	if err == nil {
		t.Error("expected error when runtime tags file doesn't exist")
	}

	if tags != nil {
		t.Error("expected nil tags when file read fails")
	}

	expectedErrorMsg := "failed to read runtime tags file"
	if err == nil || len(err.Error()) < len(expectedErrorMsg) || err.Error()[:len(expectedErrorMsg)] != expectedErrorMsg {
		t.Errorf("expected error to start with %q, got %q", expectedErrorMsg, err.Error())
	}
}

func TestRuby_CreateTagsMap_InvalidJSON(t *testing.T) {
	testDir := ".dd"
	defer os.RemoveAll(testDir)

	mockExecutor := &mockCommandExecutor{
		output: []byte("Tags written successfully"),
		err:    nil,
		onExecution: func(cmd *exec.Cmd) {
			// Create directory and invalid JSON file
			if err := os.MkdirAll(testDir, 0755); err != nil {
				t.Fatalf("failed to create test directory: %v", err)
			}

			invalidJSON := `{invalid json}`
			if err := os.WriteFile(filepath.Join(testDir, "runtime_tags.json"), []byte(invalidJSON), 0644); err != nil {
				t.Fatalf("failed to write invalid JSON file: %v", err)
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

	expectedErrorMsg := "failed to parse runtime tags file"
	if err == nil || len(err.Error()) < len(expectedErrorMsg) || err.Error()[:len(expectedErrorMsg)] != expectedErrorMsg {
		t.Errorf("expected error to start with %q, got %q", expectedErrorMsg, err.Error())
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
		"require \"fileutils\"",
		"tags_map",
		".dd/runtime_tags.json",
		"to_json",
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
