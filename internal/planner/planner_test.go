package planner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	ciConstants "github.com/DataDog/ddtest/civisibility/constants"
	"github.com/DataDog/ddtest/internal/ciprovider"
	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization"
	ciUtils "github.com/DataDog/ddtest/internal/utils"
	"github.com/DataDog/ddtest/internal/utils/net"
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
	FrameworkName      string
	TestPatternValue   string
	Tests              []testoptimization.Test
	TestFiles          []string
	Err                error
	DiscoverTestsErr   error // If set, overrides Err for DiscoverTests
	OnDiscoverTests    func()
	RunTestsCalls      []RunTestsCall
	DiscoverTestsFiles []discovery.TestFileSet
	mu                 sync.Mutex
}

type RunTestsCall struct {
	TestFiles []string
	EnvMap    map[string]string
}

func setPlannerTestsExcludePattern(t *testing.T, pattern string) {
	t.Helper()
	previous := os.Getenv("DD_TEST_OPTIMIZATION_RUNNER_TESTS_EXCLUDE_PATTERN")
	if err := os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_TESTS_EXCLUDE_PATTERN", pattern); err != nil {
		t.Fatalf("failed to set tests_exclude_pattern env: %v", err)
	}
	settings.Init()

	t.Cleanup(func() {
		if previous == "" {
			if err := os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_TESTS_EXCLUDE_PATTERN"); err != nil {
				t.Errorf("failed to unset tests_exclude_pattern env: %v", err)
			}
		} else {
			if err := os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_TESTS_EXCLUDE_PATTERN", previous); err != nil {
				t.Errorf("failed to restore tests_exclude_pattern env: %v", err)
			}
		}
		settings.Init()
	})
}

func setPlannerTestsLocation(t *testing.T, pattern string) {
	t.Helper()
	previous := os.Getenv("DD_TEST_OPTIMIZATION_RUNNER_TESTS_LOCATION")
	if err := os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_TESTS_LOCATION", pattern); err != nil {
		t.Fatalf("failed to set tests_location env: %v", err)
	}
	settings.Init()

	t.Cleanup(func() {
		if previous == "" {
			if err := os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_TESTS_LOCATION"); err != nil {
				t.Errorf("failed to unset tests_location env: %v", err)
			}
		} else {
			if err := os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_TESTS_LOCATION", previous); err != nil {
				t.Errorf("failed to restore tests_location env: %v", err)
			}
		}
		settings.Init()
	})
}

func setPlannerRuntimeTags(t *testing.T, tags string) {
	t.Helper()
	t.Cleanup(settings.Init)
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_RUNTIME_TAGS", tags)
	settings.Init()
}

func (m *MockFramework) Name() string {
	return m.FrameworkName
}

func (m *MockFramework) TestPattern() string {
	if m.TestPatternValue != "" {
		return m.TestPatternValue
	}
	if m.Err != nil && len(m.TestFiles) == 0 {
		return "["
	}
	ensureMockTestFiles(m.TestFiles)
	return mockTestFilesPattern(m.TestFiles)
}

var (
	mockTestFilesMu      sync.Mutex
	mockTestFilesCreated []string
	mockTestDirsCreated  []string
)

func ensureMockTestFiles(files []string) {
	mockTestFilesMu.Lock()
	defer mockTestFilesMu.Unlock()

	cleanupMockTestFilesLocked()
	for _, file := range files {
		if file == "" {
			continue
		}

		absFile, err := filepath.Abs(file)
		if err != nil {
			panic(err)
		}
		if _, err := os.Stat(absFile); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			panic(err)
		}

		dir := filepath.Dir(absFile)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			panic(err)
		}
		mockTestDirsCreated = append(mockTestDirsCreated, dir)
		if err := os.WriteFile(absFile, []byte("# mock test\n"), 0o644); err != nil {
			panic(err)
		}
		mockTestFilesCreated = append(mockTestFilesCreated, absFile)
	}
}

func cleanupMockTestFilesLocked() {
	for _, file := range mockTestFilesCreated {
		_ = os.Remove(file)
	}
	for i := len(mockTestDirsCreated) - 1; i >= 0; i-- {
		_ = os.Remove(mockTestDirsCreated[i])
	}
	mockTestFilesCreated = nil
	mockTestDirsCreated = nil
}

func TestMain(m *testing.M) {
	discoveryCacheGitOutput = func(args ...string) ([]byte, error) {
		return nil, errors.New("test discovery cache disabled by default")
	}
	_ = os.RemoveAll(filepath.Dir(filepath.Dir(discovery.TestsFilePath)))
	code := m.Run()
	mockTestFilesMu.Lock()
	cleanupMockTestFilesLocked()
	mockTestFilesMu.Unlock()
	_ = os.RemoveAll(filepath.Dir(filepath.Dir(discovery.TestsFilePath)))
	os.Exit(code)
}

func mockTestFilesPattern(files []string) string {
	if len(files) == 0 {
		return ""
	}

	normalized := make([]string, 0, len(files))
	for _, file := range files {
		normalized = append(normalized, filepath.ToSlash(filepath.Clean(file)))
	}
	if len(normalized) == 1 {
		return normalized[0]
	}
	return "{" + strings.Join(normalized, ",") + "}"
}

func (m *MockFramework) DiscoverTests(ctx context.Context, testFiles discovery.TestFileSet) ([]testoptimization.Test, error) {
	m.mu.Lock()
	m.DiscoverTestsFiles = append(m.DiscoverTestsFiles, testFiles)
	m.mu.Unlock()
	if m.OnDiscoverTests != nil {
		m.OnDiscoverTests()
	}
	if m.DiscoverTestsErr != nil {
		return nil, m.DiscoverTestsErr
	}
	if m.Err != nil {
		return nil, m.Err
	}
	return m.Tests, writeDiscoveryTests(m.Tests)
}

func writeDiscoveryTests(tests []testoptimization.Test) error {
	if err := os.MkdirAll(filepath.Dir(discovery.TestsFilePath), 0755); err != nil {
		return err
	}
	file, err := os.Create(discovery.TestsFilePath)
	if err != nil {
		return err
	}
	defer func() {
		_ = file.Close()
	}()
	encoder := json.NewEncoder(file)
	for _, test := range tests {
		if err := encoder.Encode(test); err != nil {
			return err
		}
	}
	return nil
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

type longRunningDiscoveryFramework struct {
	MockFramework
}

func (m *longRunningDiscoveryFramework) DiscoverTests(ctx context.Context, testFiles discovery.TestFileSet) ([]testoptimization.Test, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// MockTestOptimizationClient mocks the test optimization client
type MockTestOptimizationClient struct {
	InitializeCalled    bool
	InitializeErr       error
	Settings            *net.SettingsResponseData
	SkippableTests      map[string]bool
	KnownTests          *net.KnownTestsResponseData
	TestManagementTests *net.TestManagementTestsResponseDataModules
	ShutdownCalled      bool
	Tags                map[string]string
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

func (m *MockTestOptimizationClient) GetKnownTests() *net.KnownTestsResponseData {
	return m.KnownTests
}

func (m *MockTestOptimizationClient) GetTestManagementTestsData() *net.TestManagementTestsResponseDataModules {
	return m.TestManagementTests
}

func (m *MockTestOptimizationClient) StoreCacheAndExit() {
	m.ShutdownCalled = true
}

type waitForDiscoveryOptimizationClient struct {
	MockTestOptimizationClient
	discoveryStarted <-chan struct{}
	mu               sync.Mutex
	timedOut         bool
}

func (m *waitForDiscoveryOptimizationClient) GetSettings() *net.SettingsResponseData {
	select {
	case <-m.discoveryStarted:
	case <-time.After(500 * time.Millisecond):
		m.mu.Lock()
		m.timedOut = true
		m.mu.Unlock()
	}
	return m.Settings
}

func (m *waitForDiscoveryOptimizationClient) TimedOut() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.timedOut
}

type MockTestSuiteDurationsClient struct {
	Durations map[string]map[string]testoptimization.TestSuiteDurationInfo
	Called    bool
}

func (m *MockTestSuiteDurationsClient) GetTestSuiteDurations() map[string]map[string]testoptimization.TestSuiteDurationInfo {
	m.Called = true
	if m.Durations == nil {
		return map[string]map[string]testoptimization.TestSuiteDurationInfo{}
	}
	return m.Durations
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

func assertFileContent(t *testing.T, path string, expected string) {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read %s: %v", path, err)
	}
	if string(content) != expected {
		t.Fatalf("expected %s content %q, got %q", path, expected, string(content))
	}
}

func gitTestEnv() []string {
	return append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
	)
}

func testOptimizationSettings(tiaEnabled, testsSkipping, testManagementEnabled bool) *net.SettingsResponseData {
	settings := &net.SettingsResponseData{
		ItrEnabled:    tiaEnabled,
		TestsSkipping: testsSkipping,
	}
	settings.TestManagement.Enabled = testManagementEnabled
	return settings
}

func testOptimizationClientRequiringFullDiscovery() *MockTestOptimizationClient {
	unmatchedSkippableTest := testoptimization.Test{
		Module: "rspec",
		Suite:  "UnmatchedSuite",
		Name:   "unmatched",
	}
	return &MockTestOptimizationClient{
		Settings: testOptimizationSettings(true, true, false),
		SkippableTests: map[string]bool{
			unmatchedSkippableTest.DatadogTestId(): true,
		},
	}
}

func TestNew(t *testing.T) {
	runner := New()

	if runner == nil {
		t.Error("New() should return non-nil TestPlanner")
		return
	}

	if len(runner.testFiles) != 0 {
		t.Error("New() should initialize testFiles to empty map")
	}

	if len(runner.suiteAggregates) != 0 {
		t.Error("New() should initialize suiteAggregates to empty map")
	}

	if len(runner.suitesBySourceFile) != 0 {
		t.Error("New() should initialize suitesBySourceFile to empty map")
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

	if runner.durationsClient == nil {
		t.Error("New() should initialize durationsClient")
	}
}

func TestNewWithDependencies(t *testing.T) {
	mockPlatformDetector := &MockPlatformDetector{}
	mockOptimizationClient := &MockTestOptimizationClient{}
	mockDurationsClient := &MockTestSuiteDurationsClient{}
	mockCIProviderDetector := newDefaultMockCIProviderDetector()

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, mockDurationsClient, mockCIProviderDetector)

	if runner == nil {
		t.Error("NewWithDependencies() should return non-nil TestPlanner")
		return
	}

	if runner.platformDetector != mockPlatformDetector {
		t.Error("NewWithDependencies() should use injected platformDetector")
	}

	if runner.optimizationClient != mockOptimizationClient {
		t.Error("NewWithDependencies() should use injected optimizationClient")
	}

	if runner.durationsClient != mockDurationsClient {
		t.Error("NewWithDependencies() should use injected durationsClient")
	}

	if len(runner.testFiles) != 0 {
		t.Error("NewWithDependencies() should initialize testFiles to empty map")
	}

	if len(runner.suiteAggregates) != 0 {
		t.Error("NewWithDependencies() should initialize suiteAggregates to empty map")
	}

	if len(runner.suitesBySourceFile) != 0 {
		t.Error("NewWithDependencies() should initialize suitesBySourceFile to empty map")
	}
}

func TestTestPlanner_Setup_WithParallelRunners(t *testing.T) {
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
	logs := captureLogs(t)

	// Setup mocks for a test with 40% skippable percentage
	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		Tests: []testoptimization.Test{
			{Module: "rspec", Suite: "TestSuite1", Name: "test1", Parameters: "", SuiteSourceFile: "test/file1_test.rb"},
			{Module: "rspec", Suite: "TestSuite1", Name: "test2", Parameters: "", SuiteSourceFile: "test/file1_test.rb"},
			{Module: "rspec", Suite: "TestSuite2", Name: "test3", Parameters: "", SuiteSourceFile: "test/file2_test.rb"},
			{Module: "rspec", Suite: "TestSuite3", Name: "test4", Parameters: "", SuiteSourceFile: "test/file3_test.rb"},
			{Module: "rspec", Suite: "TestSuite4", Name: "test5", Parameters: "", SuiteSourceFile: "test/file4_test.rb"},
		},
	}

	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags:         map[string]string{"platform": "ruby"},
		Framework:    mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{Platform: mockPlatform}
	mockOptimizationClient := &MockTestOptimizationClient{
		Settings: testOptimizationSettings(true, true, false),
		SkippableTests: map[string]bool{
			(&testoptimization.Test{Module: "rspec", Suite: "TestSuite1", Name: "test2"}).DatadogTestId(): true, // Skip test2
			(&testoptimization.Test{Module: "rspec", Suite: "TestSuite4", Name: "test5"}).DatadogTestId(): true, // Skip test5
		},
	}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, &MockTestSuiteDurationsClient{}, newDefaultMockCIProviderDetector())
	var reportOutput bytes.Buffer
	runner.reportWriter = &reportOutput

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

	report := reportOutput.String()
	for _, expectedReportLine := range []string{
		"Split\n",
		"  Runners: 1\n",
		"  Expected wall time:",
		"  Imbalance:",
		"  Total estimated runtime:",
	} {
		if !strings.Contains(report, expectedReportLine) {
			t.Errorf("Expected report to contain %q, got report: %s", expectedReportLine, report)
		}
	}

	logOutput := logs.String()
	if strings.Contains(logOutput, "Test execution planning completed") {
		t.Errorf("Expected selected split information to be reported, not logged, got logs: %s", logOutput)
	}
}

func TestTestPlanner_Plan_WritesManifestAndRunnerLayout(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM", "1")
	_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM", "1")
	defer func() {
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM")
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM")
	}()
	settings.Init()

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		TestFiles:     []string{"test/file1_test.rb", "test/file2_test.rb"},
		Tests: []testoptimization.Test{
			{Module: "rspec", Suite: "TestSuite1", Name: "test1", Parameters: "", SuiteSourceFile: "test/file1_test.rb"},
			{Module: "rspec", Suite: "TestSuite2", Name: "test2", Parameters: "", SuiteSourceFile: "test/file2_test.rb"},
		},
	}
	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags:         map[string]string{"platform": "ruby"},
		Framework:    mockFramework,
	}

	runner := NewWithDependencies(
		&MockPlatformDetector{Platform: mockPlatform},
		&MockTestOptimizationClient{SkippableTests: map[string]bool{}},
		&MockTestSuiteDurationsClient{},
		newDefaultMockCIProviderDetector(),
	)

	if err := runner.Plan(context.Background()); err != nil {
		t.Fatalf("Plan() should not return error, got: %v", err)
	}

	assertFileContent(t, constants.ManifestPath, constants.ManifestVersion+"\n")

	expectedTestFiles := "test/file1_test.rb\ntest/file2_test.rb\n"
	assertFileContent(t, constants.TestFilesOutputPath, expectedTestFiles)

	assertFileContent(t, constants.ParallelRunnersOutputPath, "1")
	assertFileContent(t, constants.SkippablePercentageOutputPath, "0.00")

	assertFileContent(t, filepath.Join(constants.TestsSplitDir, "runner-0"), expectedTestFiles)
}

