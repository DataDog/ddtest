package planner

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testParallelRunnerOverhead = 25 * time.Second

func testCalculateParallelRunners(testFileWeights map[string]int, minParallelism, maxParallelism int) int {
	return calculateParallelRunnerSplit(testFileWeights, minParallelism, maxParallelism, testParallelRunnerOverhead, 0).parallelRunners
}

func TestCalculateParallelRunners_MaxParallelismIsOne(t *testing.T) {
	testFileWeights := map[string]int{
		"test1.rb": 10,
		"test2.rb": 10,
	}

	for _, maxParallelism := range []int{1, 0, -1, -100} {
		t.Run(fmt.Sprintf("maxParallelism=%d", maxParallelism), func(t *testing.T) {
			result := testCalculateParallelRunners(testFileWeights, 1, maxParallelism)
			if result != 1 {
				t.Errorf("calculateParallelRunners() with maxParallelism=%d = %d, expected 1", maxParallelism, result)
			}
		})
	}
}

func TestCalculateParallelRunners_MinParallelismLessThanOne(t *testing.T) {
	testFileWeights := map[string]int{
		"test1.rb": 60_000,
		"test2.rb": 60_000,
		"test3.rb": 60_000,
		"test4.rb": 60_000,
	}

	result := testCalculateParallelRunners(testFileWeights, 0, 4)
	if result != 4 {
		t.Errorf("calculateParallelRunners() = %d, expected 4 when min_parallelism is normalized to 1", result)
	}
}

func TestCalculateParallelRunners_MaxLessThanMin(t *testing.T) {
	testFileWeights := map[string]int{
		"test1.rb": 10,
		"test2.rb": 10,
		"test3.rb": 10,
		"test4.rb": 10,
	}

	result := testCalculateParallelRunners(testFileWeights, 5, 3)
	if result != 3 {
		t.Errorf("calculateParallelRunners() = %d, expected 3 when max_parallelism is clamped", result)
	}
}

func TestCalculateParallelRunners_MinEqualsMax(t *testing.T) {
	testFileWeights := map[string]int{
		"test1.rb": 10,
		"test2.rb": 10,
		"test3.rb": 10,
	}

	result := testCalculateParallelRunners(testFileWeights, 4, 4)
	if result != 4 {
		t.Errorf("calculateParallelRunners() = %d, expected 4 when min and max match", result)
	}
}

func TestCalculateParallelRunners_EmptyTestFiles(t *testing.T) {
	result := testCalculateParallelRunners(map[string]int{}, 2, 8)
	if result != 2 {
		t.Errorf("calculateParallelRunners() = %d, expected normalized min parallelism 2 for empty tests", result)
	}
}

func TestCalculateParallelRunners_WallTimeWins(t *testing.T) {
	testFileWeights := map[string]int{
		"test1.rb": 60_000,
		"test2.rb": 60_000,
		"test3.rb": 60_000,
		"test4.rb": 60_000,
	}

	result := testCalculateParallelRunners(testFileWeights, 1, 4)
	if result != 4 {
		t.Errorf("calculateParallelRunners() = %d, expected 4 to minimize slowest runner time", result)
	}
}

func TestCalculateParallelRunners_DoesNotFanOutPastLargestFileLowerBound(t *testing.T) {
	testFileWeights := map[string]int{
		"test/very_slow_test.rb": 3_600_000,
	}
	for index := range 100 {
		testFileWeights[fmt.Sprintf("test/medium_%03d_test.rb", index)] = 30_000
	}

	result := testCalculateParallelRunners(testFileWeights, 1, 8)
	if result != 2 {
		t.Errorf("calculateParallelRunners() = %d, expected 2 because extra runners cannot beat the largest-file lower bound", result)
	}
}

func TestCalculateParallelRunners_ImbalanceBreaksWallTimeTie(t *testing.T) {
	testFileWeights := map[string]int{
		"test1.rb": 10,
		"test2.rb": 6,
		"test3.rb": 6,
		"test4.rb": 6,
		"test5.rb": 6,
	}

	result := testCalculateParallelRunners(testFileWeights, 3, 4)
	if result != 3 {
		t.Errorf("calculateParallelRunners() = %d, expected 3 because it keeps the same wall time with lower imbalance", result)
	}
}

