package runner

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization"
)

func TestTestRunner_PrepareTestOptimization_Success(t *testing.T) {
	ctx := context.Background()

	// Setup mocks
	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		Tests: []testoptimization.Test{
			{Suite: "TestSuite1", Name: "test1", Parameters: "", SuiteSourceFile: "test/file1_test.rb"},
			{Suite: "TestSuite1", Name: "test2", Parameters: "", SuiteSourceFile: "test/file1_test.rb"},
			{Suite: "TestSuite2", Name: "test3", Parameters: "", SuiteSourceFile: "test/file2_test.rb"},
			{Suite: "TestSuite3", Name: "test4", Parameters: "", SuiteSourceFile: "test/file3_test.rb"},
		},
	}

	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags: map[string]string{
			"platform": "ruby",
			"version":  "3.0",
		},
		Framework: mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{
		Platform: mockPlatform,
	}

	mockOptimizationClient := &MockTestOptimizationClient{
		SkippableTests: map[string]bool{
			"TestSuite1.test2.": true, // Skip test2
			"TestSuite3.test4.": true, // Skip test4
		},
	}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, newDefaultMockCIProviderDetector())

	err := runner.PrepareTestOptimization(ctx)

	if err != nil {
		t.Errorf("PrepareTestOptimization() should not return error, got: %v", err)
	}

	// Verify optimization client was initialized
	if !mockOptimizationClient.InitializeCalled {
		t.Error("PrepareTestOptimization() should initialize optimization client")
	}

	// Verify tags were passed to optimization client
	if mockOptimizationClient.Tags["platform"] != "ruby" {
		t.Error("PrepareTestOptimization() should pass platform tags to optimization client")
	}

	// Verify optimization client was shut down
	if !mockOptimizationClient.ShutdownCalled {
		t.Error("PrepareTestOptimization() should shutdown optimization client")
	}

	// Verify test files were calculated correctly (should include file1 and file2, but not file3)
	expectedFiles := map[string]bool{
		"test/file1_test.rb": true, // test1 is not skipped
		"test/file2_test.rb": true, // test3 is not skipped
	}

	if len(runner.testFiles) != 2 {
		t.Errorf("PrepareTestOptimization() should result in 2 test files, got %d", len(runner.testFiles))
	}

	for file := range runner.testFiles {
		if !expectedFiles[file] {
			t.Errorf("Unexpected test file: %s", file)
		}
	}

	// Verify skippable percentage was calculated correctly (2 out of 4 tests skipped = 50%)
	expectedPercentage := 50.0
	if runner.skippablePercentage != expectedPercentage {
		t.Errorf("PrepareTestOptimization() should calculate skippable percentage as %.2f, got %.2f",
			expectedPercentage, runner.skippablePercentage)
	}
}

func TestTestRunner_PrepareTestOptimization_PlatformDetectionError(t *testing.T) {
	ctx := context.Background()

	mockPlatformDetector := &MockPlatformDetector{
		Err: errors.New("platform detection failed"),
	}

	mockOptimizationClient := &MockTestOptimizationClient{}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, newDefaultMockCIProviderDetector())

	err := runner.PrepareTestOptimization(ctx)

	if err == nil {
		t.Error("PrepareTestOptimization() should return error when platform detection fails")
	}

	expectedMsg := "failed to detect platform"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("PrepareTestOptimization() error should contain '%s', got: %v", expectedMsg, err)
	}
}

func TestTestRunner_PrepareTestOptimization_TagsCreationError(t *testing.T) {
	ctx := context.Background()

	mockPlatform := &MockPlatform{
		TagsErr: errors.New("tags creation failed"),
	}

	mockPlatformDetector := &MockPlatformDetector{
		Platform: mockPlatform,
	}

	mockOptimizationClient := &MockTestOptimizationClient{}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, newDefaultMockCIProviderDetector())

	err := runner.PrepareTestOptimization(ctx)

	if err == nil {
		t.Error("PrepareTestOptimization() should return error when tags creation fails")
	}

	expectedMsg := "failed to create platform tags"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("PrepareTestOptimization() error should contain '%s', got: %v", expectedMsg, err)
	}
}

func TestTestRunner_PrepareTestOptimization_OptimizationClientInitError(t *testing.T) {
	ctx := context.Background()

	mockFramework := &MockFramework{
		Tests: []testoptimization.Test{
			{Suite: "", Name: "test1", Parameters: "", SuiteSourceFile: "file1.rb"},
		},
	}

	mockPlatform := &MockPlatform{
		Tags:      map[string]string{"platform": "ruby"},
		Framework: mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{
		Platform: mockPlatform,
	}

	mockOptimizationClient := &MockTestOptimizationClient{
		InitializeErr: errors.New("client initialization failed"),
	}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, newDefaultMockCIProviderDetector())

	err := runner.PrepareTestOptimization(ctx)

	if err == nil {
		t.Error("PrepareTestOptimization() should return error when optimization client initialization fails")
	}

	expectedMsg := "failed to initialize optimization client"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("PrepareTestOptimization() error should contain '%s', got: %v", expectedMsg, err)
	}
}

