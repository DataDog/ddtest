package testoptimization

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/DataDog/datadog-test-runner/civisibility/integrations"
	"github.com/DataDog/datadog-test-runner/civisibility/utils"
	"github.com/DataDog/datadog-test-runner/civisibility/utils/net"
)

// TestOptimizationClient defines interface for test optimization operations
type TestOptimizationClient interface {
	Initialize(tags map[string]string) error
	GetSkippableTests() map[string]bool
	StoreContextAndExit()
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
	integrations   CIVisibilityIntegrations
	utils          UtilsInterface
	contextManager *ContextManager
}

func NewDatadogClient() *DatadogClient {
	return &DatadogClient{
		integrations:   &DatadogCIVisibilityIntegrations{},
		utils:          &DatadogUtils{},
		contextManager: NewContextManager(),
	}
}

func NewDatadogClientWithDependencies(integrations CIVisibilityIntegrations, utils UtilsInterface) *DatadogClient {
	return &DatadogClient{
		integrations:   integrations,
		utils:          utils,
		contextManager: NewContextManager(),
	}
}

func (c *DatadogClient) Initialize(tags map[string]string) error {
	c.utils.AddCITagsMap(tags)

	// Create context directory for storing context data
	if err := c.contextManager.CreateContextDirectory(); err != nil {
		return fmt.Errorf("failed to create context directory: %w", err)
	}

	startTime := time.Now()
	c.integrations.EnsureCiVisibilityInitialization()

	duration := time.Since(startTime)
	slog.Debug("Finished Datadog Test Optimization initialization", "duration", duration)

	return nil
}

func (c *DatadogClient) GetSkippableTests() map[string]bool {
	startTime := time.Now()
	skippedTests := make(map[string]bool)

	slog.Debug("Fetching skippable tests...")
	skippableTests := c.integrations.GetSkippableTests()

	// Store skippable tests using context manager
	if err := c.contextManager.StoreSkippableTestsContext(skippableTests); err != nil {
		slog.Warn("Failed to store skippable tests context", "error", err)
	}

	for _, suites := range skippableTests {
		for _, tests := range suites {
			for _, test := range tests {
				testFQN := c.buildTestFQN(test.Suite, test.Name, test.Parameters)
				skippedTests[testFQN] = true
			}
		}
	}

	duration := time.Since(startTime)
	slog.Debug("Finished fetching skippable tests", "count", len(skippedTests), "duration", duration)

	return skippedTests
}

func (c *DatadogClient) StoreContextAndExit() {
	// store repository settings
	repositorySettings := c.integrations.GetSettings()
	if repositorySettings != nil {
		slog.Debug("Repository settings", "itr_enabled", repositorySettings.ItrEnabled, "tests_skipping", repositorySettings.TestsSkipping)

		// Store repository settings using context manager
		if err := c.contextManager.StoreRepositorySettings(repositorySettings); err != nil {
			slog.Warn("Failed to store repository settings", "error", err)
		}
	}

	// store known tests
	knownTests := c.integrations.GetKnownTests()
	if knownTests != nil {
		slog.Debug("Storing known tests context")

		// Store known tests using context manager
		if err := c.contextManager.StoreKnownTestsContext(knownTests); err != nil {
			slog.Warn("Failed to store known tests context", "error", err)
		}
	}

	// store test management tests
	testManagementTests := c.integrations.GetTestManagementTestsData()
	if testManagementTests != nil {
		slog.Debug("Storing test management tests context")

		// Store test management tests using context manager
		if err := c.contextManager.StoreTestManagementTestsContext(testManagementTests); err != nil {
			slog.Warn("Failed to store test management tests context", "error", err)
		}
	}

	c.integrations.ExitCiVisibility()
}

func (c *DatadogClient) buildTestFQN(suite, test, parameters string) string {
	return fmt.Sprintf("%s.%s.%s", suite, test, parameters)
}
