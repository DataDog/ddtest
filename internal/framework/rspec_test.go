package framework

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/testoptimization"
)

func cleanupDiscoveryDir() {
	_ = os.RemoveAll(filepath.Dir(filepath.Dir(TestsDiscoveryFilePath)))
}

type mockCommandExecutor struct {
	output         []byte
	err            error
	onExecution    func(name string, args []string) // Called when command is executed
	capturedEnvMap map[string]string                // Captured environment map from Run calls
}

func (m *mockCommandExecutor) CombinedOutput(ctx context.Context, name string, args []string, envMap map[string]string) ([]byte, error) {
	if m.onExecution != nil {
		m.onExecution(name, args)
	}
	return m.output, m.err
}

func (m *mockCommandExecutor) Run(ctx context.Context, name string, args []string, envMap map[string]string) error {
	// Capture the envMap for test assertions
	m.capturedEnvMap = envMap
	if m.onExecution != nil {
		m.onExecution(name, args)
	}
	return m.err
}

func TestNewRSpec(t *testing.T) {
	rspec := NewRSpec()
	if rspec == nil {
		t.Error("NewRSpec() returned nil")
		return
	}
	if rspec.executor == nil {
		t.Error("NewRSpec() created RSpec with nil executor")
		return
	}

	// Verify it's using the default executor
	if _, ok := rspec.executor.(*ext.DefaultCommandExecutor); !ok {
		t.Error("NewRSpec() should use DefaultCommandExecutor")
	}
}

func TestRSpec_Name(t *testing.T) {
	rspec := NewRSpec()
	expected := "rspec"
	actual := rspec.Name()

	if actual != expected {
		t.Errorf("expected %q, got %q", expected, actual)
	}
}

func TestRSpec_getRSpecCommand_WithBinRSpec(t *testing.T) {
	// Create a temporary bin/rspec file
	if err := os.MkdirAll("bin", 0755); err != nil {
		t.Fatalf("failed to create bin directory: %v", err)
	}
	defer func() {
		_ = os.RemoveAll("bin")
	}()

	// Create an executable bin/rspec file
	if err := os.WriteFile("bin/rspec", []byte("#!/usr/bin/env ruby\n# test file"), 0755); err != nil {
		t.Fatalf("failed to create bin/rspec: %v", err)
	}

	rspec := NewRSpec()
	command, baseArgs := rspec.getRSpecCommand()

	if command != "bin/rspec" {
		t.Errorf("expected command to be 'bin/rspec', got %q", command)
	}
	if len(baseArgs) != 0 {
		t.Errorf("expected baseArgs to be empty, got %v", baseArgs)
	}
}

func TestRSpec_getRSpecCommand_WithNonExecutableBinRSpec(t *testing.T) {
	// Create a temporary bin/rspec file that is NOT executable
	if err := os.MkdirAll("bin", 0755); err != nil {
		t.Fatalf("failed to create bin directory: %v", err)
	}
	defer func() {
		_ = os.RemoveAll("bin")
	}()

	// Create a non-executable bin/rspec file (0644 instead of 0755)
	if err := os.WriteFile("bin/rspec", []byte("#!/usr/bin/env ruby\n# test file"), 0644); err != nil {
		t.Fatalf("failed to create bin/rspec: %v", err)
	}

	rspec := NewRSpec()
	command, baseArgs := rspec.getRSpecCommand()

	if command != "bundle" {
		t.Errorf("expected command to be 'bundle', got %q", command)
	}
	expectedBaseArgs := []string{"exec", "rspec"}
	if len(baseArgs) != len(expectedBaseArgs) {
		t.Errorf("expected %d baseArgs, got %d", len(expectedBaseArgs), len(baseArgs))
	}
	for i, expected := range expectedBaseArgs {
		if i >= len(baseArgs) || baseArgs[i] != expected {
			t.Errorf("expected baseArgs[%d] to be %q, got %q", i, expected, baseArgs[i])
		}
	}
}

func TestRSpec_getRSpecCommand_WithoutBinRSpec(t *testing.T) {
	// Ensure bin/rspec doesn't exist
	_ = os.RemoveAll("bin")

	rspec := NewRSpec()
	command, baseArgs := rspec.getRSpecCommand()

	if command != "bundle" {
		t.Errorf("expected command to be 'bundle', got %q", command)
	}
	expectedBaseArgs := []string{"exec", "rspec"}
	if len(baseArgs) != len(expectedBaseArgs) {
		t.Errorf("expected %d baseArgs, got %d", len(expectedBaseArgs), len(baseArgs))
	}
	for i, expected := range expectedBaseArgs {
		if i >= len(baseArgs) || baseArgs[i] != expected {
			t.Errorf("expected baseArgs[%d] to be %q, got %q", i, expected, baseArgs[i])
		}
	}
}

