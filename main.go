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
		fmt.Println("Calculating skippable percentage...")

		tags := make(map[string]string)
		tags[constants.RuntimeName] = "ruby"
		tags[constants.RuntimeVersion] = "3.3.3"
		tags["language"] = "ruby"

		utils.AddCITagsMap(tags)
		integrations.EnsureCiVisibilityInitialization()
	},
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
