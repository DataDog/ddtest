package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/DataDog/datadog-test-runner/internal/runner"
	"github.com/DataDog/datadog-test-runner/internal/server"
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

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Start HTTP server to serve context data",
	Long:  "Starts an HTTP server on configurable port that serves merged context data from .dd/context folder at /context endpoint.",
	Run: func(cmd *cobra.Command, args []string) {
		port := viper.GetInt("port")
		contextServer := server.New(port)
		if err := contextServer.Start(); err != nil {
			slog.Error("Server failed", "error", err)
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.PersistentFlags().String("platform", "ruby", "Platform that runs tests")
	rootCmd.PersistentFlags().String("framework", "rspec", "Test framework to use")
	if err := viper.BindPFlag("platform", rootCmd.PersistentFlags().Lookup("platform")); err != nil {
		fmt.Fprintf(os.Stderr, "Error binding platform flag: %v\n", err)
		os.Exit(1)
	}
	if err := viper.BindPFlag("framework", rootCmd.PersistentFlags().Lookup("framework")); err != nil {
		fmt.Fprintf(os.Stderr, "Error binding framework flag: %v\n", err)
		os.Exit(1)
	}

	serverCmd.Flags().IntP("port", "p", 7890, "Port for the HTTP server")
	if err := viper.BindPFlag("port", serverCmd.Flags().Lookup("port")); err != nil {
		fmt.Fprintf(os.Stderr, "Error binding port flag: %v\n", err)
		os.Exit(1)
	}

	rootCmd.AddCommand(setupCmd)
	rootCmd.AddCommand(serverCmd)

	cobra.OnInitialize(settings.Init)
}

func Execute() error {
	return rootCmd.Execute()
}
