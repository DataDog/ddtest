package runner

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/DataDog/datadog-test-runner/internal/ciprovider"
	"github.com/DataDog/datadog-test-runner/internal/framework"
	"github.com/DataDog/datadog-test-runner/internal/platform"
	"github.com/DataDog/datadog-test-runner/internal/settings"
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
	RunTestsCalls []RunTestsCall
}

type RunTestsCall struct {
	TestFiles []string
	EnvMap    map[string]string
}

func (m *MockFramework) Name() string {
	return m.FrameworkName
}

func (m *MockFramework) DiscoverTests() ([]testoptimization.Test, error) {
	return m.Tests, m.Err
}

func (m *MockFramework) RunTests(testFiles []string, envMap map[string]string) error {
	// Record the call
	m.RunTestsCalls = append(m.RunTestsCalls, RunTestsCall{
		TestFiles: slices.Clone(testFiles),
		EnvMap:    maps.Clone(envMap),
	})
	return m.Err
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

func TestTestRunner_Setup_WithTestSplit(t *testing.T) {
	t.Run("single runner - copy test-files.txt to runner-0", func(t *testing.T) {
		// Create a temporary directory for test output
		tempDir := t.TempDir()

		// Save current working directory and change to temp dir
		oldWd, _ := os.Getwd()
		defer func() { _ = os.Chdir(oldWd) }()
		_ = os.Chdir(tempDir)

		// Create .dd directory
		_ = os.MkdirAll(".dd", 0755)

		// Setup mocks for single runner scenario
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
			SkippableTests: map[string]bool{}, // No tests skipped
		}

		runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, newDefaultMockCIProviderDetector())

		// Run Setup
		err := runner.Setup(context.Background())
		if err != nil {
			t.Fatalf("Setup() should not return error, got: %v", err)
		}

		// Verify that tests-split directory was created
		if _, err := os.Stat(".dd/tests-split"); os.IsNotExist(err) {
			t.Error("Expected .dd/tests-split directory to be created when parallelRunners = 1")
		}

		// Verify that runner-0 file was created
		runnerFilePath := ".dd/tests-split/runner-0"
		if _, err := os.Stat(runnerFilePath); os.IsNotExist(err) {
			t.Error("Expected runner-0 file to be created when parallelRunners = 1")
		}

		// Verify that runner-0 contains the same content as test-files.txt
		testFilesContent, err := os.ReadFile(".dd/test-files.txt")
		if err != nil {
			t.Fatalf("Failed to read test-files.txt: %v", err)
		}

		runnerContent, err := os.ReadFile(runnerFilePath)
		if err != nil {
			t.Fatalf("Failed to read runner-0 file: %v", err)
		}

		if string(testFilesContent) != string(runnerContent) {
			t.Errorf("Expected runner-0 content to match test-files.txt content.\ntest-files.txt: %q\nrunner-0: %q",
				string(testFilesContent), string(runnerContent))
		}

		// Verify the content contains the expected test files
		expectedContent := "test/file1_test.rb\ntest/file2_test.rb\n"
		if string(runnerContent) != expectedContent {
			t.Errorf("Expected runner-0 content %q, got %q", expectedContent, string(runnerContent))
		}
	})

	t.Run("multiple runners - split files created", func(t *testing.T) {
		// Create a temporary directory for test output
		tempDir := t.TempDir()

		// Save current working directory and change to temp dir
		oldWd, _ := os.Getwd()
		defer func() { _ = os.Chdir(oldWd) }()
		_ = os.Chdir(tempDir)

		// Create .dd directory
		_ = os.MkdirAll(".dd", 0755)

		// Setup mocks with test files that will create a predictable distribution
		mockFramework := &MockFramework{
			FrameworkName: "rspec",
			Tests: []testoptimization.Test{
				{FQN: "TestSuite1.test1", SourceFile: "test/file1_test.rb", SuiteSourceFile: "test/file1_test.rb"},
				{FQN: "TestSuite1.test2", SourceFile: "test/file1_test.rb", SuiteSourceFile: "test/file1_test.rb"}, // 2 tests in file1
				{FQN: "TestSuite2.test3", SourceFile: "test/file2_test.rb", SuiteSourceFile: "test/file2_test.rb"}, // 1 test in file2
				{FQN: "TestSuite3.test4", SourceFile: "test/file3_test.rb", SuiteSourceFile: "test/file3_test.rb"}, // 1 test in file3
			},
		}

		mockPlatform := &MockPlatform{
			PlatformName: "ruby",
			Tags:         map[string]string{"platform": "ruby"},
			Framework:    mockFramework,
		}

		mockPlatformDetector := &MockPlatformDetector{Platform: mockPlatform}
		mockOptimizationClient := &MockTestOptimizationClient{
			SkippableTests: map[string]bool{}, // No tests skipped
		}

		expectedParallelRunnersCount := 4
		// Set environment variables to force multiple parallel runners
		_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM", "2")
		_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM", strconv.Itoa(expectedParallelRunnersCount))
		defer func() {
			_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM")
			_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM")
		}()

		// Reinitialize settings to pick up environment variables
		settings.Init()

		runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, newDefaultMockCIProviderDetector())

		// Run Setup
		err := runner.Setup(context.Background())
		if err != nil {
			t.Fatalf("Setup() should not return error, got: %v", err)
		}

		// Verify that tests-split directory was created
		if _, err := os.Stat(".dd/tests-split"); os.IsNotExist(err) {
			t.Error("Expected .dd/tests-split directory to be created")
		}

		// With min=2 and 0% skippable tests, we should get 4 parallel runners
		// Verify runner files exist
		for i := range expectedParallelRunnersCount {
			runnerPath := fmt.Sprintf(".dd/tests-split/runner-%d", i)
			if _, err := os.Stat(runnerPath); os.IsNotExist(err) {
				t.Errorf("Expected runner-%d file to exist", i)
			}
		}

		// Verify content of runner files
		// With the test distribution (file1: 2 tests, file2: 1 test, file3: 1 test)
		// and 4 runners, expected: Runner 0 gets file1 (2 tests), others get 1 test each
		runner0Content, err := os.ReadFile(".dd/tests-split/runner-0")
		if err != nil {
			t.Fatalf("Failed to read runner-0 file: %v", err)
		}

		// Verify runner-0 has the largest file (file1 with 2 tests)
		runner0Files := strings.Fields(strings.TrimSpace(string(runner0Content)))
		if !slices.Contains(runner0Files, "test/file1_test.rb") {
			t.Error("Expected runner-0 to contain test/file1_test.rb (largest file)")
		}

		// Count total files across all runners
		totalFiles := 0
		for i := range expectedParallelRunnersCount {
			runnerPath := fmt.Sprintf(".dd/tests-split/runner-%d", i)
			content, err := os.ReadFile(runnerPath)
			if err != nil {
				continue
			}
			files := strings.Fields(strings.TrimSpace(string(content)))
			totalFiles += len(files)
		}

		// Should have all 3 test files distributed
		if totalFiles != 3 {
			t.Errorf("Expected 3 total files distributed across runners, got %d", totalFiles)
		}
	})
}

