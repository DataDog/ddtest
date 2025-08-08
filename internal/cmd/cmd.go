package cmd

import (
	"context"
	"log/slog"
	"os"

	"github.com/DataDog/datadog-test-runner/internal/runner"
	"github.com/DataDog/datadog-test-runner/internal/settings"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
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
		ctx := context.Background()
		testRunner := runner.New()
		if err := testRunner.PrintTestFiles(ctx); err != nil {
			slog.Error("Runner failed", "error", err)
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.PersistentFlags().String("platform", "ruby", "Platform that runs tests")
	viper.BindPFlag("platform", rootCmd.PersistentFlags().Lookup("platform"))

	rootCmd.AddCommand(testFilesCmd)

	cobra.OnInitialize(settings.Init)
}

func Execute() error {
	return rootCmd.Execute()
}
