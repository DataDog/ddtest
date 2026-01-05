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
	// Capture env for assertions
	m.capturedEnvMap = envMap

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
	command, args, isRails := minitest.createDiscoveryCommand()

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

	if isRails {
		t.Error("expected Rails detection to be false for default Minitest discovery")
	}
}

func TestMinitest_createDiscoveryCommand_WithOverride(t *testing.T) {
	mockExecutor := &mockRailsCommandExecutor{isRails: false}
	minitest := &Minitest{executor: mockExecutor, commandOverride: []string{"./custom-minitest", "--flag"}}
	command, args, isRails := minitest.createDiscoveryCommand()

	if command != "./custom-minitest" {
		t.Errorf("expected command to be './custom-minitest', got %q", command)
	}
	if len(args) == 0 || args[0] != "--flag" {
		t.Errorf("expected args to start with '--flag', got %v", args)
	}
	if isRails {
		t.Error("expected Rails detection to be false when override is provided")
	}
}

func TestMinitest_DiscoverTests_Success(t *testing.T) {
	if err := os.MkdirAll(filepath.Dir(TestsDiscoveryFilePath), 0755); err != nil {
		t.Fatalf("failed to create discovery directory: %v", err)
	}
	defer cleanupDiscoveryDir()
	if err := os.MkdirAll("test/models", 0755); err != nil {
		t.Fatalf("failed to create test/models directory: %v", err)
	}
	if err := os.MkdirAll("test/controllers", 0755); err != nil {
		t.Fatalf("failed to create test/controllers directory: %v", err)
	}
	if err := os.WriteFile("test/models/user_test.rb", []byte("# test"), 0644); err != nil {
		t.Fatalf("failed to create user test file: %v", err)
	}
	if err := os.WriteFile("test/controllers/users_controller_test.rb", []byte("# test"), 0644); err != nil {
		t.Fatalf("failed to create controller test file: %v", err)
	}
	defer func() {
		_ = os.RemoveAll("test")
	}()

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
	if err := os.MkdirAll("test/sample", 0755); err != nil {
		t.Fatalf("failed to create test/sample directory: %v", err)
	}
	if err := os.WriteFile("test/sample/sample_test.rb", []byte("# test"), 0644); err != nil {
		t.Fatalf("failed to create sample test file: %v", err)
	}
	defer func() {
		_ = os.RemoveAll("test")
	}()

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
	if err := os.MkdirAll("test/sample", 0755); err != nil {
		t.Fatalf("failed to create test/sample directory: %v", err)
	}
	if err := os.WriteFile("test/sample/sample_test.rb", []byte("# test"), 0644); err != nil {
		t.Fatalf("failed to create sample test file: %v", err)
	}
	defer func() {
		_ = os.RemoveAll("test")
	}()

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

func TestMinitest_RunTests_WithOverride(t *testing.T) {
	testFiles := []string{"test/models/user_test.rb"}

	var capturedName string
	var capturedArgs []string
	mockExecutor := &mockRailsCommandExecutor{
		isRails: false,
		onTestExecution: func(name string, args []string) {
			capturedName = name
			capturedArgs = args
		},
	}

	minitest := &Minitest{executor: mockExecutor, commandOverride: []string{"./custom-minitest", "--flag"}}
	if err := minitest.RunTests(context.Background(), testFiles, nil); err != nil {
		t.Fatalf("RunTests failed: %v", err)
	}

	if capturedName != "./custom-minitest" {
		t.Fatalf("expected command './custom-minitest', got %q", capturedName)
	}
	if len(capturedArgs) == 0 || capturedArgs[0] != "--flag" {
		t.Errorf("expected args to start with '--flag', got %v", capturedArgs)
	}
	if mockExecutor.capturedEnvMap["TEST_FILES"] != testFiles[0] {
		t.Errorf("expected TEST_FILES env var to be %q, got %q", testFiles[0], mockExecutor.capturedEnvMap["TEST_FILES"])
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
	command, args, isRails := minitest.createDiscoveryCommand()

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

	if !isRails {
		t.Error("expected Rails detection to be true for rails discovery command")
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

func TestMinitest_DiscoverTestFiles_WithTestsLocation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "minitest-tests-location-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current directory: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to change to temp directory: %v", err)
	}
	defer cleanupDiscoveryDir()
	defer func() {
		_ = os.Chdir(originalDir)
	}()

	matchingFiles := []string{
		filepath.Join("custom", "test", "models", "user_test.rb"),
		filepath.Join("custom", "test", "controllers", "users_controller_test.rb"),
	}

	for _, file := range matchingFiles {
		dir := filepath.Dir(file)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("failed to create directory %s: %v", dir, err)
		}
		if err := os.WriteFile(file, []byte("# test"), 0644); err != nil {
			t.Fatalf("failed to create file %s: %v", file, err)
		}
	}

	ignoredFile := filepath.Join("test", "models", "ignored_test.rb")
	if err := os.MkdirAll(filepath.Dir(ignoredFile), 0755); err != nil {
		t.Fatalf("failed to create directory %s: %v", filepath.Dir(ignoredFile), err)
	}
	if err := os.WriteFile(ignoredFile, []byte("# ignored"), 0644); err != nil {
		t.Fatalf("failed to create file %s: %v", ignoredFile, err)
	}

	setTestsLocation(t, filepath.Join("custom", "test", "**", "*_test.rb"))

	minitest := NewMinitest()
	files, err := minitest.DiscoverTestFiles()
	if err != nil {
		t.Fatalf("DiscoverTestFiles failed: %v", err)
	}

	if len(files) != len(matchingFiles) {
		t.Errorf("expected %d matched files, got %d", len(matchingFiles), len(files))
	}

	for _, expected := range matchingFiles {
		if !slices.Contains(files, expected) {
			t.Errorf("expected file %q to be present", expected)
		}
	}

	if slices.Contains(files, ignoredFile) {
		t.Errorf("expected ignored file %q to be excluded", ignoredFile)
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

func TestMinitest_DiscoverTests_WithTestsLocation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "minitest-tests-location-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current directory: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to change to temp directory: %v", err)
	}
	defer cleanupDiscoveryDir()
	defer func() {
		_ = os.Chdir(originalDir)
	}()

	matchingFiles := []string{
		filepath.Join("custom", "test", "controllers", "users_controller_test.rb"),
		filepath.Join("custom", "test", "models", "user_test.rb"),
	}

	for _, file := range matchingFiles {
		dir := filepath.Dir(file)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("failed to create directory %s: %v", dir, err)
		}
		if err := os.WriteFile(file, []byte("# test"), 0644); err != nil {
			t.Fatalf("failed to create file %s: %v", file, err)
		}
	}

	setTestsLocation(t, filepath.Join("custom", "test", "**", "*_test.rb"))

	if err := os.MkdirAll(filepath.Dir(TestsDiscoveryFilePath), 0755); err != nil {
		t.Fatalf("failed to create discovery directory: %v", err)
	}

	testData := []testoptimization.Test{
		{
			Name:            "test_user_validation",
			Suite:           "UserTest",
			Module:          "minitest",
			Parameters:      "{}",
			SuiteSourceFile: matchingFiles[0],
		},
	}

	var capturedArgs []string
	mockExecutor := &mockRailsCommandExecutor{
		isRails: false,
		onTestExecution: func(name string, args []string) {
			capturedArgs = append([]string(nil), args...)
			file, err := os.Create(TestsDiscoveryFilePath)
			if err != nil {
				t.Fatalf("mock failed to create test file: %v", err)
			}
			defer func() { _ = file.Close() }()

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

	if len(capturedArgs) == 0 {
		t.Fatal("expected discovery command to capture arguments")
	}

	for _, file := range matchingFiles {
		if slices.Contains(capturedArgs, file) {
			t.Errorf("expected discovery args not to contain %q for non-Rails command", file)
		}
	}

	if mockExecutor.capturedEnvMap == nil {
		t.Fatal("expected env map to be captured")
	}

	if mockExecutor.capturedEnvMap["DD_TEST_OPTIMIZATION_DISCOVERY_ENABLED"] != "1" {
		t.Error("expected discovery env to include DD_TEST_OPTIMIZATION_DISCOVERY_ENABLED")
	}

	expectedPattern := filepath.Join("custom", "test", "**", "*_test.rb")
	if mockExecutor.capturedEnvMap["TEST"] != expectedPattern {
		t.Errorf("expected TEST to be %q, got %q", expectedPattern, mockExecutor.capturedEnvMap["TEST"])
	}
}

func TestMinitest_DiscoverTests_WithTestsLocation_Rails(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "minitest-tests-location-rails-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current directory: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to change to temp directory: %v", err)
	}
	defer cleanupDiscoveryDir()
	defer func() {
		_ = os.Chdir(originalDir)
	}()

	matchingFile := filepath.Join("custom", "test", "models", "user_test.rb")
	if err := os.MkdirAll(filepath.Dir(matchingFile), 0755); err != nil {
		t.Fatalf("failed to create directory %s: %v", filepath.Dir(matchingFile), err)
	}
	if err := os.WriteFile(matchingFile, []byte("# test"), 0644); err != nil {
		t.Fatalf("failed to create file %s: %v", matchingFile, err)
	}

	setTestsLocation(t, filepath.Join("custom", "test", "**", "*_test.rb"))

	if err := os.MkdirAll(filepath.Dir(TestsDiscoveryFilePath), 0755); err != nil {
		t.Fatalf("failed to create discovery directory: %v", err)
	}

	testData := []testoptimization.Test{
		{
			Name:            "test_user_validation",
			Suite:           "UserTest",
			Module:          "minitest",
			Parameters:      "{}",
			SuiteSourceFile: matchingFile,
		},
	}

	var capturedArgs []string
	mockExecutor := &mockRailsCommandExecutor{
		isRails: true,
		onTestExecution: func(name string, args []string) {
			capturedArgs = append([]string(nil), args...)
			file, err := os.Create(TestsDiscoveryFilePath)
			if err != nil {
				t.Fatalf("mock failed to create test file: %v", err)
			}
			defer func() { _ = file.Close() }()

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

	if len(capturedArgs) == 0 {
		t.Fatal("expected discovery command to capture arguments")
	}

	expectedPattern := filepath.Join("custom", "test", "**", "*_test.rb")
	if !slices.Contains(capturedArgs, expectedPattern) {
		t.Errorf("expected discovery args to include pattern %q, got %v", expectedPattern, capturedArgs)
	}

	if _, exists := mockExecutor.capturedEnvMap["TEST_FILES"]; exists {
		t.Error("expected TEST_FILES not to be set for Rails discovery")
	}

	if _, exists := mockExecutor.capturedEnvMap["TEST"]; exists {
		t.Error("expected TEST not to be set for Rails discovery")
	}
}

func TestMinitest_DiscoverTests_WithTestsLocation_NoMatches(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "minitest-tests-location-none-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current directory: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to change to temp directory: %v", err)
	}
	defer cleanupDiscoveryDir()
	defer func() {
		_ = os.Chdir(originalDir)
	}()

	setTestsLocation(t, filepath.Join("custom", "test", "**", "*_test.rb"))

	if err := os.MkdirAll(filepath.Dir(TestsDiscoveryFilePath), 0755); err != nil {
		t.Fatalf("failed to create discovery directory: %v", err)
	}

	var executed bool
	mockExecutor := &mockRailsCommandExecutor{
		onTestExecution: func(name string, args []string) {
			executed = true
			// Create empty discovery file when no tests match
			file, err := os.Create(TestsDiscoveryFilePath)
			if err != nil {
				t.Fatalf("mock failed to create test file: %v", err)
			}
			_ = file.Close()
		},
	}

	minitest := &Minitest{executor: mockExecutor}
	tests, err := minitest.DiscoverTests(context.Background())
	if err != nil {
		t.Fatalf("DiscoverTests should not fail when no matches: %v", err)
	}

	if len(tests) != 0 {
		t.Errorf("expected no tests when pattern matches nothing, got %d", len(tests))
	}

	if !executed {
		t.Error("expected discovery command to run even when pattern matches nothing")
	}
}

