package runner

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/DataDog/ddtest/civisibility/utils/net"
	"github.com/DataDog/ddtest/internal/ciprovider"
	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization"
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
	SanityErr    error
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

func (m *MockPlatform) SanityCheck() error {
	return m.SanityErr
}

// MockFramework mocks a testing framework
type MockFramework struct {
	FrameworkName string
	Tests         []testoptimization.Test
	TestFiles     []string
	Err           error
	RunTestsCalls []RunTestsCall
	mu            sync.Mutex
}

type RunTestsCall struct {
	TestFiles []string
	EnvMap    map[string]string
}

func (m *MockFramework) Name() string {
	return m.FrameworkName
}

func (m *MockFramework) DiscoverTests(ctx context.Context) ([]testoptimization.Test, error) {
	return m.Tests, m.Err
}

func (m *MockFramework) DiscoverTestFiles() ([]string, error) {
	return m.TestFiles, m.Err
}

func (m *MockFramework) RunTests(ctx context.Context, testFiles []string, envMap map[string]string) error {
	// Record the call
	m.mu.Lock()
	m.RunTestsCalls = append(m.RunTestsCalls, RunTestsCall{
		TestFiles: slices.Clone(testFiles),
		EnvMap:    maps.Clone(envMap),
	})
	m.mu.Unlock()
	return m.Err
}

func (m *MockFramework) SetPlatformEnv(platformEnv map[string]string) {
	// No-op for mock
}

func (m *MockFramework) GetPlatformEnv() map[string]string {
	return nil
}

func (m *MockFramework) GetRunTestsCallsCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.RunTestsCalls)
}

func (m *MockFramework) GetRunTestsCalls() []RunTestsCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.RunTestsCalls)
}