func TestTestRunner_Run_SingleRunner(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	// Setup directory structure and files
	_ = os.MkdirAll(".dd", 0755)

	// Create test files output with specific test files
	testFiles := "test/file1_test.rb\ntest/file2_test.rb\n"
	_ = os.WriteFile(".dd/test-files.txt", []byte(testFiles), 0644)
	_ = os.WriteFile(".dd/parallel-runners.txt", []byte("1"), 0644)

	// Setup mock framework to track RunTests calls
	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		Tests: []testoptimization.Test{
			{FQN: "test1", SourceFile: "test/file1_test.rb", SuiteSourceFile: "test/file1_test.rb"},
			{FQN: "test2", SourceFile: "test/file2_test.rb", SuiteSourceFile: "test/file2_test.rb"},
		},
		RunTestsCalls: []RunTestsCall{},
	}

	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags:         map[string]string{"platform": "ruby"},
		Framework:    mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{Platform: mockPlatform}
	mockOptimizationClient := &MockTestOptimizationClient{SkippableTests: map[string]bool{}}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, newDefaultMockCIProviderDetector())

	// Run the tests
	err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() should not return error, got: %v", err)
	}

	// Verify RunTests was called exactly once
	if len(mockFramework.RunTestsCalls) != 1 {
		t.Fatalf("Expected RunTests to be called once, got %d calls", len(mockFramework.RunTestsCalls))
	}

	call := mockFramework.RunTestsCalls[0]

	// Verify correct test files were passed
	expectedFiles := []string{"test/file1_test.rb", "test/file2_test.rb"}
	if !slices.Equal(call.TestFiles, expectedFiles) {
		t.Errorf("Expected test files %v, got %v", expectedFiles, call.TestFiles)
	}

	// Verify empty env map (no worker env configured)
	if len(call.EnvMap) != 0 {
		t.Errorf("Expected empty env map, got %v", call.EnvMap)
	}
}

