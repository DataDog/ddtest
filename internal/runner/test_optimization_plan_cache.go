package runner

import (
	"log/slog"

	"github.com/DataDog/ddtest/internal/testoptimization"
)

type testOptimizationPlanCache struct {
	TestSuiteDurations      map[string]map[string]testoptimization.TestSuiteDurationInfo `json:"testSuiteDurations"`
	SuiteAggregates         map[testSuiteKey]testSuiteAggregate                          `json:"suiteAggregates"`
	SuitesBySourceFile      map[string][]testSuiteKey                                    `json:"suitesBySourceFile"`
	TestFileWeights         map[string]int                                               `json:"testFileWeights"`
	TestFileDurationSources map[string]testFileDurationSource                            `json:"testFileDurationSources"`
	RunInfo                 runInfoReport                                                `json:"runInfo"`
}

func (tr *TestRunner) storeTestOptimizationPlanCache() error {
	cache := testOptimizationPlanCache{
		TestSuiteDurations:      tr.testSuiteDurations,
		SuiteAggregates:         tr.suiteAggregates,
		SuitesBySourceFile:      tr.suitesBySourceFile,
		TestFileWeights:         tr.testFileWeights,
		TestFileDurationSources: tr.testFileDurationSources,
		RunInfo:                 tr.runInfoReport,
	}

	return testoptimization.NewCacheManager().StoreTestOptimizationPlanCache(cache)
}

func (tr *TestRunner) restoreTestOptimizationPlanCache() error {
	var cache testOptimizationPlanCache
	if err := testoptimization.NewCacheManager().ReadTestOptimizationPlanCache(&cache); err != nil {
		return err
	}

	tr.testSuiteDurations = cache.TestSuiteDurations
	if tr.testSuiteDurations == nil {
		tr.testSuiteDurations = make(map[string]map[string]testoptimization.TestSuiteDurationInfo)
	}

	tr.suiteAggregates = cache.SuiteAggregates
	if tr.suiteAggregates == nil {
		tr.suiteAggregates = make(map[testSuiteKey]testSuiteAggregate)
	}

	tr.suitesBySourceFile = cache.SuitesBySourceFile
	if tr.suitesBySourceFile == nil {
		tr.suitesBySourceFile = indexSuitesBySourceFile(tr.suiteAggregates)
	}

	tr.testFileWeights = cache.TestFileWeights
	if tr.testFileWeights == nil {
		tr.testFileWeights = tr.testFileWeightsFromSuites()
	}

	tr.testFileDurationSources = cache.TestFileDurationSources
	if tr.testFileDurationSources == nil {
		tr.testFileDurationSources = make(map[string]testFileDurationSource)
	}

	tr.runInfoReport = cache.RunInfo

	testSuitesCount := countTestSuites(tr.testSuiteDurations)
	suiteAggregatesCount := len(tr.suiteAggregates)
	suitesBySourceFileCount := len(tr.suitesBySourceFile)
	testFileWeightsCount := len(tr.testFileWeights)
	slog.Info("Restored test optimization plan cache",
		"objectsCount", testSuitesCount+suiteAggregatesCount+suitesBySourceFileCount+testFileWeightsCount,
		"modulesCount", len(tr.testSuiteDurations),
		"testSuitesCount", testSuitesCount,
		"suiteAggregatesCount", suiteAggregatesCount,
		"suitesBySourceFileCount", suitesBySourceFileCount,
		"testFileWeightsCount", testFileWeightsCount)

	return nil
}

func countTestSuites(testSuiteDurations map[string]map[string]testoptimization.TestSuiteDurationInfo) int {
	totalSuites := 0
	for _, suites := range testSuiteDurations {
		totalSuites += len(suites)
	}
	return totalSuites
}

func (tr *TestRunner) testFileWeightsFromSuites() map[string]int {
	testFiles := make(map[string]struct{}, len(tr.suitesBySourceFile))
	for testFile := range tr.suitesBySourceFile {
		testFiles[testFile] = struct{}{}
	}
	return tr.estimateTestFileWeights(testFiles)
}
