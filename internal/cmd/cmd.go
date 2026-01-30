package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/runner"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// defaultParallelism stores the computed default at init time for CLI flags
var defaultParallelism = settings.DefaultParallelism()

var rootCmd = &cobra.Command{
	Use:   "ddtest",
	Short: "A test runner from Datadog",
	Long:  "Command line tool for running tests with Datadog Test Optimization.",
}

var planCmd = &cobra.Command{
	Use:   "plan",
	Short: "Prepare test optimization data",
	Long: fmt.Sprintf(
		"Discovers test files and calculates the percentage of tests that can be skipped using Datadog's Test Impact Analysis. Outputs results to %s and %s.",
		constants.TestFilesOutputPath,
		constants.SkippablePercentageOutputPath,
	),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()
		testRunner := runner.New()
		if err := testRunner.Plan(ctx); err != nil {
			slog.Error("Runner failed", "error", err)
			os.Exit(1)
		}
	},
}

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run tests using test optimization",
	Long:  "Runs tests using Datadog Test Optimization to execute only necessary test files based on code changes.",
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()
		testRunner := runner.New()
		if err := testRunner.Run(ctx); err != nil {
			slog.Error("Runner failed", "error", err)
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.PersistentFlags().String("platform", "ruby", "Platform that runs tests")
	rootCmd.PersistentFlags().String("framework", "rspec", "Test framework to use")
	rootCmd.PersistentFlags().Int("min-parallelism", defaultParallelism, "Minimum number of parallel test processes (default: number of CPUs)")
	rootCmd.PersistentFlags().Int("max-parallelism", defaultParallelism, "Maximum number of parallel test processes (default: number of CPUs)")
	rootCmd.PersistentFlags().String("worker-env", "", "Worker environment configuration")
	rootCmd.PersistentFlags().Int("ci-node-workers", defaultParallelism, "Number of parallel workers per CI node (default: number of CPUs)")
	rootCmd.PersistentFlags().String("command", "", "Test command that ddtest should wrap")
	rootCmd.PersistentFlags().String("tests-location", "", "Glob pattern used to discover test files")
	rootCmd.PersistentFlags().String("runtime-tags", "", "JSON string to override runtime tags (e.g. '{\"os.platform\":\"linux\",\"runtime.version\":\"3.2.0\"}')")
	if err := viper.BindPFlag("platform", rootCmd.PersistentFlags().Lookup("platform")); err != nil {
		fmt.Fprintf(os.Stderr, "Error binding platform flag: %v\n", err)
		os.Exit(1)
	}
	if err := viper.BindPFlag("framework", rootCmd.PersistentFlags().Lookup("framework")); err != nil {
		fmt.Fprintf(os.Stderr, "Error binding framework flag: %v\n", err)
		os.Exit(1)
	}
	if err := viper.BindPFlag("min_parallelism", rootCmd.PersistentFlags().Lookup("min-parallelism")); err != nil {
		fmt.Fprintf(os.Stderr, "Error binding min-parallelism flag: %v\n", err)
		os.Exit(1)
	}
	if err := viper.BindPFlag("max_parallelism", rootCmd.PersistentFlags().Lookup("max-parallelism")); err != nil {
		fmt.Fprintf(os.Stderr, "Error binding max-parallelism flag: %v\n", err)
		os.Exit(1)
	}
	if err := viper.BindPFlag("worker_env", rootCmd.PersistentFlags().Lookup("worker-env")); err != nil {
		fmt.Fprintf(os.Stderr, "Error binding worker-env flag: %v\n", err)
		os.Exit(1)
	}
	if err := viper.BindPFlag("ci_node_workers", rootCmd.PersistentFlags().Lookup("ci-node-workers")); err != nil {
		fmt.Fprintf(os.Stderr, "Error binding ci-node-workers flag: %v\n", err)
		os.Exit(1)
	}
	if err := viper.BindPFlag("command", rootCmd.PersistentFlags().Lookup("command")); err != nil {
		fmt.Fprintf(os.Stderr, "Error binding command flag: %v\n", err)
		os.Exit(1)
	}
	if err := viper.BindPFlag("tests_location", rootCmd.PersistentFlags().Lookup("tests-location")); err != nil {
		fmt.Fprintf(os.Stderr, "Error binding tests-location flag: %v\n", err)
		os.Exit(1)
	}
	if err := viper.BindPFlag("runtime_tags", rootCmd.PersistentFlags().Lookup("runtime-tags")); err != nil {
		fmt.Fprintf(os.Stderr, "Error binding runtime-tags flag: %v\n", err)
		os.Exit(1)
	}

	rootCmd.AddCommand(planCmd)
	rootCmd.AddCommand(runCmd)

	cobra.OnInitialize(settings.Init)
}

func Execute() error {
	return rootCmd.Execute()
}