func TestTestRunner_Run_MultipleRunners(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	// Setup directory structure
	_ = os.MkdirAll(".dd/tests-split", 0755)

	// Create parallel runners file
	_ = os.WriteFile(".dd/parallel-runners.txt", []byte("3"), 0644)

	// Create split files for each runner
	_ = os.WriteFile(".dd/tests-split/runner-0", []byte("test/file1_test.rb\n"), 0644)
	_ = os.WriteFile(".dd/tests-split/runner-1", []byte("test/file2_test.rb\n"), 0644)
	_ = os.WriteFile(".dd/tests-split/runner-2", []byte("test/file3_test.rb\n"), 0644)

	// Setup mock framework to track all RunTests calls
	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		RunTestsCalls: []RunTestsCall{},
	}

	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Framework:    mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{Platform: mockPlatform}
	mockOptimizationClient := &MockTestOptimizationClient{}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, newDefaultMockCIProviderDetector())

	// Run the tests
	err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() should not return error, got: %v", err)
	}

	// Verify RunTests was called exactly 3 times (once per runner)
	if len(mockFramework.RunTestsCalls) != 3 {
		t.Fatalf("Expected RunTests to be called 3 times, got %d calls", len(mockFramework.RunTestsCalls))
	}

	// Collect all test files that were run
	allRunFiles := make(map[string]bool)
	for _, call := range mockFramework.RunTestsCalls {
		// Each call should have exactly one test file
		if len(call.TestFiles) != 1 {
			t.Errorf("Expected each RunTests call to have 1 test file, got %d", len(call.TestFiles))
		}
		for _, file := range call.TestFiles {
			allRunFiles[file] = true
		}
	}

	// Verify all expected test files were run
	expectedFiles := map[string]bool{
		"test/file1_test.rb": true,
		"test/file2_test.rb": true,
		"test/file3_test.rb": true,
	}

	if !maps.Equal(allRunFiles, expectedFiles) {
		t.Errorf("Expected test files %v, got %v", expectedFiles, allRunFiles)
	}
}

