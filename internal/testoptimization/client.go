package testoptimization

import (
	"encoding/json"
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
	GetKnownTests() *net.KnownTestsResponseData
	GetTestManagementTestsData() *net.TestManagementTestsResponseDataModules
	StoreCacheAndExit()
}

// These interfaces define the subset of the copied CI Visibility API ddtest uses.
type CIVisibilityIntegrations interface {
	EnsureCiVisibilityInitialization()
	ExitCiVisibility()
	GetSettings() *net.SettingsResponseData
	GetSettingsRawResponse() json.RawMessage
	GetSkippableTests() net.SkippableTests
	GetSkippableTestsRawResponse() json.RawMessage
	GetKnownTests() *net.KnownTestsResponseData
	GetKnownTestsRawResponse() json.RawMessage
	GetTestManagementTestsData() *net.TestManagementTestsResponseDataModules
	GetTestManagementTestsRawResponse() json.RawMessage
}

type UtilsInterface interface {
	AddCITagsMap(tags map[string]string)
}

// DatadogCIVisibilityIntegrations implements CIVisibilityIntegrations using the copied integrations package.
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

func (d *DatadogCIVisibilityIntegrations) GetSettingsRawResponse() json.RawMessage {
	return integrations.GetSettingsRawResponse()
}

func (d *DatadogCIVisibilityIntegrations) GetSkippableTests() net.SkippableTests {
	return integrations.GetSkippableTests()
}

func (d *DatadogCIVisibilityIntegrations) GetSkippableTestsRawResponse() json.RawMessage {
	return integrations.GetSkippableTestsRawResponse()
}

func (d *DatadogCIVisibilityIntegrations) GetKnownTests() *net.KnownTestsResponseData {
	return integrations.GetKnownTests()
}

func (d *DatadogCIVisibilityIntegrations) GetKnownTestsRawResponse() json.RawMessage {
	return integrations.GetKnownTestsRawResponse()
}

func (d *DatadogCIVisibilityIntegrations) GetTestManagementTestsData() *net.TestManagementTestsResponseDataModules {
	return integrations.GetTestManagementTestsData()
}

func (d *DatadogCIVisibilityIntegrations) GetTestManagementTestsRawResponse() json.RawMessage {
	return integrations.GetTestManagementTestsRawResponse()
}

// DatadogUtils implements UtilsInterface using the copied utils package.
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

	slog.Debug("Fetching skippable tests...")
	skippableTests := c.integrations.GetSkippableTests()
	if skippableTests == nil {
		skippableTests = net.SkippableTests{}
	}

	if err := c.cacheManager.StoreSkippableTestsCache(c.integrations.GetSkippableTestsRawResponse()); err != nil {
		slog.Warn("Failed to store skippable tests cache", "error", err)
	}

	duration := time.Since(startTime)
	slog.Debug("Finished fetching skippable tests", "count", len(skippableTests), "duration", duration)

	return skippableTests
}

func (c *DatadogClient) GetKnownTests() *net.KnownTestsResponseData {
	if c.settings == nil || !c.settings.KnownTestsEnabled {
		return nil
	}
	return c.integrations.GetKnownTests()
}

func (c *DatadogClient) GetTestManagementTestsData() *net.TestManagementTestsResponseDataModules {
	if c.settings == nil || !c.settings.TestManagement.Enabled {
		return nil
	}
	return c.integrations.GetTestManagementTestsData()
}

func (c *DatadogClient) StoreCacheAndExit() {
	repositorySettings := c.integrations.GetSettings()
	if repositorySettings != nil {
		slog.Debug("Repository settings", "itr_enabled", repositorySettings.ItrEnabled, "tests_skipping", repositorySettings.TestsSkipping)
	}
	if err := c.cacheManager.StoreRepositorySettings(c.integrations.GetSettingsRawResponse()); err != nil {
		slog.Warn("Failed to store repository settings cache", "error", err)
	}

	if err := c.cacheManager.StoreKnownTestsCache(c.integrations.GetKnownTestsRawResponse()); err != nil {
		slog.Warn("Failed to store known tests cache", "error", err)
	}

	if err := c.cacheManager.StoreTestManagementTestsCache(c.integrations.GetTestManagementTestsRawResponse()); err != nil {
		slog.Warn("Failed to store test management tests cache", "error", err)
	}

	c.integrations.ExitCiVisibility()
}
