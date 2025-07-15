package main

import (
	"fmt"
	"os"

	"github.com/DataDog/datadog-test-runner/civisibility/constants"
	"github.com/DataDog/datadog-test-runner/civisibility/integrations"
	"github.com/DataDog/datadog-test-runner/civisibility/utils"
	"github.com/DataDog/datadog-test-runner/civisibility/utils/net"
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
		fmt.Println("Calculating skippable percentage...")

		tags := make(map[string]string)
		tags[constants.RuntimeName] = "ruby"
		tags[constants.RuntimeVersion] = "3.3.3"
		tags["language"] = "ruby"

		utils.AddCITagsMap(tags)
		integrations.EnsureCiVisibilityInitialization()

		// settings
		librarySettings := *integrations.GetSettings()
		printSettings(librarySettings)

		if librarySettings.ItrEnabled && librarySettings.TestsSkipping {
			// fetch skippable tests
		} else {
			fmt.Println("Test skipping is not enabled")
			fmt.Println("0.0")
		}
	},
}

func printSettings(settings net.SettingsResponseData) {
	fmt.Printf("Library Settings:\n")
	fmt.Printf("  ItrEnabled: %v\n", settings.ItrEnabled)
	fmt.Printf("  TestsSkipping: %v\n", settings.TestsSkipping)
	fmt.Printf("  CodeCoverage: %v\n", settings.CodeCoverage)
	fmt.Printf("  RequireGit: %v\n", settings.RequireGit)
	fmt.Printf("  FlakyTestRetriesEnabled: %v\n", settings.FlakyTestRetriesEnabled)
	fmt.Printf("  KnownTestsEnabled: %v\n", settings.KnownTestsEnabled)
	fmt.Printf("  ImpactedTestsEnabled: %v\n", settings.ImpactedTestsEnabled)
	fmt.Printf("  EarlyFlakeDetection.Enabled: %v\n", settings.EarlyFlakeDetection.Enabled)
	fmt.Printf("  EarlyFlakeDetection.FaultySessionThreshold: %d\n", settings.EarlyFlakeDetection.FaultySessionThreshold)
	fmt.Printf("  TestManagement.Enabled: %v\n", settings.TestManagement.Enabled)
	fmt.Printf("  TestManagement.AttemptToFixRetries: %d\n", settings.TestManagement.AttemptToFixRetries)
	fmt.Println()
}

func init() {
	rootCmd.AddCommand(skippablePercentageCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
