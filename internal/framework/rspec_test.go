package framework

import (
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
	output      []byte
	err         error
	onExecution func(cmd *exec.Cmd) // Called when command is executed
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
	cmd := rspec.createDiscoveryCommand()

	// Verify command contains necessary arguments
	argsStr := filepath.Join(cmd.Args...)
	if !slices.Contains(cmd.Args, "--format") {
		t.Error("expected --format argument")
	}
	if !slices.Contains(cmd.Args, "progress") {
		t.Error("expected progress argument")
	}
	if !slices.Contains(cmd.Args, "--dry-run") {
		t.Error("expected --dry-run argument")
	}

	// Verify it contains rspec in some form
	if !slices.ContainsFunc(cmd.Args, func(arg string) bool {
		return arg == "rspec" || arg == "bin/rspec"
	}) {
		t.Errorf("expected 'rspec' or 'bin/rspec' in arguments, got: %v", argsStr)
	}

	expectedEnv := []string{
		"DD_TEST_OPTIMIZATION_DISCOVERY_ENABLED=1",
		"DD_TEST_OPTIMIZATION_DISCOVERY_FILE=" + TestsDiscoveryFilePath,
	}

	for _, expected := range expectedEnv {
		if !slices.Contains(cmd.Env, expected) {
			t.Errorf("expected %q in environment", expected)
		}
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
			FQN:             "spec/models/user_spec.rb[1:1]",
			Name:            "User should be valid",
			Suite:           "User",
			SourceFile:      "spec/models/user_spec.rb",
			SuiteSourceFile: "spec/models/user_spec.rb",
		},
		{
			FQN:             "spec/controllers/users_controller_spec.rb[1:1]",
			Name:            "UsersController GET index should return success",
			Suite:           "UsersController",
			SourceFile:      "spec/controllers/users_controller_spec.rb",
			SuiteSourceFile: "spec/controllers/users_controller_spec.rb",
		},
	}

	mockExecutor := &mockCommandExecutor{
		output: []byte("Finished in 0.12345 seconds (files took 0.67890 seconds to load)"),
		err:    nil,
		onExecution: func(cmd *exec.Cmd) {
			// Verify the command has necessary arguments
			if !slices.Contains(cmd.Args, "--format") {
				t.Error("expected --format argument")
			}
			if !slices.Contains(cmd.Args, "progress") {
				t.Error("expected progress argument")
			}
			if !slices.Contains(cmd.Args, "--dry-run") {
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

	tests, err := rspec.DiscoverTests()
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
		if actual.FQN != expected.FQN {
			t.Errorf("test[%d].FQN: expected %q, got %q", i, expected.FQN, actual.FQN)
		}
		if actual.Name != expected.Name {
			t.Errorf("test[%d].Name: expected %q, got %q", i, expected.Name, actual.Name)
		}
		if actual.Suite != expected.Suite {
			t.Errorf("test[%d].Suite: expected %q, got %q", i, expected.Suite, actual.Suite)
		}
		if actual.SourceFile != expected.SourceFile {
			t.Errorf("test[%d].SourceFile: expected %q, got %q", i, expected.SourceFile, actual.SourceFile)
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
		onExecution: func(cmd *exec.Cmd) {},
	}

	rspec := &RSpec{executor: mockExecutor}

	tests, err := rspec.DiscoverTests()
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
		onExecution: func(cmd *exec.Cmd) {
			// Create invalid JSON file as the real command would (simulating corrupted output)
			if err := os.WriteFile(TestsDiscoveryFilePath, []byte(`{invalid json}`), 0644); err != nil {
				t.Fatalf("mock failed to write invalid JSON: %v", err)
			}
		},
	}

	rspec := &RSpec{executor: mockExecutor}

	tests, err := rspec.DiscoverTests()
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

	var capturedCmd *exec.Cmd
	mockExecutor := &mockCommandExecutor{
		err: nil, // Simulate successful execution
		onExecution: func(cmd *exec.Cmd) {
			capturedCmd = cmd
		},
	}

	rspec := &RSpec{executor: mockExecutor}
	err := rspec.RunTests(testFiles, nil)

	if err != nil {
		t.Fatalf("RunTests failed: %v", err)
	}

	if capturedCmd == nil {
		t.Fatal("Expected command to be executed but none was captured")
	}

	// Verify the command has necessary arguments
	if !slices.Contains(capturedCmd.Args, "--format") {
		t.Error("expected --format argument")
	}
	if !slices.Contains(capturedCmd.Args, "progress") {
		t.Error("expected progress argument")
	}
	// Verify test files are included
	for _, testFile := range testFiles {
		if !slices.Contains(capturedCmd.Args, testFile) {
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

	var capturedCmd *exec.Cmd
	mockExecutor := &mockCommandExecutor{
		err: nil,
		onExecution: func(cmd *exec.Cmd) {
			capturedCmd = cmd
		},
	}

	rspec := &RSpec{executor: mockExecutor}
	err := rspec.RunTests(testFiles, envMap)

	if err != nil {
		t.Fatalf("RunTests failed: %v", err)
	}

	if capturedCmd == nil {
		t.Fatal("Expected command to be executed but none was captured")
	}

	// Verify environment variables are set
	foundRailsDb := false
	foundTestEnv := false
	for _, env := range capturedCmd.Env {
		if env == "RAILS_DB=my_project_test_1" {
			foundRailsDb = true
		}
		if env == "TEST_ENV=rspec" {
			foundTestEnv = true
		}
	}

	if !foundRailsDb {
		t.Error("Expected RAILS_DB environment variable to be set")
	}
	if !foundTestEnv {
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
	cmd := rspec.createDiscoveryCommand()

	// Verify command uses bin/rspec
	if len(cmd.Args) < 1 || cmd.Args[0] != "bin/rspec" {
		t.Errorf("expected command to use bin/rspec, got %v", cmd.Args)
	}

	// Verify necessary arguments are present
	if !slices.Contains(cmd.Args, "--format") {
		t.Error("expected --format argument")
	}
	if !slices.Contains(cmd.Args, "progress") {
		t.Error("expected progress argument")
	}
	if !slices.Contains(cmd.Args, "--dry-run") {
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

	var capturedCmd *exec.Cmd
	mockExecutor := &mockCommandExecutor{
		err: nil,
		onExecution: func(cmd *exec.Cmd) {
			capturedCmd = cmd
		},
	}

	rspec := &RSpec{executor: mockExecutor}
	err := rspec.RunTests(testFiles, nil)

	if err != nil {
		t.Fatalf("RunTests failed: %v", err)
	}

	if capturedCmd == nil {
		t.Fatal("Expected command to be executed but none was captured")
	}

	// Verify command uses bin/rspec
	if len(capturedCmd.Args) < 1 || capturedCmd.Args[0] != "bin/rspec" {
		t.Errorf("expected command to use bin/rspec, got %v", capturedCmd.Args)
	}

	// Verify test files are included
	for _, testFile := range testFiles {
		if !slices.Contains(capturedCmd.Args, testFile) {
			t.Errorf("expected test file %q in arguments", testFile)
		}
	}
}
