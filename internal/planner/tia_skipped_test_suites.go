package planner

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/DataDog/ddtest/internal/constants"
)

const tiaSkippedTestSuitesArtifactVersion = 1

type tiaSkippedTestSuitesArtifact struct {
	Version    int      `json:"version"`
	TestSuites []string `json:"test_suites"`
}

func (tp *TestPlanner) writeTIASkippedTestSuiteSplits(parallelRunners int) error {
	runnableDistribution := tp.DistributeWeightedTestFiles(tp.testFileWeights, parallelRunners)
	skippedDistribution := distributeTIASkippedTestSuites(
		runnableDistribution,
		tp.fullySkippedJestTIASuites(),
	)

	for runnerIndex, testSuites := range skippedDistribution {
		payload, err := json.Marshal(tiaSkippedTestSuitesArtifact{
			Version:    tiaSkippedTestSuitesArtifactVersion,
			TestSuites: testSuites,
		})
		if err != nil {
			return fmt.Errorf("failed to encode TIA-skipped test suites for runner-%d: %w", runnerIndex, err)
		}
		payload = append(payload, '\n')

		artifactPath := filepath.Join(constants.TIASkippedTestSuitesDir, fmt.Sprintf("runner-%d.json", runnerIndex))
		if err := writePlanFile(artifactPath, payload); err != nil {
			return fmt.Errorf("failed to write TIA-skipped test suites for runner-%d: %w", runnerIndex, err)
		}
	}

	return nil
}

func (tp *TestPlanner) fullySkippedJestTIASuites() []string {
	if tp.planMetadata.Platform != "javascript" || tp.planMetadata.Framework != "jest" {
		return nil
	}

	uniqueSuites := make(map[string]struct{})
	for key := range tp.reportStats.uniqueTIASkippableSuitesApplied {
		if key.Suite == "" {
			continue
		}
		aggregate, ok := tp.suiteAggregates[key]
		if !ok || aggregate.SourceFile == "" || aggregate.NumTests == 0 || aggregate.NumTestsSkipped != aggregate.NumTests {
			continue
		}
		if _, runnable := tp.testFileWeights[aggregate.SourceFile]; runnable {
			continue
		}
		uniqueSuites[key.Suite] = struct{}{}
	}

	testSuites := make([]string, 0, len(uniqueSuites))
	for testSuite := range uniqueSuites {
		testSuites = append(testSuites, testSuite)
	}
	slices.Sort(testSuites)
	return testSuites
}

func distributeTIASkippedTestSuites(runnableDistribution [][]string, testSuites []string) [][]string {
	distribution := make([][]string, len(runnableDistribution))
	runnerSuiteCounts := make([]int, len(runnableDistribution))
	for runnerIndex, runnableTestFiles := range runnableDistribution {
		distribution[runnerIndex] = []string{}
		runnerSuiteCounts[runnerIndex] = len(runnableTestFiles)
	}

	for _, testSuite := range testSuites {
		lightestRunner := 0
		for runnerIndex := 1; runnerIndex < len(runnerSuiteCounts); runnerIndex++ {
			if runnerSuiteCounts[runnerIndex] < runnerSuiteCounts[lightestRunner] {
				lightestRunner = runnerIndex
			}
		}
		distribution[lightestRunner] = append(distribution[lightestRunner], testSuite)
		runnerSuiteCounts[lightestRunner]++
	}

	return distribution
}