// MockTestOptimizationClient mocks the test optimization client
type MockTestOptimizationClient struct {
	InitializeCalled bool
	InitializeErr    error
	Settings         *net.SettingsResponseData
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

func (m *MockTestOptimizationClient) GetSettings() *net.SettingsResponseData {
	return m.Settings
}

func (m *MockTestOptimizationClient) GetSkippableTests() map[string]bool {
	return m.SkippableTests
}

func (m *MockTestOptimizationClient) StoreCacheAndExit() {
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

func TestTestRunner_Setup_WithParallelRunners(t *testing.T) {
	// Create a temporary directory for test output
	tempDir := t.TempDir()

	// Save current working directory and change to temp dir
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	// Create .testoptimization directory
	_ = os.MkdirAll(constants.PlanDirectory, 0755)

	// Set parallelism to 1 to test single runner behavior
	_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM", "1")
	_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM", "1")
	defer func() {
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM")
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM")
	}()
	settings.Init()

	// Setup mocks for a test with 40% skippable percentage
	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		Tests: []testoptimization.Test{
			{Suite: "TestSuite1", Name: "test1", Parameters: "", SuiteSourceFile: "test/file1_test.rb"},
			{Suite: "TestSuite1", Name: "test2", Parameters: "", SuiteSourceFile: "test/file1_test.rb"},
			{Suite: "TestSuite2", Name: "test3", Parameters: "", SuiteSourceFile: "test/file2_test.rb"},
			{Suite: "TestSuite3", Name: "test4", Parameters: "", SuiteSourceFile: "test/file3_test.rb"},
			{Suite: "TestSuite4", Name: "test5", Parameters: "", SuiteSourceFile: "test/file4_test.rb"},
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
			"TestSuite1.test2.": true, // Skip test2
			"TestSuite4.test5.": true, // Skip test5
		},
	}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, newDefaultMockCIProviderDetector())

	// Run Setup
	err := runner.Plan(context.Background())
	if err != nil {
		t.Fatalf("Setup() should not return error, got: %v", err)
	}

	// Expected: 1 (since max=1)
	content, err := os.ReadFile(constants.ParallelRunnersOutputPath)
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

	// Create .testoptimization directory
	_ = os.MkdirAll(constants.PlanDirectory, 0755)

	// Set parallelism to 1 to test single runner behavior
	_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM", "1")
	_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM", "1")
	defer func() {
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM")
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM")
	}()
	settings.Init()

	// Setup mocks for test with CI provider
	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		Tests: []testoptimization.Test{
			{Suite: "TestSuite1", Name: "test1", Parameters: "", SuiteSourceFile: "test/file1_test.rb"},
			{Suite: "TestSuite2", Name: "test2", Parameters: "", SuiteSourceFile: "test/file2_test.rb"},
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
	err := runner.Plan(context.Background())
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

	// Create .testoptimization directory
	_ = os.MkdirAll(constants.PlanDirectory, 0755)

	// Setup mocks for test without CI provider
	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		Tests: []testoptimization.Test{
			{Suite: "TestSuite1", Name: "test1", Parameters: "", SuiteSourceFile: "test/file1_test.rb"},
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
	err := runner.Plan(context.Background())
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

	_ = os.MkdirAll(constants.PlanDirectory, 0755)

	// Setup mocks for test with failing CI provider
	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		Tests: []testoptimization.Test{
			{Suite: "TestSuite1", Name: "test1", Parameters: "", SuiteSourceFile: "test/file1_test.rb"},
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
	err := runner.Plan(context.Background())
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

		// Create .testoptimization directory
		_ = os.MkdirAll(constants.PlanDirectory, 0755)

		// Set parallelism to 1 to test single runner behavior
		_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM", "1")
		_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM", "1")
		defer func() {
			_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM")
			_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM")
		}()
		settings.Init()

		// Setup mocks for single runner scenario
		mockFramework := &MockFramework{
			FrameworkName: "rspec",
			Tests: []testoptimization.Test{
				{Suite: "TestSuite1", Name: "test1", Parameters: "", SuiteSourceFile: "test/file1_test.rb"},
				{Suite: "TestSuite2", Name: "test2", Parameters: "", SuiteSourceFile: "test/file2_test.rb"},
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
		err := runner.Plan(context.Background())
		if err != nil {
			t.Fatalf("Setup() should not return error, got: %v", err)
		}

		// Verify that tests-split directory was created
		if _, err := os.Stat(filepath.Join(constants.PlanDirectory, "tests-split")); os.IsNotExist(err) {
			t.Error("Expected tests-split directory to be created when parallelRunners = 1")
		}

		// Verify that runner-0 file was created
		runnerFilePath := filepath.Join(constants.PlanDirectory, "tests-split", "runner-0")
		if _, err := os.Stat(runnerFilePath); os.IsNotExist(err) {
			t.Error("Expected runner-0 file to be created when parallelRunners = 1")
		}

		// Verify that runner-0 contains the same content as test-files.txt
		testFilesContent, err := os.ReadFile(filepath.Join(constants.PlanDirectory, "test-files.txt"))
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

		// Create .testoptimization directory
		_ = os.MkdirAll(constants.PlanDirectory, 0755)

		// Setup mocks with test files that will create a predictable distribution
		mockFramework := &MockFramework{
			FrameworkName: "rspec",
			Tests: []testoptimization.Test{
				{Suite: "TestSuite1", Name: "test1", Parameters: "", SuiteSourceFile: "test/file1_test.rb"},
				{Suite: "TestSuite1", Name: "test2", Parameters: "", SuiteSourceFile: "test/file1_test.rb"}, // 2 tests in file1
				{Suite: "TestSuite2", Name: "test3", Parameters: "", SuiteSourceFile: "test/file2_test.rb"}, // 1 test in file2
				{Suite: "TestSuite3", Name: "test4", Parameters: "", SuiteSourceFile: "test/file3_test.rb"}, // 1 test in file3
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
		err := runner.Plan(context.Background())
		if err != nil {
			t.Fatalf("Setup() should not return error, got: %v", err)
		}

		// Verify that tests-split directory was created
		if _, err := os.Stat(filepath.Join(constants.PlanDirectory, "tests-split")); os.IsNotExist(err) {
			t.Error("Expected tests-split directory to be created")
		}

		// With min=2 and 0% skippable tests, we should get 4 parallel runners
		// Verify runner files exist
		for i := range expectedParallelRunnersCount {
			runnerPath := filepath.Join(constants.PlanDirectory, "tests-split", fmt.Sprintf("runner-%d", i))
			if _, err := os.Stat(runnerPath); os.IsNotExist(err) {
				t.Errorf("Expected runner-%d file to exist", i)
			}
		}

		// Verify content of runner files
		// With the test distribution (file1: 2 tests, file2: 1 test, file3: 1 test)
		// and 4 runners, expected: Runner 0 gets file1 (2 tests), others get 1 test each
		runner0Content, err := os.ReadFile(filepath.Join(constants.PlanDirectory, "tests-split", "runner-0"))
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
			runnerPath := filepath.Join(constants.PlanDirectory, "tests-split", fmt.Sprintf("runner-%d", i))
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
