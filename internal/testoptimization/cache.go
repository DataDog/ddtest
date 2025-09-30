package testoptimization

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/DataDog/datadog-test-runner/civisibility/constants"
	"github.com/DataDog/datadog-test-runner/civisibility/utils"
	"github.com/DataDog/datadog-test-runner/civisibility/utils/net"
	appConstants "github.com/DataDog/datadog-test-runner/internal/constants"
)

// SkippableTestsCache represents the structure for storing skippable tests with correlation ID
type SkippableTestsCache struct {
	CorrelationID  string                                                      `json:"correlationId"`
	SkippableTests map[string]map[string][]net.SkippableResponseDataAttributes `json:"skippableTests"`
}

// CacheManager handles creation and storage of cache data for test runners
type CacheManager struct{}

// NewCacheManager creates a new CacheManager instance
func NewCacheManager() *CacheManager {
	return &CacheManager{}
}

// CreateCacheDirectory creates the .testoptimization/cache directory for storing cache data
func (cm *CacheManager) CreateCacheDirectory() error {
	cacheDir := filepath.Join(appConstants.PlanDirectory, "cache")
	return os.MkdirAll(cacheDir, 0755)
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

// StoreRepositorySettings stores repository settings in .testoptimization/cache/settings.json
func (cm *CacheManager) StoreRepositorySettings(repositorySettings *net.SettingsResponseData) error {
	if err := cm.CreateCacheDirectory(); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}

	settingsPath := filepath.Join(appConstants.PlanDirectory, "cache", "settings.json")
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

	skippableTestsPath := filepath.Join(appConstants.PlanDirectory, "cache", "skippable_tests.json")
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

	knownTestsPath := filepath.Join(appConstants.PlanDirectory, "cache", "known_tests.json")
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

	testManagementTestsPath := filepath.Join(appConstants.PlanDirectory, "cache", "test_management_tests.json")
	if err := cm.writeJSONToFile(testManagementTests, testManagementTestsPath); err != nil {
		slog.Error("Failed to write test management tests to file", "error", err, "path", testManagementTestsPath)
		return err
	}

	slog.Debug("Test management tests written to file", "path", testManagementTestsPath)
	return nil
}
