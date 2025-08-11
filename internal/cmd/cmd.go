package cmd

import (
	"context"
	"fmt"
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

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Prepare test optimization data",
	Long: fmt.Sprintf(
		"Discovers test files and calculates the percentage of tests that can be skipped using Datadog's Test Impact Analysis. Outputs results to %s and %s.",
		runner.TestFilesOutputPath,
		runner.SkippablePercentageOutputPath,
	),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()
		testRunner := runner.New()
		if err := testRunner.Setup(ctx); err != nil {
			slog.Error("Runner failed", "error", err)
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.PersistentFlags().String("platform", "ruby", "Platform that runs tests")
	rootCmd.PersistentFlags().String("framework", "rspec", "Test framework to use")
	viper.BindPFlag("platform", rootCmd.PersistentFlags().Lookup("platform"))
	viper.BindPFlag("framework", rootCmd.PersistentFlags().Lookup("framework"))

	rootCmd.AddCommand(setupCmd)

	cobra.OnInitialize(settings.Init)
}

func Execute() error {
	return rootCmd.Execute()
}
