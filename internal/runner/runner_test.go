package runner

import (
	"context"
	"errors"
	"maps"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/DataDog/datadog-test-runner/internal/ciprovider"
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

func (m *MockTestOptimizationClient) StoreContextAndExit() {
	m.ShutdownCalled = true
}

// MockCIProvider mocks a CI provider
type MockCIProvider struct {
	ProviderName    string
	ConfigureCalled bool
	ConfigureErr    error
	ParallelRunners int
}

func (m *MockCIProvider) Name() string {
	return m.ProviderName
}

func (m *MockCIProvider) Configure(parallelRunners int) error {
	m.ConfigureCalled = true
	m.ParallelRunners = parallelRunners
	return m.ConfigureErr
}

// MockCIProviderDetector mocks CI provider detection
type MockCIProviderDetector struct {
	CIProvider ciprovider.CIProvider
	Err        error
}

func (m *MockCIProviderDetector) DetectCIProvider() (ciprovider.CIProvider, error) {
	return m.CIProvider, m.Err
}

// Helper function to create a default mock CI provider detector that returns no provider
func newDefaultMockCIProviderDetector() *MockCIProviderDetector {
	return &MockCIProviderDetector{
		Err: errors.New("no CI provider detected"),
	}
}

func TestNew(t *testing.T) {
	runner := New()

	if runner == nil {
		t.Error("New() should return non-nil TestRunner")
		return
	}

	if len(runner.testFiles) != 0 {
		t.Error("New() should initialize testFiles to empty map")
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
	mockCIProviderDetector := newDefaultMockCIProviderDetector()

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, mockCIProviderDetector)

	if runner == nil {
		t.Error("NewWithDependencies() should return non-nil TestRunner")
		return
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

// Helper function to run calculateParallelRunnersWithParams tests
func testCalculateParallelRunners(skippablePercentage float64, minParallelism, maxParallelism int) int {
	return calculateParallelRunnersWithParams(skippablePercentage, minParallelism, maxParallelism)
}

func TestCalculateParallelRunners_MaxParallelismIsOne(t *testing.T) {
	tests := []struct {
		name                string
		skippablePercentage float64
		expected            int
	}{
		{"0% skippable", 0.0, 1},
		{"50% skippable", 50.0, 1},
		{"100% skippable", 100.0, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := testCalculateParallelRunners(tt.skippablePercentage, 1, 1)
			if result != tt.expected {
				t.Errorf("calculateParallelRunners(%f) = %d, expected %d", tt.skippablePercentage, result, tt.expected)
			}
		})
	}
}

func TestCalculateParallelRunners_MinParallelismLessThanOne(t *testing.T) {
	tests := []struct {
		name                string
		skippablePercentage float64
		expected            int
	}{
		{"0% skippable with min<1", 0.0, 1},
		{"50% skippable with min<1", 50.0, 1},
		{"100% skippable with min<1", 100.0, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := testCalculateParallelRunners(tt.skippablePercentage, 0, 5)
			if result != tt.expected {
				t.Errorf("calculateParallelRunners(%f) = %d, expected %d", tt.skippablePercentage, result, tt.expected)
			}
		})
	}
}

func TestCalculateParallelRunners_MaxLessThanMin(t *testing.T) {
	result := testCalculateParallelRunners(50.0, 5, 3) // max < min
	expected := 5                                      // Should return min_parallelism
	if result != expected {
		t.Errorf("calculateParallelRunners(50.0) = %d, expected %d when max < min", result, expected)
	}
}

func TestCalculateParallelRunners_LinearInterpolation(t *testing.T) {
	tests := []struct {
		name                string
		skippablePercentage float64
		expected            int
	}{
		{"0% skippable -> max parallelism", 0.0, 8},
		{"25% skippable", 25.0, 7}, // 8 - 0.25 * (8-2) = 8 - 1.5 = 6.5 -> 7
		{"50% skippable", 50.0, 5}, // 8 - 0.5 * (8-2) = 8 - 3 = 5
		{"75% skippable", 75.0, 4}, // 8 - 0.75 * (8-2) = 8 - 4.5 = 3.5 -> 4
		{"100% skippable -> min parallelism", 100.0, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := testCalculateParallelRunners(tt.skippablePercentage, 2, 8)
			if result != tt.expected {
				t.Errorf("calculateParallelRunners(%f) = %d, expected %d", tt.skippablePercentage, result, tt.expected)
			}
		})
	}
}