func TestTestPlanner_Plan_DoesNotPrintReportWhenDisabled(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM", "1")
	_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM", "1")
	_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_REPORT_ENABLED", "false")
	defer func() {
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM")
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM")
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_REPORT_ENABLED")
		settings.Init()
	}()
	settings.Init()

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

	runner := NewWithDependencies(
		&MockPlatformDetector{Platform: mockPlatform},
		&MockTestOptimizationClient{SkippableTests: map[string]bool{}},
		&MockTestSuiteDurationsClient{},
		newDefaultMockCIProviderDetector(),
	)
	var output strings.Builder
	runner.reportWriter = &output

	if err := runner.Plan(context.Background()); err != nil {
		t.Fatalf("Plan() should not return error, got: %v", err)
	}
	if output.Len() != 0 {
		t.Errorf("Expected no report output when report is disabled, got: %s", output.String())
	}
}

func TestTestPlanner_Plan_ChoosesParallelismFromFanoutAdjustedSplit(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM", "2")
	_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM", "4")
	defer func() {
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM")
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM")
	}()
	settings.Init()

	var tests []testoptimization.Test
	skippableTests := map[string]bool{}
	for suiteIndex := range 4 {
		suite := fmt.Sprintf("TestSuite%d", suiteIndex)
		sourceFile := fmt.Sprintf("test/file%d_test.rb", suiteIndex)
		for testIndex := range 10 {
			name := fmt.Sprintf("test%d", testIndex)
			test := testoptimization.Test{
				Module:          "rspec",
				Suite:           suite,
				Name:            name,
				Parameters:      "",
				SuiteSourceFile: sourceFile,
			}
			tests = append(tests, test)
			if testIndex > 0 {
				skippableTests[test.DatadogTestId()] = true
			}
		}
	}

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		Tests:         tests,
	}
	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags:         map[string]string{"platform": "ruby"},
		Framework:    mockFramework,
	}

	runner := NewWithDependencies(
		&MockPlatformDetector{Platform: mockPlatform},
		&MockTestOptimizationClient{
			Settings:       testOptimizationSettings(true, true, false),
			SkippableTests: skippableTests,
		},
		&MockTestSuiteDurationsClient{},
		newDefaultMockCIProviderDetector(),
	)

	if err := runner.Plan(context.Background()); err != nil {
		t.Fatalf("Plan() should not return error, got: %v", err)
	}

	assertFileContent(t, constants.SkippablePercentageOutputPath, "90.00")
	assertFileContent(t, constants.ParallelRunnersOutputPath, "2")
}

func TestTestPlanner_Setup_WithCIProvider(t *testing.T) {
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
		Settings: testOptimizationSettings(true, true, false),
		SkippableTests: map[string]bool{
			(&testoptimization.Test{Module: "rspec", Suite: "TestSuite1", Name: "test1"}).DatadogTestId(): true, // Skip test1 = 50% skippable
		},
	}

	// Mock CI provider that should be called
	mockCIProvider := &MockCIProvider{
		ProviderName: "github",
	}
	mockCIProviderDetector := &MockCIProviderDetector{
		CIProvider: mockCIProvider,
	}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, &MockTestSuiteDurationsClient{}, mockCIProviderDetector)

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

func TestTestPlanner_Setup_CIProviderDetectionFailure(t *testing.T) {
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

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, &MockTestSuiteDurationsClient{}, mockCIProviderDetector)

	// Run Setup - should succeed even if CI provider detection fails
	err := runner.Plan(context.Background())
	if err != nil {
		t.Fatalf("Setup() should not fail when CI provider detection fails, got: %v", err)
	}
}

func TestTestPlanner_Setup_CIProviderConfigureFailure(t *testing.T) {
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

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, &MockTestSuiteDurationsClient{}, mockCIProviderDetector)

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

func TestTestPlanner_Setup_WithTestSplit(t *testing.T) {
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
			TestFiles:     []string{"test/file1_test.rb", "test/file2_test.rb"},
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

		runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, &MockTestSuiteDurationsClient{}, newDefaultMockCIProviderDetector())

		// Run Setup
		err := runner.Plan(context.Background())
		if err != nil {
			t.Fatalf("Setup() should not return error, got: %v", err)
		}

		// Verify that tests-split directory was created
		if _, err := os.Stat(constants.TestsSplitDir); os.IsNotExist(err) {
			t.Error("Expected tests-split directory to be created when parallelRunners = 1")
		}

		// Verify that runner-0 file was created
		runnerFilePath := filepath.Join(constants.TestsSplitDir, "runner-0")
		if _, err := os.Stat(runnerFilePath); os.IsNotExist(err) {
			t.Error("Expected runner-0 file to be created when parallelRunners = 1")
		}

		// Verify that runner-0 contains the same content as test-files.txt
		testFilesContent, err := os.ReadFile(constants.TestFilesOutputPath)
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
			TestFiles:     []string{"test/file1_test.rb", "test/file2_test.rb", "test/file3_test.rb"},
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

		expectedParallelRunnersCount := 2
		maxParallelism := 4
		// Set environment variables to force multiple parallel runners
		_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM", "2")
		_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM", strconv.Itoa(maxParallelism))
		defer func() {
			_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM")
			_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM")
		}()

		// Reinitialize settings to pick up environment variables
		settings.Init()

		runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, &MockTestSuiteDurationsClient{}, newDefaultMockCIProviderDetector())

		// Run Setup
		err := runner.Plan(context.Background())
		if err != nil {
			t.Fatalf("Setup() should not return error, got: %v", err)
		}

		// Verify that tests-split directory was created
		if _, err := os.Stat(constants.TestsSplitDir); os.IsNotExist(err) {
			t.Error("Expected tests-split directory to be created")
		}

		// With this split, 2 runners are as fast as 3 and more balanced.
		// Verify runner files exist
		for i := range expectedParallelRunnersCount {
			runnerPath := filepath.Join(constants.TestsSplitDir, fmt.Sprintf("runner-%d", i))
			if _, err := os.Stat(runnerPath); os.IsNotExist(err) {
				t.Errorf("Expected runner-%d file to exist", i)
			}
		}

		// Verify content of runner files
		// With the test distribution (file1: 2 tests, file2: 1 test, file3: 1 test),
		// expected: runner 0 gets file1 (2 tests), runner 1 gets file2+file3 (2 tests).
		runner0Content, err := os.ReadFile(filepath.Join(constants.TestsSplitDir, "runner-0"))
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
			runnerPath := filepath.Join(constants.TestsSplitDir, fmt.Sprintf("runner-%d", i))
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

// TestTestPlanner_Plan_SubdirRootRelativeDiscovery_WritesNormalizedPaths
// reproduces the end-to-end bug from issue #33: Plan writes repo-root-relative paths
// that become invalid for workers running from a monorepo subdirectory.
func TestTestPlanner_Plan_SubdirRootRelativeDiscovery_WritesNormalizedPaths(t *testing.T) {
	// Create a temp monorepo: repoRoot/core/spec/...
	repoRoot := t.TempDir()

	// Initialize git repo at the root
	cmd := exec.Command("git", "init")
	cmd.Dir = repoRoot
	cmd.Env = gitTestEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to init git repo: %v\n%s", err, string(out))
	}
	cmd = exec.Command("git", "commit", "--allow-empty", "-m", "init")
	cmd.Dir = repoRoot
	cmd.Env = append(gitTestEnv(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to create initial commit: %v\n%s", err, string(out))
	}

	coreDir := filepath.Join(repoRoot, "core")
	_ = os.MkdirAll(filepath.Join(coreDir, "spec", "models"), 0755)
	_ = os.WriteFile(filepath.Join(coreDir, "spec", "models", "order_spec.rb"), []byte("# spec"), 0644)
	_ = os.WriteFile(filepath.Join(coreDir, "spec", "models", "payment_spec.rb"), []byte("# spec"), 0644)

	// chdir into subdirectory
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(coreDir)
	ciUtils.ResetCwdSubdirPrefixForTesting()
	t.Cleanup(ciUtils.ResetCwdSubdirPrefixForTesting)

	// Set parallelism to 1
	_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM", "1")
	_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM", "1")
	defer func() {
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM")
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM")
	}()
	settings.Init()

	// Full discovery returns repo-root-relative paths (the bug)
	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		Tests: []testoptimization.Test{
			{Suite: "Order", Name: "should be valid", Parameters: "", SuiteSourceFile: "core/spec/models/order_spec.rb"},
			{Suite: "Payment", Name: "should process", Parameters: "", SuiteSourceFile: "core/spec/models/payment_spec.rb"},
		},
	}

	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags:         map[string]string{"platform": "ruby"},
		Framework:    mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{Platform: mockPlatform}
	mockOptimizationClient := testOptimizationClientRequiringFullDiscovery()

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, &MockTestSuiteDurationsClient{}, newDefaultMockCIProviderDetector())

	err := runner.Plan(context.Background())
	if err != nil {
		t.Fatalf("Plan() should not return error, got: %v", err)
	}

	// Verify test-files.txt contains CWD-relative paths
	testFilesContent, err := os.ReadFile(constants.TestFilesOutputPath)
	if err != nil {
		t.Fatalf("Failed to read test-files.txt: %v", err)
	}

	testFilesStr := string(testFilesContent)
	if strings.Contains(testFilesStr, "core/") {
		t.Errorf("test-files.txt should not contain repo-root prefix 'core/', got:\n%s", testFilesStr)
	}

	expectedContent := "spec/models/order_spec.rb\nspec/models/payment_spec.rb\n"
	if testFilesStr != expectedContent {
		t.Errorf("Expected test-files.txt content:\n%s\nGot:\n%s", expectedContent, testFilesStr)
	}

	// Verify runner-0 split file also contains CWD-relative paths
	runnerContent, err := os.ReadFile(filepath.Join(constants.TestsSplitDir, "runner-0"))
	if err != nil {
		t.Fatalf("Failed to read runner-0: %v", err)
	}

	runnerStr := string(runnerContent)
	if strings.Contains(runnerStr, "core/") {
		t.Errorf("runner-0 should not contain repo-root prefix 'core/', got:\n%s", runnerStr)
	}
}
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	originalLogger := slog.Default()
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)
	t.Cleanup(func() {
		slog.SetDefault(originalLogger)
	})
	return &buf
}

func TestTestPlanner_PreparePlanningData_Success(t *testing.T) {
	ctx := context.Background()
	ciUtils.ResetCITags()
	t.Cleanup(ciUtils.ResetCITags)

	// Setup mocks
	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		TestFiles: []string{
			"test/file1_test.rb",
			"test/file2_test.rb",
			"test/fast_only_test.rb",
		},
		Tests: []testoptimization.Test{
			{Module: "rspec", Suite: "TestSuite1", Name: "test1", Parameters: "", SuiteSourceFile: "test/file1_test.rb"},
			{Module: "rspec", Suite: "TestSuite1", Name: "test2", Parameters: "", SuiteSourceFile: "test/file1_test.rb"},
			{Module: "rspec", Suite: "TestSuite2", Name: "test3", Parameters: "", SuiteSourceFile: "test/file2_test.rb"},
			{Module: "rspec", Suite: "TestSuite3", Name: "test4", Parameters: "", SuiteSourceFile: "test/file3_test.rb"},
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
		Settings: testOptimizationSettings(true, true, false),
		SkippableTests: map[string]bool{
			(&testoptimization.Test{Module: "rspec", Suite: "TestSuite1", Name: "test2"}).DatadogTestId(): true, // Skip test2
			(&testoptimization.Test{Module: "rspec", Suite: "TestSuite3", Name: "test4"}).DatadogTestId(): true, // Skip test4
		},
	}
	mockDurationsClient := &MockTestSuiteDurationsClient{
		Durations: map[string]map[string]testoptimization.TestSuiteDurationInfo{
			"rspec": {
				"TestSuite1": {
					SourceFile: "test/file1_test.rb",
					Duration:   testoptimization.DurationPercentiles{P50: "7000000", P90: "2000000"},
				},
			},
		},
	}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, mockDurationsClient, newDefaultMockCIProviderDetector())

	err := runner.PreparePlanningData(ctx)

	if err != nil {
		t.Errorf("PreparePlanningData() should not return error, got: %v", err)
	}

	// Verify optimization client was initialized
	if !mockOptimizationClient.InitializeCalled {
		t.Error("PreparePlanningData() should initialize optimization client")
	}

	// Verify tags were passed to optimization client
	if mockOptimizationClient.Tags["platform"] != "ruby" {
		t.Error("PreparePlanningData() should pass platform tags to optimization client")
	}

	// Verify optimization client was shut down
	if !mockOptimizationClient.ShutdownCalled {
		t.Error("PreparePlanningData() should shutdown optimization client")
	}

	expectedFiles := map[string]bool{
		"test/file1_test.rb": true, // test1 is not skipped
		"test/file2_test.rb": true, // test3 is not skipped
		"test/file3_test.rb": true, // test4 is skipped but the source file is discovered
	}

	if len(runner.testFiles) != len(expectedFiles) {
		t.Errorf("PreparePlanningData() should result in %d test files, got %d", len(expectedFiles), len(runner.testFiles))
	}

	if weightedFiles := runner.calculateFileWeights(); len(weightedFiles) != 2 {
		t.Errorf("Expected weighted files to omit fully skipped and fast-only files, got %v", weightedFiles)
	}
	expectedTestFileWeights := map[string]int{
		"test/file1_test.rb": 3,
		"test/file2_test.rb": DefaultTestFileWeight,
	}
	if len(runner.testFileWeights) != len(expectedTestFileWeights) {
		t.Errorf("Expected precomputed test file weights to have %d entries, got %v", len(expectedTestFileWeights), runner.testFileWeights)
	}
	for testFile, expectedWeight := range expectedTestFileWeights {
		if runner.testFileWeights[testFile] != expectedWeight {
			t.Errorf("Expected precomputed weight for %s to be %d, got %d", testFile, expectedWeight, runner.testFileWeights[testFile])
		}
	}

	for file := range runner.testFiles {
		if !expectedFiles[file] {
			t.Errorf("Unexpected test file: %s", file)
		}
	}

	suite1 := runner.suiteAggregates[testSuiteKey{Module: "rspec", Suite: "TestSuite1"}]
	if suite1.NumTests != 2 {
		t.Errorf("Expected Suite1 total test count 2, got %d", suite1.NumTests)
	}
	if suite1.NumTestsSkipped != 1 {
		t.Errorf("Expected Suite1 skipped test count 1, got %d", suite1.NumTestsSkipped)
	}

	if weight, ok := runner.testFileWeight("test/file1_test.rb"); !ok || weight != 3 {
		t.Errorf("Expected file1 weight to use backend p50 adjusted for skipped tests and converted to 3ms, got weight=%d ok=%t", weight, ok)
	}

	expectedFile2Weight := int(time.Second / time.Millisecond)
	if weight, ok := runner.testFileWeight("test/file2_test.rb"); !ok || weight != expectedFile2Weight {
		t.Errorf("Expected file2 weight to use count fallback %d, got weight=%d ok=%t", expectedFile2Weight, weight, ok)
	}

	// Verify skippable percentage was calculated correctly (2 out of 4 tests skipped = 50%)
	expectedPercentage := 50.0
	if runner.skippablePercentage != expectedPercentage {
		t.Errorf("PreparePlanningData() should calculate skippable percentage as %.2f, got %.2f",
			expectedPercentage, runner.skippablePercentage)
	}

	if !mockDurationsClient.Called {
		t.Error("PreparePlanningData() should fetch test suite durations")
	}
}

