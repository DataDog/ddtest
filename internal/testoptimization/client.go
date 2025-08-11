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
	Shutdown()
}

// these interfaces define our expectactions for dd-trace-go's public API
type CIVisibilityIntegrations interface {
	EnsureCiVisibilityInitialization()
	ExitCiVisibility()
	GetSettings() *net.SettingsResponseData
	GetSkippableTests() map[string]map[string][]net.SkippableResponseDataAttributes
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

// DatadogUtils implements UtilsInterface using the real utils package from dd-trace-go
type DatadogUtils struct{}

func (d *DatadogUtils) AddCITagsMap(tags map[string]string) {
	utils.AddCITagsMap(tags)
}

type DatadogClient struct {
	integrations CIVisibilityIntegrations
	utils        UtilsInterface
}

func NewDatadogClient() *DatadogClient {
	return &DatadogClient{
		integrations: &DatadogCIVisibilityIntegrations{},
		utils:        &DatadogUtils{},
	}
}

func NewDatadogClientWithDependencies(integrations CIVisibilityIntegrations, utils UtilsInterface) *DatadogClient {
	return &DatadogClient{
		integrations: integrations,
		utils:        utils,
	}
}

func (c *DatadogClient) Initialize(tags map[string]string) error {
	c.utils.AddCITagsMap(tags)

	startTime := time.Now()
	c.integrations.EnsureCiVisibilityInitialization()

	duration := time.Since(startTime)
	slog.Debug("Finished Datadog Test Optimization initialization", "duration", duration)

	return nil
}

func (c *DatadogClient) GetSkippableTests() map[string]bool {
	startTime := time.Now()

	repositorySettings := c.integrations.GetSettings()
	skippedTests := make(map[string]bool)

	if repositorySettings != nil {
		slog.Debug("Received repository settings", "itr_enabled", repositorySettings.ItrEnabled, "tests_skipping", repositorySettings.TestsSkipping)

		if repositorySettings.ItrEnabled && repositorySettings.TestsSkipping {
			slog.Debug("Fetching skippable tests...")
			skippableTests := c.integrations.GetSkippableTests()

			for _, suites := range skippableTests {
				for _, tests := range suites {
					for _, test := range tests {
						testFQN := c.buildTestFQN(test.Suite, test.Name, test.Parameters)
						skippedTests[testFQN] = true
					}
				}
			}
		}
	}

	duration := time.Since(startTime)
	slog.Debug("Finished fetching skippable tests", "count", len(skippedTests), "duration", duration)

	return skippedTests
}

func (c *DatadogClient) Shutdown() {
	c.integrations.ExitCiVisibility()
}

func (c *DatadogClient) buildTestFQN(suite, test, parameters string) string {
	return fmt.Sprintf("%s.%s.%s", suite, test, parameters)
}