func TestRSpec_createDiscoveryCommand(t *testing.T) {
	_ = os.RemoveAll("bin")

	rspec := NewRSpec()
	command, args, envMap := rspec.createDiscoveryCommand()

	// Verify command contains necessary arguments
	if !slices.Contains(args, "--format") {
		t.Error("expected --format argument")
	}
	if !slices.Contains(args, "progress") {
		t.Error("expected progress argument")
	}
	if !slices.Contains(args, "--dry-run") {
		t.Error("expected --dry-run argument")
	}

	// Verify command is "bundle" with "rspec" in args
	if command != "bundle" {
		t.Errorf("expected command to be 'bundle', got %q", command)
	}
	if !slices.Contains(args, "rspec") {
		t.Errorf("expected 'rspec' in arguments, got: %v", args)
	}

	// Verify environment variables
	if envMap["DD_TEST_OPTIMIZATION_DISCOVERY_ENABLED"] != "1" {
		t.Error("expected DD_TEST_OPTIMIZATION_DISCOVERY_ENABLED=1 in envMap")
	}
	if envMap["DD_TEST_OPTIMIZATION_DISCOVERY_FILE"] != TestsDiscoveryFilePath {
		t.Errorf("expected DD_TEST_OPTIMIZATION_DISCOVERY_FILE=%q in envMap, got %q", TestsDiscoveryFilePath, envMap["DD_TEST_OPTIMIZATION_DISCOVERY_FILE"])
	}
}

func TestRSpec_DiscoverTests_Success(t *testing.T) {
	_ = os.RemoveAll("bin")

	if err := os.MkdirAll(filepath.Dir(TestsDiscoveryFilePath), 0755); err != nil {
		t.Fatalf("failed to create discovery directory: %v", err)
	}
	defer cleanupDiscoveryDir()

	testData := []testoptimization.Test{
		{
			Name:            "User should be valid",
			Suite:           "User",
			Module:          "rspec",
			Parameters:      "{a: 1}",
			SuiteSourceFile: "spec/models/user_spec.rb",
		},
		{
			Name:            "UsersController GET index should return success",
			Suite:           "UsersController",
			Module:          "rspec",
			Parameters:      "{a: 2}",
			SuiteSourceFile: "spec/controllers/users_controller_spec.rb",
		},
	}

	mockExecutor := &mockCommandExecutor{
		output: []byte("Finished in 0.12345 seconds (files took 0.67890 seconds to load)"),
		err:    nil,
		onExecution: func(name string, args []string) {
			// Verify the command has necessary arguments
			if !slices.Contains(args, "--format") {
				t.Error("expected --format argument")
			}
			if !slices.Contains(args, "progress") {
				t.Error("expected progress argument")
			}
			if !slices.Contains(args, "--dry-run") {
				t.Error("expected --dry-run argument")
			}

			// Create the test file as the real command would
			file, err := os.Create(TestsDiscoveryFilePath)
			if err != nil {
				t.Fatalf("mock failed to create test file: %v", err)
			}
			defer func() {
				_ = file.Close()
			}()

			encoder := json.NewEncoder(file)
			for _, test := range testData {
				if err := encoder.Encode(test); err != nil {
					t.Fatalf("mock failed to encode test data: %v", err)
				}
			}
		},
	}

	rspec := &RSpec{executor: mockExecutor}

	tests, err := rspec.DiscoverTests(context.Background())
	if err != nil {
		t.Fatalf("DiscoverTests failed: %v", err)
	}

	if len(tests) != len(testData) {
		t.Errorf("expected %d tests, got %d", len(testData), len(tests))
	}

	for i, expected := range testData {
		if i >= len(tests) {
			t.Errorf("missing test at index %d", i)
			continue
		}
		actual := tests[i]
		if actual.Parameters != expected.Parameters {
			t.Errorf("test[%d].Parameters: expected %q, got %q", i, expected.Parameters, actual.Parameters)
		}
		if actual.Name != expected.Name {
			t.Errorf("test[%d].Name: expected %q, got %q", i, expected.Name, actual.Name)
		}
		if actual.Suite != expected.Suite {
			t.Errorf("test[%d].Suite: expected %q, got %q", i, expected.Suite, actual.Suite)
		}
		if actual.Module != expected.Module {
			t.Errorf("test[%d].Module: expected %q, got %q", i, expected.Module, actual.Module)
		}
		if actual.SuiteSourceFile != expected.SuiteSourceFile {
			t.Errorf("test[%d].SuiteSourceFile: expected %q, got %q", i, expected.SuiteSourceFile, actual.SuiteSourceFile)
		}
	}
}