func TestTestRunner_Run_WithWorkerEnv(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	// Setup directory structure
	_ = os.MkdirAll(".dd/tests-split", 0755)
	_ = os.WriteFile(".dd/parallel-runners.txt", []byte("2"), 0644)
	_ = os.WriteFile(".dd/tests-split/runner-0", []byte("test/file1_test.rb\n"), 0644)
	_ = os.WriteFile(".dd/tests-split/runner-1", []byte("test/file2_test.rb\n"), 0644)

	// Set worker environment with nodeIndex placeholder
	_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_WORKER_ENV", "NODE_INDEX={{nodeIndex}};BUILD_ID=123")
	defer func() { _ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_WORKER_ENV") }()

	// Reinitialize settings to pick up environment variable
	settings.Init()

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		RunTestsCalls: []RunTestsCall{},
	}

	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Framework:    mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{Platform: mockPlatform}
	mockOptimizationClient := &MockTestOptimizationClient{}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, newDefaultMockCIProviderDetector())

	err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() should not return error, got: %v", err)
	}

	// Verify 2 RunTests calls
	if len(mockFramework.RunTestsCalls) != 2 {
		t.Fatalf("Expected 2 RunTests calls, got %d", len(mockFramework.RunTestsCalls))
	}

	// Verify that each worker got the correct nodeIndex
	nodeIndices := make(map[string]bool)
	for _, call := range mockFramework.RunTestsCalls {
		nodeIndex := call.EnvMap["NODE_INDEX"]
		if nodeIndex == "" {
			t.Error("Expected NODE_INDEX to be set in env map")
		}
		nodeIndices[nodeIndex] = true

		// Verify BUILD_ID is preserved
		if call.EnvMap["BUILD_ID"] != "123" {
			t.Errorf("Expected BUILD_ID=123, got %s", call.EnvMap["BUILD_ID"])
		}
	}

	// Verify we got node indices 0 and 1
	expectedIndices := map[string]bool{"0": true, "1": true}
	if !maps.Equal(nodeIndices, expectedIndices) {
		t.Errorf("Expected node indices %v, got %v", expectedIndices, nodeIndices)
	}
}

func TestTestRunner_Run_SetupCalledWhenNeeded(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	// Don't create parallel-runners.txt - this should trigger Setup
	_ = os.MkdirAll(".dd", 0755)

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		Tests: []testoptimization.Test{
			{FQN: "test1", SourceFile: "test/file1_test.rb", SuiteSourceFile: "test/file1_test.rb"},
		},
		RunTestsCalls: []RunTestsCall{},
	}

	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags:         map[string]string{"platform": "ruby"},
		Framework:    mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{Platform: mockPlatform}
	mockOptimizationClient := &MockTestOptimizationClient{SkippableTests: map[string]bool{}}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, newDefaultMockCIProviderDetector())

	err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() should not return error, got: %v", err)
	}

	// Verify Setup was called by checking that files were created
	if _, err := os.Stat(".dd/parallel-runners.txt"); os.IsNotExist(err) {
		t.Error("Expected parallel-runners.txt to be created by Setup")
	}

	if _, err := os.Stat(".dd/test-files.txt"); os.IsNotExist(err) {
		t.Error("Expected test-files.txt to be created by Setup")
	}

	// Verify RunTests was called
	if len(mockFramework.RunTestsCalls) != 1 {
		t.Fatalf("Expected RunTests to be called once, got %d calls", len(mockFramework.RunTestsCalls))
	}
}

func TestTestRunner_Run_PlatformDetectionError(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	_ = os.MkdirAll(".dd", 0755)
	_ = os.WriteFile(".dd/parallel-runners.txt", []byte("1"), 0644)

	mockPlatformDetector := &MockPlatformDetector{
		Err: errors.New("platform detection failed"),
	}

	runner := NewWithDependencies(mockPlatformDetector, &MockTestOptimizationClient{}, newDefaultMockCIProviderDetector())

	err := runner.Run(context.Background())
	if err == nil {
		t.Error("Run() should return error when platform detection fails")
	}

	expectedMsg := "failed to detect platform"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("Run() error should contain '%s', got: %v", expectedMsg, err)
	}
}