func TestTestRunner_PrepareTestOptimization_FrameworkDetectionError(t *testing.T) {
	ctx := context.Background()

	mockPlatform := &MockPlatform{
		Tags:         map[string]string{"platform": "ruby"},
		FrameworkErr: errors.New("framework detection failed"),
	}

	mockPlatformDetector := &MockPlatformDetector{
		Platform: mockPlatform,
	}

	mockOptimizationClient := &MockTestOptimizationClient{}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, newDefaultMockCIProviderDetector())

	err := runner.PrepareTestOptimization(ctx)

	if err == nil {
		t.Error("PrepareTestOptimization() should return error when framework detection fails")
	}

	expectedMsg := "failed to detect framework"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("PrepareTestOptimization() error should contain '%s', got: %v", expectedMsg, err)
	}
}

func TestTestRunner_PrepareTestOptimization_TestDiscoveryError(t *testing.T) {
	ctx := context.Background()

	mockFramework := &MockFramework{
		Err: errors.New("test discovery failed"),
	}

	mockPlatform := &MockPlatform{
		Tags:      map[string]string{"platform": "ruby"},
		Framework: mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{
		Platform: mockPlatform,
	}

	mockOptimizationClient := &MockTestOptimizationClient{}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, newDefaultMockCIProviderDetector())

	err := runner.PrepareTestOptimization(ctx)

	if err == nil {
		t.Error("PrepareTestOptimization() should return error when test discovery fails")
	}

	expectedMsg := "test discovery failed"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("PrepareTestOptimization() error should contain '%s', got: %v", expectedMsg, err)
	}
}

func TestTestRunner_PrepareTestOptimization_EmptyTests(t *testing.T) {
	ctx := context.Background()

	mockFramework := &MockFramework{
		Tests: []testoptimization.Test{}, // Empty test list
	}

	mockPlatform := &MockPlatform{
		Tags:      map[string]string{"platform": "ruby"},
		Framework: mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{Platform: mockPlatform}
	mockOptimizationClient := &MockTestOptimizationClient{SkippableTests: map[string]bool{}}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, newDefaultMockCIProviderDetector())

	err := runner.PrepareTestOptimization(ctx)

	if err != nil {
		t.Errorf("PrepareTestOptimization() should handle empty tests, got: %v", err)
	}

	if len(runner.testFiles) != 0 {
		t.Errorf("PrepareTestOptimization() should result in 0 test files for empty tests, got %d", len(runner.testFiles))
	}

	// Division by zero should be handled gracefully
	if runner.skippablePercentage != 0.0 {
		t.Logf("Skippable percentage for empty tests: %f", runner.skippablePercentage)
	}
}

func TestTestRunner_PrepareTestOptimization_AllTestsSkipped(t *testing.T) {
	ctx := context.Background()

	mockFramework := &MockFramework{
		Tests: []testoptimization.Test{
			{Suite: "", Name: "test1", Parameters: "", SuiteSourceFile: "file1.rb"},
			{Suite: "", Name: "test2", Parameters: "", SuiteSourceFile: "file2.rb"},
		},
	}

	mockPlatform := &MockPlatform{
		Tags:      map[string]string{"platform": "ruby"},
		Framework: mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{Platform: mockPlatform}
	mockOptimizationClient := &MockTestOptimizationClient{
		SkippableTests: map[string]bool{
			".test1.": true,
			".test2.": true,
		},
	}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, newDefaultMockCIProviderDetector())

	err := runner.PrepareTestOptimization(ctx)

	if err != nil {
		t.Errorf("PrepareTestOptimization() should handle all tests skipped, got: %v", err)
	}

	if len(runner.testFiles) != 0 {
		t.Errorf("PrepareTestOptimization() should result in 0 test files when all tests are skipped, got %d", len(runner.testFiles))
	}

	if runner.skippablePercentage != 100.0 {
		t.Errorf("PrepareTestOptimization() should calculate 100%% skippable when all tests are skipped, got %.2f", runner.skippablePercentage)
	}
}