func TestMinitest_getMinitestCommand_WithBinRails(t *testing.T) {
	// Create a temporary bin/rails file
	if err := os.MkdirAll("bin", 0755); err != nil {
		t.Fatalf("failed to create bin directory: %v", err)
	}
	defer func() {
		_ = os.RemoveAll("bin")
	}()

	// Create an executable bin/rails file
	if err := os.WriteFile("bin/rails", []byte("#!/usr/bin/env ruby\n# test file"), 0755); err != nil {
		t.Fatalf("failed to create bin/rails: %v", err)
	}

	mockExecutor := &mockRailsCommandExecutor{
		isRails: true,
	}

	minitest := &Minitest{executor: mockExecutor}
	command, args, isRails := minitest.getMinitestCommand()

	if !isRails {
		t.Error("expected Rails to be detected")
	}
	if command != "bin/rails" {
		t.Errorf("expected command to be 'bin/rails', got %q", command)
	}
	expectedArgs := []string{"test"}
	if len(args) != len(expectedArgs) {
		t.Errorf("expected %d args, got %d: %v", len(expectedArgs), len(args), args)
	}
	for i, expected := range expectedArgs {
		if i >= len(args) || args[i] != expected {
			t.Errorf("expected args[%d] to be %q, got %q", i, expected, args[i])
		}
	}
}