func TestTestPlanner_PreparePlanningData_DisabledTestManagementTestsAreSkipped(t *testing.T) {
	ctx := context.Background()
	ciUtils.ResetCITags()
	t.Cleanup(ciUtils.ResetCITags)

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		Tests: []testoptimization.Test{
			{Module: "rspec", Suite: "Suite1", Name: "test1", Parameters: "", SuiteSourceFile: "spec/file1_spec.rb"},
			{Module: "rspec", Suite: "Suite1", Name: "test2", Parameters: "", SuiteSourceFile: "spec/file1_spec.rb"},
			{Module: "rspec", Suite: "Suite2", Name: "test3", Parameters: `{"arguments":{},"metadata":{"scoped_id":"1:2"}}`, SuiteSourceFile: "spec/file2_spec.rb"},
			{Module: "rspec", Suite: "Suite3", Name: "test4", Parameters: "", SuiteSourceFile: "spec/file3_spec.rb"},
		},
	}
	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags:         map[string]string{"platform": "ruby"},
		Framework:    mockFramework,
	}
	mockOptimizationClient := &MockTestOptimizationClient{
		Settings: testOptimizationSettings(true, true, true),
		SkippableTests: map[string]bool{
			(&testoptimization.Test{Module: "rspec", Suite: "Suite1", Name: "test2"}).DatadogTestId(): true,
		},
		TestManagementTests: &net.TestManagementTestsResponseDataModules{
			Modules: map[string]net.TestManagementTestsResponseDataSuites{
				"rspec": {
					Suites: map[string]net.TestManagementTestsResponseDataTests{
						"Suite2": {
							Tests: map[string]net.TestManagementTestsResponseDataTestProperties{
								"test3": {Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{Disabled: true}},
							},
						},
						"Suite3": {
							Tests: map[string]net.TestManagementTestsResponseDataTestProperties{
								"test4": {Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{Quarantined: true}},
							},
						},
					},
				},
			},
		},
	}

	runner := NewWithDependencies(
		&MockPlatformDetector{Platform: mockPlatform},
		mockOptimizationClient,
		&MockTestSuiteDurationsClient{},
		newDefaultMockCIProviderDetector(),
	)

	if err := runner.PreparePlanningData(ctx); err != nil {
		t.Fatalf("PreparePlanningData() should not return error, got: %v", err)
	}

	suite1 := runner.suiteAggregates[testSuiteKey{Module: "rspec", Suite: "Suite1"}]
	if suite1.NumTests != 2 || suite1.NumTestsSkipped != 1 {
		t.Errorf("Expected Suite1 to skip only the TIA-skippable test, got %+v", suite1)
	}

	suite2 := runner.suiteAggregates[testSuiteKey{Module: "rspec", Suite: "Suite2"}]
	if suite2.NumTests != 1 || suite2.NumTestsSkipped != 1 {
		t.Errorf("Expected Suite2 disabled test to be skipped, got %+v", suite2)
	}

	suite3 := runner.suiteAggregates[testSuiteKey{Module: "rspec", Suite: "Suite3"}]
	if suite3.NumTests != 1 || suite3.NumTestsSkipped != 0 {
		t.Errorf("Expected Suite3 quarantined test to remain runnable, got %+v", suite3)
	}

	if runner.planReport.SkippableTestsCount != 2 {
		t.Errorf("Expected planner skip set to include TIA-skippable and disabled tests, got %d", runner.planReport.SkippableTestsCount)
	}
}

func TestTestPlanner_PreparePlanningData_TIASkipsRequireParametersMatch(t *testing.T) {
	ctx := context.Background()
	ciUtils.ResetCITags()
	t.Cleanup(ciUtils.ResetCITags)

	parameterizedRunnableTest := testoptimization.Test{
		Module:          "rspec",
		Suite:           "Suite1",
		Name:            "same name",
		Parameters:      `{"arguments":{},"metadata":{"scoped_id":"1:1"}}`,
		SuiteSourceFile: "spec/file1_spec.rb",
	}
	parameterizedSkippedTest := testoptimization.Test{
		Module:          "rspec",
		Suite:           "Suite1",
		Name:            "same name",
		Parameters:      `{"arguments":{},"metadata":{"scoped_id":"1:2"}}`,
		SuiteSourceFile: "spec/file1_spec.rb",
	}
	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		Tests: []testoptimization.Test{
			parameterizedRunnableTest,
			parameterizedSkippedTest,
		},
	}
	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags:         map[string]string{"platform": "ruby"},
		Framework:    mockFramework,
	}
	mockOptimizationClient := &MockTestOptimizationClient{
		Settings: testOptimizationSettings(true, true, false),
		SkippableTests: map[string]bool{
			(&testoptimization.Test{Module: "rspec", Suite: "Suite1", Name: "same name"}).DatadogTestId(): true,
			parameterizedSkippedTest.DatadogTestId():                                                      true,
		},
	}

	runner := NewWithDependencies(
		&MockPlatformDetector{Platform: mockPlatform},
		mockOptimizationClient,
		&MockTestSuiteDurationsClient{},
		newDefaultMockCIProviderDetector(),
	)

	if err := runner.PreparePlanningData(ctx); err != nil {
		t.Fatalf("PreparePlanningData() should not return error, got: %v", err)
	}

	aggregate := runner.suiteAggregates[testSuiteKey{Module: "rspec", Suite: "Suite1"}]
	if aggregate.NumTests != 2 || aggregate.NumTestsSkipped != 1 {
		t.Errorf("Expected only the exact TIA parameter match to be skipped, got %+v", aggregate)
	}
}

func TestTestPlanner_PreparePlanningData_ModuleQualifiedSkipsDoNotCrossModules(t *testing.T) {
	ctx := context.Background()
	ciUtils.ResetCITags()
	t.Cleanup(ciUtils.ResetCITags)

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		Tests: []testoptimization.Test{
			{Module: "module-a", Suite: "SharedSuite", Name: "same name", Parameters: "", SuiteSourceFile: "spec/module_a_spec.rb"},
			{Module: "module-b", Suite: "SharedSuite", Name: "same name", Parameters: "", SuiteSourceFile: "spec/module_b_spec.rb"},
			{Module: "module-c", Suite: "ManagedSuite", Name: "same name", Parameters: "", SuiteSourceFile: "spec/module_c_spec.rb"},
			{Module: "module-d", Suite: "ManagedSuite", Name: "same name", Parameters: "", SuiteSourceFile: "spec/module_d_spec.rb"},
		},
	}
	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags:         map[string]string{"platform": "ruby"},
		Framework:    mockFramework,
	}
	mockOptimizationClient := &MockTestOptimizationClient{
		Settings: testOptimizationSettings(true, true, true),
		SkippableTests: map[string]bool{
			(&testoptimization.Test{Module: "module-a", Suite: "SharedSuite", Name: "same name"}).DatadogTestId(): true,
		},
		TestManagementTests: &net.TestManagementTestsResponseDataModules{
			Modules: map[string]net.TestManagementTestsResponseDataSuites{
				"module-c": {
					Suites: map[string]net.TestManagementTestsResponseDataTests{
						"ManagedSuite": {
							Tests: map[string]net.TestManagementTestsResponseDataTestProperties{
								"same name": {Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{Disabled: true}},
							},
						},
					},
				},
			},
		},
	}

	runner := NewWithDependencies(
		&MockPlatformDetector{Platform: mockPlatform},
		mockOptimizationClient,
		&MockTestSuiteDurationsClient{},
		newDefaultMockCIProviderDetector(),
	)

	if err := runner.PreparePlanningData(ctx); err != nil {
		t.Fatalf("PreparePlanningData() should not return error, got: %v", err)
	}

	expectSkipped := func(module, suite string, skipped int) {
		t.Helper()
		aggregate := runner.suiteAggregates[testSuiteKey{Module: module, Suite: suite}]
		if aggregate.NumTests != 1 || aggregate.NumTestsSkipped != skipped {
			t.Errorf("Expected %s/%s to skip %d tests, got %+v", module, suite, skipped, aggregate)
		}
	}

	expectSkipped("module-a", "SharedSuite", 1)
	expectSkipped("module-b", "SharedSuite", 0)
	expectSkipped("module-c", "ManagedSuite", 1)
	expectSkipped("module-d", "ManagedSuite", 0)
}

func TestTestPlanner_PreparePlanningData_TestManagementDoesNotKeepFullDiscoveryWhenTIASkippingDisabled(t *testing.T) {
	t.Chdir(t.TempDir())
	ctx := context.Background()
	ciUtils.ResetCITags()
	t.Cleanup(ciUtils.ResetCITags)

	mockFramework := &MockFramework{
		FrameworkName:    "rspec",
		TestFiles:        []string{"spec/file1_spec.rb", "spec/file2_spec.rb", "spec/file3_spec.rb"},
		DiscoverTestsErr: errors.New("full discovery cancelled because TIA skipping is disabled"),
		Tests: []testoptimization.Test{
			{Module: "rspec", Suite: "Suite1", Name: "runnable", Parameters: "", SuiteSourceFile: "spec/file1_spec.rb"},
			{Module: "rspec", Suite: "Suite1", Name: "disabled", Parameters: "", SuiteSourceFile: "spec/file1_spec.rb"},
			{Module: "rspec", Suite: "Suite2", Name: "not_applied", Parameters: "", SuiteSourceFile: "spec/file2_spec.rb"},
			{Module: "rspec", Suite: "Suite3", Name: "disabled", Parameters: "", SuiteSourceFile: "spec/file3_spec.rb"},
		},
	}
	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags:         map[string]string{"platform": "ruby"},
		Framework:    mockFramework,
	}
	mockOptimizationClient := &MockTestOptimizationClient{
		Settings: testOptimizationSettings(false, false, true),
		SkippableTests: map[string]bool{
			(&testoptimization.Test{Module: "rspec", Suite: "Suite2", Name: "not_applied"}).DatadogTestId(): true,
		},
		TestManagementTests: &net.TestManagementTestsResponseDataModules{
			Modules: map[string]net.TestManagementTestsResponseDataSuites{
				"rspec": {
					Suites: map[string]net.TestManagementTestsResponseDataTests{
						"Suite1": {
							Tests: map[string]net.TestManagementTestsResponseDataTestProperties{
								"disabled": {Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{Disabled: true}},
							},
						},
						"Suite3": {
							Tests: map[string]net.TestManagementTestsResponseDataTestProperties{
								"disabled": {Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{Disabled: true}},
							},
						},
					},
				},
			},
		},
	}

	runner := NewWithDependencies(
		&MockPlatformDetector{Platform: mockPlatform},
		mockOptimizationClient,
		&MockTestSuiteDurationsClient{},
		newDefaultMockCIProviderDetector(),
	)

	if err := runner.PreparePlanningData(ctx); err != nil {
		t.Fatalf("PreparePlanningData() should not return error, got: %v", err)
	}

	if len(runner.suiteAggregates) != 0 {
		t.Errorf("Expected fast discovery fallback without full-discovery suite aggregates, got %+v", runner.suiteAggregates)
	}

	if _, ok := runner.testFileWeights["spec/file3_spec.rb"]; !ok {
		t.Errorf("Expected disabled test management file to remain runnable in fast discovery fallback, got %v", runner.testFileWeights)
	}

	if len(runner.testFileWeights) != len(mockFramework.TestFiles) {
		t.Errorf("Expected fast discovery fallback to keep all discovered files, got %v", runner.testFileWeights)
	}

	if runner.planReport.SkippableTestsCount != 2 {
		t.Errorf("Expected planner skip set to include fetched disabled tests for reporting, got %d", runner.planReport.SkippableTestsCount)
	}
}

func TestTestPlanner_PreparePlanningData_CancelsFullDiscoveryWhenNoTIASkippableTests(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ciUtils.ResetCITags()
	t.Cleanup(ciUtils.ResetCITags)
	logs := captureLogs(t)

	mockFramework := &longRunningDiscoveryFramework{
		MockFramework: MockFramework{
			FrameworkName: "rspec",
			TestFiles:     []string{"spec/local_spec.rb"},
		},
	}
	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags:         map[string]string{"platform": "ruby"},
		Framework:    mockFramework,
	}
	mockOptimizationClient := &MockTestOptimizationClient{
		Settings:       testOptimizationSettings(true, true, false),
		SkippableTests: map[string]bool{},
	}

	runner := NewWithDependencies(
		&MockPlatformDetector{Platform: mockPlatform},
		mockOptimizationClient,
		&MockTestSuiteDurationsClient{},
		newDefaultMockCIProviderDetector(),
	)

	if err := runner.PreparePlanningData(ctx); err != nil {
		t.Fatalf("PreparePlanningData() should not return error, got: %v", err)
	}

	if _, ok := runner.testFiles["spec/local_spec.rb"]; !ok {
		t.Errorf("Expected fast-discovered file after empty TIA skip set, got %v", runner.testFiles)
	}
	if len(runner.suiteAggregates) != 0 {
		t.Errorf("Expected cancelled full discovery to leave no suite aggregates, got %+v", runner.suiteAggregates)
	}
	if runner.skippablePercentage != 0 {
		t.Errorf("Expected zero skippable percentage without full discovery data, got %.2f", runner.skippablePercentage)
	}
	if !strings.Contains(logs.String(), "No TIA-skippable tests found for this run, cancelling full test discovery") {
		t.Errorf("Expected log about skipping full discovery when no TIA-skippable tests exist, got: %s", logs.String())
	}
}

func TestTestPlanner_PreparePlanningData_RunsFullDiscoveryInParallelWithBackend(t *testing.T) {
	t.Chdir(t.TempDir())
	ctx := context.Background()
	ciUtils.ResetCITags()
	t.Cleanup(ciUtils.ResetCITags)

	discoveredTest := testoptimization.Test{
		Module:          "rspec",
		Suite:           "ParallelSuite",
		Name:            "test1",
		SuiteSourceFile: "spec/parallel_spec.rb",
	}
	discoveryStarted := make(chan struct{})
	var closeDiscoveryStarted sync.Once
	mockFramework := &MockFramework{
		FrameworkName:    "rspec",
		TestPatternValue: filepath.Join("spec", "**", "*_spec.rb"),
		Tests:            []testoptimization.Test{discoveredTest},
		OnDiscoverTests: func() {
			closeDiscoveryStarted.Do(func() {
				close(discoveryStarted)
			})
		},
	}
	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags:         map[string]string{"platform": "ruby"},
		Framework:    mockFramework,
	}
	mockOptimizationClient := &waitForDiscoveryOptimizationClient{
		MockTestOptimizationClient: MockTestOptimizationClient{
			Settings: testOptimizationSettings(true, true, false),
			SkippableTests: map[string]bool{
				discoveredTest.DatadogTestId(): true,
			},
		},
		discoveryStarted: discoveryStarted,
	}

	runner := NewWithDependencies(
		&MockPlatformDetector{Platform: mockPlatform},
		mockOptimizationClient,
		&MockTestSuiteDurationsClient{},
		newDefaultMockCIProviderDetector(),
	)

	if err := runner.PreparePlanningData(ctx); err != nil {
		t.Fatalf("PreparePlanningData() should not return error, got: %v", err)
	}
	if mockOptimizationClient.TimedOut() {
		t.Fatal("backend settings finished before full discovery started; expected full discovery to run in parallel")
	}
	if len(mockFramework.DiscoverTestsFiles) != 1 {
		t.Fatalf("expected full discovery to run once, got %d calls", len(mockFramework.DiscoverTestsFiles))
	}
	aggregate := runner.suiteAggregates[testSuiteKey{Module: "rspec", Suite: "ParallelSuite"}]
	if aggregate.NumTests != 1 || aggregate.NumTestsSkipped != 1 {
		t.Fatalf("aggregate = %+v, want one skipped test from full discovery", aggregate)
	}
}