func TestRSpec_DiscoverTests_CommandFailure(t *testing.T) {
	if err := os.MkdirAll(filepath.Dir(TestsDiscoveryFilePath), 0755); err != nil {
		t.Fatalf("failed to create discovery directory: %v", err)
	}
	defer cleanupDiscoveryDir()

	mockExecutor := &mockCommandExecutor{
		output:      []byte("Could not locate Gemfile or .bundle/ directory"),
		err:         &exec.ExitError{},
		onExecution: func(name string, args []string) {},
	}

	rspec := &RSpec{executor: mockExecutor}

	tests, err := rspec.DiscoverTests(context.Background())
	if err == nil {
		t.Error("expected error when command fails")
	}
	if tests != nil {
		t.Error("expected nil tests when command fails")
	}
}

func TestRSpec_DiscoverTests_InvalidJSON(t *testing.T) {
	if err := os.MkdirAll(filepath.Dir(TestsDiscoveryFilePath), 0755); err != nil {
		t.Fatalf("failed to create discovery directory: %v", err)
	}
	defer cleanupDiscoveryDir()

	mockExecutor := &mockCommandExecutor{
		output: []byte("Finished in 0.12345 seconds (files took 0.67890 seconds to load)"),
		err:    nil,
		onExecution: func(name string, args []string) {
			// Create invalid JSON file as the real command would (simulating corrupted output)
			if err := os.WriteFile(TestsDiscoveryFilePath, []byte(`{invalid json}`), 0644); err != nil {
				t.Fatalf("mock failed to write invalid JSON: %v", err)
			}
		},
	}

	rspec := &RSpec{executor: mockExecutor}

	tests, err := rspec.DiscoverTests(context.Background())
	if err == nil {
		t.Error("expected error when JSON is invalid")
	}
	if tests != nil {
		t.Error("expected nil tests when JSON is invalid")
	}
}

func TestRSpec_RunTests(t *testing.T) {
	// Ensure bin/rspec doesn't exist to have predictable behavior
	_ = os.RemoveAll("bin")

	testFiles := []string{"spec/models/user_spec.rb", "spec/controllers/users_controller_spec.rb"}

	var capturedName string
	var capturedArgs []string
	mockExecutor := &mockCommandExecutor{
		err: nil, // Simulate successful execution
		onExecution: func(name string, args []string) {
			capturedName = name
			capturedArgs = args
		},
	}

	rspec := &RSpec{executor: mockExecutor}
	err := rspec.RunTests(context.Background(), testFiles, nil)

	if err != nil {
		t.Fatalf("RunTests failed: %v", err)
	}

	if capturedName == "" {
		t.Fatal("Expected command to be executed but none was captured")
	}

	// Verify the command has necessary arguments
	if !slices.Contains(capturedArgs, "--format") {
		t.Error("expected --format argument")
	}
	if !slices.Contains(capturedArgs, "progress") {
		t.Error("expected progress argument")
	}
	// Verify test files are included
	for _, testFile := range testFiles {
		if !slices.Contains(capturedArgs, testFile) {
			t.Errorf("expected test file %q in arguments", testFile)
		}
	}
}

func TestRSpec_RunTestsWithEnvMap(t *testing.T) {
	// Ensure bin/rspec doesn't exist to have predictable behavior
	_ = os.RemoveAll("bin")

	testFiles := []string{"spec/models/user_spec.rb"}
	envMap := map[string]string{
		"RAILS_DB": "my_project_test_1",
		"TEST_ENV": "rspec",
	}

	mockExecutor := &mockCommandExecutor{
		err: nil,
	}

	rspec := &RSpec{executor: mockExecutor}
	err := rspec.RunTests(context.Background(), testFiles, envMap)

	if err != nil {
		t.Fatalf("RunTests failed: %v", err)
	}

	// Verify environment variables are set
	if mockExecutor.capturedEnvMap["RAILS_DB"] != "my_project_test_1" {
		t.Error("Expected RAILS_DB environment variable to be set")
	}
	if mockExecutor.capturedEnvMap["TEST_ENV"] != "rspec" {
		t.Error("Expected TEST_ENV environment variable to be set")
	}
}

func TestRSpec_createDiscoveryCommand_WithBinRSpec(t *testing.T) {
	// Create a temporary bin/rspec file
	if err := os.MkdirAll("bin", 0755); err != nil {
		t.Fatalf("failed to create bin directory: %v", err)
	}
	defer func() {
		_ = os.RemoveAll("bin")
	}()

	// Create an executable bin/rspec file
	if err := os.WriteFile("bin/rspec", []byte("#!/usr/bin/env ruby\n# test file"), 0755); err != nil {
		t.Fatalf("failed to create bin/rspec: %v", err)
	}

	rspec := NewRSpec()
	command, args, _ := rspec.createDiscoveryCommand()

	// Verify command uses bin/rspec
	if command != "bin/rspec" {
		t.Errorf("expected command to use bin/rspec, got %q", command)
	}

	// Verify necessary arguments are present
	if !slices.Contains(args, "--format") {
		t.Error("expected --format argument")
	}
	if !slices.Contains(args, "progress") {
		t.Error("expected progress argument")
	}
	if !slices.Contains(args, "--dry-run") {
		t.Error("expected --dry-run argument")
	}
}

