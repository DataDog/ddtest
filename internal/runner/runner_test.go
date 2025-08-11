package runner

import (
	"context"
	"errors"
	"maps"
	"os/exec"
	"strings"
	"testing"

	"github.com/DataDog/datadog-test-runner/internal/framework"
	"github.com/DataDog/datadog-test-runner/internal/platform"
	"github.com/DataDog/datadog-test-runner/internal/testoptimization"
)

// Mock implementations for testing

// MockPlatformDetector mocks platform detection
type MockPlatformDetector struct {
	Platform platform.Platform
	Err      error
}

func (m *MockPlatformDetector) DetectPlatform() (platform.Platform, error) {
	return m.Platform, m.Err
}

// MockPlatform mocks a platform
type MockPlatform struct {
	PlatformName string
	Tags         map[string]string
	TagsErr      error
	Framework    framework.Framework
	FrameworkErr error
}

func (m *MockPlatform) Name() string {
	return m.PlatformName
}

func (m *MockPlatform) CreateTagsMap() (map[string]string, error) {
	return m.Tags, m.TagsErr
}

func (m *MockPlatform) DetectFramework() (framework.Framework, error) {
	return m.Framework, m.FrameworkErr
}

// MockFramework mocks a testing framework
type MockFramework struct {
	FrameworkName string
	Tests         []testoptimization.Test
	Err           error
}

func (m *MockFramework) Name() string {
	return m.FrameworkName
}

func (m *MockFramework) DiscoverTests() ([]testoptimization.Test, error) {
	return m.Tests, m.Err
}

func (m *MockFramework) CreateDiscoveryCommand() *exec.Cmd {
	return nil // Not used in our tests
}

// MockTestOptimizationClient mocks the test optimization client
type MockTestOptimizationClient struct {
	InitializeCalled bool
	InitializeErr    error
	SkippableTests   map[string]bool
	ShutdownCalled   bool
	Tags             map[string]string
}

func (m *MockTestOptimizationClient) Initialize(tags map[string]string) error {
	m.InitializeCalled = true
	if m.Tags == nil {
		m.Tags = make(map[string]string)
	}
	maps.Copy(m.Tags, tags)
	return m.InitializeErr
}

func (m *MockTestOptimizationClient) GetSkippableTests() map[string]bool {
	return m.SkippableTests
}

func (m *MockTestOptimizationClient) Shutdown() {
	m.ShutdownCalled = true
}

func TestNew(t *testing.T) {
	runner := New()

	if runner == nil {
		t.Error("New() should return non-nil TestRunner")
	}

	if runner.testFiles != nil {
		t.Error("New() should initialize testFiles to nil")
	}

	if runner.skippablePercentage != 0.0 {
		t.Errorf("New() should initialize skippablePercentage to 0.0, got %f", runner.skippablePercentage)
	}

	if runner.platformDetector == nil {
		t.Error("New() should initialize platformDetector")
	}

	if runner.optimizationClient == nil {
		t.Error("New() should initialize optimizationClient")
	}
}

func TestNewWithDependencies(t *testing.T) {
	mockPlatformDetector := &MockPlatformDetector{}
	mockOptimizationClient := &MockTestOptimizationClient{}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient)

	if runner == nil {
		t.Error("NewWithDependencies() should return non-nil TestRunner")
	}

	if runner.platformDetector != mockPlatformDetector {
		t.Error("NewWithDependencies() should use injected platformDetector")
	}

	if runner.optimizationClient != mockOptimizationClient {
		t.Error("NewWithDependencies() should use injected optimizationClient")
	}
}

func TestTestRunner_PrepareTestOptimization_Success(t *testing.T) {
	ctx := context.Background()

	// Setup mocks
	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		Tests: []testoptimization.Test{
			{FQN: "TestSuite1.test1", SourceFile: "test/file1_test.rb"},
			{FQN: "TestSuite1.test2", SourceFile: "test/file1_test.rb"},
			{FQN: "TestSuite2.test3", SourceFile: "test/file2_test.rb"},
			{FQN: "TestSuite3.test4", SourceFile: "test/file3_test.rb"},
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

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient)

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

	for _, file := range runner.testFiles {
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

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient)

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

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient)

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
			{FQN: "test1", SourceFile: "file1.rb"},
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

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient)

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

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient)

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

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient)

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

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient)

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
			{FQN: "test1", SourceFile: "file1.rb"},
			{FQN: "test2", SourceFile: "file2.rb"},
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

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient)

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