func TestTestPlanner_PreparePlanningData_UsesCompletedFullDiscoveryWhenNoTIASkippableTests(t *testing.T) {
	ctx := context.Background()
	ciUtils.ResetCITags()
	t.Cleanup(ciUtils.ResetCITags)

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		TestFiles:     []string{"spec/fast_discovery_spec.rb"},
		Tests: []testoptimization.Test{
			{Module: "rspec", Suite: "FullDiscoverySuite", Name: "test1", SuiteSourceFile: "spec/full_discovery_spec.rb"},
		},
	}
	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags:         map[string]string{"platform": "ruby"},
		Framework:    mockFramework,
	}
	mockOptimizationClient := &MockTestOptimizationClient{
		Settings:       testOptimizationSettings(true, true, false),
		SkippableTests: map[string]bool{},
	}

	runner := NewWithDependencies(
		&MockPlatformDetector{Platform: mockPlatform},
		mockOptimizationClient,
		&MockTestSuiteDurationsClient{},
		newDefaultMockCIProviderDetector(),
	)

	if err := runner.PreparePlanningData(ctx); err != nil {
		t.Fatalf("PreparePlanningData() should not return error, got: %v", err)
	}

	if _, ok := runner.testFiles["spec/full_discovery_spec.rb"]; !ok {
		t.Errorf("Expected completed full discovery result to be planned when no tests are skippable, got %v", runner.testFiles)
	}
	if _, ok := runner.testFiles["spec/fast_discovery_spec.rb"]; ok {
		t.Errorf("Expected completed full discovery to ignore fast-discovered-only files, got %v", runner.testFiles)
	}
	if aggregate := runner.suiteAggregates[testSuiteKey{Module: "rspec", Suite: "FullDiscoverySuite"}]; aggregate.NumTests != 1 {
		t.Errorf("Expected completed full discovery suite aggregate, got %+v", runner.suiteAggregates)
	}
}

func TestTestPlanner_PreparePlanningData_EmptyDurationsContinues(t *testing.T) {
	ctx := context.Background()
	ciUtils.ResetCITags()
	t.Cleanup(ciUtils.ResetCITags)

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		Tests: []testoptimization.Test{
			{Suite: "Suite", Name: "test1", Parameters: "", SuiteSourceFile: "spec/file1_test.rb"},
		},
	}
	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags: map[string]string{
			ciConstants.GitRepositoryURL: "github.com/DataDog/ddtest",
		},
		Framework: mockFramework,
	}
	mockPlatformDetector := &MockPlatformDetector{Platform: mockPlatform}
	mockOptimizationClient := &MockTestOptimizationClient{}
	mockDurationsClient := &MockTestSuiteDurationsClient{}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, mockDurationsClient, newDefaultMockCIProviderDetector())

	err := runner.PreparePlanningData(ctx)
	if err != nil {
		t.Fatalf("PreparePlanningData() should not fail with empty durations, got: %v", err)
	}

	if !mockDurationsClient.Called {
		t.Error("PreparePlanningData() should fetch test suite durations")
	}

	if len(runner.testSuiteDurations) != 0 {
		t.Errorf("Expected empty in-memory test suite durations on empty response, got %v", runner.testSuiteDurations)
	}
}

func TestTestPlanner_PreparePlanningData_NonEmptyDurationsUsesP50ForMatchingSuites(t *testing.T) {
	ctx := context.Background()
	ciUtils.ResetCITags()
	t.Cleanup(ciUtils.ResetCITags)

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		TestFiles:     []string{"spec/file1_test.rb", "spec/file2_test.rb"},
		Tests: []testoptimization.Test{
			{Module: "rspec", Suite: "Suite1", Name: "test1", Parameters: "", SuiteSourceFile: "spec/file1_test.rb"},
			{Module: "rspec", Suite: "Suite1", Name: "test2", Parameters: "", SuiteSourceFile: "spec/file1_test.rb"},
			{Module: "rspec", Suite: "Suite2", Name: "test3", Parameters: "", SuiteSourceFile: "spec/file2_test.rb"},
		},
	}
	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags: map[string]string{
			ciConstants.GitRepositoryURL: "github.com/DataDog/ddtest",
		},
		Framework: mockFramework,
	}
	mockPlatformDetector := &MockPlatformDetector{Platform: mockPlatform}
	mockOptimizationClient := &MockTestOptimizationClient{}
	mockDurationsClient := &MockTestSuiteDurationsClient{
		Durations: map[string]map[string]testoptimization.TestSuiteDurationInfo{
			"rspec": {
				"Suite1": {
					SourceFile: "spec/file1_test.rb",
					Duration:   testoptimization.DurationPercentiles{P50: "10000000", P90: "20000000"},
				},
				"Suite2": {
					SourceFile: "spec/file2_test.rb",
					Duration:   testoptimization.DurationPercentiles{P50: "30000000", P90: "40000000"},
				},
			},
		},
	}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, mockDurationsClient, newDefaultMockCIProviderDetector())

	err := runner.PreparePlanningData(ctx)
	if err != nil {
		t.Fatalf("PreparePlanningData() should not fail with durations data, got: %v", err)
	}

	if len(runner.testSuiteDurations) != 1 {
		t.Fatalf("Expected stored durations data, got %v", runner.testSuiteDurations)
	}

	if _, ok := runner.testFiles["spec/file1_test.rb"]; !ok {
		t.Error("Expected file1 in test files")
	}
	if _, ok := runner.testFiles["spec/file2_test.rb"]; !ok {
		t.Error("Expected file2 in test files")
	}

	if weight, ok := runner.testFileWeight("spec/file1_test.rb"); !ok || weight != 10 {
		t.Errorf("Expected file1 weight to use backend p50 converted to 10ms, got weight=%d ok=%t", weight, ok)
	}
	if weight, ok := runner.testFileWeight("spec/file2_test.rb"); !ok || weight != 30 {
		t.Errorf("Expected file2 weight to use backend p50 converted to 30ms, got weight=%d ok=%t", weight, ok)
	}
}

func TestTestPlanner_PreparePlanningData_SkippablePercentageUsesDurations(t *testing.T) {
	ctx := context.Background()
	ciUtils.ResetCITags()
	t.Cleanup(ciUtils.ResetCITags)

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		TestFiles:     []string{"spec/slow_spec.rb", "spec/fast_spec.rb"},
		Tests: []testoptimization.Test{
			{Module: "rspec", Suite: "SlowSuite", Name: "test1", SuiteSourceFile: "spec/slow_spec.rb"},
			{Module: "rspec", Suite: "SlowSuite", Name: "test2", SuiteSourceFile: "spec/slow_spec.rb"},
			{Module: "rspec", Suite: "FastSuite", Name: "test1", SuiteSourceFile: "spec/fast_spec.rb"},
			{Module: "rspec", Suite: "FastSuite", Name: "test2", SuiteSourceFile: "spec/fast_spec.rb"},
		},
	}
	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags: map[string]string{
			ciConstants.GitRepositoryURL: "github.com/DataDog/ddtest",
		},
		Framework: mockFramework,
	}
	skippedTest := mockFramework.Tests[0]
	mockOptimizationClient := &MockTestOptimizationClient{
		Settings:       testOptimizationSettings(true, true, false),
		SkippableTests: map[string]bool{skippedTest.DatadogTestId(): true},
	}
	mockDurationsClient := &MockTestSuiteDurationsClient{
		Durations: map[string]map[string]testoptimization.TestSuiteDurationInfo{
			"rspec": {
				"SlowSuite": {
					SourceFile: "spec/slow_spec.rb",
					Duration:   testoptimization.DurationPercentiles{P50: "8000000000"},
				},
				"FastSuite": {
					SourceFile: "spec/fast_spec.rb",
					Duration:   testoptimization.DurationPercentiles{P50: "2000000000"},
				},
			},
		},
	}

	runner := NewWithDependencies(&MockPlatformDetector{Platform: mockPlatform}, mockOptimizationClient, mockDurationsClient, newDefaultMockCIProviderDetector())

	err := runner.PreparePlanningData(ctx)
	if err != nil {
		t.Fatalf("PreparePlanningData() should not fail, got: %v", err)
	}

	expectedPercentage := 40.0
	if runner.skippablePercentage != expectedPercentage {
		t.Errorf("Expected skippable percentage to use saved time %.2f, got %.2f", expectedPercentage, runner.skippablePercentage)
	}
}

func TestTestPlanner_TestFileWeight_CountFallbackForMissingSuiteDuration(t *testing.T) {
	runner := NewWithDependencies(&MockPlatformDetector{}, &MockTestOptimizationClient{}, &MockTestSuiteDurationsClient{}, newDefaultMockCIProviderDetector())
	runner.testFiles = map[string]struct{}{
		"spec/file1_test.rb":   {},
		"spec/file2_test.rb":   {},
		"spec/unknown_test.rb": {},
	}
	runner.suiteAggregates = map[testSuiteKey]testSuiteAggregate{
		{Module: "rspec", Suite: "Suite1"}: {
			Module:     "rspec",
			Suite:      "Suite1",
			SourceFile: "spec/file1_test.rb",
			NumTests:   2,
		},
		{Module: "rspec", Suite: "Suite2"}: {
			Module:     "rspec",
			Suite:      "Suite2",
			SourceFile: "spec/file2_test.rb",
			NumTests:   3,
		},
	}

	runner.testSuiteDurations = map[string]map[string]testoptimization.TestSuiteDurationInfo{
		"rspec": {
			"Suite1": {
				SourceFile: "spec/file1_test.rb",
				Duration:   testoptimization.DurationPercentiles{P50: "11000000", P90: "22000000"},
			},
		},
	}

	runner.estimateDiscoveredSuiteDurations()
	runner.suitesBySourceFile = indexSuitesBySourceFile(runner.suiteAggregates)

	if weight, ok := runner.testFileWeight("spec/file1_test.rb"); !ok || weight != 11 {
		t.Errorf("Expected Suite1 file weight to use p50 converted to 11ms, got weight=%d ok=%t", weight, ok)
	}

	expectedSuite2Weight := 3 * int(time.Second/time.Millisecond)
	if weight, ok := runner.testFileWeight("spec/file2_test.rb"); !ok || weight != expectedSuite2Weight {
		t.Errorf("Expected Suite2 file weight to use count fallback %d, got weight=%d ok=%t", expectedSuite2Weight, weight, ok)
	}

	if weight, ok := runner.testFileWeight("spec/unknown_test.rb"); !ok || weight != int(time.Second/time.Millisecond) {
		t.Errorf("Expected unknown file weight to use default 1 second, got weight=%d ok=%t", weight, ok)
	}

	runner.calculateFileWeights()
	if source := runner.testFileDurationSources["spec/file1_test.rb"]; source != testFileDurationSourceKnown {
		t.Errorf("Expected Suite1 file duration source to be known, got %q", source)
	}
	if source := runner.testFileDurationSources["spec/file2_test.rb"]; source != testFileDurationSourceDefault {
		t.Errorf("Expected Suite2 file duration source to be default, got %q", source)
	}
	if source := runner.testFileDurationSources["spec/unknown_test.rb"]; source != testFileDurationSourceDefault {
		t.Errorf("Expected unknown file duration source to be default, got %q", source)
	}
}

func TestTestPlanner_TestFileWeight_InvalidP50FallsBackForFullDiscoveryAggregate(t *testing.T) {
	runner := NewWithDependencies(&MockPlatformDetector{}, &MockTestOptimizationClient{}, &MockTestSuiteDurationsClient{}, newDefaultMockCIProviderDetector())
	runner.testFiles = map[string]struct{}{
		"spec/file1_test.rb": {},
	}
	runner.suiteAggregates = map[testSuiteKey]testSuiteAggregate{
		{Module: "rspec", Suite: "Suite1"}: {
			Module:          "rspec",
			Suite:           "Suite1",
			SourceFile:      "spec/file1_test.rb",
			NumTests:        3,
			NumTestsSkipped: 1,
		},
	}

	runner.testSuiteDurations = map[string]map[string]testoptimization.TestSuiteDurationInfo{
		"rspec": {
			"Suite1": {
				SourceFile: "spec/file1_test.rb",
				Duration:   testoptimization.DurationPercentiles{P50: "not-a-number"},
			},
		},
	}

	runner.estimateDiscoveredSuiteDurations()
	runner.suitesBySourceFile = indexSuitesBySourceFile(runner.suiteAggregates)

	aggregate := runner.suiteAggregates[testSuiteKey{Module: "rspec", Suite: "Suite1"}]
	expectedTotalDuration := 3 * float64(time.Second)
	if aggregate.TotalDuration != expectedTotalDuration {
		t.Errorf("Expected invalid p50 to keep count-based total duration %.0f, got %.0f", expectedTotalDuration, aggregate.TotalDuration)
	}

	expectedEstimatedDuration := 2 * int(time.Second/time.Millisecond)
	if weight, ok := runner.testFileWeight("spec/file1_test.rb"); !ok || weight != expectedEstimatedDuration {
		t.Errorf("Expected invalid p50 to use runnable count fallback %d, got weight=%d ok=%t", expectedEstimatedDuration, weight, ok)
	}
}

func TestTestPlanner_TestFileWeight_ZeroP50FallsBackForFullDiscoveryAggregate(t *testing.T) {
	runner := NewWithDependencies(&MockPlatformDetector{}, &MockTestOptimizationClient{}, &MockTestSuiteDurationsClient{}, newDefaultMockCIProviderDetector())
	runner.testFiles = map[string]struct{}{
		"spec/file1_test.rb": {},
	}
	runner.suiteAggregates = map[testSuiteKey]testSuiteAggregate{
		{Module: "rspec", Suite: "Suite1"}: {
			Module:          "rspec",
			Suite:           "Suite1",
			SourceFile:      "spec/file1_test.rb",
			NumTests:        3,
			NumTestsSkipped: 1,
		},
	}

	runner.testSuiteDurations = map[string]map[string]testoptimization.TestSuiteDurationInfo{
		"rspec": {
			"Suite1": {
				SourceFile: "spec/file1_test.rb",
				Duration:   testoptimization.DurationPercentiles{P50: "0"},
			},
		},
	}

	runner.estimateDiscoveredSuiteDurations()
	runner.suitesBySourceFile = indexSuitesBySourceFile(runner.suiteAggregates)

	aggregate := runner.suiteAggregates[testSuiteKey{Module: "rspec", Suite: "Suite1"}]
	expectedTotalDuration := 3 * float64(time.Second)
	if aggregate.TotalDuration != expectedTotalDuration {
		t.Errorf("Expected zero p50 to keep count-based total duration %.0f, got %.0f", expectedTotalDuration, aggregate.TotalDuration)
	}

	expectedEstimatedDuration := 2 * int(time.Second/time.Millisecond)
	if weight, ok := runner.testFileWeight("spec/file1_test.rb"); !ok || weight != expectedEstimatedDuration {
		t.Errorf("Expected zero p50 to use runnable count fallback %d, got weight=%d ok=%t", expectedEstimatedDuration, weight, ok)
	}
}

