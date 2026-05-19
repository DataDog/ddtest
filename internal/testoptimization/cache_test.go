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

func newTestSuiteDurationsCacheFixture(sourceFile string, weight int) testSuiteDurationsCacheFixture {
	return testSuiteDurationsCacheFixture{
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

func TestCacheManager_StoreAndReadTestSuiteDurationsCache(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	cache := newTestSuiteDurationsCacheFixture("spec/suite1_spec.rb", 2500)

	cacheManager := NewCacheManager()
	if err := cacheManager.StoreTestSuiteDurationsCache(cache); err != nil {
		t.Fatalf("StoreTestSuiteDurationsCache() should not return error, got: %v", err)
	}

	runnerCachePath := filepath.Join(appConstants.RunnerCacheDir, TestSuiteDurationsCacheFile)
	if _, err := os.Stat(runnerCachePath); err != nil {
		t.Fatalf("Expected runner test suite durations cache file to be written: %v", err)
	}

	legacyCachePath := filepath.Join(appConstants.CacheDir, TestSuiteDurationsCacheFile)
	if _, err := os.Stat(legacyCachePath); !os.IsNotExist(err) {
		t.Fatalf("Expected test suite durations cache to stay out of legacy cache dir, got error: %v", err)
	}

	httpCachePath := filepath.Join(appConstants.HTTPCacheDir, TestSuiteDurationsCacheFile)
	if _, err := os.Stat(httpCachePath); !os.IsNotExist(err) {
		t.Fatalf("Expected test suite durations cache to stay out of cache/http, got error: %v", err)
	}

	var restored testSuiteDurationsCacheFixture
	if err := cacheManager.ReadTestSuiteDurationsCache(&restored); err != nil {
		t.Fatalf("ReadTestSuiteDurationsCache() should not return error, got: %v", err)
	}

	if !reflect.DeepEqual(restored, cache) {
		t.Errorf("Expected restored cache to match stored cache.\nexpected: %v\nactual: %v", cache, restored)
	}
}

func TestCacheManager_ReadTestSuiteDurationsCache_DoesNotReadLegacyCache(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	cacheManager := NewCacheManager()
	if err := cacheManager.CreateCacheDirectory(); err != nil {
		t.Fatalf("CreateCacheDirectory() should not return error, got: %v", err)
	}

	legacyCache := newTestSuiteDurationsCacheFixture("spec/legacy_spec.rb", 1000)
	legacyCachePath := filepath.Join(appConstants.CacheDir, TestSuiteDurationsCacheFile)
	if err := cacheManager.writeJSONToFile(legacyCache, legacyCachePath); err != nil {
		t.Fatalf("writeJSONToFile() should not return error for legacy cache, got: %v", err)
	}

	var restored testSuiteDurationsCacheFixture
	if err := cacheManager.ReadTestSuiteDurationsCache(&restored); err == nil {
		t.Fatal("ReadTestSuiteDurationsCache() should not read the legacy cache path")
	}
}
