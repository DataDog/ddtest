package testoptimization

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	appConstants "github.com/DataDog/ddtest/internal/constants"
)

type testOptimizationPlanCacheFixture struct {
	TestSuiteDurations map[string]map[string]TestSuiteDurationInfo `json:"testSuiteDurations"`
	SuiteAggregates    []testSuiteAggregateCacheFixture            `json:"suiteAggregates"`
	SuitesBySourceFile map[string][]testSuiteCacheKeyFixture       `json:"suitesBySourceFile"`
	TestFileWeights    map[string]int                              `json:"testFileWeights"`
}

type testSuiteCacheKeyFixture struct {
	Module string `json:"module"`
	Suite  string `json:"suite"`
}

type testSuiteAggregateCacheFixture struct {
	Module            string  `json:"module"`
	Suite             string  `json:"suite"`
	SourceFile        string  `json:"sourceFile"`
	TotalDuration     float64 `json:"totalDuration"`
	EstimatedDuration float64 `json:"estimatedDuration"`
	NumTests          int     `json:"numTests"`
	NumTestsSkipped   int     `json:"numTestsSkipped"`
}

func newTestOptimizationPlanCacheFixture(sourceFile string, weight int) testOptimizationPlanCacheFixture {
	return testOptimizationPlanCacheFixture{
		TestSuiteDurations: map[string]map[string]TestSuiteDurationInfo{
			"rspec": {
				"Suite1": {
					SourceFile: sourceFile,
					Duration:   DurationPercentiles{P50: "5000000000", P90: "7000000000"},
				},
			},
		},
		SuiteAggregates: []testSuiteAggregateCacheFixture{
			{
				Module:            "rspec",
				Suite:             "Suite1",
				SourceFile:        sourceFile,
				TotalDuration:     5000000000,
				EstimatedDuration: 2500000000,
				NumTests:          2,
				NumTestsSkipped:   1,
			},
		},
		SuitesBySourceFile: map[string][]testSuiteCacheKeyFixture{
			sourceFile: {{Module: "rspec", Suite: "Suite1"}},
		},
		TestFileWeights: map[string]int{
			sourceFile: weight,
		},
	}
}

func TestCacheManager_StoreAndReadTestOptimizationPlanCache(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	cache := newTestOptimizationPlanCacheFixture("spec/suite1_spec.rb", 2500)

	cacheManager := NewCacheManager()
	if err := cacheManager.StoreTestOptimizationPlanCache(cache); err != nil {
		t.Fatalf("StoreTestOptimizationPlanCache() should not return error, got: %v", err)
	}

	runnerCachePath := filepath.Join(appConstants.RunnerCacheDir, TestOptimizationPlanCacheFile)
	if _, err := os.Stat(runnerCachePath); err != nil {
		t.Fatalf("Expected runner test optimization plan cache file to be written: %v", err)
	}

	httpCachePath := filepath.Join(appConstants.HTTPCacheDir, TestOptimizationPlanCacheFile)
	if _, err := os.Stat(httpCachePath); !os.IsNotExist(err) {
		t.Fatalf("Expected test optimization plan cache to stay out of cache/http, got error: %v", err)
	}

	var restored testOptimizationPlanCacheFixture
	if err := cacheManager.ReadTestOptimizationPlanCache(&restored); err != nil {
		t.Fatalf("ReadTestOptimizationPlanCache() should not return error, got: %v", err)
	}

	if !reflect.DeepEqual(restored, cache) {
		t.Errorf("Expected restored cache to match stored cache.\nexpected: %v\nactual: %v", cache, restored)
	}
}