func TestCalculateParallelRunners_EdgeCases(t *testing.T) {
	tests := []struct {
		name                string
		skippablePercentage float64
		expected            int
	}{
		{"Negative percentage", -10.0, 10}, // Should clamp to 0%
		{"Over 100%", 150.0, 3},            // Should clamp to 100%
		{"Exact boundary 0%", 0.0, 10},
		{"Exact boundary 100%", 100.0, 3},
		{"Fractional result rounds", 33.33, 8}, // 10 - 0.3333 * 7 = 7.67 -> 8
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := testCalculateParallelRunners(tt.skippablePercentage, 3, 10)
			if result != tt.expected {
				t.Errorf("calculateParallelRunners(%f) = %d, expected %d", tt.skippablePercentage, result, tt.expected)
			}
		})
	}
}

func TestCalculateParallelRunners_MinEqualsMax(t *testing.T) {
	tests := []struct {
		name                string
		skippablePercentage float64
		expected            int
	}{
		{"0% skippable", 0.0, 4},
		{"50% skippable", 50.0, 4},
		{"100% skippable", 100.0, 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := testCalculateParallelRunners(tt.skippablePercentage, 4, 4)
			if result != tt.expected {
				t.Errorf("calculateParallelRunners(%f) = %d, expected %d", tt.skippablePercentage, result, tt.expected)
			}
		})
	}
}

func TestCalculateParallelRunners_RealWorldScenarios(t *testing.T) {
	tests := []struct {
		name                string
		minParallelism      int
		maxParallelism      int
		skippablePercentage float64
		expected            int
		description         string
	}{
		{"Small project", 1, 4, 25.0, 3, "25% skippable in small project"},
		{"Medium project", 2, 12, 60.0, 6, "60% skippable in medium project"},
		{"Large project", 4, 32, 80.0, 10, "80% skippable in large project"},
		{"CI with high parallelism", 8, 64, 90.0, 14, "90% skippable with high parallelism"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := testCalculateParallelRunners(tt.skippablePercentage, tt.minParallelism, tt.maxParallelism)
			if result != tt.expected {
				t.Errorf("%s: calculateParallelRunners(%f) = %d, expected %d",
					tt.description, tt.skippablePercentage, result, tt.expected)
			}

			// Verify result is within bounds
			if result < tt.minParallelism {
				t.Errorf("%s: result %d is less than min_parallelism %d", tt.description, result, tt.minParallelism)
			}
			if result > tt.maxParallelism {
				t.Errorf("%s: result %d is greater than max_parallelism %d", tt.description, result, tt.maxParallelism)
			}
		})
	}
}

func TestTestRunner_Setup_WithParallelRunners(t *testing.T) {
	// Create a temporary directory for test output
	tempDir := t.TempDir()

	// Save current working directory and change to temp dir
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	// Create .dd directory
	_ = os.MkdirAll(".dd", 0755)

	// Setup mocks for a test with 40% skippable percentage
	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		Tests: []testoptimization.Test{
			{FQN: "TestSuite1.test1", SourceFile: "test/file1_test.rb", SuiteSourceFile: "test/file1_test.rb"},
			{FQN: "TestSuite1.test2", SourceFile: "test/file1_test.rb", SuiteSourceFile: "test/file1_test.rb"},
			{FQN: "TestSuite2.test3", SourceFile: "test/file2_test.rb", SuiteSourceFile: "test/file2_test.rb"},
			{FQN: "TestSuite3.test4", SourceFile: "test/file3_test.rb", SuiteSourceFile: "test/file3_test.rb"},
			{FQN: "TestSuite4.test5", SourceFile: "test/file4_test.rb", SuiteSourceFile: "test/file4_test.rb"},
		},
	}

	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags:         map[string]string{"platform": "ruby"},
		Framework:    mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{Platform: mockPlatform}
	mockOptimizationClient := &MockTestOptimizationClient{
		SkippableTests: map[string]bool{
			"TestSuite1.test2": true, // Skip test2
			"TestSuite4.test5": true, // Skip test5
		},
	}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, newDefaultMockCIProviderDetector())

	// Run Setup
	err := runner.Setup(context.Background())
	if err != nil {
		t.Fatalf("Setup() should not return error, got: %v", err)
	}

	// Expected: 1 (since max=1)
	content, err := os.ReadFile(ParallelRunnersOutputPath)
	if err != nil {
		t.Fatalf("Failed to read parallel runners file: %v", err)
	}

	expected := "1"
	if string(content) != expected {
		t.Errorf("Expected parallel runners file content '%s', got '%s'", expected, string(content))
	}
}