func TestTestPlanner_TestFileWeight_SubMillisecondP50MinimumWeight(t *testing.T) {
	runner := NewWithDependencies(&MockPlatformDetector{}, &MockTestOptimizationClient{}, &MockTestSuiteDurationsClient{}, newDefaultMockCIProviderDetector())
	runner.testFiles = map[string]struct{}{
		"spec/fast_test.rb": {},
	}
	runner.suiteAggregates = map[testSuiteKey]testSuiteAggregate{
		{Module: "rspec", Suite: "FastSuite"}: {
			Module:     "rspec",
			Suite:      "FastSuite",
			SourceFile: "spec/fast_test.rb",
			NumTests:   1,
		},
	}

	runner.testSuiteDurations = map[string]map[string]testoptimization.TestSuiteDurationInfo{
		"rspec": {
			"FastSuite": {
				SourceFile: "spec/fast_test.rb",
				Duration:   testoptimization.DurationPercentiles{P50: "500000"},
			},
		},
	}

	runner.estimateDiscoveredSuiteDurations()
	runner.suitesBySourceFile = indexSuitesBySourceFile(runner.suiteAggregates)

	if weight, ok := runner.testFileWeight("spec/fast_test.rb"); !ok || weight != 1 {
		t.Errorf("Expected sub-millisecond p50 to use minimum weight 1, got weight=%d ok=%t", weight, ok)
	}
}

func TestTestPlanner_TestFileWeight_SkipsFullySkippedSuites(t *testing.T) {
	runner := NewWithDependencies(&MockPlatformDetector{}, &MockTestOptimizationClient{}, &MockTestSuiteDurationsClient{}, newDefaultMockCIProviderDetector())
	runner.testFiles = map[string]struct{}{
		"spec/skipped_test.rb": {},
	}
	runner.suiteAggregates = map[testSuiteKey]testSuiteAggregate{
		{Module: "rspec", Suite: "SkippedSuite"}: {
			Module:            "rspec",
			Suite:             "SkippedSuite",
			SourceFile:        "spec/skipped_test.rb",
			EstimatedDuration: float64(time.Second),
			NumTests:          2,
			NumTestsSkipped:   2,
		},
	}
	runner.suitesBySourceFile = indexSuitesBySourceFile(runner.suiteAggregates)

	if _, ok := runner.suitesBySourceFile["spec/skipped_test.rb"]; !ok {
		t.Fatal("Expected fully skipped suite to be indexed by source file")
	}

	if weight, ok := runner.testFileWeight("spec/skipped_test.rb"); ok || weight != 0 {
		t.Errorf("Expected fully skipped suite file to have no weight, got weight=%d ok=%t", weight, ok)
	}

	if weightedFiles := runner.calculateFileWeights(); len(weightedFiles) != 0 {
		t.Errorf("Expected fully skipped suite file to be omitted from weighted files, got %v", weightedFiles)
	}
}

func TestCalculateSavedTimePercentage_IgnoresInvalidDurationAggregates(t *testing.T) {
	suiteAggregates := map[testSuiteKey]testSuiteAggregate{
		{Module: "rspec", Suite: "ZeroTests"}: {
			TotalDuration:     10,
			EstimatedDuration: 5,
			NumTests:          0,
		},
		{Module: "rspec", Suite: "ZeroDuration"}: {
			TotalDuration:     0,
			EstimatedDuration: 0,
			NumTests:          1,
		},
		{Module: "rspec", Suite: "NegativeDuration"}: {
			TotalDuration:     -10,
			EstimatedDuration: 0,
			NumTests:          1,
		},
	}

	if percentage := calculateSavedTimePercentage(suiteAggregates); percentage != 0.0 {
		t.Errorf("Expected invalid duration aggregates to produce 0 saved time percentage, got %.2f", percentage)
	}
}

func TestIndexSuitesBySourceFile_IgnoresEmptySourceFile(t *testing.T) {
	suiteAggregates := map[testSuiteKey]testSuiteAggregate{
		{Module: "rspec", Suite: "MissingSource"}: {
			Module: "rspec",
			Suite:  "MissingSource",
		},
		{Module: "rspec", Suite: "WithSource"}: {
			Module:     "rspec",
			Suite:      "WithSource",
			SourceFile: "spec/with_source_spec.rb",
		},
	}

	suitesBySourceFile := indexSuitesBySourceFile(suiteAggregates)

	if _, ok := suitesBySourceFile[""]; ok {
		t.Error("Expected empty source file to be ignored")
	}
	if got := suitesBySourceFile["spec/with_source_spec.rb"]; len(got) != 1 || got[0] != (testSuiteKey{Module: "rspec", Suite: "WithSource"}) {
		t.Errorf("Expected only suite with source file to be indexed, got %v", suitesBySourceFile)
	}
}

func TestTestPlanner_PreparePlanningData_FastDiscoveryUsesBackendDurations(t *testing.T) {
	t.Chdir(t.TempDir())
	ctx := context.Background()
	ciUtils.ResetCITags()
	t.Cleanup(ciUtils.ResetCITags)

	mockFramework := &MockFramework{
		FrameworkName:    "rspec",
		TestFiles:        []string{"spec/backend_only_spec.rb"},
		DiscoverTestsErr: errors.New("full discovery failed"),
	}
	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags: map[string]string{
			ciConstants.GitRepositoryURL: "github.com/DataDog/ddtest",
		},
		Framework: mockFramework,
	}
	mockDurationsClient := &MockTestSuiteDurationsClient{
		Durations: map[string]map[string]testoptimization.TestSuiteDurationInfo{
			"rspec": {
				"BackendOnlySuite": {
					SourceFile: "spec/backend_only_spec.rb",
					Duration:   testoptimization.DurationPercentiles{P50: "42000000", P90: "84000000"},
				},
			},
		},
	}

	runner := NewWithDependencies(&MockPlatformDetector{Platform: mockPlatform}, &MockTestOptimizationClient{}, mockDurationsClient, newDefaultMockCIProviderDetector())

	err := runner.PreparePlanningData(ctx)
	if err != nil {
		t.Fatalf("PreparePlanningData() should not fail when full discovery fails but fast discovery succeeds, got: %v", err)
	}

	if weight, ok := runner.testFileWeight("spec/backend_only_spec.rb"); !ok || weight != 42 {
		t.Errorf("Expected fast-discovery file to use backend p50 converted to 42ms, got weight=%d ok=%t", weight, ok)
	}
}

func TestTestPlanner_PreparePlanningData_FastDiscoveryUsesOneBackendDurationPerSourceFile(t *testing.T) {
	t.Chdir(t.TempDir())
	ctx := context.Background()
	ciUtils.ResetCITags()
	t.Cleanup(ciUtils.ResetCITags)

	mockFramework := &MockFramework{
		FrameworkName:    "rspec",
		TestFiles:        []string{"spec/backend_only_spec.rb"},
		DiscoverTestsErr: errors.New("full discovery failed"),
	}
	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags: map[string]string{
			ciConstants.GitRepositoryURL: "github.com/DataDog/ddtest",
		},
		Framework: mockFramework,
	}
	mockDurationsClient := &MockTestSuiteDurationsClient{
		Durations: map[string]map[string]testoptimization.TestSuiteDurationInfo{
			"rspec": {
				"BackendOnlySuite": {
					SourceFile: "spec/backend_only_spec.rb",
					Duration:   testoptimization.DurationPercentiles{P50: "42000000"},
				},
				"DuplicateBackendOnlySuite": {
					SourceFile: "spec/backend_only_spec.rb",
					Duration:   testoptimization.DurationPercentiles{P50: "84000000"},
				},
			},
		},
	}

	runner := NewWithDependencies(&MockPlatformDetector{Platform: mockPlatform}, &MockTestOptimizationClient{}, mockDurationsClient, newDefaultMockCIProviderDetector())

	err := runner.PreparePlanningData(ctx)
	if err != nil {
		t.Fatalf("PreparePlanningData() should not fail when full discovery fails but fast discovery succeeds, got: %v", err)
	}

	if len(runner.suiteAggregates) != 1 {
		t.Fatalf("Expected one backend fallback suite aggregate per source file, got %v", runner.suiteAggregates)
	}
	if suiteKeys := runner.suitesBySourceFile["spec/backend_only_spec.rb"]; len(suiteKeys) != 1 {
		t.Fatalf("Expected rebuilt source-file index to contain one suite key, got %v", runner.suitesBySourceFile)
	}
}

func TestTestPlanner_PreparePlanningData_IgnoresZeroBackendDurationForFastDiscovery(t *testing.T) {
	t.Chdir(t.TempDir())
	ctx := context.Background()
	ciUtils.ResetCITags()
	t.Cleanup(ciUtils.ResetCITags)

	mockFramework := &MockFramework{
		FrameworkName:    "rspec",
		TestFiles:        []string{"spec/backend_only_spec.rb"},
		DiscoverTestsErr: errors.New("full discovery failed"),
	}
	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags: map[string]string{
			ciConstants.GitRepositoryURL: "github.com/DataDog/ddtest",
		},
		Framework: mockFramework,
	}
	mockDurationsClient := &MockTestSuiteDurationsClient{
		Durations: map[string]map[string]testoptimization.TestSuiteDurationInfo{
			"rspec": {
				"BrokenZeroDurationSuite": {
					SourceFile: "spec/backend_only_spec.rb",
					Duration:   testoptimization.DurationPercentiles{P50: "0", P90: "0"},
				},
			},
		},
	}

	runner := NewWithDependencies(&MockPlatformDetector{Platform: mockPlatform}, &MockTestOptimizationClient{}, mockDurationsClient, newDefaultMockCIProviderDetector())

	err := runner.PreparePlanningData(ctx)
	if err != nil {
		t.Fatalf("PreparePlanningData() should not fail when full discovery fails but fast discovery succeeds, got: %v", err)
	}

	if len(runner.suiteAggregates) != 0 {
		t.Errorf("Expected zero-duration backend suite to be ignored, got aggregates: %v", runner.suiteAggregates)
	}

	if weight, ok := runner.testFileWeight("spec/backend_only_spec.rb"); !ok || weight != DefaultTestFileWeight {
		t.Errorf("Expected fast-discovery file with broken backend duration to use default weight, got weight=%d ok=%t", weight, ok)
	}
}

func TestTestPlanner_PreparePlanningData_BackendDurationSubdirMatchesFastDiscovery(t *testing.T) {
	ctx := context.Background()
	ciUtils.ResetCITags()
	t.Cleanup(ciUtils.ResetCITags)

	repoRoot := t.TempDir()
	initGitRepo(t, repoRoot)
	coreDir := filepath.Join(repoRoot, "core")
	_ = os.MkdirAll(filepath.Join(coreDir, "spec", "models"), 0755)

	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(coreDir)
	ciUtils.ResetCwdSubdirPrefixForTesting()
	t.Cleanup(ciUtils.ResetCwdSubdirPrefixForTesting)

	mockFramework := &MockFramework{
		FrameworkName:    "rspec",
		TestFiles:        []string{"spec/models/order_spec.rb"},
		DiscoverTestsErr: errors.New("full discovery failed"),
	}
	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags: map[string]string{
			ciConstants.GitRepositoryURL: repoRoot,
		},
		Framework: mockFramework,
	}
	mockDurationsClient := &MockTestSuiteDurationsClient{
		Durations: map[string]map[string]testoptimization.TestSuiteDurationInfo{
			"rspec": {
				"OrderSuite": {
					SourceFile: "core/spec/models/order_spec.rb",
					Duration:   testoptimization.DurationPercentiles{P50: "55000000", P90: "110000000"},
				},
			},
		},
	}
	ciUtils.AddCITagsMap(map[string]string{ciConstants.GitRepositoryURL: repoRoot})

	runner := NewWithDependencies(&MockPlatformDetector{Platform: mockPlatform}, &MockTestOptimizationClient{}, mockDurationsClient, newDefaultMockCIProviderDetector())

	err := runner.PreparePlanningData(ctx)
	if err != nil {
		t.Fatalf("PreparePlanningData() should not fail, got: %v", err)
	}
	if !mockDurationsClient.Called {
		t.Fatal("Expected durations client to be called")
	}

	if weight, ok := runner.testFileWeight("spec/models/order_spec.rb"); !ok || weight != 55 {
		t.Errorf("Expected subdir fast-discovery file to use backend p50 converted to 55ms, got weight=%d ok=%t", weight, ok)
	}

	if got := runner.testSuiteDurations["rspec"]["OrderSuite"].SourceFile; got != "core/spec/models/order_spec.rb" {
		t.Errorf("Expected raw backend source file to remain git-root-relative, got %q", got)
	}
}

func TestTestPlanner_PreparePlanningData_IgnoresBackendDurationsForUndiscoveredFiles(t *testing.T) {
	t.Chdir(t.TempDir())
	ctx := context.Background()
	ciUtils.ResetCITags()
	t.Cleanup(ciUtils.ResetCITags)

	mockFramework := &MockFramework{
		FrameworkName:    "rspec",
		TestFiles:        []string{"spec/discovered_spec.rb"},
		DiscoverTestsErr: errors.New("full discovery failed"),
	}
	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags: map[string]string{
			ciConstants.GitRepositoryURL: "github.com/DataDog/ddtest",
		},
		Framework: mockFramework,
	}
	mockDurationsClient := &MockTestSuiteDurationsClient{
		Durations: map[string]map[string]testoptimization.TestSuiteDurationInfo{
			"rspec": {
				"StaleSuite": {
					SourceFile: "spec/stale_spec.rb",
					Duration:   testoptimization.DurationPercentiles{P50: "99000000", P90: "198000000"},
				},
			},
		},
	}

	runner := NewWithDependencies(&MockPlatformDetector{Platform: mockPlatform}, &MockTestOptimizationClient{}, mockDurationsClient, newDefaultMockCIProviderDetector())

	err := runner.PreparePlanningData(ctx)
	if err != nil {
		t.Fatalf("PreparePlanningData() should not fail, got: %v", err)
	}

	if len(runner.suiteAggregates) != 0 {
		t.Errorf("Expected stale backend suite to be ignored, got aggregates: %v", runner.suiteAggregates)
	}

	if weight, ok := runner.testFileWeight("spec/discovered_spec.rb"); !ok || weight != int(time.Second/time.Millisecond) {
		t.Errorf("Expected discovered file without backend aggregate to use default 1 second, got weight=%d ok=%t", weight, ok)
	}
}

func TestTestPlanner_PreparePlanningData_FullDiscoveryIgnoresFastOnlyFiles(t *testing.T) {
	t.Chdir(t.TempDir())
	ctx := context.Background()
	ciUtils.ResetCITags()
	t.Cleanup(ciUtils.ResetCITags)

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		TestFiles:     []string{"spec/discovered_spec.rb", "spec/fast_only_spec.rb"},
		Tests: []testoptimization.Test{
			{
				Module:          "rspec",
				Suite:           "DiscoveredSuite",
				Name:            "test1",
				SuiteSourceFile: "spec/discovered_spec.rb",
			},
		},
	}
	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags: map[string]string{
			ciConstants.GitRepositoryURL: "github.com/DataDog/ddtest",
		},
		Framework: mockFramework,
	}

	runner := NewWithDependencies(&MockPlatformDetector{Platform: mockPlatform}, testOptimizationClientRequiringFullDiscovery(), &MockTestSuiteDurationsClient{}, newDefaultMockCIProviderDetector())

	err := runner.PreparePlanningData(ctx)
	if err != nil {
		t.Fatalf("PreparePlanningData() should not fail, got: %v", err)
	}

	if _, ok := runner.testFiles["spec/discovered_spec.rb"]; !ok {
		t.Fatalf("Expected full-discovered file to be planned, got %v", runner.testFiles)
	}
	if _, ok := runner.testFiles["spec/fast_only_spec.rb"]; ok {
		t.Errorf("Expected fast-only file to be ignored after successful full discovery, got %v", runner.testFiles)
	}
	if _, ok := runner.testFileWeights["spec/fast_only_spec.rb"]; ok {
		t.Errorf("Expected fast-only file not to have a weight, got %v", runner.testFileWeights)
	}
	if _, ok := runner.testFileDurationSources["spec/fast_only_spec.rb"]; ok {
		t.Errorf("Expected fast-only file not to have a duration source, got %v", runner.testFileDurationSources)
	}
}

