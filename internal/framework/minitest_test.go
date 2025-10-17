package framework

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/DataDog/ddtest/internal/testoptimization"
)

func TestNewMinitest(t *testing.T) {
	minitest := NewMinitest()
	if minitest == nil {
		t.Error("NewMinitest() returned nil")
		return
	}
	if minitest.executor == nil {
		t.Error("NewMinitest() created Minitest with nil executor")
		return
	}
}

func TestMinitest_Name(t *testing.T) {
	minitest := NewMinitest()
	expected := "minitest"
	actual := minitest.Name()

	if actual != expected {
		t.Errorf("expected %q, got %q", expected, actual)
	}
}

func TestMinitest_createDiscoveryCommand(t *testing.T) {
	minitest := NewMinitest()
	cmd := minitest.createDiscoveryCommand()

	// Verify command structure: bundle exec rake test
	expectedArgs := []string{"bundle", "exec", "rake", "test"}
	if len(cmd.Args) != len(expectedArgs) {
		t.Errorf("expected %d args, got %d: %v", len(expectedArgs), len(cmd.Args), cmd.Args)
	}
	for i, expected := range expectedArgs {
		if i >= len(cmd.Args) || cmd.Args[i] != expected {
			t.Errorf("expected args[%d] to be %q, got %q", i, expected, cmd.Args[i])
		}
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

func TestMinitest_DiscoverTests_Success(t *testing.T) {
	if err := os.MkdirAll(filepath.Dir(TestsDiscoveryFilePath), 0755); err != nil {
		t.Fatalf("failed to create discovery directory: %v", err)
	}
	defer cleanupDiscoveryDir()

	testData := []testoptimization.Test{
		{
			Name:            "test_user_validation",
			Suite:           "UserTest",
			Module:          "minitest",
			Parameters:      "{}",
			SuiteSourceFile: "test/models/user_test.rb",
		},
		{
			Name:            "test_get_index",
			Suite:           "UsersControllerTest",
			Module:          "minitest",
			Parameters:      "{}",
			SuiteSourceFile: "test/controllers/users_controller_test.rb",
		},
	}

	mockExecutor := &mockCommandExecutor{
		output: []byte("Finished in 0.12345 seconds"),
		err:    nil,
		onExecution: func(cmd *exec.Cmd) {
			// Verify the command structure
			if len(cmd.Args) < 4 {
				t.Errorf("expected at least 4 args, got %d", len(cmd.Args))
			}
			if !slices.Contains(cmd.Args, "bundle") {
				t.Error("expected 'bundle' in arguments")
			}
			if !slices.Contains(cmd.Args, "exec") {
				t.Error("expected 'exec' in arguments")
			}
			if !slices.Contains(cmd.Args, "rake") {
				t.Error("expected 'rake' in arguments")
			}
			if !slices.Contains(cmd.Args, "test") {
				t.Error("expected 'test' in arguments")
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

	minitest := &Minitest{executor: mockExecutor}

	tests, err := minitest.DiscoverTests()
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

func TestMinitest_DiscoverTests_CommandFailure(t *testing.T) {
	if err := os.MkdirAll(filepath.Dir(TestsDiscoveryFilePath), 0755); err != nil {
		t.Fatalf("failed to create discovery directory: %v", err)
	}
	defer cleanupDiscoveryDir()

	mockExecutor := &mockCommandExecutor{
		output:      []byte("Could not locate Gemfile or .bundle/ directory"),
		err:         &exec.ExitError{},
		onExecution: func(cmd *exec.Cmd) {},
	}

	minitest := &Minitest{executor: mockExecutor}

	tests, err := minitest.DiscoverTests()
	if err == nil {
		t.Error("expected error when command fails")
	}
	if tests != nil {
		t.Error("expected nil tests when command fails")
	}
}

func TestMinitest_DiscoverTests_InvalidJSON(t *testing.T) {
	if err := os.MkdirAll(filepath.Dir(TestsDiscoveryFilePath), 0755); err != nil {
		t.Fatalf("failed to create discovery directory: %v", err)
	}
	defer cleanupDiscoveryDir()

	mockExecutor := &mockCommandExecutor{
		output: []byte("Finished in 0.12345 seconds"),
		err:    nil,
		onExecution: func(cmd *exec.Cmd) {
			// Create invalid JSON file as the real command would (simulating corrupted output)
			if err := os.WriteFile(TestsDiscoveryFilePath, []byte(`{invalid json}`), 0644); err != nil {
				t.Fatalf("mock failed to write invalid JSON: %v", err)
			}
		},
	}

	minitest := &Minitest{executor: mockExecutor}

	tests, err := minitest.DiscoverTests()
	if err == nil {
		t.Error("expected error when JSON is invalid")
	}
	if tests != nil {
		t.Error("expected nil tests when JSON is invalid")
	}
}

func TestMinitest_RunTests(t *testing.T) {
	testFiles := []string{"test/models/user_test.rb", "test/controllers/users_controller_test.rb"}

	var capturedCmd *exec.Cmd
	mockExecutor := &mockCommandExecutor{
		err: nil, // Simulate successful execution
		onExecution: func(cmd *exec.Cmd) {
			capturedCmd = cmd
		},
	}

	minitest := &Minitest{executor: mockExecutor}
	err := minitest.RunTests(testFiles, nil)

	if err != nil {
		t.Fatalf("RunTests failed: %v", err)
	}

	if capturedCmd == nil {
		t.Fatal("Expected command to be executed but none was captured")
	}

	// Verify the command structure
	if !slices.Contains(capturedCmd.Args, "bundle") {
		t.Error("expected 'bundle' in arguments")
	}
	if !slices.Contains(capturedCmd.Args, "exec") {
		t.Error("expected 'exec' in arguments")
	}
	if !slices.Contains(capturedCmd.Args, "rake") {
		t.Error("expected 'rake' in arguments")
	}
	if !slices.Contains(capturedCmd.Args, "test") {
		t.Error("expected 'test' in arguments")
	}

	// Verify test files are included
	for _, testFile := range testFiles {
		if !slices.Contains(capturedCmd.Args, testFile) {
			t.Errorf("expected test file %q in arguments", testFile)
		}
	}
}

func TestMinitest_RunTestsWithEnvMap(t *testing.T) {
	testFiles := []string{"test/models/user_test.rb"}
	envMap := map[string]string{
		"RAILS_DB": "my_project_test_1",
		"TEST_ENV": "minitest",
	}

	var capturedCmd *exec.Cmd
	mockExecutor := &mockCommandExecutor{
		err: nil,
		onExecution: func(cmd *exec.Cmd) {
			capturedCmd = cmd
		},
	}

	minitest := &Minitest{executor: mockExecutor}
	err := minitest.RunTests(testFiles, envMap)

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
		if env == "TEST_ENV=minitest" {
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

func TestMinitest_RunTests_NoTestFiles(t *testing.T) {
	var capturedCmd *exec.Cmd
	mockExecutor := &mockCommandExecutor{
		err: nil,
		onExecution: func(cmd *exec.Cmd) {
			capturedCmd = cmd
		},
	}

	minitest := &Minitest{executor: mockExecutor}
	err := minitest.RunTests([]string{}, nil)

	if err != nil {
		t.Fatalf("RunTests failed: %v", err)
	}

	if capturedCmd == nil {
		t.Fatal("Expected command to be executed but none was captured")
	}

	// Should still have the basic command structure
	expectedArgs := []string{"bundle", "exec", "rake", "test"}
	if len(capturedCmd.Args) != len(expectedArgs) {
		t.Errorf("expected %d args, got %d: %v", len(expectedArgs), len(capturedCmd.Args), capturedCmd.Args)
	}
}
