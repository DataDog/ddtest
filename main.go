package main

import (
	"fmt"
	"os"

	"github.com/DataDog/datadog-test-runner/civisibility/constants"
	"github.com/DataDog/datadog-test-runner/civisibility/integrations"
	"github.com/DataDog/datadog-test-runner/civisibility/utils"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "datadog-test-runner",
	Short: "A test runner with Datadog",
	Long:  "A command line tool for running tests with Datadog Test Optimization.",
}

var skippablePercentageCmd = &cobra.Command{
	Use:   "skippable-percentage",
	Short: "Calculate skippable percentage with Datadog tracing",
	Run: func(cmd *cobra.Command, args []string) {
		tags := make(map[string]string)
		tags[constants.RuntimeName] = "ruby"
		tags[constants.RuntimeVersion] = "3.3.3"
		tags[constants.OSPlatform] = "darwin23"
		tags[constants.OSVersion] = "24.5.0"
		tags["language"] = "ruby"

		utils.AddCITagsMap(tags)
		integrations.EnsureCiVisibilityInitialization()

		// copy settings
		librarySettings := *integrations.GetSettings()

		// Set of FQNs for tests that can be skipped
		ddSkippedTests := make(map[string]any)

		if librarySettings.ItrEnabled && librarySettings.TestsSkipping {
			skippableTests := integrations.GetSkippableTests()

			// we don't need any more data from Test Optimization, make it stop
			integrations.ExitCiVisibility()

			// fill the storage of all tests to be skipped
			for _, suites := range skippableTests {
				for _, tests := range suites {
					for _, test := range tests {
						testFQN := testFQN(test.Suite, test.Name, test.Parameters)
						ddSkippedTests[testFQN] = struct{}{}
					}
				}
			}
		}

		for testFQN := range ddSkippedTests {
			fmt.Println(testFQN)
		}
	},
}

func testFQN(suite, test, parameters string) string {
	return fmt.Sprintf("%s.%s.%s", suite, test, parameters)
}

// func printSettings(settings net.SettingsResponseData) {
// 	fmt.Printf("Library Settings:\n")
// 	fmt.Printf("  ItrEnabled: %v\n", settings.ItrEnabled)
// 	fmt.Printf("  TestsSkipping: %v\n", settings.TestsSkipping)
// 	fmt.Printf("  CodeCoverage: %v\n", settings.CodeCoverage)
// 	fmt.Printf("  RequireGit: %v\n", settings.RequireGit)
// 	fmt.Printf("  FlakyTestRetriesEnabled: %v\n", settings.FlakyTestRetriesEnabled)
// 	fmt.Printf("  KnownTestsEnabled: %v\n", settings.KnownTestsEnabled)
// 	fmt.Printf("  ImpactedTestsEnabled: %v\n", settings.ImpactedTestsEnabled)
// 	fmt.Printf("  EarlyFlakeDetection.Enabled: %v\n", settings.EarlyFlakeDetection.Enabled)
// 	fmt.Printf("  EarlyFlakeDetection.FaultySessionThreshold: %d\n", settings.EarlyFlakeDetection.FaultySessionThreshold)
// 	fmt.Printf("  TestManagement.Enabled: %v\n", settings.TestManagement.Enabled)
// 	fmt.Printf("  TestManagement.AttemptToFixRetries: %d\n", settings.TestManagement.AttemptToFixRetries)
// 	fmt.Println()
// }

// func printSkippableTests(skippableTests map[string]map[string][]net.SkippableResponseDataAttributes) {
// 	fmt.Printf("Skippable Tests:\n")
// 	fmt.Printf("  Total Suites: %d\n", len(skippableTests))

// 	totalTests := 0
// 	for suite, tests := range skippableTests {
// 		totalTests += len(tests)
// 		fmt.Printf("  Suite: %s (%d tests)\n", suite, len(tests))
// 		for testName, testInstances := range tests {
// 			fmt.Printf("    Test: %s (%d instances)\n", testName, len(testInstances))
// 			for i, instance := range testInstances {
// 				fmt.Printf("      Instance %d:\n", i+1)
// 				fmt.Printf("        Parameters: %s\n", instance.Parameters)
// 				fmt.Printf("        Configurations: OsPlatform=%s, OsArchitecture=%s, RuntimeName=%s, RuntimeVersion=%s\n",
// 					instance.Configurations.OsPlatform, instance.Configurations.OsArchitecture,
// 					instance.Configurations.RuntimeName, instance.Configurations.RuntimeVersion)
// 			}
// 		}
// 	}
// 	fmt.Printf("  Total Tests: %d\n", totalTests)
// 	fmt.Println()
// }

func init() {
	rootCmd.AddCommand(skippablePercentageCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
