package planner

import (
	"log/slog"

	"github.com/DataDog/ddtest/internal/runmetadata"
	"github.com/DataDog/ddtest/internal/testoptimization"
	"github.com/DataDog/ddtest/internal/testoptimization/api"
)

type testOptimizationPlanCache struct {
	TestSuiteDurations      map[string]map[string]api.TestSuiteDurationInfo `json:"testSuiteDurations"`
	SuiteAggregates         map[testSuiteKey]testSuiteAggregate             `json:"suiteAggregates"`
	SuitesBySourceFile      map[string][]testSuiteKey                       `json:"suitesBySourceFile"`
	TestFileWeights         map[string]int                                  `json:"testFileWeights"`
	TestFileDurationSources map[string]testFileDurationSource               `json:"testFileDurationSources"`
	RunInfo                 runmetadata.RunInfo                             `json:"runInfo"`
	PlanMetadata            PlanMetadata                                    `json:"planMetadata"`
}

func (tp *TestPlanner) storeTestOptimizationPlanCache() error {
	cache := testOptimizationPlanCache{
		TestSuiteDurations:      tp.testSuiteDurations,
		SuiteAggregates:         tp.suiteAggregates,
		SuitesBySourceFile:      tp.suitesBySourceFile,
		TestFileWeights:         tp.testFileWeights,
		TestFileDurationSources: tp.testFileDurationSources,
		RunInfo:                 tp.runInfo,
		PlanMetadata:            tp.planMetadata,
	}

	return testoptimization.NewCacheManager().StoreTestOptimizationPlanCache(cache)
}

func LoadPlan() (PlanMetadata, error) {
	return newTestPlannerWithDefaults().LoadPlan()
}

func (tp *TestPlanner) LoadPlan() (PlanMetadata, error) {
	if !tp.planLoaded {
		if err := tp.restoreTestOptimizationPlanCache(); err != nil {
			return PlanMetadata{}, err
		}
	}

	return tp.planMetadata, nil
}

func (tp *TestPlanner) restoreTestOptimizationPlanCache() error {
	var cache testOptimizationPlanCache
	if err := readAndNormalizeTestOptimizationPlanCache(&cache); err != nil {
		return err
	}

	tp.testSuiteDurations = cache.TestSuiteDurations
	tp.suiteAggregates = cache.SuiteAggregates
	tp.suitesBySourceFile = cache.SuitesBySourceFile
	tp.testFileWeights = cache.TestFileWeights
	tp.testFileDurationSources = cache.TestFileDurationSources
	tp.runInfo = cache.RunInfo
	tp.planMetadata = cache.PlanMetadata
	tp.planLoaded = true

	testSuitesCount := countTestSuites(tp.testSuiteDurations)
	suiteAggregatesCount := len(tp.suiteAggregates)
	suitesBySourceFileCount := len(tp.suitesBySourceFile)
	testFileWeightsCount := len(tp.testFileWeights)
	slog.Info("Restored test optimization plan cache",
		"objectsCount", testSuitesCount+suiteAggregatesCount+suitesBySourceFileCount+testFileWeightsCount,
		"modulesCount", len(tp.testSuiteDurations),
		"testSuitesCount", testSuitesCount,
		"suiteAggregatesCount", suiteAggregatesCount,
		"suitesBySourceFileCount", suitesBySourceFileCount,
		"testFileWeightsCount", testFileWeightsCount)

	return nil
}

func readAndNormalizeTestOptimizationPlanCache(cache *testOptimizationPlanCache) error {
	if err := testoptimization.NewCacheManager().ReadTestOptimizationPlanCache(cache); err != nil {
		return err
	}

	if cache.TestSuiteDurations == nil {
		cache.TestSuiteDurations = make(map[string]map[string]api.TestSuiteDurationInfo)
	}
	if cache.SuiteAggregates == nil {
		cache.SuiteAggregates = make(map[testSuiteKey]testSuiteAggregate)
	}
	if cache.SuitesBySourceFile == nil {
		cache.SuitesBySourceFile = indexSuitesBySourceFile(cache.SuiteAggregates)
	}
	if cache.TestFileWeights == nil {
		cache.TestFileWeights = testFileWeightsFromSuites(cache.SuiteAggregates, cache.SuitesBySourceFile)
	}
	if cache.TestFileDurationSources == nil {
		cache.TestFileDurationSources = make(map[string]testFileDurationSource)
	}
	return nil
}

func countTestSuites(testSuiteDurations map[string]map[string]api.TestSuiteDurationInfo) int {
	totalSuites := 0
	for _, suites := range testSuiteDurations {
		totalSuites += len(suites)
	}
	return totalSuites
}

func testFileWeightsFromSuites(
	suiteAggregates map[testSuiteKey]testSuiteAggregate,
	suitesBySourceFile map[string][]testSuiteKey,
) map[string]int {
	planner := newTestPlannerWithDefaults()
	planner.suiteAggregates = suiteAggregates
	planner.suitesBySourceFile = suitesBySourceFile
	testFiles := make(map[string]struct{}, len(suitesBySourceFile))
	for testFile := range suitesBySourceFile {
		testFiles[testFile] = struct{}{}
	}
	return planner.estimateTestFileWeights(testFiles)
}