func TestTestRunner_PrepareTestOptimization_RuntimeTagsOverride(t *testing.T) {
	ctx := context.Background()

	// Set runtime tags override via environment variable - only override some tags
	overrideTags := `{"os.platform":"linux","runtime.version":"3.2.0"}`
	_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_RUNTIME_TAGS", overrideTags)
	defer func() {
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_RUNTIME_TAGS")
	}()

	// Reinitialize settings to pick up the environment variable
	settings.Init()

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		Tests: []testoptimization.Test{
			{Suite: "TestSuite1", Name: "test1", Parameters: "", SuiteSourceFile: "test/file1_test.rb"},
		},
	}

	// Platform tags should have more tags than the override
	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags: map[string]string{
			"os.platform":     "darwin",
			"os.architecture": "arm64",
			"runtime.name":    "ruby",
			"runtime.version": "3.3.0",
			"language":        "ruby",
		},
		Framework: mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{
		Platform: mockPlatform,
	}

	mockOptimizationClient := &MockTestOptimizationClient{
		SkippableTests: map[string]bool{},
	}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, newDefaultMockCIProviderDetector())

	err := runner.PrepareTestOptimization(ctx)

	if err != nil {
		t.Errorf("PrepareTestOptimization() should not return error, got: %v", err)
	}

	// Verify optimization client was initialized
	if !mockOptimizationClient.InitializeCalled {
		t.Error("PrepareTestOptimization() should initialize optimization client")
	}

	// Check that override tags replaced the detected values
	if mockOptimizationClient.Tags["os.platform"] != "linux" {
		t.Errorf("Expected os.platform to be 'linux' from override, got %q", mockOptimizationClient.Tags["os.platform"])
	}

	if mockOptimizationClient.Tags["runtime.version"] != "3.2.0" {
		t.Errorf("Expected runtime.version to be '3.2.0' from override, got %q", mockOptimizationClient.Tags["runtime.version"])
	}

	// Check that detected tags NOT in override were preserved
	if mockOptimizationClient.Tags["os.architecture"] != "arm64" {
		t.Errorf("Expected os.architecture to be 'arm64' from detected tags (not overridden), got %q", mockOptimizationClient.Tags["os.architecture"])
	}

	if mockOptimizationClient.Tags["runtime.name"] != "ruby" {
		t.Errorf("Expected runtime.name to be 'ruby' from detected tags (not overridden), got %q", mockOptimizationClient.Tags["runtime.name"])
	}

	if mockOptimizationClient.Tags["language"] != "ruby" {
		t.Errorf("Expected language to be 'ruby' from detected tags (not overridden), got %q", mockOptimizationClient.Tags["language"])
	}
}

func TestTestRunner_PrepareTestOptimization_RuntimeTagsOverrideInvalidJSON(t *testing.T) {
	ctx := context.Background()

	// Set invalid JSON as runtime tags override
	_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_RUNTIME_TAGS", `{invalid json}`)
	defer func() {
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_RUNTIME_TAGS")
	}()

	// Reinitialize settings to pick up the environment variable
	settings.Init()

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		Tests:         []testoptimization.Test{},
	}

	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags:         map[string]string{"platform": "ruby"},
		Framework:    mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{
		Platform: mockPlatform,
	}

	mockOptimizationClient := &MockTestOptimizationClient{}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, newDefaultMockCIProviderDetector())

	err := runner.PrepareTestOptimization(ctx)

	if err == nil {
		t.Error("PrepareTestOptimization() should return error when runtime tags JSON is invalid")
	}

	expectedMsg := "failed to parse runtime tags override"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("PrepareTestOptimization() error should contain '%s', got: %v", expectedMsg, err)
	}

	// Optimization client should not be initialized when there's a parse error
	if mockOptimizationClient.InitializeCalled {
		t.Error("PrepareTestOptimization() should not initialize optimization client when runtime tags JSON is invalid")
	}
}

func TestTestRunner_PrepareTestOptimization_NoRuntimeTagsOverride(t *testing.T) {
	ctx := context.Background()

	// Ensure no runtime tags override is set
	_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_RUNTIME_TAGS")

	// Reinitialize settings to ensure clean state
	settings.Init()

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		Tests: []testoptimization.Test{
			{Suite: "TestSuite1", Name: "test1", Parameters: "", SuiteSourceFile: "test/file1_test.rb"},
		},
	}

	// Platform tags that should be used when no override is provided
	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags: map[string]string{
			"os.platform":     "darwin",
			"runtime.version": "3.3.0",
			"language":        "ruby",
		},
		Framework: mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{
		Platform: mockPlatform,
	}

	mockOptimizationClient := &MockTestOptimizationClient{
		SkippableTests: map[string]bool{},
	}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, newDefaultMockCIProviderDetector())

	err := runner.PrepareTestOptimization(ctx)

	if err != nil {
		t.Errorf("PrepareTestOptimization() should not return error, got: %v", err)
	}

	// Verify optimization client was initialized with platform tags
	if !mockOptimizationClient.InitializeCalled {
		t.Error("PrepareTestOptimization() should initialize optimization client")
	}

	// Check that platform tags were used (not override)
	if mockOptimizationClient.Tags["os.platform"] != "darwin" {
		t.Errorf("Expected os.platform to be 'darwin' from platform, got %q", mockOptimizationClient.Tags["os.platform"])
	}

	if mockOptimizationClient.Tags["runtime.version"] != "3.3.0" {
		t.Errorf("Expected runtime.version to be '3.3.0' from platform, got %q", mockOptimizationClient.Tags["runtime.version"])
	}
}
