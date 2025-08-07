package cmd

import (
	"log/slog"
	"os"

	"github.com/DataDog/datadog-test-runner/internal/runner"
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
		testRunner := runner.New()
		if err := testRunner.PrintTestFiles(); err != nil {
			slog.Error("Runner failed", "error", err)
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.AddCommand(testFilesCmd)
}

func Execute() error {
	return rootCmd.Execute()
}