func TestTestPlanner_PreparePlanningData_FullDiscoveryDoesNotReintroduceFastOnlyBackendSuite(t *testing.T) {
	t.Chdir(t.TempDir())
	ctx := context.Background()
	ciUtils.ResetCITags()
	t.Cleanup(ciUtils.ResetCITags)
	ciUtils.AddCITagsMap(map[string]string{ciConstants.GitRepositoryURL: "github.com/DataDog/ddtest"})

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		TestFiles:     []string{"spec/discovered_spec.rb", "spec/fast_only_spec.rb"},
		Tests: []testoptimization.Test{
			{
				Module:          "rspec",
				Suite:           "DiscoveredSuite",
				Name:            "test1",
				SuiteSourceFile: "spec/discovered_spec.rb",
			},
		},
	}
	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags: map[string]string{
			ciConstants.GitRepositoryURL: "github.com/DataDog/ddtest",
		},
		Framework: mockFramework,
	}
	mockDurationsClient := &MockTestSuiteDurationsClient{
		Durations: map[string]map[string]testoptimization.TestSuiteDurationInfo{
			"rspec": {
				"FastOnlySuite": {
					SourceFile: "spec/fast_only_spec.rb",
					Duration:   testoptimization.DurationPercentiles{P50: "42000000", P90: "84000000"},
				},
			},
		},
	}

	runner := NewWithDependencies(&MockPlatformDetector{Platform: mockPlatform}, testOptimizationClientRequiringFullDiscovery(), mockDurationsClient, newDefaultMockCIProviderDetector())

	err := runner.PreparePlanningData(ctx)
	if err != nil {
		t.Fatalf("PreparePlanningData() should not fail, got: %v", err)
	}
	if !mockDurationsClient.Called {
		t.Fatal("Expected durations client to be called")
	}

	if _, ok := runner.suiteAggregates[testSuiteKey{Module: "rspec", Suite: "FastOnlySuite"}]; ok {
		t.Errorf("Expected backend suite for fast-only file not to be added, got aggregates %v", runner.suiteAggregates)
	}
	if _, ok := runner.testFiles["spec/fast_only_spec.rb"]; ok {
		t.Errorf("Expected fast-only file not to be planned despite backend duration, got %v", runner.testFiles)
	}
	if _, ok := runner.testFileWeights["spec/fast_only_spec.rb"]; ok {
		t.Errorf("Expected fast-only file not to be runnable despite backend duration, got %v", runner.testFileWeights)
	}
}

func TestTestPlanner_PreparePlanningData_FastDiscoveryDoesNotRunStaleBackendFilesWhenSkippingDisabled(t *testing.T) {
	t.Chdir(t.TempDir())
	ctx := context.Background()
	ciUtils.ResetCITags()
	t.Cleanup(ciUtils.ResetCITags)

	mockFramework := &MockFramework{
		FrameworkName:    "rspec",
		TestFiles:        []string{"spec/local_spec.rb"},
		DiscoverTestsErr: errors.New("full discovery cancelled because test skipping is disabled"),
	}
	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags: map[string]string{
			ciConstants.GitRepositoryURL: "github.com/DataDog/ddtest",
		},
		Framework: mockFramework,
	}
	mockOptimizationClient := &MockTestOptimizationClient{
		Settings: &net.SettingsResponseData{
			ItrEnabled:    true,
			TestsSkipping: false,
		},
	}
	mockDurationsClient := &MockTestSuiteDurationsClient{
		Durations: map[string]map[string]testoptimization.TestSuiteDurationInfo{
			"rspec": {
				"LocalSuite": {
					SourceFile: "spec/local_spec.rb",
					Duration:   testoptimization.DurationPercentiles{P50: "11000000"},
				},
				"DeletedSuite": {
					SourceFile: "spec/deleted_spec.rb",
					Duration:   testoptimization.DurationPercentiles{P50: "99000000"},
				},
			},
		},
	}

	runner := NewWithDependencies(&MockPlatformDetector{Platform: mockPlatform}, mockOptimizationClient, mockDurationsClient, newDefaultMockCIProviderDetector())

	err := runner.PreparePlanningData(ctx)
	if err != nil {
		t.Fatalf("PreparePlanningData() should not fail, got: %v", err)
	}

	weightedFiles := runner.calculateFileWeights()
	if len(weightedFiles) != 1 {
		t.Fatalf("Expected only local fast-discovery file to be runnable, got %v", weightedFiles)
	}
	if _, ok := weightedFiles["spec/local_spec.rb"]; !ok {
		t.Errorf("Expected local fast-discovery file to be runnable, got %v", weightedFiles)
	}
	if _, ok := weightedFiles["spec/deleted_spec.rb"]; ok {
		t.Errorf("Expected stale backend file not to be runnable, got %v", weightedFiles)
	}
	if _, ok := runner.suiteAggregates[testSuiteKey{Module: "rspec", Suite: "DeletedSuite"}]; ok {
		t.Errorf("Expected stale backend suite not to be added, got aggregates %v", runner.suiteAggregates)
	}
}

func TestTestPlanner_PreparePlanningData_BackendDoesNotReintroduceFullySkippedSuite(t *testing.T) {
	t.Chdir(t.TempDir())
	ctx := context.Background()
	ciUtils.ResetCITags()
	t.Cleanup(ciUtils.ResetCITags)

	skippedTest := testoptimization.Test{
		Module:          "rspec",
		Suite:           "SkippedSuite",
		Name:            "test1",
		SuiteSourceFile: "spec/skipped_spec.rb",
	}
	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		TestFiles:     []string{"spec/skipped_spec.rb"},
		Tests:         []testoptimization.Test{skippedTest},
	}
	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags: map[string]string{
			ciConstants.GitRepositoryURL: "github.com/DataDog/ddtest",
		},
		Framework: mockFramework,
	}
	mockOptimizationClient := &MockTestOptimizationClient{
		Settings:       testOptimizationSettings(true, true, false),
		SkippableTests: map[string]bool{skippedTest.DatadogTestId(): true},
	}
	mockDurationsClient := &MockTestSuiteDurationsClient{
		Durations: map[string]map[string]testoptimization.TestSuiteDurationInfo{
			"rspec": {
				"SkippedSuite": {
					SourceFile: "spec/skipped_spec.rb",
					Duration:   testoptimization.DurationPercentiles{P50: "99000000", P90: "198000000"},
				},
			},
		},
	}

	runner := NewWithDependencies(&MockPlatformDetector{Platform: mockPlatform}, mockOptimizationClient, mockDurationsClient, newDefaultMockCIProviderDetector())

	err := runner.PreparePlanningData(ctx)
	if err != nil {
		t.Fatalf("PreparePlanningData() should not fail, got: %v", err)
	}

	aggregate := runner.suiteAggregates[testSuiteKey{Module: "rspec", Suite: "SkippedSuite"}]
	if aggregate.NumTests != 1 || aggregate.NumTestsSkipped != 1 {
		t.Errorf("Expected full-discovery skip metadata to remain intact, got %+v", aggregate)
	}

	if weight, ok := runner.testFileWeight("spec/skipped_spec.rb"); ok || weight != 0 {
		t.Errorf("Expected fully skipped suite to be omitted despite backend duration, got weight=%d ok=%t", weight, ok)
	}
}

func TestTestPlanner_PreparePlanningData_BackendDoesNotDuplicateDiscoveredSourceFile(t *testing.T) {
	t.Chdir(t.TempDir())
	ctx := context.Background()
	ciUtils.ResetCITags()
	t.Cleanup(ciUtils.ResetCITags)

	skippedTest := testoptimization.Test{
		Module:          "rspec",
		Suite:           "DiscoveredSuite",
		Name:            "test1",
		SuiteSourceFile: "spec/skipped_spec.rb",
	}
	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		TestFiles:     []string{"spec/skipped_spec.rb"},
		Tests:         []testoptimization.Test{skippedTest},
	}
	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags: map[string]string{
			ciConstants.GitRepositoryURL: "github.com/DataDog/ddtest",
		},
		Framework: mockFramework,
	}
	mockOptimizationClient := &MockTestOptimizationClient{
		Settings:       testOptimizationSettings(true, true, false),
		SkippableTests: map[string]bool{skippedTest.DatadogTestId(): true},
	}
	mockDurationsClient := &MockTestSuiteDurationsClient{
		Durations: map[string]map[string]testoptimization.TestSuiteDurationInfo{
			"rspec": {
				"BackendDuplicateSuite": {
					SourceFile: "spec/skipped_spec.rb",
					Duration:   testoptimization.DurationPercentiles{P50: "99000000"},
				},
			},
		},
	}

	runner := NewWithDependencies(&MockPlatformDetector{Platform: mockPlatform}, mockOptimizationClient, mockDurationsClient, newDefaultMockCIProviderDetector())

	err := runner.PreparePlanningData(ctx)
	if err != nil {
		t.Fatalf("PreparePlanningData() should not fail, got: %v", err)
	}

	duplicateKey := testSuiteKey{Module: "rspec", Suite: "BackendDuplicateSuite"}
	if _, ok := runner.suiteAggregates[duplicateKey]; ok {
		t.Errorf("Expected backend suite for already-discovered source file not to be added, got aggregates %v", runner.suiteAggregates)
	}

	if weight, ok := runner.testFileWeight("spec/skipped_spec.rb"); ok || weight != 0 {
		t.Errorf("Expected fully skipped file to remain omitted despite backend duplicate, got weight=%d ok=%t", weight, ok)
	}
}

func TestTestPlanner_RecordFullDiscoveryResults_AppliesExcludeAfterSubdirNormalization(t *testing.T) {
	setPlannerTestsExcludePattern(t, "spec/system/**/*_spec.rb")
	repoRoot := t.TempDir()
	initGitRepo(t, repoRoot)
	coreDir := filepath.Join(repoRoot, "core")
	if err := os.MkdirAll(coreDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(coreDir)
	ciUtils.ResetCwdSubdirPrefixForTesting()
	t.Cleanup(ciUtils.ResetCwdSubdirPrefixForTesting)

	runner := newTestPlannerWithDefaults()
	tests := []testoptimization.Test{
		{
			Module:          "rspec",
			Suite:           "User",
			Name:            "should be valid",
			SuiteSourceFile: "core/spec/models/user_spec.rb",
		},
		{
			Module:          "rspec",
			Suite:           "Checkout",
			Name:            "checks out",
			SuiteSourceFile: "core/spec/system/checkout_spec.rb",
		},
	}

	err := runner.recordFullDiscoveryResults(tests, newTestSkipper(nil, nil))
	if err != nil {
		t.Fatalf("recordFullDiscoveryResults() should not fail, got: %v", err)
	}

	if _, ok := runner.testFiles["spec/models/user_spec.rb"]; !ok {
		t.Errorf("expected included normalized file to be recorded, got %v", runner.testFiles)
	}
	if _, ok := runner.testFiles["spec/system/checkout_spec.rb"]; ok {
		t.Errorf("expected excluded normalized file not to be recorded, got %v", runner.testFiles)
	}
	if _, ok := runner.suiteAggregates[testSuiteKey{Module: "rspec", Suite: "User"}]; !ok {
		t.Errorf("expected included suite aggregate to be recorded, got %v", runner.suiteAggregates)
	}
	if _, ok := runner.suiteAggregates[testSuiteKey{Module: "rspec", Suite: "Checkout"}]; ok {
		t.Errorf("expected excluded suite aggregate not to be recorded, got %v", runner.suiteAggregates)
	}
}

func TestTestPlanner_RecordFastDiscoveryFallbackFiles_ExcludedBackendDurationsAreNotReintroduced(t *testing.T) {
	setPlannerTestsExcludePattern(t, "spec/system/**/*_spec.rb")

	runner := newTestPlannerWithDefaults()
	err := runner.recordFastDiscoveryFallbackFiles([]string{
		"spec/models/user_spec.rb",
		"spec/system/checkout_spec.rb",
	})
	if err != nil {
		t.Fatalf("recordFastDiscoveryFallbackFiles() should not fail, got: %v", err)
	}

	runner.testSuiteDurations = map[string]map[string]testoptimization.TestSuiteDurationInfo{
		"rspec": {
			"User": {
				SourceFile: "spec/models/user_spec.rb",
				Duration:   testoptimization.DurationPercentiles{P50: "100000000"},
			},
			"Checkout": {
				SourceFile: "spec/system/checkout_spec.rb",
				Duration:   testoptimization.DurationPercentiles{P50: "200000000"},
			},
		},
	}
	runner.addDurationDataForFastDiscoveryFallback()

	if _, ok := runner.testFiles["spec/models/user_spec.rb"]; !ok {
		t.Errorf("expected included fast-discovered file to be recorded, got %v", runner.testFiles)
	}
	if _, ok := runner.testFiles["spec/system/checkout_spec.rb"]; ok {
		t.Errorf("expected excluded fast-discovered file not to be recorded, got %v", runner.testFiles)
	}
	if _, ok := runner.suiteAggregates[testSuiteKey{Module: "rspec", Suite: "User"}]; !ok {
		t.Errorf("expected included backend duration suite to be recorded, got %v", runner.suiteAggregates)
	}
	if _, ok := runner.suiteAggregates[testSuiteKey{Module: "rspec", Suite: "Checkout"}]; ok {
		t.Errorf("expected excluded backend duration suite not to be reintroduced, got %v", runner.suiteAggregates)
	}
}

func TestTestPlanner_PreparePlanningData_ResolvesFilteredTestFilesOnce(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	t.Chdir(root)
	if err := os.MkdirAll(filepath.Join(root, "spec", "models"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "spec", "system"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "spec", "models", "user_spec.rb"), []byte("# spec\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "spec", "system", "checkout_spec.rb"), []byte("# spec\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	setPlannerTestsExcludePattern(t, filepath.Join("spec", "system", "**", "*_spec.rb"))

	mockFramework := &MockFramework{
		FrameworkName:    "rspec",
		TestPatternValue: filepath.Join("spec", "**", "*_spec.rb"),
		Tests: []testoptimization.Test{
			{Module: "rspec", Suite: "User", Name: "test1", SuiteSourceFile: "spec/models/user_spec.rb"},
		},
	}
	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags:         map[string]string{"platform": "ruby"},
		Framework:    mockFramework,
	}
	mockOptimizationClient := &MockTestOptimizationClient{
		Settings: testOptimizationSettings(true, true, false),
		SkippableTests: map[string]bool{
			(&testoptimization.Test{Module: "rspec", Suite: "User", Name: "test1"}).DatadogTestId(): true,
		},
	}
	runner := NewWithDependencies(
		&MockPlatformDetector{Platform: mockPlatform},
		mockOptimizationClient,
		&MockTestSuiteDurationsClient{},
		newDefaultMockCIProviderDetector(),
	)

	if err := runner.PreparePlanningData(ctx); err != nil {
		t.Fatalf("PreparePlanningData() should not fail, got: %v", err)
	}

	expectedFiles := []string{"spec/models/user_spec.rb"}
	if len(mockFramework.DiscoverTestsFiles) != 1 {
		t.Fatalf("expected full discovery to receive test files once, got %d", len(mockFramework.DiscoverTestsFiles))
	}
	if !slices.Equal(mockFramework.DiscoverTestsFiles[0].ExplicitFiles, expectedFiles) {
		t.Fatalf("full discovery files = %v, expected %v", mockFramework.DiscoverTestsFiles[0].ExplicitFiles, expectedFiles)
	}
	if _, ok := runner.testFiles["spec/models/user_spec.rb"]; !ok {
		t.Fatalf("expected filtered included file to be planned, got %v", runner.testFiles)
	}
	if _, ok := runner.testFiles["spec/system/checkout_spec.rb"]; ok {
		t.Fatalf("expected excluded file not to be planned, got %v", runner.testFiles)
	}
}

func TestTestPlanner_PreparePlanningData_PostFiltersFullDiscoveryWhenExplicitFileListIsTooLarge(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	t.Chdir(root)

	pattern := filepath.Join("spec", "**", "*_spec.rb")
	excludePattern := filepath.Join("spec", "system", "**", "*_spec.rb")
	setPlannerTestsExcludePattern(t, excludePattern)

	discoveredTests := make([]testoptimization.Test, 0, discovery.MaxExplicitTestFiles+2)
	for i := range discovery.MaxExplicitTestFiles + 1 {
		file := filepath.Join("spec", "models", fmt.Sprintf("generated_%04d_spec.rb", i))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatalf("failed to create directory for %s: %v", file, err)
		}
		if err := os.WriteFile(file, []byte("# spec\n"), 0o644); err != nil {
			t.Fatalf("failed to create file %s: %v", file, err)
		}
		discoveredTests = append(discoveredTests, testoptimization.Test{
			Module:          "rspec",
			Suite:           fmt.Sprintf("GeneratedSuite%d", i),
			Name:            "test1",
			SuiteSourceFile: file,
		})
	}

	excludedFile := filepath.Join("spec", "system", "checkout_spec.rb")
	if err := os.MkdirAll(filepath.Dir(excludedFile), 0o755); err != nil {
		t.Fatalf("failed to create directory for %s: %v", excludedFile, err)
	}
	if err := os.WriteFile(excludedFile, []byte("# spec\n"), 0o644); err != nil {
		t.Fatalf("failed to create file %s: %v", excludedFile, err)
	}
	discoveredTests = append(discoveredTests, testoptimization.Test{
		Module:          "rspec",
		Suite:           "ExcludedSuite",
		Name:            "test1",
		SuiteSourceFile: excludedFile,
	})

	mockFramework := &MockFramework{
		FrameworkName:    "rspec",
		TestPatternValue: pattern,
		Tests:            discoveredTests,
	}
	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags:         map[string]string{"platform": "ruby"},
		Framework:    mockFramework,
	}
	runner := NewWithDependencies(
		&MockPlatformDetector{Platform: mockPlatform},
		testOptimizationClientRequiringFullDiscovery(),
		&MockTestSuiteDurationsClient{},
		newDefaultMockCIProviderDetector(),
	)

	if err := runner.PreparePlanningData(ctx); err != nil {
		t.Fatalf("PreparePlanningData() should not fail, got: %v", err)
	}

	if len(mockFramework.DiscoverTestsFiles) != 1 {
		t.Fatalf("expected full discovery to receive test file selection once, got %d", len(mockFramework.DiscoverTestsFiles))
	}
	if mockFramework.DiscoverTestsFiles[0].UseExplicitFiles() {
		t.Fatalf("expected full discovery to receive pattern mode when filtered file count exceeds %d, got %d explicit files",
			discovery.MaxExplicitTestFiles, len(mockFramework.DiscoverTestsFiles[0].ExplicitFiles))
	}
	if mockFramework.DiscoverTestsFiles[0].Pattern != pattern {
		t.Fatalf("expected full discovery pattern %q, got %q", pattern, mockFramework.DiscoverTestsFiles[0].Pattern)
	}
	if len(runner.testFiles) != discovery.MaxExplicitTestFiles+1 {
		t.Fatalf("expected %d included files in final plan, got %d", discovery.MaxExplicitTestFiles+1, len(runner.testFiles))
	}
	if _, ok := runner.testFiles[excludedFile]; ok {
		t.Fatalf("expected excluded file %q not to be planned", excludedFile)
	}
	if _, ok := runner.suiteAggregates[testSuiteKey{Module: "rspec", Suite: "ExcludedSuite"}]; ok {
		t.Fatalf("expected excluded full-discovery suite not to be recorded")
	}
}

