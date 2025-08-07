package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/DataDog/datadog-test-runner/internal/platform"
	"github.com/DataDog/datadog-test-runner/internal/testoptimization"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "ddruntest",
	Short: "A test runner from Datadog",
	Long:  "Command line tool for running tests with Datadog Test Optimization.",
}

var testFilesCmd = &cobra.Command{
	Use:   "test-files",
	Short: "prints test files that are discovered in the project and not skipped completely by Datadog's Test Impact Analysis",
	Run: func(cmd *cobra.Command, args []string) {
		platform, err := platform.DetectPlatform()
		if err != nil {
			slog.Error("Failed to detect platform", "error", err)
			os.Exit(1)
		}

		tags := platform.CreateTagsMap()

		client := testoptimization.NewDatadogClient()
		if err := client.Initialize(tags); err != nil {
			slog.Error("Failed to initialize optimization client", "error", err)
			os.Exit(1)
		}

		ddSkippedTests := client.GetSkippableTests()
		client.Shutdown()

		framework, err := platform.DetectFramework()
		if err != nil {
			slog.Error("Failed to detect framework", "error", err)
			os.Exit(1)
		}

		tests, err := framework.DiscoverTests()
		if err != nil {
			slog.Error("Failed to discover tests", "error", err)
			os.Exit(1)
		}

		testFiles := make(map[string]bool)
		for _, test := range tests {
			if !ddSkippedTests[test.FQN] {
				slog.Debug("Test is not skipped", "test", test.FQN, "sourceFile", test.SourceFile)
				testFiles[test.SourceFile] = true
			}
		}

		for testFile := range testFiles {
			fmt.Print(testFile + " ")
		}
		fmt.Println()
	},
}

func init() {
	rootCmd.AddCommand(testFilesCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		slog.Error("FAILURE", "error", err)
		os.Exit(1)
	}
}
