package runner

import "github.com/DataDog/ddtest/internal/testoptimization"

type testSuiteDurationsCache struct {
	TestSuiteDurations map[string]map[string]testoptimization.TestSuiteDurationInfo `json:"testSuiteDurations"`
	SuiteAggregates    map[testSuiteKey]testSuiteAggregate                          `json:"suiteAggregates"`
	SuitesBySourceFile map[string][]testSuiteKey                                    `json:"suitesBySourceFile"`
}

func (tr *TestRunner) storeTestSuiteDurationsCache() error {
	cache := testSuiteDurationsCache{
		TestSuiteDurations: tr.testSuiteDurations,
		SuiteAggregates:    tr.suiteAggregates,
		SuitesBySourceFile: tr.suitesBySourceFile,
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

	return nil
}
