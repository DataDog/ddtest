package runner

import (
	"log/slog"

	"github.com/DataDog/ddtest/internal/testoptimization"
)

type testSuiteDurationsCache struct {
	TestSuiteDurations map[string]map[string]testoptimization.TestSuiteDurationInfo `json:"testSuiteDurations"`
	SuiteAggregates    map[testSuiteKey]testSuiteAggregate                          `json:"suiteAggregates"`
	SuitesBySourceFile map[string][]testSuiteKey                                    `json:"suitesBySourceFile"`
	TestFileWeights    map[string]int                                               `json:"testFileWeights"`
}

func (tr *TestRunner) storeTestSuiteDurationsCache() error {
	cache := testSuiteDurationsCache{
		TestSuiteDurations: tr.testSuiteDurations,
		SuiteAggregates:    tr.suiteAggregates,
		SuitesBySourceFile: tr.suitesBySourceFile,
		TestFileWeights:    tr.testFileWeights,
	}

	return testoptimization.NewCacheManager().StoreTestSuiteDurationsCache(cache)
}

func (tr *TestRunner) restoreTestSuiteDurationsCache() error {
	var cache testSuiteDurationsCache
	if err := testoptimization.NewCacheManager().ReadTestSuiteDurationsCache(&cache); err != nil {
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

	testSuitesCount := countTestSuites(tr.testSuiteDurations)
	suiteAggregatesCount := len(tr.suiteAggregates)
	suitesBySourceFileCount := len(tr.suitesBySourceFile)
	testFileWeightsCount := len(tr.testFileWeights)
	slog.Info("Restored test suite durations cache",
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
	testFileWeights := make(map[string]int, len(tr.suitesBySourceFile))
	for testFile := range tr.suitesBySourceFile {
		weight, ok := tr.testFileWeight(testFile)
		if ok {
			testFileWeights[testFile] = weight
		}
	}
	return testFileWeights
}
