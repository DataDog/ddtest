package runner

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ciConstants "github.com/DataDog/ddtest/civisibility/constants"
	ciUtils "github.com/DataDog/ddtest/civisibility/utils"
	ciNet "github.com/DataDog/ddtest/civisibility/utils/net"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization"
)

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

func TestTestRunner_PrepareTestOptimization_Success(t *testing.T) {
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
		SkippableTests: map[string]bool{
			"TestSuite1.test2.": true, // Skip test2
			"TestSuite3.test4.": true, // Skip test4
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

	expectedFiles := map[string]bool{
		"test/file1_test.rb":     true, // test1 is not skipped
		"test/file2_test.rb":     true, // test3 is not skipped
		"test/file3_test.rb":     true, // test4 is skipped but the source file is discovered
		"test/fast_only_test.rb": true, // from fast discovery only
	}

	if len(runner.testFiles) != len(expectedFiles) {
		t.Errorf("PrepareTestOptimization() should result in %d test files, got %d", len(expectedFiles), len(runner.testFiles))
	}

	if weightedFiles := runner.weightedTestFiles(); len(weightedFiles) != 3 {
		t.Errorf("Expected weighted files to omit fully skipped file and keep 3 files, got %v", weightedFiles)
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

	if weight, ok := runner.testFileWeight("test/fast_only_test.rb"); !ok || weight != expectedFile2Weight {
		t.Errorf("Expected fast-only file weight to use default %d, got weight=%d ok=%t", expectedFile2Weight, weight, ok)
	}

	// Verify skippable percentage was calculated correctly (2 out of 4 tests skipped = 50%)
	expectedPercentage := 50.0
	if runner.skippablePercentage != expectedPercentage {
		t.Errorf("PrepareTestOptimization() should calculate skippable percentage as %.2f, got %.2f",
			expectedPercentage, runner.skippablePercentage)
	}

	if !mockDurationsClient.Called {
		t.Error("PrepareTestOptimization() should fetch test suite durations")
	}
}

func TestTestRunner_PrepareTestOptimization_DurationsErrorContinues(t *testing.T) {
	ctx := context.Background()
	ciUtils.ResetCITags()
	t.Cleanup(ciUtils.ResetCITags)
	logs := captureLogs(t)

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
	mockDurationsClient := &MockTestSuiteDurationsClient{Err: errors.New("durations backend failed")}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, mockDurationsClient, newDefaultMockCIProviderDetector())

	err := runner.PrepareTestOptimization(ctx)
	if err != nil {
		t.Fatalf("PrepareTestOptimization() should not fail when durations API errors, got: %v", err)
	}

	if len(runner.testSuiteDurations) != 0 {
		t.Errorf("Expected empty in-memory test suite durations on error, got %v", runner.testSuiteDurations)
	}

	if !strings.Contains(logs.String(), "level=ERROR") || !strings.Contains(logs.String(), "Test durations API errored") {
		t.Errorf("Expected ERROR log for durations API failure, got logs: %s", logs.String())
	}
}

func TestTestRunner_PrepareTestOptimization_EmptyDurationsWarns(t *testing.T) {
	ctx := context.Background()
	ciUtils.ResetCITags()
	t.Cleanup(ciUtils.ResetCITags)
	logs := captureLogs(t)

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
	mockDurationsClient := &MockTestSuiteDurationsClient{
		Durations: map[string]map[string]testoptimization.TestSuiteDurationInfo{},
	}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, mockDurationsClient, newDefaultMockCIProviderDetector())

	err := runner.PrepareTestOptimization(ctx)
	if err != nil {
		t.Fatalf("PrepareTestOptimization() should not fail with empty durations, got: %v", err)
	}

	if len(runner.testSuiteDurations) != 0 {
		t.Errorf("Expected empty in-memory test suite durations on empty response, got %v", runner.testSuiteDurations)
	}

	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "Test durations API returned no test suites") {
		t.Errorf("Expected WARN log for empty durations response, got logs: %s", logs.String())
	}
}