func TestTestRunner_Setup_WithCIProvider(t *testing.T) {
	tempDir := t.TempDir()

	// Save current working directory and change to temp dir
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	// Create .dd directory
	_ = os.MkdirAll(".dd", 0755)

	// Setup mocks for test with CI provider
	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		Tests: []testoptimization.Test{
			{FQN: "TestSuite1.test1", SourceFile: "test/file1_test.rb", SuiteSourceFile: "test/file1_test.rb"},
			{FQN: "TestSuite2.test2", SourceFile: "test/file2_test.rb", SuiteSourceFile: "test/file2_test.rb"},
		},
	}

	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags:         map[string]string{"platform": "ruby"},
		Framework:    mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{Platform: mockPlatform}
	mockOptimizationClient := &MockTestOptimizationClient{
		SkippableTests: map[string]bool{
			"TestSuite1.test1": true, // Skip test1 = 50% skippable
		},
	}

	// Mock CI provider that should be called
	mockCIProvider := &MockCIProvider{
		ProviderName: "github",
	}
	mockCIProviderDetector := &MockCIProviderDetector{
		CIProvider: mockCIProvider,
	}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, mockCIProviderDetector)

	// Run Setup
	err := runner.Setup(context.Background())
	if err != nil {
		t.Fatalf("Setup() should not return error, got: %v", err)
	}

	// Verify CI provider Configure was called
	if !mockCIProvider.ConfigureCalled {
		t.Error("Expected CI provider Configure to be called")
	}

	// Verify Configure was called with the correct parallel runners count (1, since max=1)
	expectedRunners := 1
	if mockCIProvider.ParallelRunners != expectedRunners {
		t.Errorf("Expected CI provider Configure called with %d parallel runners, got %d",
			expectedRunners, mockCIProvider.ParallelRunners)
	}
}

func TestTestRunner_Setup_CIProviderDetectionFailure(t *testing.T) {
	tempDir := t.TempDir()

	// Save current working directory and change to temp dir
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	// Create .dd directory
	_ = os.MkdirAll(".dd", 0755)

	// Setup mocks for test without CI provider
	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		Tests: []testoptimization.Test{
			{FQN: "TestSuite1.test1", SourceFile: "test/file1_test.rb", SuiteSourceFile: "test/file1_test.rb"},
		},
	}

	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags:         map[string]string{"platform": "ruby"},
		Framework:    mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{Platform: mockPlatform}
	mockOptimizationClient := &MockTestOptimizationClient{SkippableTests: map[string]bool{}}

	// Mock CI provider detector that fails
	mockCIProviderDetector := &MockCIProviderDetector{
		Err: errors.New("no CI provider detected"),
	}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, mockCIProviderDetector)

	// Run Setup - should succeed even if CI provider detection fails
	err := runner.Setup(context.Background())
	if err != nil {
		t.Fatalf("Setup() should not fail when CI provider detection fails, got: %v", err)
	}
}

func TestTestRunner_Setup_CIProviderConfigureFailure(t *testing.T) {
	tempDir := t.TempDir()

	// Save current working directory and change to temp dir
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	// Create .dd directory
	_ = os.MkdirAll(".dd", 0755)

	// Setup mocks for test with failing CI provider
	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		Tests: []testoptimization.Test{
			{FQN: "TestSuite1.test1", SourceFile: "test/file1_test.rb", SuiteSourceFile: "test/file1_test.rb"},
		},
	}

	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags:         map[string]string{"platform": "ruby"},
		Framework:    mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{Platform: mockPlatform}
	mockOptimizationClient := &MockTestOptimizationClient{SkippableTests: map[string]bool{}}

	// Mock CI provider that fails during configuration
	mockCIProvider := &MockCIProvider{
		ProviderName: "github",
		ConfigureErr: errors.New("configuration failed"),
	}
	mockCIProviderDetector := &MockCIProviderDetector{
		CIProvider: mockCIProvider,
	}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, mockCIProviderDetector)

	// Run Setup - should succeed even if CI provider configuration fails
	err := runner.Setup(context.Background())
	if err != nil {
		t.Fatalf("Setup() should not fail when CI provider configuration fails, got: %v", err)
	}

	// Verify CI provider Configure was attempted
	if !mockCIProvider.ConfigureCalled {
		t.Error("Expected CI provider Configure to be called even if it fails")
	}
}