func TestRecordRunnableAndSkippedTest_CountsTestsPerSuite(t *testing.T) {
	suiteAggregates := make(map[testSuiteKey]testSuiteAggregate)

	recordRunnableTest(suiteAggregates, testoptimization.Test{
		Module:          "rspec",
		Suite:           "Suite1",
		Name:            "test1",
		SuiteSourceFile: "spec/file1_test.rb",
	}, "spec/file1_test.rb")
	recordSkippedTest(suiteAggregates, testoptimization.Test{
		Module:          "rspec",
		Suite:           "Suite1",
		Name:            "test2",
		SuiteSourceFile: "spec/file1_test.rb",
	}, "spec/file1_test.rb")
	recordRunnableTest(suiteAggregates, testoptimization.Test{
		Module:          "rspec",
		Suite:           "Suite2",
		Name:            "test3",
		SuiteSourceFile: "spec/file2_test.rb",
	}, "spec/file2_test.rb")

	suite1 := suiteAggregates[testSuiteKey{Module: "rspec", Suite: "Suite1"}]
	if suite1.NumTests != 2 {
		t.Errorf("Expected Suite1 test count 2, got %d", suite1.NumTests)
	}
	if suite1.NumTestsSkipped != 1 {
		t.Errorf("Expected Suite1 skipped test count 1, got %d", suite1.NumTestsSkipped)
	}
	if suite1.SourceFile != "spec/file1_test.rb" {
		t.Errorf("Expected Suite1 source file spec/file1_test.rb, got %s", suite1.SourceFile)
	}

	suite2 := suiteAggregates[testSuiteKey{Module: "rspec", Suite: "Suite2"}]
	if suite2.NumTests != 1 {
		t.Errorf("Expected Suite2 test count 1, got %d", suite2.NumTests)
	}
	if suite2.NumTestsSkipped != 0 {
		t.Errorf("Expected Suite2 skipped test count 0, got %d", suite2.NumTestsSkipped)
	}
}

func TestTestPlanner_PreparePlanningData_PlatformDetectionError(t *testing.T) {
	ctx := context.Background()

	mockPlatformDetector := &MockPlatformDetector{
		Err: errors.New("platform detection failed"),
	}

	mockOptimizationClient := &MockTestOptimizationClient{}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, &MockTestSuiteDurationsClient{}, newDefaultMockCIProviderDetector())

	err := runner.PreparePlanningData(ctx)

	if err == nil {
		t.Error("PreparePlanningData() should return error when platform detection fails")
	}

	expectedMsg := "failed to detect platform"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("PreparePlanningData() error should contain '%s', got: %v", expectedMsg, err)
	}
}

func TestTestPlanner_PreparePlanningData_TagsCreationError(t *testing.T) {
	ctx := context.Background()

	mockPlatform := &MockPlatform{
		TagsErr: errors.New("tags creation failed"),
	}

	mockPlatformDetector := &MockPlatformDetector{
		Platform: mockPlatform,
	}

	mockOptimizationClient := &MockTestOptimizationClient{}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, &MockTestSuiteDurationsClient{}, newDefaultMockCIProviderDetector())

	err := runner.PreparePlanningData(ctx)

	if err == nil {
		t.Error("PreparePlanningData() should return error when tags creation fails")
	}

	expectedMsg := "failed to create platform tags"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("PreparePlanningData() error should contain '%s', got: %v", expectedMsg, err)
	}
}

func TestTestPlanner_PreparePlanningData_OptimizationClientInitError(t *testing.T) {
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

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, &MockTestSuiteDurationsClient{}, newDefaultMockCIProviderDetector())

	err := runner.PreparePlanningData(ctx)

	if err == nil {
		t.Error("PreparePlanningData() should return error when optimization client initialization fails")
	}

	expectedMsg := "failed to initialize optimization client"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("PreparePlanningData() error should contain '%s', got: %v", expectedMsg, err)
	}
}

func TestTestPlanner_PreparePlanningData_FrameworkDetectionError(t *testing.T) {
	ctx := context.Background()

	mockPlatform := &MockPlatform{
		Tags:         map[string]string{"platform": "ruby"},
		FrameworkErr: errors.New("framework detection failed"),
	}

	mockPlatformDetector := &MockPlatformDetector{
		Platform: mockPlatform,
	}

	mockOptimizationClient := &MockTestOptimizationClient{}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, &MockTestSuiteDurationsClient{}, newDefaultMockCIProviderDetector())

	err := runner.PreparePlanningData(ctx)

	if err == nil {
		t.Error("PreparePlanningData() should return error when framework detection fails")
	}

	expectedMsg := "failed to detect framework"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("PreparePlanningData() error should contain '%s', got: %v", expectedMsg, err)
	}
}

func TestTestPlanner_PreparePlanningData_TestDiscoveryError(t *testing.T) {
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

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, &MockTestSuiteDurationsClient{}, newDefaultMockCIProviderDetector())

	err := runner.PreparePlanningData(ctx)

	if err == nil {
		t.Error("PreparePlanningData() should return error when test discovery fails")
	}

	expectedMsg := "test discovery failed"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("PreparePlanningData() error should contain '%s', got: %v", expectedMsg, err)
	}
}

func TestTestPlanner_PreparePlanningData_EmptyTests(t *testing.T) {
	ctx := context.Background()
	logs := captureLogs(t)

	mockFramework := &MockFramework{
		TestFiles: []string{"file1.rb"},      // Fast discovery should be used when full discovery returns no tests.
		Tests:     []testoptimization.Test{}, // Empty test list
	}

	mockPlatform := &MockPlatform{
		Tags:      map[string]string{"platform": "ruby"},
		Framework: mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{Platform: mockPlatform}
	mockOptimizationClient := &MockTestOptimizationClient{
		Settings: testOptimizationSettings(true, true, false),
		SkippableTests: map[string]bool{
			(&testoptimization.Test{Module: "rspec", Suite: "Suite", Name: "test1"}).DatadogTestId(): true,
		},
	}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, &MockTestSuiteDurationsClient{}, newDefaultMockCIProviderDetector())

	err := runner.PreparePlanningData(ctx)

	if err != nil {
		t.Errorf("PreparePlanningData() should handle empty tests, got: %v", err)
	}

	if len(runner.testFiles) != 1 {
		t.Errorf("PreparePlanningData() should use fast discovery fallback for empty full discovery, got %d files", len(runner.testFiles))
	}
	if _, ok := runner.testFiles["file1.rb"]; !ok {
		t.Errorf("PreparePlanningData() should include fast-discovered file after empty full discovery, got %v", runner.testFiles)
	}
	if _, ok := runner.testFileWeights["file1.rb"]; !ok {
		t.Errorf("PreparePlanningData() should schedule fast-discovered file after empty full discovery, got %v", runner.testFileWeights)
	}
	if !strings.Contains(logs.String(), "level=WARN") ||
		!strings.Contains(logs.String(), "Full test discovery results could not be processed") {
		t.Errorf("Expected WARN log for empty full discovery, got logs: %s", logs.String())
	}

	// Division by zero should be handled gracefully
	if runner.skippablePercentage != 0.0 {
		t.Logf("Skippable percentage for empty tests: %f", runner.skippablePercentage)
	}
}

func TestTestPlanner_PreparePlanningData_AllTestsSkipped(t *testing.T) {
	ctx := context.Background()

	mockFramework := &MockFramework{
		Tests: []testoptimization.Test{
			{Module: "rspec", Suite: "Suite1", Name: "test1", Parameters: "", SuiteSourceFile: "file1.rb"},
			{Module: "rspec", Suite: "Suite2", Name: "test2", Parameters: "", SuiteSourceFile: "file2.rb"},
		},
	}

	mockPlatform := &MockPlatform{
		Tags:      map[string]string{"platform": "ruby"},
		Framework: mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{Platform: mockPlatform}
	mockOptimizationClient := &MockTestOptimizationClient{
		Settings: testOptimizationSettings(true, true, false),
		SkippableTests: map[string]bool{
			(&testoptimization.Test{Module: "rspec", Suite: "Suite1", Name: "test1"}).DatadogTestId(): true,
			(&testoptimization.Test{Module: "rspec", Suite: "Suite2", Name: "test2"}).DatadogTestId(): true,
		},
	}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, &MockTestSuiteDurationsClient{}, newDefaultMockCIProviderDetector())

	err := runner.PreparePlanningData(ctx)

	if err != nil {
		t.Errorf("PreparePlanningData() should handle all tests skipped, got: %v", err)
	}

	if len(runner.testFiles) != 2 {
		t.Errorf("PreparePlanningData() should keep all discovered files even when all tests are skipped, got %d", len(runner.testFiles))
	}

	if weightedFiles := runner.calculateFileWeights(); len(weightedFiles) != 0 {
		t.Errorf("PreparePlanningData() should result in 0 weighted files when all tests are skipped, got %v", weightedFiles)
	}

	if runner.skippablePercentage != 100.0 {
		t.Errorf("PreparePlanningData() should calculate 100%% skippable when all tests are skipped, got %.2f", runner.skippablePercentage)
	}
}

func TestTestPlanner_PreparePlanningData_RuntimeTagsOverride(t *testing.T) {
	ctx := context.Background()

	// Set runtime tags override via environment variable - only override some tags
	setPlannerRuntimeTags(t, `{"os.platform":"linux","runtime.version":"3.2.0"}`)

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

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, &MockTestSuiteDurationsClient{}, newDefaultMockCIProviderDetector())

	err := runner.PreparePlanningData(ctx)

	if err != nil {
		t.Errorf("PreparePlanningData() should not return error, got: %v", err)
	}

	// Verify optimization client was initialized
	if !mockOptimizationClient.InitializeCalled {
		t.Error("PreparePlanningData() should initialize optimization client")
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

func TestTestPlanner_PreparePlanningData_RuntimeTagsOverrideInvalidJSON(t *testing.T) {
	ctx := context.Background()

	// Set invalid JSON as runtime tags override
	setPlannerRuntimeTags(t, `{invalid json}`)

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

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, &MockTestSuiteDurationsClient{}, newDefaultMockCIProviderDetector())

	err := runner.PreparePlanningData(ctx)

	if err == nil {
		t.Error("PreparePlanningData() should return error when runtime tags JSON is invalid")
	}

	expectedMsg := "failed to parse runtime tags override"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("PreparePlanningData() error should contain '%s', got: %v", expectedMsg, err)
	}

	// Optimization client should not be initialized when there's a parse error
	if mockOptimizationClient.InitializeCalled {
		t.Error("PreparePlanningData() should not initialize optimization client when runtime tags JSON is invalid")
	}
}