func TestTestRunner_PrepareTestOptimization_NonEmptyDurationsUsesP50ForMatchingSuites(t *testing.T) {
	ctx := context.Background()
	ciUtils.ResetCITags()
	t.Cleanup(ciUtils.ResetCITags)
	logs := captureLogs(t)

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

	err := runner.PrepareTestOptimization(ctx)
	if err != nil {
		t.Fatalf("PrepareTestOptimization() should not fail with durations data, got: %v", err)
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

	if !strings.Contains(logs.String(), "level=DEBUG") || !strings.Contains(logs.String(), "Found test suite durations") || !strings.Contains(logs.String(), "testSuitesCount=2") {
		t.Errorf("Expected DEBUG log for non-empty durations response, got logs: %s", logs.String())
	}
}

func TestTestRunner_PrepareTestOptimization_SkippablePercentageUsesDurations(t *testing.T) {
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
		SkippableTests: map[string]bool{skippedTest.FQN(): true},
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

	err := runner.PrepareTestOptimization(ctx)
	if err != nil {
		t.Fatalf("PrepareTestOptimization() should not fail, got: %v", err)
	}

	expectedPercentage := 40.0
	if runner.skippablePercentage != expectedPercentage {
		t.Errorf("Expected skippable percentage to use saved time %.2f, got %.2f", expectedPercentage, runner.skippablePercentage)
	}
}

func TestTestRunner_TestFileWeight_CountFallbackForMissingSuiteDuration(t *testing.T) {
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

	runner.resolveSuiteDurations()
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
}

func TestTestRunner_TestFileWeight_InvalidP50FallsBackForFullDiscoveryAggregate(t *testing.T) {
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

	runner.resolveSuiteDurations()
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

func TestTestRunner_TestFileWeight_SubMillisecondP50MinimumWeight(t *testing.T) {
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

	runner.resolveSuiteDurations()
	runner.suitesBySourceFile = indexSuitesBySourceFile(runner.suiteAggregates)

	if weight, ok := runner.testFileWeight("spec/fast_test.rb"); !ok || weight != 1 {
		t.Errorf("Expected sub-millisecond p50 to use minimum weight 1, got weight=%d ok=%t", weight, ok)
	}
}

func TestTestRunner_TestFileWeight_SkipsFullySkippedSuites(t *testing.T) {
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

	if weightedFiles := runner.weightedTestFiles(); len(weightedFiles) != 0 {
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

func TestTestRunner_PrepareTestOptimization_FastDiscoveryUsesBackendDurations(t *testing.T) {
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

	err := runner.PrepareTestOptimization(ctx)
	if err != nil {
		t.Fatalf("PrepareTestOptimization() should not fail when full discovery fails but fast discovery succeeds, got: %v", err)
	}

	if weight, ok := runner.testFileWeight("spec/backend_only_spec.rb"); !ok || weight != 42 {
		t.Errorf("Expected fast-discovery file to use backend p50 converted to 42ms, got weight=%d ok=%t", weight, ok)
	}
}

func TestTestRunner_PrepareTestOptimization_BackendDurationSubdirMatchesFastDiscovery(t *testing.T) {
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

	err := runner.PrepareTestOptimization(ctx)
	if err != nil {
		t.Fatalf("PrepareTestOptimization() should not fail, got: %v", err)
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

func TestTestRunner_PrepareTestOptimization_IgnoresBackendDurationsForUndiscoveredFiles(t *testing.T) {
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

	err := runner.PrepareTestOptimization(ctx)
	if err != nil {
		t.Fatalf("PrepareTestOptimization() should not fail, got: %v", err)
	}

	if len(runner.suiteAggregates) != 0 {
		t.Errorf("Expected stale backend suite to be ignored, got aggregates: %v", runner.suiteAggregates)
	}

	if weight, ok := runner.testFileWeight("spec/discovered_spec.rb"); !ok || weight != int(time.Second/time.Millisecond) {
		t.Errorf("Expected discovered file without backend aggregate to use default 1 second, got weight=%d ok=%t", weight, ok)
	}
}

func TestTestRunner_PrepareTestOptimization_FastDiscoveryDoesNotRunStaleBackendFilesWhenSkippingDisabled(t *testing.T) {
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
		Settings: &ciNet.SettingsResponseData{
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

	err := runner.PrepareTestOptimization(ctx)
	if err != nil {
		t.Fatalf("PrepareTestOptimization() should not fail, got: %v", err)
	}

	weightedFiles := runner.weightedTestFiles()
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

func TestTestRunner_PrepareTestOptimization_BackendDoesNotReintroduceFullySkippedSuite(t *testing.T) {
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
		SkippableTests: map[string]bool{skippedTest.FQN(): true},
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

	err := runner.PrepareTestOptimization(ctx)
	if err != nil {
		t.Fatalf("PrepareTestOptimization() should not fail, got: %v", err)
	}

	aggregate := runner.suiteAggregates[testSuiteKey{Module: "rspec", Suite: "SkippedSuite"}]
	if aggregate.NumTests != 1 || aggregate.NumTestsSkipped != 1 {
		t.Errorf("Expected full-discovery skip metadata to remain intact, got %+v", aggregate)
	}

	if weight, ok := runner.testFileWeight("spec/skipped_spec.rb"); ok || weight != 0 {
		t.Errorf("Expected fully skipped suite to be omitted despite backend duration, got weight=%d ok=%t", weight, ok)
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

func TestTestRunner_PrepareTestOptimization_PlatformDetectionError(t *testing.T) {
	ctx := context.Background()

	mockPlatformDetector := &MockPlatformDetector{
		Err: errors.New("platform detection failed"),
	}

	mockOptimizationClient := &MockTestOptimizationClient{}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, &MockTestSuiteDurationsClient{}, newDefaultMockCIProviderDetector())

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

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, &MockTestSuiteDurationsClient{}, newDefaultMockCIProviderDetector())

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

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, &MockTestSuiteDurationsClient{}, newDefaultMockCIProviderDetector())

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

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, &MockTestSuiteDurationsClient{}, newDefaultMockCIProviderDetector())

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

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, &MockTestSuiteDurationsClient{}, newDefaultMockCIProviderDetector())

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

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, &MockTestSuiteDurationsClient{}, newDefaultMockCIProviderDetector())

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
			{Suite: "Suite1", Name: "test1", Parameters: "", SuiteSourceFile: "file1.rb"},
			{Suite: "Suite2", Name: "test2", Parameters: "", SuiteSourceFile: "file2.rb"},
		},
	}

	mockPlatform := &MockPlatform{
		Tags:      map[string]string{"platform": "ruby"},
		Framework: mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{Platform: mockPlatform}
	mockOptimizationClient := &MockTestOptimizationClient{
		SkippableTests: map[string]bool{
			"Suite1.test1.": true,
			"Suite2.test2.": true,
		},
	}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, &MockTestSuiteDurationsClient{}, newDefaultMockCIProviderDetector())

	err := runner.PrepareTestOptimization(ctx)

	if err != nil {
		t.Errorf("PrepareTestOptimization() should handle all tests skipped, got: %v", err)
	}

	if len(runner.testFiles) != 2 {
		t.Errorf("PrepareTestOptimization() should keep all discovered files even when all tests are skipped, got %d", len(runner.testFiles))
	}

	if weightedFiles := runner.weightedTestFiles(); len(weightedFiles) != 0 {
		t.Errorf("PrepareTestOptimization() should result in 0 weighted files when all tests are skipped, got %v", weightedFiles)
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

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, &MockTestSuiteDurationsClient{}, newDefaultMockCIProviderDetector())

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

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, &MockTestSuiteDurationsClient{}, newDefaultMockCIProviderDetector())

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

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, &MockTestSuiteDurationsClient{}, newDefaultMockCIProviderDetector())

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

// initGitRepo initializes a bare git repo in the given directory so that
// `git rev-parse --show-toplevel` resolves correctly during tests.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to init git repo in %s: %v\n%s", dir, err, string(out))
	}
	// Need at least one commit for some git operations to work
	cmd = exec.Command("git", "commit", "--allow-empty", "-m", "init")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to create initial commit in %s: %v\n%s", dir, err, string(out))
	}
}