func TestCalculateParallelRunners_OverParallelizedInputsPreserveMinimum(t *testing.T) {
	testFileWeights := map[string]int{
		"test1.rb": 10,
	}

	result := testCalculateParallelRunners(testFileWeights, 2, 20)
	if result != 2 {
		t.Errorf("calculateParallelRunners() = %d, expected 2 because min_parallelism is unavoidable", result)
	}
}

func TestCalculateParallelRunnerSplit_ReturnsSelectedScore(t *testing.T) {
	testFileWeights := map[string]int{
		"test1.rb": 10,
		"test2.rb": 6,
		"test3.rb": 6,
		"test4.rb": 6,
		"test5.rb": 6,
	}

	result := calculateParallelRunnerSplit(testFileWeights, 3, 4, testParallelRunnerOverhead, 0)
	expected := splitScore{
		parallelRunners: 3,
		wallTime:        12,
		imbalance:       2,
		totalRuntime:    34,
	}

	if result != expected {
		t.Errorf("calculateParallelRunnerSplit() = %+v, expected %+v", result, expected)
	}
}

func TestCalculateParallelRunnerSplit_RealGitHubActionsArtifacts(t *testing.T) {
	tests := []struct {
		name                    string
		fixturePath             string
		expectedParallelRunners int
	}{
		{
			name:                    "forem tiny modeled wall-time win should not add worker",
			fixturePath:             "forem-26214779547.json",
			expectedParallelRunners: 5,
		},
		{
			name:                    "spree measure 1 high TIA savings should reduce fanout",
			fixturePath:             "spree-26223858840.json",
			expectedParallelRunners: 3,
		},
		{
			name:                    "spree measure 2 high TIA savings should reduce fanout",
			fixturePath:             "spree-26224156491.json",
			expectedParallelRunners: 3,
		},
		{
			name:                    "spree measure 3 high TIA savings should reduce fanout",
			fixturePath:             "spree-26224387824.json",
			expectedParallelRunners: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := loadSplitSelectionFixture(t, tt.fixturePath)
			if fixture.CurrentParallelRunners == tt.expectedParallelRunners {
				t.Fatalf("fixture does not capture a pre-fix over-fanout regression")
			}

			skippablePercentage := calculateSavedTimePercentage(fixture.SuiteAggregates)
			if math.Abs(skippablePercentage-fixture.SkippablePercentage) > 0.01 {
				t.Fatalf(
					"fixture skippable percentage = %.4f, expected %.2f from artifact",
					skippablePercentage,
					fixture.SkippablePercentage,
				)
			}

			result := calculateParallelRunnerSplit(fixture.TestFileWeights, 1, 8, testParallelRunnerOverhead, 0)
			if result.parallelRunners != tt.expectedParallelRunners {
				t.Fatalf(
					"calculateParallelRunnerSplit() = %d runners, expected %d; artifact selected %d before this fix",
					result.parallelRunners,
					tt.expectedParallelRunners,
					fixture.CurrentParallelRunners,
				)
			}
		})
	}
}

func TestCalculateParallelRunnerSplit_ParallelRunnerOverheadTunesFanout(t *testing.T) {
	fixture := loadSplitSelectionFixture(t, "spree-26223858840.json")

	tests := []struct {
		name                    string
		parallelRunnerOverhead  time.Duration
		expectedParallelRunners int
	}{
		{
			name:                    "lower overhead prefers faster wall time",
			parallelRunnerOverhead:  20 * time.Second,
			expectedParallelRunners: 4,
		},
		{
			name:                    "higher overhead prefers fewer CI jobs",
			parallelRunnerOverhead:  testParallelRunnerOverhead,
			expectedParallelRunners: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calculateParallelRunnerSplit(fixture.TestFileWeights, 1, 8, tt.parallelRunnerOverhead, 0)
			if result.parallelRunners != tt.expectedParallelRunners {
				t.Errorf("calculateParallelRunnerSplit() = %d runners, expected %d", result.parallelRunners, tt.expectedParallelRunners)
			}
		})
	}
}

