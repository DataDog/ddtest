package testoptimization

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	appConstants "github.com/DataDog/ddtest/internal/constants"
)

type testSuiteDurationsCacheFixture struct {
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

func TestCacheManager_StoreAndReadTestSuiteDurationsCache(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	cache := testSuiteDurationsCacheFixture{
		TestSuiteDurations: map[string]map[string]TestSuiteDurationInfo{
			"rspec": {
				"Suite1": {
					SourceFile: "spec/suite1_spec.rb",
					Duration:   DurationPercentiles{P50: "5000000000", P90: "7000000000"},
				},
			},
		},
		SuiteAggregates: []testSuiteAggregateCacheFixture{
			{
				Module:            "rspec",
				Suite:             "Suite1",
				SourceFile:        "spec/suite1_spec.rb",
				TotalDuration:     5000000000,
				EstimatedDuration: 2500000000,
				NumTests:          2,
				NumTestsSkipped:   1,
			},
		},
		SuitesBySourceFile: map[string][]testSuiteCacheKeyFixture{
			"spec/suite1_spec.rb": {{Module: "rspec", Suite: "Suite1"}},
		},
		TestFileWeights: map[string]int{
			"spec/suite1_spec.rb": 2500,
		},
	}

	cacheManager := NewCacheManager()
	if err := cacheManager.StoreTestSuiteDurationsCache(cache); err != nil {
		t.Fatalf("StoreTestSuiteDurationsCache() should not return error, got: %v", err)
	}

	cachePath := filepath.Join(appConstants.PlanDirectory, "cache", TestSuiteDurationsCacheFile)
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("Expected test suite durations cache file to be written: %v", err)
	}

	var restored testSuiteDurationsCacheFixture
	if err := cacheManager.ReadTestSuiteDurationsCache(&restored); err != nil {
		t.Fatalf("ReadTestSuiteDurationsCache() should not return error, got: %v", err)
	}

	if !reflect.DeepEqual(restored, cache) {
		t.Errorf("Expected restored cache to match stored cache.\nexpected: %v\nactual: %v", cache, restored)
	}
}