// TestPrepareTestOptimization_ITRFullDiscovery_SubdirRootRelativePath_NormalizesToCwdRelative
// reproduces issue #33: full discovery returns repo-root-relative SuiteSourceFile paths
// (e.g. "core/spec/...") but workers run from subdirectory "core/", causing double-prefix.
func TestPrepareTestOptimization_ITRFullDiscovery_SubdirRootRelativePath_NormalizesToCwdRelative(t *testing.T) {
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
	mockOptimizationClient := &MockTestOptimizationClient{
		SkippableTests: map[string]bool{}, // No tests skipped (ITR enabled but all tests need to run)
	}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, &MockTestSuiteDurationsClient{}, newDefaultMockCIProviderDetector())

	err := runner.PrepareTestOptimization(ctx)
	if err != nil {
		t.Fatalf("PrepareTestOptimization() should not return error, got: %v", err)
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

// TestPrepareTestOptimization_RepoRootRun_LeavesRepoRelativePathsUnchanged
// ensures that when running from the repo root (not a subdirectory), paths are not modified.
func TestPrepareTestOptimization_RepoRootRun_LeavesRepoRelativePathsUnchanged(t *testing.T) {
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
	mockOptimizationClient := &MockTestOptimizationClient{
		SkippableTests: map[string]bool{},
	}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, &MockTestSuiteDurationsClient{}, newDefaultMockCIProviderDetector())

	err := runner.PrepareTestOptimization(ctx)
	if err != nil {
		t.Fatalf("PrepareTestOptimization() should not return error, got: %v", err)
	}

	// Paths should remain unchanged when running from repo root
	if _, ok := runner.testFiles["spec/models/user_spec.rb"]; !ok {
		t.Errorf("Expected test file 'spec/models/user_spec.rb' to remain unchanged, got: %v", runner.testFiles)
	}
}

