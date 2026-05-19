package runner

import (
	"slices"
	"testing"
)

func TestSortedWeightedTestFiles(t *testing.T) {
	testFiles := map[string]int{
		"small.rb":  1,
		"same-b.rb": 5,
		"large.rb":  10,
		"same-a.rb": 5,
	}

	result := sortedWeightedTestFiles(testFiles)
	expected := []weightedTestFile{
		{path: "large.rb", weight: 10},
		{path: "same-a.rb", weight: 5},
		{path: "same-b.rb", weight: 5},
		{path: "small.rb", weight: 1},
	}

	if !slices.Equal(result, expected) {
		t.Fatalf("sortedWeightedTestFiles() = %v, expected %v", result, expected)
	}
}

func TestScoreSortedWeightedRunnerSplit(t *testing.T) {
	files := []weightedTestFile{
		{path: "slow.rb", weight: 10},
		{path: "medium.rb", weight: 6},
		{path: "fast.rb", weight: 4},
		{path: "tiny.rb", weight: 2},
	}

	result := scoreSortedWeightedRunnerSplit(files, 2)
	expected := splitScore{
		parallelRunners: 2,
		wallTime:        12,
		imbalance:       2,
	}

	if result != expected {
		t.Fatalf("scoreSortedWeightedRunnerSplit() = %+v, expected %+v", result, expected)
	}
}

func TestScoreSortedWeightedRunnerSplit_UnavoidableEmptyRunners(t *testing.T) {
	files := []weightedTestFile{
		{path: "slow.rb", weight: 10},
	}

	result := scoreSortedWeightedRunnerSplit(files, 3)
	expected := splitScore{
		parallelRunners: 3,
		wallTime:        10,
		imbalance:       10,
	}

	if result != expected {
		t.Fatalf("scoreSortedWeightedRunnerSplit() = %+v, expected %+v", result, expected)
	}
}

func TestDistributeWeightedTestFiles(t *testing.T) {
	testFiles := map[string]int{
		"fast.rb":   1,
		"medium.rb": 2,
		"slow.rb":   3,
	}

	result := distributeWeightedTestFiles(testFiles, 2)
	expected := [][]string{
		{"slow.rb"},
		{"medium.rb", "fast.rb"},
	}

	assertDistribution(t, result, expected)
}

func TestDistributeSortedWeightedTestFiles(t *testing.T) {
	files := []weightedTestFile{
		{path: "slow.rb", weight: 3},
		{path: "medium.rb", weight: 2},
		{path: "fast.rb", weight: 1},
	}

	result := distributeSortedWeightedTestFiles(files, 2)
	expected := [][]string{
		{"slow.rb"},
		{"medium.rb", "fast.rb"},
	}

	assertDistribution(t, result, expected)
}

func assertDistribution(t *testing.T, result, expected [][]string) {
	t.Helper()

	if len(result) != len(expected) {
		t.Fatalf("distribution has %d runners, expected %d", len(result), len(expected))
	}

	for i := range expected {
		if !slices.Equal(result[i], expected[i]) {
			t.Errorf("runner %d = %v, expected %v", i, result[i], expected[i])
		}
	}
}
