package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/DataDog/datadog-test-runner/internal/runner"
	"github.com/DataDog/datadog-test-runner/internal/server"
	"github.com/DataDog/datadog-test-runner/internal/settings"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var rootCmd = &cobra.Command{
	Use:   "ddtest",
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

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Start HTTP server to serve context data",
	Long:  "Starts an HTTP server on configurable port that serves merged context data from .dd/context folder at /context endpoint.",
	Run: func(cmd *cobra.Command, args []string) {
		// Create context that cancels on SIGINT/SIGTERM
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()

		port := viper.GetInt("port")
		contextServer := server.New(port)
		if err := contextServer.Start(ctx); err != nil {
			slog.Error("Server failed", "error", err)
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.PersistentFlags().String("platform", "ruby", "Platform that runs tests")
	rootCmd.PersistentFlags().String("framework", "rspec", "Test framework to use")
	rootCmd.PersistentFlags().Int("min-parallelism", 1, "Minimum number of parallel test processes")
	rootCmd.PersistentFlags().Int("max-parallelism", 1, "Maximum number of parallel test processes")
	rootCmd.PersistentFlags().String("worker-env", "", "Worker environment configuration")
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

	serverCmd.Flags().IntP("port", "p", 7890, "Port for the HTTP server")
	if err := viper.BindPFlag("port", serverCmd.Flags().Lookup("port")); err != nil {
		fmt.Fprintf(os.Stderr, "Error binding port flag: %v\n", err)
		os.Exit(1)
	}

	rootCmd.AddCommand(setupCmd)
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(serverCmd)

	cobra.OnInitialize(settings.Init)
}

func Execute() error {
	return rootCmd.Execute()
}
