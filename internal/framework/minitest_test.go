package framework

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/DataDog/ddtest/internal/testoptimization"
)

// mockRailsCommandExecutor is a mock that can handle Rails detection commands
type mockRailsCommandExecutor struct {
	isRails         bool
	onTestExecution func(name string, args []string)
	railsGemPath    string            // Optional: custom path for rails gem, defaults to temp dir
	capturedEnvMap  map[string]string // Captured environment map from Run calls
}

func (m *mockRailsCommandExecutor) CombinedOutput(ctx context.Context, name string, args []string, envMap map[string]string) ([]byte, error) {
	// Check if this is a Rails detection command
	if name == "bundle" && len(args) >= 2 && slices.Contains(args, "show") && slices.Contains(args, "rails") {
		// bundle show rails
		if m.isRails {
			// Return a valid file path that exists
			railsPath := m.railsGemPath
			if railsPath == "" {
				// Create a temporary directory to simulate rails gem path
				tmpDir := filepath.Join(os.TempDir(), "rails-gem-mock")
				_ = os.MkdirAll(tmpDir, 0755)
				railsPath = tmpDir
			}
			return []byte(railsPath), nil
		}
		return []byte("Could not locate Gemfile"), &exec.ExitError{}
	}
	if name == "bundle" && len(args) >= 3 && slices.Contains(args, "rails") && slices.Contains(args, "version") {
		// bundle exec rails version
		if m.isRails {
			return []byte("Rails 7.0.0"), nil
		}
		return []byte("Rails is not currently installed"), &exec.ExitError{}
	}

	// This is the actual test command
	if m.onTestExecution != nil {
		m.onTestExecution(name, args)
	}
	return []byte("Finished in 0.12345 seconds"), nil
}