func TestMinitest_getMinitestCommand_WithNonExecutableBinRails(t *testing.T) {
	// Create a temporary bin/rails file that is NOT executable
	if err := os.MkdirAll("bin", 0755); err != nil {
		t.Fatalf("failed to create bin directory: %v", err)
	}
	defer func() {
		_ = os.RemoveAll("bin")
	}()

	// Create a non-executable bin/rails file (0644 instead of 0755)
	if err := os.WriteFile("bin/rails", []byte("#!/usr/bin/env ruby\n# test file"), 0644); err != nil {
		t.Fatalf("failed to create bin/rails: %v", err)
	}

	mockExecutor := &mockRailsCommandExecutor{
		isRails: true,
	}

	minitest := &Minitest{executor: mockExecutor}
	command, args, isRails := minitest.getMinitestCommand()

	if !isRails {
		t.Error("expected Rails to be detected")
	}
	if command != "bundle" {
		t.Errorf("expected command to be 'bundle', got %q", command)
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
}

func TestMinitest_getMinitestCommand_WithoutBinRails(t *testing.T) {
	// Ensure bin/rails doesn't exist
	_ = os.RemoveAll("bin")

	mockExecutor := &mockRailsCommandExecutor{
		isRails: true,
	}

	minitest := &Minitest{executor: mockExecutor}
	command, args, isRails := minitest.getMinitestCommand()

	if !isRails {
		t.Error("expected Rails to be detected")
	}
	if command != "bundle" {
		t.Errorf("expected command to be 'bundle', got %q", command)
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
}

func TestMinitest_RunTests_RailsApplication_WithBinRails(t *testing.T) {
	// Create a temporary bin/rails file
	if err := os.MkdirAll("bin", 0755); err != nil {
		t.Fatalf("failed to create bin directory: %v", err)
	}
	defer func() {
		_ = os.RemoveAll("bin")
	}()

	// Create an executable bin/rails file
	if err := os.WriteFile("bin/rails", []byte("#!/usr/bin/env ruby\n# test file"), 0755); err != nil {
		t.Fatalf("failed to create bin/rails: %v", err)
	}

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

	// Verify the command structure uses "bin/rails test" instead of "bundle exec rails test"
	if capturedName != "bin/rails" {
		t.Errorf("expected 'bin/rails' as command, got %q", capturedName)
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

func TestMinitest_createDiscoveryCommand_RailsApplication_WithBinRails(t *testing.T) {
	// Create a temporary bin/rails file
	if err := os.MkdirAll("bin", 0755); err != nil {
		t.Fatalf("failed to create bin directory: %v", err)
	}
	defer func() {
		_ = os.RemoveAll("bin")
	}()

	// Create an executable bin/rails file
	if err := os.WriteFile("bin/rails", []byte("#!/usr/bin/env ruby\n# test file"), 0755); err != nil {
		t.Fatalf("failed to create bin/rails: %v", err)
	}

	mockExecutor := &mockRailsCommandExecutor{
		isRails: true,
	}

	minitest := &Minitest{executor: mockExecutor}
	command, args, isRails := minitest.createDiscoveryCommand()

	// Verify command structure: bin/rails test (Rails with bin/rails)
	if command != "bin/rails" {
		t.Errorf("expected command to be %q, got %q", "bin/rails", command)
	}

	expectedArgs := []string{"test"}
	if len(args) != len(expectedArgs) {
		t.Errorf("expected %d args, got %d: %v", len(expectedArgs), len(args), args)
	}
	for i, expected := range expectedArgs {
		if i >= len(args) || args[i] != expected {
			t.Errorf("expected args[%d] to be %q, got %q", i, expected, args[i])
		}
	}

	if !isRails {
		t.Error("expected Rails detection to be true when bin/rails is used for discovery")
	}
}

func TestMinitest_SetPlatformEnv(t *testing.T) {
	minitest := NewMinitest()

	platformEnv := map[string]string{
		"RUBYOPT": "-rbundler/setup -rdatadog/ci/auto_instrument",
	}
	minitest.SetPlatformEnv(platformEnv)

	if minitest.platformEnv["RUBYOPT"] != platformEnv["RUBYOPT"] {
		t.Errorf("expected platformEnv to be set, got %v", minitest.platformEnv)
	}
}

func TestMinitest_RunTests_UsesPlatformEnv(t *testing.T) {
	testFiles := []string{"test/models/user_test.rb"}

	mockExecutor := &mockRailsCommandExecutor{
		isRails: false,
	}

	minitest := &Minitest{executor: mockExecutor}

	// Set platform env
	platformEnv := map[string]string{
		"RUBYOPT": "-rbundler/setup -rdatadog/ci/auto_instrument",
	}
	minitest.SetPlatformEnv(platformEnv)

	err := minitest.RunTests(context.Background(), testFiles, nil)
	if err != nil {
		t.Fatalf("RunTests failed: %v", err)
	}

	// Verify platform env is passed to executor
	if mockExecutor.capturedEnvMap["RUBYOPT"] != platformEnv["RUBYOPT"] {
		t.Errorf("expected RUBYOPT to be %q, got %q", platformEnv["RUBYOPT"], mockExecutor.capturedEnvMap["RUBYOPT"])
	}
}

func TestMinitest_RunTests_MergesPlatformEnvWithPassedEnv(t *testing.T) {
	testFiles := []string{"test/models/user_test.rb"}

	mockExecutor := &mockRailsCommandExecutor{
		isRails: false,
	}

	minitest := &Minitest{executor: mockExecutor}

	// Set platform env
	platformEnv := map[string]string{
		"RUBYOPT":      "-rbundler/setup -rdatadog/ci/auto_instrument",
		"PLATFORM_VAR": "platform_value",
	}
	minitest.SetPlatformEnv(platformEnv)

	// Pass additional env vars
	additionalEnv := map[string]string{
		"RAILS_DB": "my_project_test_1",
		"TEST_ENV": "minitest",
	}

	err := minitest.RunTests(context.Background(), testFiles, additionalEnv)
	if err != nil {
		t.Fatalf("RunTests failed: %v", err)
	}

	// Verify platform env is present
	if mockExecutor.capturedEnvMap["RUBYOPT"] != platformEnv["RUBYOPT"] {
		t.Errorf("expected RUBYOPT to be %q, got %q", platformEnv["RUBYOPT"], mockExecutor.capturedEnvMap["RUBYOPT"])
	}
	if mockExecutor.capturedEnvMap["PLATFORM_VAR"] != platformEnv["PLATFORM_VAR"] {
		t.Errorf("expected PLATFORM_VAR to be %q, got %q", platformEnv["PLATFORM_VAR"], mockExecutor.capturedEnvMap["PLATFORM_VAR"])
	}

	// Verify additional env is present
	if mockExecutor.capturedEnvMap["RAILS_DB"] != additionalEnv["RAILS_DB"] {
		t.Errorf("expected RAILS_DB to be %q, got %q", additionalEnv["RAILS_DB"], mockExecutor.capturedEnvMap["RAILS_DB"])
	}
	if mockExecutor.capturedEnvMap["TEST_ENV"] != additionalEnv["TEST_ENV"] {
		t.Errorf("expected TEST_ENV to be %q, got %q", additionalEnv["TEST_ENV"], mockExecutor.capturedEnvMap["TEST_ENV"])
	}
}

func TestMinitest_RunTests_AdditionalEnvOverridesPlatformEnv(t *testing.T) {
	testFiles := []string{"test/models/user_test.rb"}

	mockExecutor := &mockRailsCommandExecutor{
		isRails: false,
	}

	minitest := &Minitest{executor: mockExecutor}

	// Set platform env with a value that will be overridden
	platformEnv := map[string]string{
		"RUBYOPT":     "-rbundler/setup -rdatadog/ci/auto_instrument",
		"SHARED_VAR":  "platform_value",
		"ANOTHER_VAR": "platform_another",
	}
	minitest.SetPlatformEnv(platformEnv)

	// Pass additional env that overrides SHARED_VAR
	additionalEnv := map[string]string{
		"SHARED_VAR": "additional_value",
	}

	err := minitest.RunTests(context.Background(), testFiles, additionalEnv)
	if err != nil {
		t.Fatalf("RunTests failed: %v", err)
	}

	// Verify SHARED_VAR is overridden by additional env
	if mockExecutor.capturedEnvMap["SHARED_VAR"] != additionalEnv["SHARED_VAR"] {
		t.Errorf("expected SHARED_VAR to be overridden to %q, got %q", additionalEnv["SHARED_VAR"], mockExecutor.capturedEnvMap["SHARED_VAR"])
	}

	// Verify non-overridden platform env values are preserved
	if mockExecutor.capturedEnvMap["RUBYOPT"] != platformEnv["RUBYOPT"] {
		t.Errorf("expected RUBYOPT to be preserved as %q, got %q", platformEnv["RUBYOPT"], mockExecutor.capturedEnvMap["RUBYOPT"])
	}
	if mockExecutor.capturedEnvMap["ANOTHER_VAR"] != platformEnv["ANOTHER_VAR"] {
		t.Errorf("expected ANOTHER_VAR to be preserved as %q, got %q", platformEnv["ANOTHER_VAR"], mockExecutor.capturedEnvMap["ANOTHER_VAR"])
	}
}

func TestMinitest_DiscoverTests_UsesPlatformEnv(t *testing.T) {
	if err := os.MkdirAll(filepath.Dir(TestsDiscoveryFilePath), 0755); err != nil {
		t.Fatalf("failed to create discovery directory: %v", err)
	}
	defer cleanupDiscoveryDir()
	if err := os.MkdirAll("test/models", 0755); err != nil {
		t.Fatalf("failed to create test/models directory: %v", err)
	}
	if err := os.WriteFile("test/models/user_test.rb", []byte("# test"), 0644); err != nil {
		t.Fatalf("failed to create user test file: %v", err)
	}
	defer func() {
		_ = os.RemoveAll("test")
	}()

	testData := []testoptimization.Test{
		{
			Name:            "test_user_validation",
			Suite:           "UserTest",
			Module:          "minitest",
			Parameters:      "{}",
			SuiteSourceFile: "test/models/user_test.rb",
		},
	}

	mockExecutor := &mockRailsCommandExecutor{
		isRails: false,
		onTestExecution: func(name string, args []string) {
			file, err := os.Create(TestsDiscoveryFilePath)
			if err != nil {
				t.Fatalf("mock failed to create test file: %v", err)
			}
			defer func() { _ = file.Close() }()

			encoder := json.NewEncoder(file)
			for _, test := range testData {
				if err := encoder.Encode(test); err != nil {
					t.Fatalf("mock failed to encode test data: %v", err)
				}
			}
		},
	}

	minitest := &Minitest{executor: mockExecutor}

	// Set platform env
	platformEnv := map[string]string{
		"RUBYOPT": "-rbundler/setup -rdatadog/ci/auto_instrument",
	}
	minitest.SetPlatformEnv(platformEnv)

	_, err := minitest.DiscoverTests(context.Background())
	if err != nil {
		t.Fatalf("DiscoverTests failed: %v", err)
	}

	// Verify platform env is passed to executor during discovery
	if mockExecutor.capturedEnvMap["RUBYOPT"] != platformEnv["RUBYOPT"] {
		t.Errorf("expected RUBYOPT to be %q, got %q", platformEnv["RUBYOPT"], mockExecutor.capturedEnvMap["RUBYOPT"])
	}

	// Verify framework-specific discovery env vars are present
	if mockExecutor.capturedEnvMap["DD_TEST_OPTIMIZATION_DISCOVERY_ENABLED"] != "1" {
		t.Error("expected DD_TEST_OPTIMIZATION_DISCOVERY_ENABLED to be set")
	}

	// Verify base discovery env vars are present
	if mockExecutor.capturedEnvMap["DD_CIVISIBILITY_ENABLED"] != "1" {
		t.Error("expected DD_CIVISIBILITY_ENABLED to be set")
	}
	if mockExecutor.capturedEnvMap["DD_CIVISIBILITY_AGENTLESS_ENABLED"] != "true" {
		t.Error("expected DD_CIVISIBILITY_AGENTLESS_ENABLED to be set")
	}
	if mockExecutor.capturedEnvMap["DD_API_KEY"] != "dummy_key" {
		t.Error("expected DD_API_KEY to be set")
	}
}

func TestMinitest_RunTests_RailsApplication_UsesPlatformEnv(t *testing.T) {
	testFiles := []string{"test/models/user_test.rb"}

	mockExecutor := &mockRailsCommandExecutor{
		isRails: true,
	}

	minitest := &Minitest{executor: mockExecutor}

	// Set platform env
	platformEnv := map[string]string{
		"RUBYOPT": "-rbundler/setup -rdatadog/ci/auto_instrument",
	}
	minitest.SetPlatformEnv(platformEnv)

	err := minitest.RunTests(context.Background(), testFiles, nil)
	if err != nil {
		t.Fatalf("RunTests failed: %v", err)
	}

	// Verify platform env is passed to executor for Rails
	if mockExecutor.capturedEnvMap["RUBYOPT"] != platformEnv["RUBYOPT"] {
		t.Errorf("expected RUBYOPT to be %q, got %q", platformEnv["RUBYOPT"], mockExecutor.capturedEnvMap["RUBYOPT"])
	}
}
