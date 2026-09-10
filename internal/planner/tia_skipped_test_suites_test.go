package planner

import (
	"slices"
	"testing"
)

func TestDistributeTIASkippedTestSuitesBalancesReportedSuiteCounts(t *testing.T) {
	runnableDistribution := [][]string{
		{"run-a.js", "run-b.js"},
		{"run-c.js"},
		{},
		{},
	}
	testSuites := []string{"skip-a.js", "skip-b.js", "skip-c.js", "skip-d.js", "skip-e.js"}

	distribution := distributeTIASkippedTestSuites(runnableDistribution, testSuites)
	want := [][]string{
		{},
		{"skip-c.js"},
		{"skip-a.js", "skip-d.js"},
		{"skip-b.js", "skip-e.js"},
	}

	if len(distribution) != len(want) {
		t.Fatalf("distribution has %d runners, want %d", len(distribution), len(want))
	}
	for runnerIndex := range want {
		if !slices.Equal(distribution[runnerIndex], want[runnerIndex]) {
			t.Errorf("runner %d skipped suites = %v, want %v", runnerIndex, distribution[runnerIndex], want[runnerIndex])
		}
	}
}
