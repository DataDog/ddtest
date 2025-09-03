package runner

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DataDog/datadog-test-runner/internal/testoptimization"
)

func TestTestRunner_PrepareTestOptimization_Success(t *testing.T) {
	ctx := context.Background()

	// Setup mocks
	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		Tests: []testoptimization.Test{
			{FQN: "TestSuite1.test1", SourceFile: "test/file1_test.rb", SuiteSourceFile: "test/file1_test.rb"},
			{FQN: "TestSuite1.test2", SourceFile: "test/file1_test.rb", SuiteSourceFile: "test/file1_test.rb"},
			{FQN: "TestSuite2.test3", SourceFile: "test/file2_test.rb", SuiteSourceFile: "test/file2_test.rb"},
			{FQN: "TestSuite3.test4", SourceFile: "test/file3_test.rb", SuiteSourceFile: "test/file3_test.rb"},
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
			"TestSuite1.test2": true, // Skip test2
			"TestSuite3.test4": true, // Skip test4
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
			{FQN: "test1", SourceFile: "file1.rb", SuiteSourceFile: "file1.rb"},
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
	if runner.skippablePercentage != 0.0 && runner.skippablePercentage == runner.skippablePercentage { // NaN != NaN
		t.Logf("Skippable percentage for empty tests: %f", runner.skippablePercentage)
	}
}

func TestTestRunner_PrepareTestOptimization_AllTestsSkipped(t *testing.T) {
	ctx := context.Background()

	mockFramework := &MockFramework{
		Tests: []testoptimization.Test{
			{FQN: "test1", SourceFile: "file1.rb", SuiteSourceFile: "file1.rb"},
			{FQN: "test2", SourceFile: "file2.rb", SuiteSourceFile: "file2.rb"},
		},
	}

	mockPlatform := &MockPlatform{
		Tags:      map[string]string{"platform": "ruby"},
		Framework: mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{Platform: mockPlatform}
	mockOptimizationClient := &MockTestOptimizationClient{
		SkippableTests: map[string]bool{
			"test1": true,
			"test2": true,
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
