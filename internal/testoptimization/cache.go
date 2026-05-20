package testoptimization

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/DataDog/ddtest/civisibility/constants"
	"github.com/DataDog/ddtest/civisibility/utils"
	"github.com/DataDog/ddtest/civisibility/utils/net"
	appConstants "github.com/DataDog/ddtest/internal/constants"
)

// SkippableTestsCache represents the structure for storing skippable tests with correlation ID
type SkippableTestsCache struct {
	CorrelationID  string                                                      `json:"correlationId"`
	SkippableTests map[string]map[string][]net.SkippableResponseDataAttributes `json:"skippableTests"`
}

// TestOptimizationPlanCacheFile keeps the historical filename used for plan/run handoff data.
const TestOptimizationPlanCacheFile = "test_suite_durations.json"

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

// CreateCacheDirectory creates the .testoptimization/cache directory for storing cache data
func (cm *CacheManager) CreateCacheDirectory() error {
	return os.MkdirAll(appConstants.CacheDir, 0755)
}

func (cm *CacheManager) createHTTPCacheDirectory() error {
	return os.MkdirAll(appConstants.HTTPCacheDir, 0755)
}

func (cm *CacheManager) createRunnerCacheDirectory() error {
	return os.MkdirAll(appConstants.RunnerCacheDir, 0755)
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

func (cm *CacheManager) writeRawJSONToFile(data json.RawMessage, filePath string) error {
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

func (cm *CacheManager) storeRawHTTPResponse(data json.RawMessage, fileName string) error {
	if len(data) == 0 {
		return nil
	}
	if err := cm.createHTTPCacheDirectory(); err != nil {
		return fmt.Errorf("failed to create HTTP cache directory: %w", err)
	}

	path := filepath.Join(appConstants.HTTPCacheDir, fileName)
	if err := cm.writeRawJSONToFile(data, path); err != nil {
		slog.Error("Failed to write raw backend response to file", "error", err, "path", path)
		return err
	}

	slog.Debug("Raw backend response written to file", "path", path)
	return nil
}

func (cm *CacheManager) StoreRawRepositorySettings(data json.RawMessage) error {
	return cm.storeRawHTTPResponse(data, httpSettingsCacheFile)
}

func (cm *CacheManager) StoreRawKnownTestsCache(data json.RawMessage) error {
	return cm.storeRawHTTPResponse(data, httpKnownTestsCacheFile)
}

func (cm *CacheManager) StoreRawSkippableTestsCache(data json.RawMessage) error {
	return cm.storeRawHTTPResponse(data, httpSkippableTestsCacheFile)
}

func (cm *CacheManager) StoreRawTestManagementTestsCache(data json.RawMessage) error {
	return cm.storeRawHTTPResponse(data, httpTestManagementCacheFile)
}

// StoreRepositorySettings stores repository settings in .testoptimization/cache/settings.json
func (cm *CacheManager) StoreRepositorySettings(repositorySettings *net.SettingsResponseData) error {
	if err := cm.CreateCacheDirectory(); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}

	settingsPath := filepath.Join(appConstants.CacheDir, "settings.json")
	if err := cm.writeJSONToFile(repositorySettings, settingsPath); err != nil {
		slog.Error("Failed to write repository settings to file", "error", err, "path", settingsPath)
		return err
	}

	slog.Debug("Repository settings written to file", "path", settingsPath)
	return nil
}

// StoreSkippableTestsCache stores skippable tests with correlation ID in .testoptimization/cache/skippable_tests.json
func (cm *CacheManager) StoreSkippableTestsCache(skippableTests map[string]map[string][]net.SkippableResponseDataAttributes) error {
	if err := cm.CreateCacheDirectory(); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}

	// Extract correlation ID from CI tags
	ciTags := utils.GetCITags()
	correlationID := ciTags[constants.ItrCorrelationIDTag]

	// Create skippable tests cache
	skippableTestsCache := SkippableTestsCache{
		CorrelationID:  correlationID,
		SkippableTests: skippableTests,
	}

	skippableTestsPath := filepath.Join(appConstants.CacheDir, "skippable_tests.json")
	if err := cm.writeJSONToFile(skippableTestsCache, skippableTestsPath); err != nil {
		slog.Error("Failed to write skippable tests to file", "error", err, "path", skippableTestsPath)
		return err
	}

	slog.Debug("Skippable tests written to file", "path", skippableTestsPath, "correlationId", correlationID)
	return nil
}

// StoreKnownTestsCache stores known tests in .testoptimization/cache/known_tests.json
func (cm *CacheManager) StoreKnownTestsCache(knownTests *net.KnownTestsResponseData) error {
	if err := cm.CreateCacheDirectory(); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}

	knownTestsPath := filepath.Join(appConstants.CacheDir, "known_tests.json")
	if err := cm.writeJSONToFile(knownTests, knownTestsPath); err != nil {
		slog.Error("Failed to write known tests to file", "error", err, "path", knownTestsPath)
		return err
	}

	slog.Debug("Known tests written to file", "path", knownTestsPath)
	return nil
}

// StoreTestManagementTestsCache stores test management tests in .testoptimization/cache/test_management_tests.json
func (cm *CacheManager) StoreTestManagementTestsCache(testManagementTests *net.TestManagementTestsResponseDataModules) error {
	if err := cm.CreateCacheDirectory(); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}

	testManagementTestsPath := filepath.Join(appConstants.CacheDir, "test_management_tests.json")
	if err := cm.writeJSONToFile(testManagementTests, testManagementTestsPath); err != nil {
		slog.Error("Failed to write test management tests to file", "error", err, "path", testManagementTestsPath)
		return err
	}

	slog.Debug("Test management tests written to file", "path", testManagementTestsPath)
	return nil
}

// StoreTestOptimizationPlanCache stores ddtest-private plan data in the runner cache.
func (cm *CacheManager) StoreTestOptimizationPlanCache(cache any) error {
	if err := cm.createRunnerCacheDirectory(); err != nil {
		return fmt.Errorf("failed to create runner cache directory: %w", err)
	}

	runnerPath := filepath.Join(appConstants.RunnerCacheDir, TestOptimizationPlanCacheFile)
	if err := cm.writeJSONToFile(cache, runnerPath); err != nil {
		slog.Error("Failed to write test optimization plan to file", "error", err, "path", runnerPath)
		return err
	}

	slog.Debug("Test optimization plan written to file", "path", runnerPath)
	return nil
}

// ReadTestOptimizationPlanCache reads ddtest-private plan data from the runner cache.
func (cm *CacheManager) ReadTestOptimizationPlanCache(cache any) error {
	runnerPath := filepath.Join(appConstants.RunnerCacheDir, TestOptimizationPlanCacheFile)
	if err := cm.readJSONFromFile(runnerPath, cache); err != nil {
		return err
	}

	slog.Debug("Test optimization plan read from file", "path", runnerPath)
	return nil
}