func TestTestPlanner_PreparePlanningData_NoRuntimeTagsOverride(t *testing.T) {
	ctx := context.Background()

	// Ensure no runtime tags override is set
	setPlannerRuntimeTags(t, "")

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

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, &MockTestSuiteDurationsClient{}, newDefaultMockCIProviderDetector())

	err := runner.PreparePlanningData(ctx)

	if err != nil {
		t.Errorf("PreparePlanningData() should not return error, got: %v", err)
	}

	// Verify optimization client was initialized with platform tags
	if !mockOptimizationClient.InitializeCalled {
		t.Error("PreparePlanningData() should initialize optimization client")
	}

	// Check that platform tags were used (not override)
	if mockOptimizationClient.Tags["os.platform"] != "darwin" {
		t.Errorf("Expected os.platform to be 'darwin' from platform, got %q", mockOptimizationClient.Tags["os.platform"])
	}

	if mockOptimizationClient.Tags["runtime.version"] != "3.3.0" {
		t.Errorf("Expected runtime.version to be '3.3.0' from platform, got %q", mockOptimizationClient.Tags["runtime.version"])
	}
}

// initGitRepo initializes a bare git repo in the given directory so that
// `git rev-parse --show-toplevel` resolves correctly during tests.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	cmd.Env = gitTestEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to init git repo in %s: %v\n%s", dir, err, string(out))
	}
	// Need at least one commit for some git operations to work
	cmd = exec.Command("git", "commit", "--allow-empty", "-m", "init")
	cmd.Dir = dir
	cmd.Env = append(gitTestEnv(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to create initial commit in %s: %v\n%s", dir, err, string(out))
	}
}

// TestPreparePlanningData_ITRFullDiscovery_SubdirRootRelativePath_NormalizesToCwdRelative
// reproduces issue #33: full discovery returns repo-root-relative SuiteSourceFile paths
// (e.g. "core/spec/...") but workers run from subdirectory "core/", causing double-prefix.
func TestPreparePlanningData_ITRFullDiscovery_SubdirRootRelativePath_NormalizesToCwdRelative(t *testing.T) {
	ctx := context.Background()

	// Create a temp monorepo: repoRoot/core/spec/models/order_spec.rb
	repoRoot := t.TempDir()
	initGitRepo(t, repoRoot)

	coreDir := filepath.Join(repoRoot, "core")
	_ = os.MkdirAll(filepath.Join(coreDir, "spec", "models"), 0755)
	_ = os.WriteFile(filepath.Join(coreDir, "spec", "models", "order_spec.rb"), []byte("# spec"), 0644)
	_ = os.MkdirAll(filepath.Join(coreDir, "spec", "finders"), 0755)
	_ = os.WriteFile(filepath.Join(coreDir, "spec", "finders", "find_spec.rb"), []byte("# spec"), 0644)

	// chdir into the subdirectory (simulating: cd core && ddtest plan)
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(coreDir)
	ciUtils.ResetCwdSubdirPrefixForTesting()
	t.Cleanup(ciUtils.ResetCwdSubdirPrefixForTesting)

	// Full discovery returns repo-root-relative paths (the bug scenario)
	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		Tests: []testoptimization.Test{
			{Suite: "Order", Name: "should be valid", Parameters: "", SuiteSourceFile: "core/spec/models/order_spec.rb"},
			{Suite: "AddressFinder", Name: "finds addresses", Parameters: "", SuiteSourceFile: "core/spec/finders/find_spec.rb"},
		},
	}

	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags:         map[string]string{"platform": "ruby"},
		Framework:    mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{Platform: mockPlatform}
	mockOptimizationClient := testOptimizationClientRequiringFullDiscovery()

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, &MockTestSuiteDurationsClient{}, newDefaultMockCIProviderDetector())

	err := runner.PreparePlanningData(ctx)
	if err != nil {
		t.Fatalf("PreparePlanningData() should not return error, got: %v", err)
	}

	// The key assertion: testFiles should contain CWD-relative paths, not repo-root-relative paths
	// i.e. "spec/models/order_spec.rb" not "core/spec/models/order_spec.rb"
	expectedFiles := map[string]bool{
		"spec/models/order_spec.rb": true,
		"spec/finders/find_spec.rb": true,
	}

	if len(runner.testFiles) != 2 {
		t.Fatalf("Expected 2 test files, got %d: %v", len(runner.testFiles), runner.testFiles)
	}

	for file := range runner.testFiles {
		if !expectedFiles[file] {
			t.Errorf("Unexpected test file path %q - should be CWD-relative, not repo-root-relative", file)
		}
		// Explicitly check for the double-prefix bug
		if strings.HasPrefix(file, "core/") {
			t.Errorf("Test file path %q still has repo-root prefix 'core/' - this is the bug from issue #33", file)
		}
	}
}

// TestPreparePlanningData_RepoRootRun_LeavesRepoRelativePathsUnchanged
// ensures that when running from the repo root (not a subdirectory), paths are not modified.
func TestPreparePlanningData_RepoRootRun_LeavesRepoRelativePathsUnchanged(t *testing.T) {
	ctx := context.Background()

	// Create a temp repo root with spec files
	repoRoot := t.TempDir()
	initGitRepo(t, repoRoot)

	_ = os.MkdirAll(filepath.Join(repoRoot, "spec", "models"), 0755)
	_ = os.WriteFile(filepath.Join(repoRoot, "spec", "models", "user_spec.rb"), []byte("# spec"), 0644)

	// chdir to repo root (normal case)
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(repoRoot)
	ciUtils.ResetCwdSubdirPrefixForTesting()
	t.Cleanup(ciUtils.ResetCwdSubdirPrefixForTesting)

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		Tests: []testoptimization.Test{
			{Suite: "User", Name: "should be valid", Parameters: "", SuiteSourceFile: "spec/models/user_spec.rb"},
		},
	}

	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags:         map[string]string{"platform": "ruby"},
		Framework:    mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{Platform: mockPlatform}
	mockOptimizationClient := testOptimizationClientRequiringFullDiscovery()

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, &MockTestSuiteDurationsClient{}, newDefaultMockCIProviderDetector())

	err := runner.PreparePlanningData(ctx)
	if err != nil {
		t.Fatalf("PreparePlanningData() should not return error, got: %v", err)
	}

	// Paths should remain unchanged when running from repo root
	if _, ok := runner.testFiles["spec/models/user_spec.rb"]; !ok {
		t.Errorf("Expected test file 'spec/models/user_spec.rb' to remain unchanged, got: %v", runner.testFiles)
	}
}

// TestPreparePlanningData_FastDiscovery_PathsRemainUnchanged
// ensures the fast discovery path (ITR disabled) does not modify paths.
func TestPreparePlanningData_FastDiscovery_PathsRemainUnchanged(t *testing.T) {
	ctx := context.Background()

	// Fast discovery returns CWD-relative paths directly from glob
	mockFramework := &MockFramework{
		FrameworkName:    "rspec",
		Tests:            nil,                                                                    // No full discovery results
		TestFiles:        []string{"spec/models/user_spec.rb", "spec/controllers/admin_spec.rb"}, // Fast discovery
		DiscoverTestsErr: errors.New("context canceled"),                                         // Simulate full discovery being cancelled
	}

	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags:         map[string]string{"platform": "ruby"},
		Framework:    mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{Platform: mockPlatform}
	mockOptimizationClient := &MockTestOptimizationClient{
		Settings: testOptimizationSettings(true, false, false),
	}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, &MockTestSuiteDurationsClient{}, newDefaultMockCIProviderDetector())

	err := runner.PreparePlanningData(ctx)
	if err != nil {
		t.Fatalf("PreparePlanningData() should not return error, got: %v", err)
	}

	// Fast discovery paths should be used as-is
	expectedFiles := map[string]bool{
		"spec/models/user_spec.rb":       true,
		"spec/controllers/admin_spec.rb": true,
	}

	if len(runner.testFiles) != 2 {
		t.Fatalf("Expected 2 test files, got %d: %v", len(runner.testFiles), runner.testFiles)
	}

	for file := range runner.testFiles {
		if !expectedFiles[file] {
			t.Errorf("Unexpected test file path %q in fast discovery", file)
		}
	}
}

// TestPreparePlanningData_ITRPathNormalization_PrefixMismatchUnchanged
// ensures that when SuiteSourceFile does not match the current subdir prefix,
// the path is not modified (conservative behavior).
func TestPreparePlanningData_ITRPathNormalization_PrefixMismatchUnchanged(t *testing.T) {
	ctx := context.Background()

	// Create monorepo with "api" and "core" subdirs
	repoRoot := t.TempDir()
	initGitRepo(t, repoRoot)

	apiDir := filepath.Join(repoRoot, "api")
	_ = os.MkdirAll(filepath.Join(apiDir, "spec", "models"), 0755)
	_ = os.WriteFile(filepath.Join(apiDir, "spec", "models", "endpoint_spec.rb"), []byte("# spec"), 0644)

	// We're in "api/" subdir but discovery returns "core/" paths (shouldn't happen in practice,
	// but tests the safety of the normalization)
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(apiDir)
	ciUtils.ResetCwdSubdirPrefixForTesting()
	t.Cleanup(ciUtils.ResetCwdSubdirPrefixForTesting)

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		Tests: []testoptimization.Test{
			// Path with different prefix than CWD subdir - should be left unchanged
			{Suite: "Endpoint", Name: "should respond", Parameters: "", SuiteSourceFile: "core/spec/models/order_spec.rb"},
			// Path that does match CWD subdir prefix
			{Suite: "ApiEndpoint", Name: "should work", Parameters: "", SuiteSourceFile: "api/spec/models/endpoint_spec.rb"},
		},
	}

	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags:         map[string]string{"platform": "ruby"},
		Framework:    mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{Platform: mockPlatform}
	mockOptimizationClient := testOptimizationClientRequiringFullDiscovery()

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, &MockTestSuiteDurationsClient{}, newDefaultMockCIProviderDetector())

	err := runner.PreparePlanningData(ctx)
	if err != nil {
		t.Fatalf("PreparePlanningData() should not return error, got: %v", err)
	}

	// "core/spec/..." doesn't match "api" subdir prefix, should be unchanged
	// "api/spec/..." matches "api" subdir prefix, should be normalized to "spec/..."
	expectedFiles := map[string]bool{
		"core/spec/models/order_spec.rb": true, // Mismatched prefix - unchanged
		"spec/models/endpoint_spec.rb":   true, // Matched "api/" prefix - stripped
	}

	if len(runner.testFiles) != 2 {
		t.Fatalf("Expected 2 test files, got %d: %v", len(runner.testFiles), runner.testFiles)
	}

	for file := range runner.testFiles {
		if !expectedFiles[file] {
			t.Errorf("Unexpected test file path %q", file)
		}
	}
}

// TestPreparePlanningData_ITRSubdir_SkipMatching_WithSuitePathsMatchingCwd
// verifies that when running from a monorepo subdirectory, skip matching works
// correctly: both the API (skippable tests) and framework discovery use the same
// CWD-relative Suite names (e.g. "Spree::Role at ./spec/models/role_spec.rb"),
// while SuiteSourceFile is repo-root-relative (e.g. "core/spec/models/role_spec.rb")
// and needs normalization for worker splitting.
func TestPreparePlanningData_ITRSubdir_SkipMatching_WithSuitePathsMatchingCwd(t *testing.T) {
	ctx := context.Background()

	// Create a temp monorepo: repoRoot/core/spec/models/
	repoRoot := t.TempDir()
	initGitRepo(t, repoRoot)

	coreDir := filepath.Join(repoRoot, "core")
	_ = os.MkdirAll(filepath.Join(coreDir, "spec", "models"), 0755)
	_ = os.WriteFile(filepath.Join(coreDir, "spec", "models", "role_spec.rb"), []byte("# spec"), 0644)
	_ = os.WriteFile(filepath.Join(coreDir, "spec", "models", "order_spec.rb"), []byte("# spec"), 0644)

	// chdir into the subdirectory (simulating: cd core && ddtest plan)
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(coreDir)
	ciUtils.ResetCwdSubdirPrefixForTesting()
	t.Cleanup(ciUtils.ResetCwdSubdirPrefixForTesting)

	// Both framework discovery and API use CWD-relative Suite names.
	// SuiteSourceFile is repo-root-relative (comes from tracer's test discovery mode).
	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		Tests: []testoptimization.Test{
			{Suite: "Spree::Role at ./spec/models/role_spec.rb", Name: "should be valid", Parameters: "", SuiteSourceFile: "core/spec/models/role_spec.rb"},
			{Suite: "Spree::Role at ./spec/models/role_spec.rb", Name: "should have permissions", Parameters: "", SuiteSourceFile: "core/spec/models/role_spec.rb"},
			{Suite: "Order at ./spec/models/order_spec.rb", Name: "should be valid", Parameters: "", SuiteSourceFile: "core/spec/models/order_spec.rb"},
		},
	}

	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags:         map[string]string{"platform": "ruby"},
		Framework:    mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{Platform: mockPlatform}

	// API returns skippable tests with the same CWD-relative Suite names
	roleTest1 := testoptimization.Test{
		Suite: "Spree::Role at ./spec/models/role_spec.rb", Name: "should be valid", Parameters: "",
	}
	roleTest2 := testoptimization.Test{
		Suite: "Spree::Role at ./spec/models/role_spec.rb", Name: "should have permissions", Parameters: "",
	}
	mockOptimizationClient := &MockTestOptimizationClient{
		Settings: testOptimizationSettings(true, true, false),
		SkippableTests: map[string]bool{
			roleTest1.DatadogTestId(): true,
			roleTest2.DatadogTestId(): true,
		},
	}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, &MockTestSuiteDurationsClient{}, newDefaultMockCIProviderDetector())

	err := runner.PreparePlanningData(ctx)
	if err != nil {
		t.Fatalf("PreparePlanningData() should not return error, got: %v", err)
	}

	// 2 of 3 tests should be skipped (the role_spec.rb tests)
	expectedSkippablePercentage := float64(2) / float64(3) * 100.0
	if runner.skippablePercentage != expectedSkippablePercentage {
		t.Errorf("Expected skippablePercentage=%.2f%%, got %.2f%%",
			expectedSkippablePercentage, runner.skippablePercentage)
	}

	// All discovered source files should remain in testFiles, while calculateFileWeights omits the fully skipped role_spec.rb.
	// The SuiteSourceFile paths should be normalized from "core/spec/..." to "spec/..." (CWD-relative).
	expectedFiles := map[string]bool{
		"spec/models/role_spec.rb":  true,
		"spec/models/order_spec.rb": true,
	}

	if len(runner.testFiles) != 2 {
		t.Fatalf("Expected 2 discovered test files, got %d: %v", len(runner.testFiles), runner.testFiles)
	}

	for file := range runner.testFiles {
		if !expectedFiles[file] {
			t.Errorf("Unexpected test file path %q", file)
		}
	}

	weightedFiles := runner.calculateFileWeights()
	if len(weightedFiles) != 1 {
		t.Fatalf("Expected 1 weighted test file (only order_spec.rb), got %d: %v", len(weightedFiles), weightedFiles)
	}
	if _, ok := weightedFiles["spec/models/order_spec.rb"]; !ok {
		t.Errorf("Expected weighted test files to contain only order_spec.rb, got %v", weightedFiles)
	}
}