func TestTestRunner_Run_FrameworkDetectionError(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	_ = os.MkdirAll(".dd", 0755)
	_ = os.WriteFile(".dd/parallel-runners.txt", []byte("1"), 0644)

	mockPlatform := &MockPlatform{
		FrameworkErr: errors.New("framework detection failed"),
	}

	mockPlatformDetector := &MockPlatformDetector{Platform: mockPlatform}

	runner := NewWithDependencies(mockPlatformDetector, &MockTestOptimizationClient{}, newDefaultMockCIProviderDetector())

	err := runner.Run(context.Background())
	if err == nil {
		t.Error("Run() should return error when framework detection fails")
	}

	expectedMsg := "failed to detect framework"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("Run() error should contain '%s', got: %v", expectedMsg, err)
	}
}

func TestTestRunner_Run_ParallelRunnersFileError(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	_ = os.MkdirAll(".dd", 0755)
	// Create malformed parallel-runners.txt
	_ = os.WriteFile(".dd/parallel-runners.txt", []byte("not-a-number"), 0644)

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		RunTestsCalls: []RunTestsCall{},
	}

	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Framework:    mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{Platform: mockPlatform}

	runner := NewWithDependencies(mockPlatformDetector, &MockTestOptimizationClient{}, newDefaultMockCIProviderDetector())

	err := runner.Run(context.Background())
	if err == nil {
		t.Error("Run() should return error when parallel-runners.txt contains invalid data")
	}

	expectedMsg := "failed to parse parallel runners count"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("Run() error should contain '%s', got: %v", expectedMsg, err)
	}
}

func TestTestRunner_Run_TestExecutionError(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	_ = os.MkdirAll(".dd", 0755)
	_ = os.WriteFile(".dd/parallel-runners.txt", []byte("1"), 0644)
	_ = os.WriteFile(".dd/test-files.txt", []byte("test/file1_test.rb\n"), 0644)

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		Err:           errors.New("test execution failed"),
		RunTestsCalls: []RunTestsCall{},
	}

	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Framework:    mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{Platform: mockPlatform}

	runner := NewWithDependencies(mockPlatformDetector, &MockTestOptimizationClient{}, newDefaultMockCIProviderDetector())

	err := runner.Run(context.Background())
	if err == nil {
		t.Error("Run() should return error when test execution fails")
	}

	expectedMsg := "failed to run tests"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("Run() error should contain '%s', got: %v", expectedMsg, err)
	}
}

func TestTestRunner_Run_ParallelTestExecutionError(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	_ = os.MkdirAll(".dd/tests-split", 0755)
	_ = os.WriteFile(".dd/parallel-runners.txt", []byte("2"), 0644)
	_ = os.WriteFile(".dd/tests-split/runner-0", []byte("test/file1_test.rb\n"), 0644)
	_ = os.WriteFile(".dd/tests-split/runner-1", []byte("test/file2_test.rb\n"), 0644)

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		Err:           errors.New("test execution failed"),
		RunTestsCalls: []RunTestsCall{},
	}

	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Framework:    mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{Platform: mockPlatform}

	runner := NewWithDependencies(mockPlatformDetector, &MockTestOptimizationClient{}, newDefaultMockCIProviderDetector())

	err := runner.Run(context.Background())
	if err == nil {
		t.Error("Run() should return error when parallel test execution fails")
	}

	expectedMsg := "failed to run parallel tests"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("Run() error should contain '%s', got: %v", expectedMsg, err)
	}
}

func TestTestRunner_Run_EmptyTestFiles(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	_ = os.MkdirAll(".dd", 0755)
	_ = os.WriteFile(".dd/parallel-runners.txt", []byte("1"), 0644)
	_ = os.WriteFile(".dd/test-files.txt", []byte(""), 0644) // Empty test files

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		RunTestsCalls: []RunTestsCall{},
	}

	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Framework:    mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{Platform: mockPlatform}

	runner := NewWithDependencies(mockPlatformDetector, &MockTestOptimizationClient{}, newDefaultMockCIProviderDetector())

	err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() should not return error for empty test files, got: %v", err)
	}

	// Verify RunTests was NOT called since there are no test files
	if len(mockFramework.RunTestsCalls) != 0 {
		t.Errorf("Expected RunTests not to be called for empty test files, got %d calls", len(mockFramework.RunTestsCalls))
	}
}