func TestCalculateParallelRunnerSplit_TargetTimeFiltersCandidateSplits(t *testing.T) {
	testFileWeights := map[string]int{
		"test1.rb": 60_000,
		"test2.rb": 60_000,
		"test3.rb": 60_000,
		"test4.rb": 60_000,
	}
	minParallelism := 1
	maxParallelism := 4
	parallelRunnerOverhead := 5 * time.Minute
	targetTime := 2 * time.Minute

	result := calculateParallelRunnerSplit(testFileWeights, minParallelism, maxParallelism, parallelRunnerOverhead, targetTime)
	if result.parallelRunners != 2 {
		t.Errorf("calculateParallelRunnerSplit() = %d runners, expected 2 to satisfy target time with the lowest selection score", result.parallelRunners)
	}
	if result.wallTimeDuration() > targetTime {
		t.Errorf("calculateParallelRunnerSplit() wall time = %s, expected at or below %s", result.wallTimeDuration(), targetTime)
	}
}

func TestCalculateParallelRunnerSplit_TargetTimeImpossibleWarnsAndSelectsLowestWallTime(t *testing.T) {
	logs := captureLogs(t)
	testFileWeights := map[string]int{
		"test1.rb": 60_000,
		"test2.rb": 60_000,
		"test3.rb": 60_000,
		"test4.rb": 60_000,
	}
	minParallelism := 1
	maxParallelism := 4
	parallelRunnerOverhead := 5 * time.Minute
	targetTime := 59 * time.Second

	result := calculateParallelRunnerSplitSelection(testFileWeights, minParallelism, maxParallelism, parallelRunnerOverhead, targetTime)
	if result.selected.parallelRunners != 4 {
		t.Errorf("calculateParallelRunnerSplit() = %d runners, expected 4 from fallback lowest wall time split", result.selected.parallelRunners)
	}
	if result.selected.wallTimeDuration() != time.Minute {
		t.Errorf("calculateParallelRunnerSplit() wall time = %s, expected 1m0s", result.selected.wallTimeDuration())
	}
	if result.bestWithoutTarget.parallelRunners != 1 {
		t.Errorf("calculateParallelRunnerSplit() without target = %d runners, expected 1 from best selection score", result.bestWithoutTarget.parallelRunners)
	}

	logOutput := logs.String()
	if !strings.Contains(logOutput, "No parallel runner split meets target time") ||
		!strings.Contains(logOutput, "targetTime=59s") ||
		!strings.Contains(logOutput, "selectedExpectedWallTime=1m0s") {
		t.Errorf("expected target-time warning with fallback details, got logs: %s", logOutput)
	}
}

type splitSelectionFixture struct {
	SkippablePercentage    float64                             `json:"skippablePercentage"`
	CurrentParallelRunners int                                 `json:"currentParallelRunners"`
	TestFileWeights        map[string]int                      `json:"testFileWeights"`
	SuiteAggregates        map[testSuiteKey]testSuiteAggregate `json:"suiteAggregates"`
}

func loadSplitSelectionFixture(t *testing.T, name string) splitSelectionFixture {
	t.Helper()

	path := filepath.Join("testdata", "split_selection", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read split selection fixture %s: %v", path, err)
	}

	var fixture splitSelectionFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("failed to unmarshal split selection fixture %s: %v", path, err)
	}

	return fixture
}

func TestCalculateParallelRunnerSplit_LogsCandidateSplits(t *testing.T) {
	logs := captureLogs(t)
	testFileWeights := map[string]int{
		"test1.rb": 10,
		"test2.rb": 10,
		"test3.rb": 10,
	}

	_ = calculateParallelRunnerSplit(testFileWeights, 1, 3, testParallelRunnerOverhead, 0)

	logOutput := logs.String()
	if strings.Count(logOutput, "Considered parallel runner split") != 3 ||
		!strings.Contains(logOutput, "parallelRunners=1") ||
		!strings.Contains(logOutput, "parallelRunners=2") ||
		!strings.Contains(logOutput, "parallelRunners=3") ||
		!strings.Contains(logOutput, "expectedWallTime=") ||
		!strings.Contains(logOutput, "imbalance=") ||
		!strings.Contains(logOutput, "expectedTotalRuntime=") {
		t.Errorf("Expected DEBUG logs for each candidate split with score fields, got logs: %s", logOutput)
	}
}

func BenchmarkCalculateParallelRunners20000TestFiles(b *testing.B) {
	testFileWeights := make(map[string]int, 20000)
	for i := range 20000 {
		testFileWeights[fmt.Sprintf("test/%05d_test.rb", i)] = (i % 1000) + 1
	}

	b.ResetTimer()
	for range b.N {
		_ = calculateParallelRunnerSplit(testFileWeights, 1, 256, testParallelRunnerOverhead, 0)
	}
}
