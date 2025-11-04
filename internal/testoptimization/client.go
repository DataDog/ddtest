package testoptimization

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/DataDog/ddtest/civisibility/integrations"
	"github.com/DataDog/ddtest/civisibility/utils"
	"github.com/DataDog/ddtest/civisibility/utils/net"
)

// TestOptimizationClient defines interface for test optimization operations
type TestOptimizationClient interface {
	Initialize(tags map[string]string) error
	GetSettings() *net.SettingsResponseData
	GetSkippableTests() map[string]bool
	StoreCacheAndExit()
}

// these interfaces define our expectactions for dd-trace-go's public API
type CIVisibilityIntegrations interface {
	EnsureCiVisibilityInitialization()
	ExitCiVisibility()
	GetSettings() *net.SettingsResponseData
	GetSkippableTests() map[string]map[string][]net.SkippableResponseDataAttributes
	GetKnownTests() *net.KnownTestsResponseData
	GetTestManagementTestsData() *net.TestManagementTestsResponseDataModules
}

type UtilsInterface interface {
	AddCITagsMap(tags map[string]string)
}

// DatadogCIVisibilityIntegrations implements CIVisibilityIntegrations using the real integrations package from dd-trace-go
type DatadogCIVisibilityIntegrations struct{}

func (d *DatadogCIVisibilityIntegrations) EnsureCiVisibilityInitialization() {
	integrations.EnsureCiVisibilityInitialization()
}

func (d *DatadogCIVisibilityIntegrations) ExitCiVisibility() {
	integrations.ExitCiVisibility()
}

func (d *DatadogCIVisibilityIntegrations) GetSettings() *net.SettingsResponseData {
	return integrations.GetSettings()
}

func (d *DatadogCIVisibilityIntegrations) GetSkippableTests() map[string]map[string][]net.SkippableResponseDataAttributes {
	return integrations.GetSkippableTests()
}

func (d *DatadogCIVisibilityIntegrations) GetKnownTests() *net.KnownTestsResponseData {
	return integrations.GetKnownTests()
}

func (d *DatadogCIVisibilityIntegrations) GetTestManagementTestsData() *net.TestManagementTestsResponseDataModules {
	return integrations.GetTestManagementTestsData()
}

// DatadogUtils implements UtilsInterface using the real utils package from dd-trace-go
type DatadogUtils struct{}

func (d *DatadogUtils) AddCITagsMap(tags map[string]string) {
	utils.AddCITagsMap(tags)
}

type DatadogClient struct {
	integrations CIVisibilityIntegrations
	utils        UtilsInterface
	cacheManager *CacheManager
	settings     *net.SettingsResponseData
}

func NewDatadogClient() *DatadogClient {
	return &DatadogClient{
		integrations: &DatadogCIVisibilityIntegrations{},
		utils:        &DatadogUtils{},
		cacheManager: NewCacheManager(),
	}
}

func NewDatadogClientWithDependencies(integrations CIVisibilityIntegrations, utils UtilsInterface) *DatadogClient {
	return &DatadogClient{
		integrations: integrations,
		utils:        utils,
		cacheManager: NewCacheManager(),
	}
}

func (c *DatadogClient) Initialize(tags map[string]string) error {
	c.utils.AddCITagsMap(tags)

	// Create cache directory for storing cache data
	if err := c.cacheManager.CreateCacheDirectory(); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}

	startTime := time.Now()
	c.integrations.EnsureCiVisibilityInitialization()

	// Fetch and store settings
	c.settings = c.integrations.GetSettings()

	duration := time.Since(startTime)
	slog.Debug("Finished Datadog Test Optimization initialization", "duration", duration)

	return nil
}

func (c *DatadogClient) GetSettings() *net.SettingsResponseData {
	return c.settings
}

func (c *DatadogClient) GetSkippableTests() map[string]bool {
	startTime := time.Now()
	skippedTests := make(map[string]bool)

	slog.Debug("Fetching skippable tests...")
	skippableTests := c.integrations.GetSkippableTests()

	// Store skippable tests using cache manager
	if err := c.cacheManager.StoreSkippableTestsCache(skippableTests); err != nil {
		slog.Warn("Failed to store skippable tests cache", "error", err)
	}

	for _, suites := range skippableTests {
		for _, tests := range suites {
			for _, test := range tests {
				t := Test{
					Name:       test.Name,
					Suite:      test.Suite,
					Parameters: test.Parameters,
				}
				skippedTests[t.FQN()] = true
			}
		}
	}

	duration := time.Since(startTime)
	slog.Debug("Finished fetching skippable tests", "count", len(skippedTests), "duration", duration)

	return skippedTests
}

func (c *DatadogClient) StoreCacheAndExit() {
	// store repository settings
	repositorySettings := c.integrations.GetSettings()
	if repositorySettings != nil {
		slog.Debug("Repository settings", "itr_enabled", repositorySettings.ItrEnabled, "tests_skipping", repositorySettings.TestsSkipping)

		// Store repository settings using cache manager
		if err := c.cacheManager.StoreRepositorySettings(repositorySettings); err != nil {
			slog.Warn("Failed to store repository settings", "error", err)
		}
	}

	// store known tests
	knownTests := c.integrations.GetKnownTests()
	if knownTests != nil {
		slog.Debug("Storing known tests cache")

		// Store known tests using cache manager
		if err := c.cacheManager.StoreKnownTestsCache(knownTests); err != nil {
			slog.Warn("Failed to store known tests cache", "error", err)
		}
	}

	// store test management tests
	testManagementTests := c.integrations.GetTestManagementTestsData()
	if testManagementTests != nil {
		slog.Debug("Storing test management tests cache")

		// Store test management tests using cache manager
		if err := c.cacheManager.StoreTestManagementTestsCache(testManagementTests); err != nil {
			slog.Warn("Failed to store test management tests cache", "error", err)
		}
	}

	c.integrations.ExitCiVisibility()
}