// TestPrepareTestOptimization_FastDiscovery_PathsRemainUnchanged
// ensures the fast discovery path (ITR disabled) does not modify paths.
func TestPrepareTestOptimization_FastDiscovery_PathsRemainUnchanged(t *testing.T) {
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
		SkippableTests: map[string]bool{},
	}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, &MockTestSuiteDurationsClient{}, newDefaultMockCIProviderDetector())

	err := runner.PrepareTestOptimization(ctx)
	if err != nil {
		t.Fatalf("PrepareTestOptimization() should not return error, got: %v", err)
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

// TestPrepareTestOptimization_ITRPathNormalization_PrefixMismatchUnchanged
// ensures that when SuiteSourceFile does not match the current subdir prefix,
// the path is not modified (conservative behavior).
func TestPrepareTestOptimization_ITRPathNormalization_PrefixMismatchUnchanged(t *testing.T) {
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
	mockOptimizationClient := &MockTestOptimizationClient{
		SkippableTests: map[string]bool{},
	}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, &MockTestSuiteDurationsClient{}, newDefaultMockCIProviderDetector())

	err := runner.PrepareTestOptimization(ctx)
	if err != nil {
		t.Fatalf("PrepareTestOptimization() should not return error, got: %v", err)
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

// TestPrepareTestOptimization_ITRSubdir_SkipMatching_WithSuitePathsMatchingCwd
// verifies that when running from a monorepo subdirectory, skip matching works
// correctly: both the API (skippable tests) and framework discovery use the same
// CWD-relative Suite names (e.g. "Spree::Role at ./spec/models/role_spec.rb"),
// while SuiteSourceFile is repo-root-relative (e.g. "core/spec/models/role_spec.rb")
// and needs normalization for worker splitting.
func TestPrepareTestOptimization_ITRSubdir_SkipMatching_WithSuitePathsMatchingCwd(t *testing.T) {
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
		SkippableTests: map[string]bool{
			roleTest1.FQN(): true,
			roleTest2.FQN(): true,
		},
	}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, &MockTestSuiteDurationsClient{}, newDefaultMockCIProviderDetector())

	err := runner.PrepareTestOptimization(ctx)
	if err != nil {
		t.Fatalf("PrepareTestOptimization() should not return error, got: %v", err)
	}

	// 2 of 3 tests should be skipped (the role_spec.rb tests)
	expectedSkippablePercentage := float64(2) / float64(3) * 100.0
	if runner.skippablePercentage != expectedSkippablePercentage {
		t.Errorf("Expected skippablePercentage=%.2f%%, got %.2f%%",
			expectedSkippablePercentage, runner.skippablePercentage)
	}

	// All discovered source files should remain in testFiles, while weightedTestFiles omits the fully skipped role_spec.rb.
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

	weightedFiles := runner.weightedTestFiles()
	if len(weightedFiles) != 1 {
		t.Fatalf("Expected 1 weighted test file (only order_spec.rb), got %d: %v", len(weightedFiles), weightedFiles)
	}
	if _, ok := weightedFiles["spec/models/order_spec.rb"]; !ok {
		t.Errorf("Expected weighted test files to contain only order_spec.rb, got %v", weightedFiles)
	}
}
