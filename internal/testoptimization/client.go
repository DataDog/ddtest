package testoptimization

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/DataDog/datadog-test-runner/civisibility/integrations"
	"github.com/DataDog/datadog-test-runner/civisibility/utils"
)

type Client interface {
	Initialize(tags map[string]string) error
	GetSkippableTests() map[string]bool
	Shutdown()
}

type DatadogClient struct{}

func NewDatadogClient() *DatadogClient {
	return &DatadogClient{}
}

func (c *DatadogClient) Initialize(tags map[string]string) error {
	utils.AddCITagsMap(tags)

	startTime := time.Now()
	integrations.EnsureCiVisibilityInitialization()

	duration := time.Since(startTime)
	slog.Debug("Finished Datadog Test Optimization initialization", "duration", duration)

	return nil
}

func (c *DatadogClient) GetSkippableTests() map[string]bool {
	startTime := time.Now()

	repositorySettings := *integrations.GetSettings()
	skippedTests := make(map[string]bool)
	slog.Debug("Received repository settings", "itr_enabled", repositorySettings.ItrEnabled, "tests_skipping", repositorySettings.TestsSkipping)

	if repositorySettings.ItrEnabled && repositorySettings.TestsSkipping {
		slog.Debug("Fetching skippable tests...")
		skippableTests := integrations.GetSkippableTests()

		for _, suites := range skippableTests {
			for _, tests := range suites {
				for _, test := range tests {
					testFQN := c.buildTestFQN(test.Suite, test.Name, test.Parameters)
					skippedTests[testFQN] = true
				}
			}
		}
	}

	duration := time.Since(startTime)
	slog.Debug("Finished fetching skippable tests", "count", len(skippedTests), "duration", duration)

	return skippedTests
}

func (c *DatadogClient) Shutdown() {
	integrations.ExitCiVisibility()
}

func (c *DatadogClient) buildTestFQN(suite, test, parameters string) string {
	return fmt.Sprintf("%s.%s.%s", suite, test, parameters)
}
