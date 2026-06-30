package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/DataDog/ddtest/internal/buildinfo"
	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/git"
	"github.com/DataDog/ddtest/internal/planner"
	"github.com/DataDog/ddtest/internal/runner"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// defaultParallelism stores the computed default at init time for CLI flags
var defaultParallelism = settings.DefaultParallelism()

var rootCmd = &cobra.Command{
	Use:     "ddtest",
	Short:   "A test runner from Datadog",
	Long:    "Command line tool for running tests with Datadog Test Optimization.",
	Version: buildinfo.CurrentVersion(),
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return git.CheckAvailable()
	},
}

var (
	planCommand = planner.Plan
	newRunner   = func() runner.Runner { return runner.New() }
	exitProcess = os.Exit
)

var planCmd = &cobra.Command{
	Use:   "plan",
	Short: "Prepare test optimization data",
	Long: fmt.Sprintf(
		"Discovers test files and calculates the percentage of tests that can be skipped using Datadog's Test Impact Analysis. Outputs results to %s and %s.",
		constants.TestFilesOutputPath,
		constants.SkippablePercentageOutputPath,
	),
	Run: runPlanCommand,
}

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run tests using test optimization",
	Long:  "Runs tests using Datadog Test Optimization to execute only necessary test files based on code changes.",
	Run:   runTestCommand,
}

type persistentFlagBinding struct {
	configKey string
	flagName  string
}

var rootPersistentFlagBindings = []persistentFlagBinding{
	{configKey: "platform", flagName: "platform"},
	{configKey: "framework", flagName: "framework"},
	{configKey: "min_parallelism", flagName: "min-parallelism"},
	{configKey: "max_parallelism", flagName: "max-parallelism"},
	{configKey: "parallel_runner_overhead", flagName: "ci-job-overhead"},
	{configKey: "worker_env", flagName: "worker-env"},
	{configKey: "ci_node", flagName: "ci-node"},
	{configKey: "ci_node_workers", flagName: "ci-node-workers"},
	{configKey: "command", flagName: "command"},
	{configKey: "tests_location", flagName: "tests-location"},
	{configKey: "tests_exclude_pattern", flagName: "tests-exclude-pattern"},
	{configKey: "test_discovery_cache", flagName: "test-discovery-cache"},
	{configKey: "test_skipping_mode", flagName: "test-skipping-mode"},
	{configKey: "force_full_test_discovery", flagName: "force-full-test-discovery"},
	{configKey: "strict_discovery", flagName: "strict-discovery"},
	{configKey: "runtime_tags", flagName: "runtime-tags"},
}

func init() {
	rootCmd.SetVersionTemplate("{{ .Version }}\n")

	rootCmd.PersistentFlags().String("platform", "ruby", "Platform that runs tests")
	rootCmd.PersistentFlags().String("framework", "rspec", "Test framework to use")
	rootCmd.PersistentFlags().Int("min-parallelism", defaultParallelism, "Minimum number of parallel test processes (default: number of physical CPUs)")
	rootCmd.PersistentFlags().Int("max-parallelism", defaultParallelism, "Maximum number of parallel test processes (default: number of physical CPUs)")
	rootCmd.PersistentFlags().String("ci-job-overhead", settings.DefaultParallelRunnerOverhead().String(), "Modeled overhead for adding one more CI job / parallel runner (for example, 25s, 1m, 1500ms, or 0s to disable the bias). Increase it to use fewer CI jobs; decrease it to prefer faster wall time")
	rootCmd.PersistentFlags().String("worker-env", "", "Worker environment configuration")
	rootCmd.PersistentFlags().Int("ci-node", -1, "CI node index to run (0-indexed; default: -1 disables CI-node mode)")
	rootCmd.PersistentFlags().String("ci-node-workers", "1", `Number of parallel workers per CI node (positive integer or "ncpu"; default: 1)`)
	rootCmd.PersistentFlags().String("command", "", "Test command that ddtest should wrap")
	rootCmd.PersistentFlags().String("tests-location", "", "Glob pattern used to discover test files")
	rootCmd.PersistentFlags().String("tests-exclude-pattern", "", "Glob pattern used to exclude test files from discovery")
	rootCmd.PersistentFlags().String("test-discovery-cache", "", "Path to a restored test discovery cache file to import before planning")
	rootCmd.PersistentFlags().String("test-skipping-mode", "test", `TIA skipping granularity for Ruby ("test" or "suite"; invalid values fall back to "test")`)
	rootCmd.PersistentFlags().Bool("force-full-test-discovery", false, "Force full test discovery when the framework supports it")
	rootCmd.PersistentFlags().Bool("strict-discovery", false, "Fail planning when full test discovery fails")
	rootCmd.PersistentFlags().String("runtime-tags", "", "JSON string to override runtime tags (e.g. '{\"os.platform\":\"linux\",\"runtime.version\":\"3.2.0\"}')")
	if err := bindPersistentFlags(rootCmd, rootPersistentFlagBindings); err != nil {
		fmt.Fprintf(os.Stderr, "Error binding CLI flags: %v\n", err)
		os.Exit(1)
	}

	rootCmd.AddCommand(planCmd)
	rootCmd.AddCommand(runCmd)

	cobra.OnInitialize(settings.Init)
}

func bindPersistentFlags(cmd *cobra.Command, bindings []persistentFlagBinding) error {
	for _, binding := range bindings {
		flag := cmd.PersistentFlags().Lookup(binding.flagName)
		if flag == nil {
			return fmt.Errorf("flag %q not found", binding.flagName)
		}
		if err := viper.BindPFlag(binding.configKey, flag); err != nil {
			return fmt.Errorf("bind %s flag: %w", binding.flagName, err)
		}
	}
	return nil
}

func runPlanCommand(cmd *cobra.Command, args []string) {
	ctx := context.Background()
	if err := planCommand(ctx); err != nil {
		slog.Error("Planner failed", "error", err)
		exitProcess(1)
		return
	}
}

func runTestCommand(cmd *cobra.Command, args []string) {
	ctx := context.Background()
	testRunner := newRunner()
	if err := testRunner.Run(ctx); err != nil {
		slog.Error("Runner failed", "error", err)
		exitProcess(1)
		return
	}
}

func Execute() error {
	return rootCmd.Execute()
}
