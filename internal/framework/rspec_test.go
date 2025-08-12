package framework

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/DataDog/datadog-test-runner/internal/ext"
	"github.com/DataDog/datadog-test-runner/internal/testoptimization"
)

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

func TestRSpec_CreateDiscoveryCommand(t *testing.T) {
	rspec := NewRSpec()
	cmd := rspec.CreateDiscoveryCommand()

	expectedArgs := []string{"bundle", "exec", "rspec", "--format", "progress", "--dry-run"}
	if len(cmd.Args) != len(expectedArgs) {
		t.Errorf("expected %d args, got %d", len(expectedArgs), len(cmd.Args))
	}

	for i, expected := range expectedArgs {
		if i >= len(cmd.Args) || cmd.Args[i] != expected {
			t.Errorf("expected arg[%d] to be %q, got %q", i, expected, cmd.Args[i])
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

func TestRSpec_DiscoverTests_Success(t *testing.T) {
	if err := os.MkdirAll(filepath.Dir(TestsDiscoveryFilePath), 0755); err != nil {
		t.Fatalf("failed to create discovery directory: %v", err)
	}
	defer func() {
		os.RemoveAll(filepath.Dir(TestsDiscoveryFilePath))
	}()

	testData := []testoptimization.Test{
		{
			FQN:        "spec/models/user_spec.rb[1:1]",
			Name:       "User should be valid",
			Suite:      "User",
			SourceFile: "spec/models/user_spec.rb",
		},
		{
			FQN:        "spec/controllers/users_controller_spec.rb[1:1]",
			Name:       "UsersController GET index should return success",
			Suite:      "UsersController",
			SourceFile: "spec/controllers/users_controller_spec.rb",
		},
	}

	mockExecutor := &mockCommandExecutor{
		output: []byte("Finished in 0.12345 seconds (files took 0.67890 seconds to load)"),
		err:    nil,
		onExecution: func(cmd *exec.Cmd) {
			// Verify the command is correct
			expectedArgs := []string{"bundle", "exec", "rspec", "--format", "progress", "--dry-run"}
			if len(cmd.Args) != len(expectedArgs) {
				t.Errorf("expected %d args, got %d", len(expectedArgs), len(cmd.Args))
			}
			for i, expected := range expectedArgs {
				if i >= len(cmd.Args) || cmd.Args[i] != expected {
					t.Errorf("expected arg[%d] to be %q, got %q", i, expected, cmd.Args[i])
				}
			}

			// Create the test file as the real command would
			file, err := os.Create(TestsDiscoveryFilePath)
			if err != nil {
				t.Fatalf("mock failed to create test file: %v", err)
			}
			defer file.Close()

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
	defer func() {
		os.RemoveAll(filepath.Dir(TestsDiscoveryFilePath))
	}()

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
	defer func() {
		os.RemoveAll(filepath.Dir(TestsDiscoveryFilePath))
	}()

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