func TestTestRunner_Run_MissingSplitDirectory(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	_ = os.MkdirAll(".dd", 0755)
	_ = os.WriteFile(".dd/parallel-runners.txt", []byte("2"), 0644)
	// Don't create tests-split directory

	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Framework:    &MockFramework{FrameworkName: "rspec", RunTestsCalls: []RunTestsCall{}},
	}

	mockPlatformDetector := &MockPlatformDetector{Platform: mockPlatform}

	runner := NewWithDependencies(mockPlatformDetector, &MockTestOptimizationClient{}, newDefaultMockCIProviderDetector())

	err := runner.Run(context.Background())
	if err == nil {
		t.Error("Run() should return error when tests-split directory is missing")
	}

	expectedMsg := "failed to read tests split directory"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("Run() error should contain '%s', got: %v", expectedMsg, err)
	}
}

func TestTestRunner_Run_WithCiNode_Success(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	// Setup directory structure
	_ = os.MkdirAll(".dd/tests-split", 0755)
	_ = os.WriteFile(".dd/parallel-runners.txt", []byte("3"), 0644)

	// Create split files for multiple runners
	_ = os.WriteFile(".dd/tests-split/runner-0", []byte("test/file1_test.rb\n"), 0644)
	_ = os.WriteFile(".dd/tests-split/runner-1", []byte("test/file2_test.rb\ntest/file3_test.rb\n"), 0644)
	_ = os.WriteFile(".dd/tests-split/runner-2", []byte("test/file4_test.rb\n"), 0644)

	// Set ci-node environment variable (0-based indexing)
	_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_CI_NODE", "1")
	defer func() { _ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_CI_NODE") }()

	// Reinitialize settings to pick up environment variable
	settings.Init()

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		RunTestsCalls: []RunTestsCall{},
	}

	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Framework:    mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{Platform: mockPlatform}
	mockOptimizationClient := &MockTestOptimizationClient{}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, newDefaultMockCIProviderDetector())

	err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() should not return error, got: %v", err)
	}

	// Verify RunTests was called exactly once (for the specific ci-node)
	if len(mockFramework.RunTestsCalls) != 1 {
		t.Fatalf("Expected RunTests to be called once for ci-node, got %d calls", len(mockFramework.RunTestsCalls))
	}

	call := mockFramework.RunTestsCalls[0]

	// Verify correct test files were passed (from runner-1, since ci-node=1)
	expectedFiles := []string{"test/file2_test.rb", "test/file3_test.rb"}
	if !slices.Equal(call.TestFiles, expectedFiles) {
		t.Errorf("Expected test files %v for ci-node=1, got %v", expectedFiles, call.TestFiles)
	}
}

func TestTestRunner_Run_WithCiNode_FileNotFound(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	// Setup directory structure with fewer runners than requested ci-node
	_ = os.MkdirAll(".dd/tests-split", 0755)
	_ = os.WriteFile(".dd/parallel-runners.txt", []byte("2"), 0644)
	_ = os.WriteFile(".dd/tests-split/runner-0", []byte("test/file1_test.rb\n"), 0644)
	_ = os.WriteFile(".dd/tests-split/runner-1", []byte("test/file2_test.rb\n"), 0644)
	// Note: runner-2 doesn't exist, but ci-node=2 will try to access it

	_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_CI_NODE", "2")
	defer func() { _ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_CI_NODE") }()

	// Reinitialize settings to pick up environment variable
	settings.Init()

	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Framework:    &MockFramework{FrameworkName: "rspec", RunTestsCalls: []RunTestsCall{}},
	}

	mockPlatformDetector := &MockPlatformDetector{Platform: mockPlatform}

	runner := NewWithDependencies(mockPlatformDetector, &MockTestOptimizationClient{}, newDefaultMockCIProviderDetector())

	err := runner.Run(context.Background())
	if err == nil {
		t.Error("Run() should return error when ci-node runner file doesn't exist")
	}

	expectedMsg := "runner file for ci-node 2 does not exist"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("Run() error should contain '%s', got: %v", expectedMsg, err)
	}
}

