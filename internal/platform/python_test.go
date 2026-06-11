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

func TestPython_Name(t *testing.T) {
	python := NewPython()
	if python.Name() != "python" {
		t.Errorf("expected %q, got %q", "python", python.Name())
	}
}

func TestNormalizePyVersion(t *testing.T) {
	cases := []struct{ in, want string }{
		{"4.12.0rc1", "4.12.0-rc1"},
		{"4.12.0b2", "4.12.0-b2"},
		{"4.12.0a1", "4.12.0-a1"},
		{"4.10.3", "4.10.3"},
		{"1.2.3.4", "1.2.3.4"},
	}
	for _, c := range cases {
		if got := normalizePyVersion(c.in); got != c.want {
			t.Errorf("normalizePyVersion(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPython_SanityCheck_SuccessWithPreRelease(t *testing.T) {
	mockExecutor := &mockCommandExecutor{
		combinedOutput: []byte("4.12.0rc1\n"),
	}
	python := NewPython()
	python.executor = mockExecutor
	if err := python.SanityCheck(); err != nil {
		t.Fatalf("SanityCheck() unexpected error for pre-release version: %v", err)
	}
}

func TestPython_SanityCheck_Success(t *testing.T) {
	mockExecutor := &mockCommandExecutor{
		combinedOutput: []byte("4.10.3\n"),
		onCombinedOutput: func(name string, args []string, envMap map[string]string) {
			if name != "python" {
				t.Fatalf("expected command 'python', got %q", name)
			}
			if len(args) < 3 || args[0] != "-c" {
				t.Fatalf("unexpected args: %v", args)
			}
			if args[2] != requiredPackageName {
				t.Errorf("expected package name arg %q, got %q", requiredPackageName, args[2])
			}
		},
	}

	python := NewPython()
	python.executor = mockExecutor
	if err := python.SanityCheck(); err != nil {
		t.Fatalf("SanityCheck() unexpected error: %v", err)
	}
}

func TestPython_SanityCheck_NotInstalled(t *testing.T) {
	mockExecutor := &mockCommandExecutor{
		combinedOutput:    []byte("No module named importlib.metadata"),
		combinedOutputErr: &exec.ExitError{},
	}

	python := NewPython()
	python.executor = mockExecutor
	err := python.SanityCheck()
	if err == nil {
		t.Fatal("SanityCheck() expected error when package is not installed")
	}

	if !strings.Contains(err.Error(), requiredPackageName) {
		t.Errorf("expected error to mention %q, got: %v", requiredPackageName, err)
	}
}

func TestPython_SanityCheck_VersionTooOld(t *testing.T) {
	mockExecutor := &mockCommandExecutor{
		combinedOutput: []byte("4.9.0\n"),
	}

	python := NewPython()
	python.executor = mockExecutor
	err := python.SanityCheck()
	if err == nil {
		t.Fatal("SanityCheck() expected error for outdated ddtrace version")
	}

	if !strings.Contains(err.Error(), "4.9.0") {
		t.Errorf("expected error to mention detected version, got: %v", err)
	}
	if !strings.Contains(err.Error(), requiredPackageVersion) {
		t.Errorf("expected error to mention required version, got: %v", err)
	}
}

func TestPython_SanityCheck_InvalidVersion(t *testing.T) {
	mockExecutor := &mockCommandExecutor{
		combinedOutput: []byte("not-a-version\n"),
	}

	python := NewPython()
	python.executor = mockExecutor
	err := python.SanityCheck()
	if err == nil {
		t.Fatal("SanityCheck() expected error for unparseable version")
	}

	if !strings.Contains(err.Error(), "failed to parse") {
		t.Errorf("expected error to mention parse failure, got: %v", err)
	}
}

func TestPython_GetPlatformEnv_SetsWhenNotSet(t *testing.T) {
	original, existed := os.LookupEnv(pytestAddOptsEnvVar)
	if existed {
		_ = os.Unsetenv(pytestAddOptsEnvVar)
		defer func() { _ = os.Setenv(pytestAddOptsEnvVar, original) }()
	}

	python := NewPython()
	envMap := python.GetPlatformEnv()

	if envMap[pytestAddOptsEnvVar] != pytestDefaultAddOpts {
		t.Errorf("expected %s=%q, got %q", pytestAddOptsEnvVar, pytestDefaultAddOpts, envMap[pytestAddOptsEnvVar])
	}
}

func TestPython_GetPlatformEnv_AppendsWhenAlreadySet(t *testing.T) {
	original, existed := os.LookupEnv(pytestAddOptsEnvVar)
	existingValue := "-v --tb=short"
	_ = os.Setenv(pytestAddOptsEnvVar, existingValue)
	defer func() {
		if existed {
			_ = os.Setenv(pytestAddOptsEnvVar, original)
		} else {
			_ = os.Unsetenv(pytestAddOptsEnvVar)
		}
	}()

	python := NewPython()
	envMap := python.GetPlatformEnv()

	expected := existingValue + " " + pytestDefaultAddOpts
	if envMap[pytestAddOptsEnvVar] != expected {
		t.Errorf("expected %s=%q, got %q", pytestAddOptsEnvVar, expected, envMap[pytestAddOptsEnvVar])
	}
}

func TestPython_CreateTagsMap_Success(t *testing.T) {
	defer func() { _ = os.RemoveAll(constants.PlanDirectory) }()

	expectedPythonTags := map[string]string{
		"runtime.name":    "python",
		"runtime.version": "3.11.0",
		"os.platform":     "linux",
		"os.architecture": "x86_64",
		"os.version":      "5.15.0",
	}

	expectedOutput, err := json.Marshal(expectedPythonTags)
	if err != nil {
		t.Fatalf("failed to marshal expected tags: %v", err)
	}

	mockExecutor := &mockCommandExecutor{
		onRun: func(name string, args []string, envMap map[string]string) {
			if name != "python" {
				t.Errorf("expected command 'python', got %q", name)
			}
			if len(args) < 3 {
				t.Errorf("expected at least 3 args, got %d", len(args))
				return
			}
			if args[0] != "-c" {
				t.Errorf("expected args[0]='-c', got %q", args[0])
			}
			if args[1] == "" {
				t.Error("python script should not be empty")
			}
			tempFile := args[2]
			if tempFile == "" {
				t.Error("temp file path should not be empty")
			}
			if err := os.WriteFile(tempFile, expectedOutput, 0644); err != nil {
				t.Errorf("failed to write temp file: %v", err)
			}
		},
	}

	python := &Python{executor: mockExecutor}
	tags, err := python.CreateTagsMap()
	if err != nil {
		t.Fatalf("CreateTagsMap failed: %v", err)
	}

	if tags["language"] != "python" {
		t.Errorf("expected language tag to be 'python', got %q", tags["language"])
	}

	for key, expectedValue := range expectedPythonTags {
		if actualValue, exists := tags[key]; !exists {
			t.Errorf("expected tag %q to exist", key)
		} else if actualValue != expectedValue {
			t.Errorf("expected tag %q=%q, got %q", key, expectedValue, actualValue)
		}
	}
}

func TestPython_CreateTagsMap_CommandFailure(t *testing.T) {
	defer func() { _ = os.RemoveAll(constants.PlanDirectory) }()

	mockExecutor := &mockCommandExecutor{
		runErr: &exec.ExitError{},
	}

	python := &Python{executor: mockExecutor}
	tags, err := python.CreateTagsMap()

	if err == nil {
		t.Error("expected error when python command fails")
	}
	if tags != nil {
		t.Error("expected nil tags when command fails")
	}

	expectedPrefix := "failed to execute Python script"
	if !strings.HasPrefix(err.Error(), expectedPrefix) {
		t.Errorf("expected error to start with %q, got %q", expectedPrefix, err.Error())
	}
}

func TestPython_CreateTagsMap_InvalidJSON(t *testing.T) {
	defer func() { _ = os.RemoveAll(constants.PlanDirectory) }()

	invalidJSON := `{invalid json}`
	mockExecutor := &mockCommandExecutor{
		onRun: func(name string, args []string, envMap map[string]string) {
			if len(args) < 3 {
				t.Errorf("expected at least 3 args, got %d", len(args))
				return
			}
			tempFile := args[2]
			if err := os.WriteFile(tempFile, []byte(invalidJSON), 0644); err != nil {
				t.Errorf("failed to write temp file: %v", err)
			}
		},
	}

	python := &Python{executor: mockExecutor}
	tags, err := python.CreateTagsMap()

	if err == nil {
		t.Error("expected error when JSON is invalid")
	}
	if tags != nil {
		t.Error("expected nil tags when JSON parsing fails")
	}

	if !strings.Contains(err.Error(), "failed to parse runtime tags JSON") {
		t.Errorf("expected error to contain 'failed to parse runtime tags JSON', got %q", err.Error())
	}
}

func TestPython_DetectFramework_Pytest(t *testing.T) {
	viper.Reset()
	viper.Set("framework", "pytest")
	defer viper.Reset()

	// Ensure PYTEST_ADDOPTS is unset so GetPlatformEnv produces a deterministic value
	original, existed := os.LookupEnv(pytestAddOptsEnvVar)
	if existed {
		_ = os.Unsetenv(pytestAddOptsEnvVar)
		defer func() { _ = os.Setenv(pytestAddOptsEnvVar, original) }()
	}

	python := NewPython()
	fw, err := python.DetectFramework()

	if err != nil {
		t.Fatalf("DetectFramework failed: %v", err)
	}
	if fw == nil {
		t.Fatal("expected framework to be non-nil")
	}
	if fw.Name() != "pytest" {
		t.Errorf("expected framework name 'pytest', got %q", fw.Name())
	}

	frameworkEnv := fw.GetPlatformEnv()
	if frameworkEnv[pytestAddOptsEnvVar] != pytestDefaultAddOpts {
		t.Errorf("expected framework platformEnv %s=%q, got %q",
			pytestAddOptsEnvVar, pytestDefaultAddOpts, frameworkEnv[pytestAddOptsEnvVar])
	}
}

func TestPython_DetectFramework_Unsupported(t *testing.T) {
	viper.Reset()
	viper.Set("framework", "unittest")
	settings.Init()
	defer func() {
		viper.Reset()
		settings.Init()
	}()

	python := NewPython()
	fw, err := python.DetectFramework()

	if err == nil {
		t.Errorf("expected error for unsupported framework, but got framework: %v", fw)
		return
	}
	if fw != nil {
		t.Error("expected nil framework for unsupported framework")
	}

	expectedError := "framework 'unittest' is not supported by platform 'python'"
	if err.Error() != expectedError {
		t.Errorf("expected error %q, got %q", expectedError, err.Error())
	}
}

func TestPython_EmbeddedScript(t *testing.T) {
	if pythonEnvScript == "" {
		t.Error("embedded Python script should not be empty")
	}

	expectedContent := []string{
		"import json",
		"import sys",
		"import platform",
		"sys.argv[1]",
		"json.dump",
	}

	for _, expected := range expectedContent {
		if !strings.Contains(pythonEnvScript, expected) {
			t.Errorf("expected Python script to contain %q", expected)
		}
	}
}

func TestDetectPlatform_Python(t *testing.T) {
	if _, err := exec.LookPath("python"); err != nil {
		t.Skip("python not in PATH — skipping integration test")
	}
	checkCmd := exec.Command("python", "-c", "import importlib.metadata; importlib.metadata.version('ddtrace')")
	if err := checkCmd.Run(); err != nil {
		t.Skip("ddtrace not installed — skipping integration test")
	}

	viper.Reset()
	viper.Set("platform", "python")
	settings.Init()
	defer func() {
		viper.Reset()
		settings.Init()
	}()

	platform, err := DetectPlatform()
	if err != nil {
		t.Fatalf("DetectPlatform() unexpected error: %v", err)
	}
	if platform == nil {
		t.Fatal("expected non-nil platform")
	}
	if platform.Name() != "python" {
		t.Errorf("expected platform name 'python', got %q", platform.Name())
	}
}