func (m *mockRailsCommandExecutor) Run(ctx context.Context, name string, args []string, envMap map[string]string) error {
	// Capture the envMap for test assertions
	m.capturedEnvMap = envMap
	if m.onTestExecution != nil {
		m.onTestExecution(name, args)
	}
	return nil
}

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
	mockExecutor := &mockRailsCommandExecutor{
		isRails: false,
	}

	minitest := &Minitest{executor: mockExecutor}
	command, args, envMap := minitest.createDiscoveryCommand()

	// Verify command structure: bundle exec rake test (non-Rails)
	if command != "bundle" {
		t.Errorf("expected command to be %q, got %q", "bundle", command)
	}

	expectedArgs := []string{"exec", "rake", "test"}
	if len(args) != len(expectedArgs) {
		t.Errorf("expected %d args, got %d: %v", len(expectedArgs), len(args), args)
	}
	for i, expected := range expectedArgs {
		if i >= len(args) || args[i] != expected {
			t.Errorf("expected args[%d] to be %q, got %q", i, expected, args[i])
		}
	}

	// Verify environment variables
	if envMap["DD_TEST_OPTIMIZATION_DISCOVERY_ENABLED"] != "1" {
		t.Error("expected DD_TEST_OPTIMIZATION_DISCOVERY_ENABLED=1 in envMap")
	}
	if envMap["DD_TEST_OPTIMIZATION_DISCOVERY_FILE"] != TestsDiscoveryFilePath {
		t.Errorf("expected DD_TEST_OPTIMIZATION_DISCOVERY_FILE=%q in envMap, got %q", TestsDiscoveryFilePath, envMap["DD_TEST_OPTIMIZATION_DISCOVERY_FILE"])
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

	mockExecutor := &mockRailsCommandExecutor{
		isRails: false,
		onTestExecution: func(name string, args []string) {
			// Verify the command structure
			if name != "bundle" {
				t.Errorf("expected 'bundle' as command, got %q", name)
			}
			if !slices.Contains(args, "exec") {
				t.Error("expected 'exec' in arguments")
			}
			if !slices.Contains(args, "rake") {
				t.Error("expected 'rake' in arguments")
			}
			if !slices.Contains(args, "test") {
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

	tests, err := minitest.DiscoverTests(context.Background())
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
		onExecution: func(name string, args []string) {},
	}

	minitest := &Minitest{executor: mockExecutor}

	tests, err := minitest.DiscoverTests(context.Background())
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
		onExecution: func(name string, args []string) {
			// Create invalid JSON file as the real command would (simulating corrupted output)
			if err := os.WriteFile(TestsDiscoveryFilePath, []byte(`{invalid json}`), 0644); err != nil {
				t.Fatalf("mock failed to write invalid JSON: %v", err)
			}
		},
	}

	minitest := &Minitest{executor: mockExecutor}

	tests, err := minitest.DiscoverTests(context.Background())
	if err == nil {
		t.Error("expected error when JSON is invalid")
	}
	if tests != nil {
		t.Error("expected nil tests when JSON is invalid")
	}
}

func TestMinitest_RunTests(t *testing.T) {
	testFiles := []string{"test/models/user_test.rb", "test/controllers/users_controller_test.rb"}

	var capturedName string
	var capturedArgs []string
	mockExecutor := &mockRailsCommandExecutor{
		isRails: false,
		onTestExecution: func(name string, args []string) {
			capturedName = name
			capturedArgs = args
		},
	}

	minitest := &Minitest{executor: mockExecutor}
	err := minitest.RunTests(context.Background(), testFiles, nil)

	if err != nil {
		t.Fatalf("RunTests failed: %v", err)
	}

	if capturedName == "" {
		t.Fatal("Expected command to be executed but none was captured")
	}

	// Verify the command structure
	if capturedName != "bundle" {
		t.Errorf("expected 'bundle' as command, got %q", capturedName)
	}
	if !slices.Contains(capturedArgs, "exec") {
		t.Error("expected 'exec' in arguments")
	}
	if !slices.Contains(capturedArgs, "rake") {
		t.Error("expected 'rake' in arguments")
	}
	if !slices.Contains(capturedArgs, "test") {
		t.Error("expected 'test' in arguments")
	}

	// For rake test, test files should NOT be in command-line arguments
	for _, testFile := range testFiles {
		if slices.Contains(capturedArgs, testFile) {
			t.Errorf("test file %q should not be in arguments for rake test", testFile)
		}
	}

	// For rake test, test files should be in TEST_FILES environment variable
	expectedTestFiles := "test/models/user_test.rb test/controllers/users_controller_test.rb"
	if mockExecutor.capturedEnvMap["TEST_FILES"] != expectedTestFiles {
		t.Errorf("Expected TEST_FILES=%q in environment, got %q", expectedTestFiles, mockExecutor.capturedEnvMap["TEST_FILES"])
	}
}

func TestMinitest_RunTestsWithEnvMap(t *testing.T) {
	testFiles := []string{"test/models/user_test.rb"}
	envMap := map[string]string{
		"RAILS_DB": "my_project_test_1",
		"TEST_ENV": "minitest",
	}

	mockExecutor := &mockRailsCommandExecutor{
		isRails: false,
	}

	minitest := &Minitest{executor: mockExecutor}
	err := minitest.RunTests(context.Background(), testFiles, envMap)

	if err != nil {
		t.Fatalf("RunTests failed: %v", err)
	}

	// Verify environment variables are set
	if mockExecutor.capturedEnvMap["RAILS_DB"] != "my_project_test_1" {
		t.Error("Expected RAILS_DB environment variable to be set")
	}
	if mockExecutor.capturedEnvMap["TEST_ENV"] != "minitest" {
		t.Error("Expected TEST_ENV environment variable to be set")
	}
	if mockExecutor.capturedEnvMap["TEST_FILES"] != "test/models/user_test.rb" {
		t.Error("Expected TEST_FILES environment variable to be set")
	}
}

func TestMinitest_RunTests_NoTestFiles(t *testing.T) {
	var capturedName string
	var capturedArgs []string
	mockExecutor := &mockRailsCommandExecutor{
		isRails: false,
		onTestExecution: func(name string, args []string) {
			capturedName = name
			capturedArgs = args
		},
	}

	minitest := &Minitest{executor: mockExecutor}
	err := minitest.RunTests(context.Background(), []string{}, nil)

	if err != nil {
		t.Fatalf("RunTests failed: %v", err)
	}

	if capturedName == "" {
		t.Fatal("Expected command to be executed but none was captured")
	}

	// Should still have the basic command structure
	if capturedName != "bundle" {
		t.Errorf("expected 'bundle' as command, got %q", capturedName)
	}
	expectedArgs := []string{"exec", "rake", "test"}
	if len(capturedArgs) != len(expectedArgs) {
		t.Errorf("expected %d args, got %d: %v", len(expectedArgs), len(capturedArgs), capturedArgs)
	}

	// Should not have TEST_FILES environment variable when no test files
	if _, exists := mockExecutor.capturedEnvMap["TEST_FILES"]; exists {
		t.Error("TEST_FILES should not be set when no test files provided")
	}
}

func TestMinitest_isRailsApplication_RailsDetected(t *testing.T) {
	mockExecutor := &mockRailsCommandExecutor{
		isRails: true,
	}

	minitest := &Minitest{executor: mockExecutor}
	isRails := minitest.isRailsApplication()

	if !isRails {
		t.Error("expected Rails to be detected")
	}
}

func TestMinitest_isRailsApplication_NoRails(t *testing.T) {
	mockExecutor := &mockRailsCommandExecutor{
		isRails: false,
	}

	minitest := &Minitest{executor: mockExecutor}
	isRails := minitest.isRailsApplication()

	if isRails {
		t.Error("expected Rails not to be detected")
	}
}

func TestMinitest_RunTests_RailsApplication(t *testing.T) {
	testFiles := []string{"test/models/user_test.rb"}

	var capturedName string
	var capturedArgs []string
	mockExecutor := &mockRailsCommandExecutor{
		isRails: true,
		onTestExecution: func(name string, args []string) {
			capturedName = name
			capturedArgs = args
		},
	}

	minitest := &Minitest{executor: mockExecutor}
	err := minitest.RunTests(context.Background(), testFiles, nil)

	if err != nil {
		t.Fatalf("RunTests failed: %v", err)
	}

	if capturedName == "" {
		t.Fatal("Expected command to be executed but none was captured")
	}

	// Verify the command structure uses "rails test" instead of "rake test"
	if capturedName != "bundle" {
		t.Errorf("expected 'bundle' as command, got %q", capturedName)
	}
	if !slices.Contains(capturedArgs, "exec") {
		t.Error("expected 'exec' in arguments")
	}
	if !slices.Contains(capturedArgs, "rails") {
		t.Error("expected 'rails' in arguments")
	}
	if !slices.Contains(capturedArgs, "test") {
		t.Error("expected 'test' in arguments")
	}

	// Verify test files are included as command-line arguments
	for _, testFile := range testFiles {
		if !slices.Contains(capturedArgs, testFile) {
			t.Errorf("expected test file %q in arguments", testFile)
		}
	}

	// For Rails test, TEST_FILES should NOT be set
	if _, exists := mockExecutor.capturedEnvMap["TEST_FILES"]; exists {
		t.Error("TEST_FILES should not be set for rails test (files should be in command-line args)")
	}
}

func TestMinitest_createDiscoveryCommand_RailsApplication(t *testing.T) {
	mockExecutor := &mockRailsCommandExecutor{
		isRails: true,
	}

	minitest := &Minitest{executor: mockExecutor}
	command, args, envMap := minitest.createDiscoveryCommand()

	// Verify command structure: bundle exec rails test (Rails)
	if command != "bundle" {
		t.Errorf("expected command to be %q, got %q", "bundle", command)
	}

	expectedArgs := []string{"exec", "rails", "test"}
	if len(args) != len(expectedArgs) {
		t.Errorf("expected %d args, got %d: %v", len(expectedArgs), len(args), args)
	}
	for i, expected := range expectedArgs {
		if i >= len(args) || args[i] != expected {
			t.Errorf("expected args[%d] to be %q, got %q", i, expected, args[i])
		}
	}

	// Verify environment variables
	if envMap["DD_TEST_OPTIMIZATION_DISCOVERY_ENABLED"] != "1" {
		t.Error("expected DD_TEST_OPTIMIZATION_DISCOVERY_ENABLED=1 in envMap")
	}
	if envMap["DD_TEST_OPTIMIZATION_DISCOVERY_FILE"] != TestsDiscoveryFilePath {
		t.Errorf("expected DD_TEST_OPTIMIZATION_DISCOVERY_FILE=%q in envMap, got %q", TestsDiscoveryFilePath, envMap["DD_TEST_OPTIMIZATION_DISCOVERY_FILE"])
	}
}

func TestMinitest_DiscoverTestFiles(t *testing.T) {
	// Create a temporary fake Minitest project
	tmpDir, err := os.MkdirTemp("", "minitest-test-*")
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

	// Create fake Minitest project structure
	testFiles := []string{
		"test/models/user_test.rb",
		"test/controllers/users_controller_test.rb",
		"test/integration/login_test.rb",
		"test/lib/utils_test.rb",
	}
	// Non-matching files that should be ignored
	nonTestFiles := []string{
		"test/test_helper.rb",
		"test/support/helper.rb",
		"test/fixtures/users.yml",
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

	minitest := NewMinitest()
	discoveredFiles, err := minitest.DiscoverTestFiles()

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

func TestMinitest_DiscoverTestFiles_NoTestDirectory(t *testing.T) {
	// Create a temporary directory without a test folder
	tmpDir, err := os.MkdirTemp("", "minitest-test-*")
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

	minitest := NewMinitest()
	discoveredFiles, err := minitest.DiscoverTestFiles()

	if err != nil {
		t.Fatalf("DiscoverTestFiles failed: %v", err)
	}

	// Should return empty slice when test directory doesn't exist
	if len(discoveredFiles) != 0 {
		t.Errorf("expected 0 test files, got %d", len(discoveredFiles))
	}
}