func TestRSpec_RunTests_WithBinRSpec(t *testing.T) {
	// Create a temporary bin/rspec file
	if err := os.MkdirAll("bin", 0755); err != nil {
		t.Fatalf("failed to create bin directory: %v", err)
	}
	defer func() {
		_ = os.RemoveAll("bin")
	}()

	// Create an executable bin/rspec file
	if err := os.WriteFile("bin/rspec", []byte("#!/usr/bin/env ruby\n# test file"), 0755); err != nil {
		t.Fatalf("failed to create bin/rspec: %v", err)
	}

	testFiles := []string{"spec/models/user_spec.rb"}

	var capturedName string
	var capturedArgs []string
	mockExecutor := &mockCommandExecutor{
		err: nil,
		onExecution: func(name string, args []string) {
			capturedName = name
			capturedArgs = args
		},
	}

	rspec := &RSpec{executor: mockExecutor}
	err := rspec.RunTests(context.Background(), testFiles, nil)

	if err != nil {
		t.Fatalf("RunTests failed: %v", err)
	}

	if capturedName == "" {
		t.Fatal("Expected command to be executed but none was captured")
	}

	// Verify command uses bin/rspec
	if capturedName != "bin/rspec" {
		t.Errorf("expected command to use bin/rspec, got %v", capturedName)
	}

	// Verify test files are included
	for _, testFile := range testFiles {
		if !slices.Contains(capturedArgs, testFile) {
			t.Errorf("expected test file %q in arguments", testFile)
		}
	}
}

func TestRSpec_DiscoverTestFiles(t *testing.T) {
	// Create a temporary fake RSpec project
	tmpDir, err := os.MkdirTemp("", "rspec-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	// Save current directory and change to temp directory
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current directory: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to change to temp directory: %v", err)
	}
	defer func() {
		_ = os.Chdir(originalDir)
	}()

	// Create fake RSpec project structure
	testFiles := []string{
		"spec/models/user_spec.rb",
		"spec/controllers/users_controller_spec.rb",
		"spec/helpers/application_helper_spec.rb",
		"spec/lib/utils_spec.rb",
	}
	// Non-matching files that should be ignored
	nonTestFiles := []string{
		"spec/support/helper.rb",
		"spec/factories/users.rb",
		"spec/spec_helper.rb",
	}

	for _, file := range append(testFiles, nonTestFiles...) {
		dir := filepath.Dir(file)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("failed to create directory %s: %v", dir, err)
		}
		if err := os.WriteFile(file, []byte("# test content"), 0644); err != nil {
			t.Fatalf("failed to create file %s: %v", file, err)
		}
	}

	rspec := NewRSpec()
	discoveredFiles, err := rspec.DiscoverTestFiles()

	if err != nil {
		t.Fatalf("DiscoverTestFiles failed: %v", err)
	}

	// Verify all test files were found
	if len(discoveredFiles) != len(testFiles) {
		t.Errorf("expected %d test files, got %d", len(testFiles), len(discoveredFiles))
	}

	// Verify each expected test file was found
	for _, expectedFile := range testFiles {
		if !slices.Contains(discoveredFiles, expectedFile) {
			t.Errorf("expected test file %q not found in discovered files", expectedFile)
		}
	}

	// Verify non-test files were not included
	for _, nonTestFile := range nonTestFiles {
		if slices.Contains(discoveredFiles, nonTestFile) {
			t.Errorf("non-test file %q should not be in discovered files", nonTestFile)
		}
	}
}

func TestRSpec_DiscoverTestFiles_NoSpecDirectory(t *testing.T) {
	// Create a temporary directory without a spec folder
	tmpDir, err := os.MkdirTemp("", "rspec-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	// Save current directory and change to temp directory
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current directory: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to change to temp directory: %v", err)
	}
	defer func() {
		_ = os.Chdir(originalDir)
	}()

	rspec := NewRSpec()
	discoveredFiles, err := rspec.DiscoverTestFiles()

	if err != nil {
		t.Fatalf("DiscoverTestFiles failed: %v", err)
	}

	// Should return empty slice when spec directory doesn't exist
	if len(discoveredFiles) != 0 {
		t.Errorf("expected 0 test files, got %d", len(discoveredFiles))
	}
}
