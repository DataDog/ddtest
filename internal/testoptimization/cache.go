package testoptimization

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/DataDog/ddtest/internal/constants"
)

const (
	httpSettingsCacheFile       = "settings.json"
	httpKnownTestsCacheFile     = "known_tests.json"
	httpSkippableTestsCacheFile = "skippable_tests.json"
	httpTestManagementCacheFile = "test_management.json"
)

// CacheManager handles creation and storage of cache data for test runners
type CacheManager struct{}

// NewCacheManager creates a new CacheManager instance
func NewCacheManager() *CacheManager {
	return &CacheManager{}
}

func (cm *CacheManager) createHTTPCacheDirectory() error {
	return os.MkdirAll(constants.HTTPCacheDir, 0755)
}

func (cm *CacheManager) createRunnerCacheDirectory() error {
	return os.MkdirAll(constants.RunnerCacheDir, 0755)
}

// writeJSONToFile writes data as JSON to the specified file path
func (cm *CacheManager) writeJSONToFile(data any, filePath string) error {
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	if err := os.WriteFile(filePath, jsonData, 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

func (cm *CacheManager) writeJSONBytesToFile(data json.RawMessage, filePath string) error {
	if len(data) == 0 {
		return nil
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

// readJSONFromFile reads JSON data from the specified file path into data.
func (cm *CacheManager) readJSONFromFile(filePath string, data any) error {
	jsonData, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	if err := json.Unmarshal(jsonData, data); err != nil {
		return fmt.Errorf("failed to unmarshal JSON: %w", err)
	}

	return nil
}

func (cm *CacheManager) storeHTTPResponse(data json.RawMessage, fileName string) error {
	if len(data) == 0 {
		return nil
	}
	if err := cm.createHTTPCacheDirectory(); err != nil {
		return fmt.Errorf("failed to create HTTP cache directory: %w", err)
	}

	path := filepath.Join(constants.HTTPCacheDir, fileName)
	if err := cm.writeJSONBytesToFile(data, path); err != nil {
		slog.Error("Failed to write backend response to file", "error", err, "path", path)
		return err
	}

	slog.Debug("Backend response written to file", "path", path)
	return nil
}

func (cm *CacheManager) StoreRepositorySettings(data json.RawMessage) error {
	return cm.storeHTTPResponse(data, httpSettingsCacheFile)
}

func (cm *CacheManager) StoreKnownTestsCache(data json.RawMessage) error {
	return cm.storeHTTPResponse(data, httpKnownTestsCacheFile)
}

func (cm *CacheManager) StoreSkippableTestsCache(data json.RawMessage) error {
	return cm.storeHTTPResponse(data, httpSkippableTestsCacheFile)
}

func (cm *CacheManager) StoreTestManagementTestsCache(data json.RawMessage) error {
	return cm.storeHTTPResponse(data, httpTestManagementCacheFile)
}

// StoreTestOptimizationPlanCache stores ddtest-private plan data in the runner cache.
func (cm *CacheManager) StoreTestOptimizationPlanCache(cache any) error {
	if err := cm.createRunnerCacheDirectory(); err != nil {
		return fmt.Errorf("failed to create runner cache directory: %w", err)
	}

	runnerPath := filepath.Join(constants.RunnerCacheDir, constants.TestOptimizationPlanCacheFile)
	if err := cm.writeJSONToFile(cache, runnerPath); err != nil {
		slog.Error("Failed to write test optimization plan to file", "error", err, "path", runnerPath)
		return err
	}

	slog.Debug("Test optimization plan written to file", "path", runnerPath)
	return nil
}

// ReadTestOptimizationPlanCache reads ddtest-private plan data from the runner cache.
func (cm *CacheManager) ReadTestOptimizationPlanCache(cache any) error {
	runnerPath := filepath.Join(constants.RunnerCacheDir, constants.TestOptimizationPlanCacheFile)
	if err := cm.readJSONFromFile(runnerPath, cache); err != nil {
		return err
	}

	slog.Debug("Test optimization plan read from file", "path", runnerPath)
	return nil
}