func TestTestRunner_Run_WithCiNode_ZeroValue(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	// Setup directory structure for multiple runners
	_ = os.MkdirAll(".dd/tests-split", 0755)
	_ = os.WriteFile(".dd/parallel-runners.txt", []byte("2"), 0644)
	_ = os.WriteFile(".dd/tests-split/runner-0", []byte("test/file1_test.rb\n"), 0644)
	_ = os.WriteFile(".dd/tests-split/runner-1", []byte("test/file2_test.rb\n"), 0644)

	// Set ci-node to -1 (should use normal parallel behavior)
	_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_CI_NODE", "-1")
	defer func() { _ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_CI_NODE") }()

	// Reinitialize settings to pick up environment variable
	settings.Init()

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		RunTestsCalls: []RunTestsCall{},
	}

	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Framework:    mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{Platform: mockPlatform}

	runner := NewWithDependencies(mockPlatformDetector, &MockTestOptimizationClient{}, newDefaultMockCIProviderDetector())

	err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() should not return error, got: %v", err)
	}

	// Verify RunTests was called twice (normal parallel behavior, not ci-node)
	if len(mockFramework.RunTestsCalls) != 2 {
		t.Fatalf("Expected RunTests to be called twice for normal parallel behavior, got %d calls", len(mockFramework.RunTestsCalls))
	}
}

func TestTestRunner_Run_WithCiNode_WithWorkerEnv(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	// Setup directory structure
	_ = os.MkdirAll(".dd/tests-split", 0755)
	_ = os.WriteFile(".dd/parallel-runners.txt", []byte("2"), 0644)
	_ = os.WriteFile(".dd/tests-split/runner-0", []byte("test/file1_test.rb\n"), 0644)
	_ = os.WriteFile(".dd/tests-split/runner-1", []byte("test/file2_test.rb\n"), 0644)

	// Set both ci-node and worker environment
	_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_CI_NODE", "0")
	_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_WORKER_ENV", "NODE_INDEX={{nodeIndex}};CI_NODE=true")
	defer func() {
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_CI_NODE")
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_WORKER_ENV")
	}()

	// Reinitialize settings to pick up environment variables
	settings.Init()

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		RunTestsCalls: []RunTestsCall{},
	}

	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Framework:    mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{Platform: mockPlatform}

	runner := NewWithDependencies(mockPlatformDetector, &MockTestOptimizationClient{}, newDefaultMockCIProviderDetector())

	err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() should not return error, got: %v", err)
	}

	// Verify RunTests was called exactly once for the ci-node
	if len(mockFramework.RunTestsCalls) != 1 {
		t.Fatalf("Expected RunTests to be called once for ci-node, got %d calls", len(mockFramework.RunTestsCalls))
	}

	call := mockFramework.RunTestsCalls[0]

	// Verify test files from runner-0 (ci-node=0)
	expectedFiles := []string{"test/file1_test.rb"}
	if !slices.Equal(call.TestFiles, expectedFiles) {
		t.Errorf("Expected test files %v for ci-node=0, got %v", expectedFiles, call.TestFiles)
	}

	// Verify worker environment was processed correctly with ci-node index
	if call.EnvMap["NODE_INDEX"] != "0" {
		t.Errorf("Expected NODE_INDEX=0 for ci-node=0, got %s", call.EnvMap["NODE_INDEX"])
	}

	if call.EnvMap["CI_NODE"] != "true" {
		t.Errorf("Expected CI_NODE=true, got %s", call.EnvMap["CI_NODE"])
	}
}
