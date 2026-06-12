package planner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	ciConstants "github.com/DataDog/ddtest/civisibility/constants"
	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization"
	ciUtils "github.com/DataDog/ddtest/internal/utils"
)

type highSkippableIntegrationFixture struct {
	Tests                     []testoptimization.Test                                      `json:"tests"`
	TestFiles                 []string                                                     `json:"testFiles"`
	SkippableTests            []string                                                     `json:"skippableTests"`
	TestSuiteDurations        map[string]map[string]testoptimization.TestSuiteDurationInfo `json:"testSuiteDurations"`
	ExpectedRunnableTestFiles []string                                                     `json:"expectedRunnableTestFiles"`
	OriginalParallelRunners   int                                                          `json:"originalParallelRunners"`
	ExpectedParallelRunners   int                                                          `json:"expectedParallelRunners"`
}

func TestTestPlanner_Plan_HighSkippableIntegrationSelectsExpectedRunnerCountAndRunnableFiles(t *testing.T) {
	fixture := loadHighSkippableIntegrationFixture(t, "spree_26236954724.json")
	if fixture.OriginalParallelRunners != 3 {
		t.Fatalf("fixture should capture the original 3-runner plan, got %d", fixture.OriginalParallelRunners)
	}

	expectedRunnableFiles := fixture.nonFullySkippedTestFiles()
	if !slices.Equal(expectedRunnableFiles, fixture.ExpectedRunnableTestFiles) {
		t.Fatalf(
			"fixture expected runnable files %v do not match computed non-fully-skipped files %v",
			fixture.ExpectedRunnableTestFiles,
			expectedRunnableFiles,
		)
	}

	repoRoot := t.TempDir()
	initGitRepo(t, repoRoot)
	coreDir := filepath.Join(repoRoot, "core")
	if err := os.MkdirAll(coreDir, 0755); err != nil {
		t.Fatalf("failed to create core dir: %v", err)
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(coreDir); err != nil {
		t.Fatalf("failed to change working directory: %v", err)
	}
	ciUtils.ResetCwdSubdirPrefixForTesting()
	t.Cleanup(ciUtils.ResetCwdSubdirPrefixForTesting)

	t.Cleanup(settings.Init)
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_MIN_PARALLELISM", "1")
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM", "8")
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_CI_JOB_OVERHEAD", "25s")
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_REPORT_ENABLED", "false")
	settings.Init()

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		Tests:         fixture.Tests,
		TestFiles:     fixture.TestFiles,
	}
	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags: map[string]string{
			ciConstants.GitRepositoryURL: repoRoot,
		},
		Framework: mockFramework,
	}
	runner := NewWithDependencies(
		&MockPlatformDetector{Platform: mockPlatform},
		&MockTestOptimizationClient{
			Settings:       testOptimizationSettings(true, true, false),
			SkippableTests: fixture.skippableTestSet(),
		},
		&MockTestSuiteDurationsClient{Durations: fixture.TestSuiteDurations},
		newDefaultMockCIProviderDetector(),
	)

	if err := runner.Plan(context.Background()); err != nil {
		t.Fatalf("Plan() should not return error, got: %v", err)
	}

	expectedParallelRunners := fixture.ExpectedParallelRunners
	if runner.planReport.Split.parallelRunners != expectedParallelRunners {
		t.Fatalf("Plan() selected %d runners, expected %d", runner.planReport.Split.parallelRunners, expectedParallelRunners)
	}
	assertFileContent(t, constants.ParallelRunnersOutputPath, "1")

	testFiles := readTestPlanLines(t, constants.TestFilesOutputPath)
	if !slices.Equal(testFiles, expectedRunnableFiles) {
		t.Fatalf("planned test files = %v, expected only non-fully-skipped files %v", testFiles, expectedRunnableFiles)
	}

	runner0 := readTestPlanLines(t, filepath.Join(constants.TestsSplitDir, "runner-0"))
	if !slices.Equal(runner0, expectedRunnableFiles) {
		t.Fatalf("runner-0 files = %v, expected %v", runner0, expectedRunnableFiles)
	}

	if _, err := os.Stat(filepath.Join(constants.TestsSplitDir, "runner-1")); !os.IsNotExist(err) {
		t.Fatalf("expected a single runner split, runner-1 stat error = %v", err)
	}
}

func loadHighSkippableIntegrationFixture(t *testing.T, name string) highSkippableIntegrationFixture {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("testdata", "high_skippable_integration", name))
	if err != nil {
		t.Fatalf("failed to read high skippable integration fixture: %v", err)
	}

	var fixture highSkippableIntegrationFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("failed to parse high skippable integration fixture: %v", err)
	}
	return fixture
}

func (f highSkippableIntegrationFixture) skippableTestSet() map[string]bool {
	skippableTests := make(map[string]bool, len(f.SkippableTests))
	for _, test := range f.SkippableTests {
		skippableTests[test] = true
	}
	return skippableTests
}

func (f highSkippableIntegrationFixture) nonFullySkippedTestFiles() []string {
	skippableTests := f.skippableTestSet()
	type fileCounts struct {
		total   int
		skipped int
	}
	countsByFile := make(map[string]fileCounts)
	for _, test := range f.Tests {
		sourceFile := strings.TrimPrefix(test.SuiteSourceFile, "core/")
		counts := countsByFile[sourceFile]
		counts.total++
		if skippableTests[test.DatadogTestId()] {
			counts.skipped++
		}
		countsByFile[sourceFile] = counts
	}

	testFiles := make([]string, 0)
	for sourceFile, counts := range countsByFile {
		if counts.total > counts.skipped {
			testFiles = append(testFiles, sourceFile)
		}
	}
	slices.Sort(testFiles)
	return testFiles
}

func readTestPlanLines(t *testing.T, path string) []string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read %s: %v", path, err)
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return nil
	}
	return strings.Split(content, "\n")
}
