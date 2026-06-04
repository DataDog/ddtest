package planner

import (
	"encoding/json"
	"log/slog"

	"github.com/DataDog/ddtest/internal/runmetadata"
	"github.com/DataDog/ddtest/internal/testoptimization"
)

type testOptimizationPlanCache struct {
	TestSuiteDurations      map[string]map[string]testoptimization.TestSuiteDurationInfo `json:"testSuiteDurations"`
	SuiteAggregates         map[testSuiteKey]testSuiteAggregate                          `json:"suiteAggregates"`
	SuitesBySourceFile      map[string][]testSuiteKey                                    `json:"suitesBySourceFile"`
	TestFileWeights         map[string]int                                               `json:"testFileWeights"`
	TestFileDurationSources map[string]testFileDurationSource                            `json:"testFileDurationSources"`
	RunInfo                 runmetadata.RunInfo                                          `json:"runInfo"`
	PlanInfo                PlanInfo                                                     `json:"planInfo"`
}

func (tp *TestPlanner) storeTestOptimizationPlanCache() error {
	cache := testOptimizationPlanCache{
		TestSuiteDurations:      tp.testSuiteDurations,
		SuiteAggregates:         tp.suiteAggregates,
		SuitesBySourceFile:      tp.suitesBySourceFile,
		TestFileWeights:         tp.testFileWeights,
		TestFileDurationSources: tp.testFileDurationSources,
		RunInfo:                 tp.runInfo,
		PlanInfo:                tp.planInfo,
	}

	return testoptimization.NewCacheManager().StoreTestOptimizationPlanCache(cache)
}

func LoadPlan() (PlanInfo, error) {
	return newTestPlannerWithDefaults().LoadPlan()
}

func (tp *TestPlanner) LoadPlan() (PlanInfo, error) {
	if !tp.planLoaded {
		if err := tp.restoreTestOptimizationPlanCache(); err != nil {
			return PlanInfo{}, err
		}
	}

	return tp.planInfo, nil
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
	tp.planInfo = cache.PlanInfo
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

type legacyRunInfo struct {
	Service     string            `json:"service"`
	Repository  string            `json:"repository"`
	Commit      string            `json:"commit"`
	Branch      string            `json:"branch"`
	Platform    string            `json:"platform"`
	Framework   string            `json:"framework"`
	OSTags      map[string]string `json:"osTags"`
	RuntimeTags map[string]string `json:"runtimeTags"`
}

func (c *testOptimizationPlanCache) UnmarshalJSON(data []byte) error {
	var decoded struct {
		TestSuiteDurations      map[string]map[string]testoptimization.TestSuiteDurationInfo `json:"testSuiteDurations"`
		SuiteAggregates         map[testSuiteKey]testSuiteAggregate                          `json:"suiteAggregates"`
		SuitesBySourceFile      map[string][]testSuiteKey                                    `json:"suitesBySourceFile"`
		TestFileWeights         map[string]int                                               `json:"testFileWeights"`
		TestFileDurationSources map[string]testFileDurationSource                            `json:"testFileDurationSources"`
		RunInfo                 legacyRunInfo                                                `json:"runInfo"`
		PlanInfo                PlanInfo                                                     `json:"planInfo"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}

	c.TestSuiteDurations = decoded.TestSuiteDurations
	c.SuiteAggregates = decoded.SuiteAggregates
	c.SuitesBySourceFile = decoded.SuitesBySourceFile
	c.TestFileWeights = decoded.TestFileWeights
	c.TestFileDurationSources = decoded.TestFileDurationSources
	c.RunInfo = decoded.RunInfo.runInfo()
	c.PlanInfo = decoded.PlanInfo
	if c.PlanInfo.IsZero() {
		c.PlanInfo = decoded.RunInfo.planInfo()
	}

	return nil
}

func (r legacyRunInfo) runInfo() runmetadata.RunInfo {
	return runmetadata.RunInfo{
		Service:    r.Service,
		Repository: r.Repository,
		Commit:     r.Commit,
		Branch:     r.Branch,
	}
}

func (r legacyRunInfo) planInfo() PlanInfo {
	return PlanInfo{
		Platform:    r.Platform,
		Framework:   r.Framework,
		OSTags:      r.OSTags,
		RuntimeTags: r.RuntimeTags,
	}
}

func readAndNormalizeTestOptimizationPlanCache(cache *testOptimizationPlanCache) error {
	if err := testoptimization.NewCacheManager().ReadTestOptimizationPlanCache(cache); err != nil {
		return err
	}

	if cache.TestSuiteDurations == nil {
		cache.TestSuiteDurations = make(map[string]map[string]testoptimization.TestSuiteDurationInfo)
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

func countTestSuites(testSuiteDurations map[string]map[string]testoptimization.TestSuiteDurationInfo) int {
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
