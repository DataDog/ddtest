package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/git"
	runnerpkg "github.com/DataDog/ddtest/internal/runner"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestRootCommandFlags(t *testing.T) {
	// Reset viper to ensure clean state
	viper.Reset()

	// Check that flags are defined
	platformFlag := rootCmd.PersistentFlags().Lookup("platform")
	if platformFlag == nil {
		t.Error("platform flag should be defined")
		return
	}

	frameworkFlag := rootCmd.PersistentFlags().Lookup("framework")
	if frameworkFlag == nil {
		t.Error("framework flag should be defined")
		return
	}

	commandFlag := rootCmd.PersistentFlags().Lookup("command")
	if commandFlag == nil {
		t.Error("command flag should be defined")
		return
	}

	testsLocationFlag := rootCmd.PersistentFlags().Lookup("tests-location")
	if testsLocationFlag == nil {
		t.Error("tests-location flag should be defined")
		return
	}

	testsExcludePatternFlag := rootCmd.PersistentFlags().Lookup("tests-exclude-pattern")
	if testsExcludePatternFlag == nil {
		t.Error("tests-exclude-pattern flag should be defined")
		return
	}

	testDiscoveryCacheFlag := rootCmd.PersistentFlags().Lookup("test-discovery-cache")
	if testDiscoveryCacheFlag == nil {
		t.Error("test-discovery-cache flag should be defined")
		return
	}

	ciNodeWorkersFlag := rootCmd.PersistentFlags().Lookup("ci-node-workers")
	if ciNodeWorkersFlag == nil {
		t.Error("ci-node-workers flag should be defined")
		return
	}

	ciNodeFlag := rootCmd.PersistentFlags().Lookup("ci-node")
	if ciNodeFlag == nil {
		t.Error("ci-node flag should be defined")
		return
	}

	parallelRunnerOverheadFlag := rootCmd.PersistentFlags().Lookup("ci-job-overhead")
	if parallelRunnerOverheadFlag == nil {
		t.Error("ci-job-overhead flag should be defined")
		return
	}

	// Check default values
	if platformFlag.DefValue != "ruby" {
		t.Errorf("expected platform default to be 'ruby', got %q", platformFlag.DefValue)
	}

	if frameworkFlag.DefValue != "rspec" {
		t.Errorf("expected framework default to be 'rspec', got %q", frameworkFlag.DefValue)
	}

	if commandFlag.DefValue != "" {
		t.Errorf("expected command default to be empty, got %q", commandFlag.DefValue)
	}

	if testsLocationFlag.DefValue != "" {
		t.Errorf("expected tests-location default to be empty, got %q", testsLocationFlag.DefValue)
	}

	if testsExcludePatternFlag.DefValue != "" {
		t.Errorf("expected tests-exclude-pattern default to be empty, got %q", testsExcludePatternFlag.DefValue)
	}

	if testDiscoveryCacheFlag.DefValue != "" {
		t.Errorf("expected test-discovery-cache default to be empty, got %q", testDiscoveryCacheFlag.DefValue)
	}

	if ciNodeWorkersFlag.DefValue != "1" {
		t.Errorf("expected ci-node-workers default to be '1', got %q", ciNodeWorkersFlag.DefValue)
	}

	if ciNodeFlag.DefValue != "-1" {
		t.Errorf("expected ci-node default to be '-1', got %q", ciNodeFlag.DefValue)
	}

	expectedParallelRunnerOverhead := settings.DefaultParallelRunnerOverhead().String()
	if parallelRunnerOverheadFlag.DefValue != expectedParallelRunnerOverhead {
		t.Errorf("expected ci-job-overhead default to be %q, got %q", expectedParallelRunnerOverhead, parallelRunnerOverheadFlag.DefValue)
	}
}

func TestCommandHierarchy(t *testing.T) {
	// Verify that planCmd and runCmd are added to rootCmd
	commands := rootCmd.Commands()
	var foundPlan, foundRun bool
	for _, cmd := range commands {
		if cmd.Use == "plan" {
			foundPlan = true
		}
		if cmd.Use == "run" {
			foundRun = true
		}
	}

	if !foundPlan {
		t.Error("plan command should be added to root command")
	}
	if !foundRun {
		t.Error("run command should be added to root command")
	}
}

func TestRootPersistentPreRunChecksGitAvailability(t *testing.T) {
	originalLookPathFunc := git.LookPathFunc
	git.LookPathFunc = func(file string) (string, error) {
		return "", errors.New("missing git")
	}
	t.Cleanup(func() {
		git.LookPathFunc = originalLookPathFunc
	})

	err := rootCmd.PersistentPreRunE(rootCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "git executable not found") {
		t.Fatalf("PersistentPreRunE() error = %v, want git availability error", err)
	}
}

func TestRunPlanCommand(t *testing.T) {
	originalPlanCommand := planCommand
	originalExitProcess := exitProcess
	t.Cleanup(func() {
		planCommand = originalPlanCommand
		exitProcess = originalExitProcess
	})

	calls := 0
	planCommand = func(ctx context.Context) error {
		calls++
		return nil
	}
	exitProcess = func(code int) {
		t.Fatalf("exitProcess(%d) should not be called", code)
	}

	runPlanCommand(&cobra.Command{}, nil)

	if calls != 1 {
		t.Fatalf("expected plan command to be called once, got %d", calls)
	}
}

func TestRunPlanCommandExitsOnError(t *testing.T) {
	originalPlanCommand := planCommand
	originalExitProcess := exitProcess
	t.Cleanup(func() {
		planCommand = originalPlanCommand
		exitProcess = originalExitProcess
	})

	planErr := errors.New("planner failed")
	planCommand = func(ctx context.Context) error {
		return planErr
	}
	var exitCodes []int
	exitProcess = func(code int) {
		exitCodes = append(exitCodes, code)
	}

	runPlanCommand(&cobra.Command{}, nil)

	if len(exitCodes) != 1 || exitCodes[0] != 1 {
		t.Fatalf("expected exit code 1, got %v", exitCodes)
	}
}

func TestRunTestCommand(t *testing.T) {
	originalNewRunner := newRunner
	originalExitProcess := exitProcess
	t.Cleanup(func() {
		newRunner = originalNewRunner
		exitProcess = originalExitProcess
	})

	fake := &fakeCommandRunner{}
	newRunner = func() runnerpkg.Runner {
		return fake
	}
	exitProcess = func(code int) {
		t.Fatalf("exitProcess(%d) should not be called", code)
	}

	runTestCommand(&cobra.Command{}, nil)

	if fake.calls != 1 {
		t.Fatalf("expected runner to be called once, got %d", fake.calls)
	}
}

func TestRunTestCommandExitsOnError(t *testing.T) {
	originalNewRunner := newRunner
	originalExitProcess := exitProcess
	t.Cleanup(func() {
		newRunner = originalNewRunner
		exitProcess = originalExitProcess
	})

	fake := &fakeCommandRunner{err: errors.New("runner failed")}
	newRunner = func() runnerpkg.Runner {
		return fake
	}
	var exitCodes []int
	exitProcess = func(code int) {
		exitCodes = append(exitCodes, code)
	}

	runTestCommand(&cobra.Command{}, nil)

	if len(exitCodes) != 1 || exitCodes[0] != 1 {
		t.Fatalf("expected exit code 1, got %v", exitCodes)
	}
}

func TestExecute(t *testing.T) {
	// Save original args
	originalArgs := os.Args
	defer func() { os.Args = originalArgs }()

	// Test with help flag to avoid actual execution
	os.Args = []string{"ddtest", "--help"}

	// Capture output
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)

	err := Execute()
	if err != nil {
		t.Errorf("Execute() with --help should not return error, got %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "ddtest") {
		t.Error("help output should contain command name 'ddtest'")
	}
}

func TestVersionFlag(t *testing.T) {
	resetBoolFlag := func(name string) {
		if rootCmd.Flags().Lookup(name) != nil {
			_ = rootCmd.Flags().Set(name, "false")
		}
	}
	resetBoolFlag("help")
	resetBoolFlag("version")

	originalLookPathFunc := git.LookPathFunc
	git.LookPathFunc = func(file string) (string, error) {
		return "", errors.New("git should not be checked for --version")
	}
	t.Cleanup(func() {
		git.LookPathFunc = originalLookPathFunc
		resetBoolFlag("help")
		resetBoolFlag("version")
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(os.Stdout)
		rootCmd.SetErr(os.Stderr)
	})

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"--version"})

	if err := Execute(); err != nil {
		t.Fatalf("Execute() with --version should not return error, got %v", err)
	}

	if got, want := buf.String(), rootCmd.Version+"\n"; got != want {
		t.Fatalf("expected version output %q, got %q", want, got)
	}
}

func TestFlagBinding(t *testing.T) {
	// Reset viper
	viper.Reset()

	// Flags are already defined in init(), so we can use them directly.
	if err := bindPersistentFlags(rootCmd, rootPersistentFlagBindings); err != nil {
		t.Fatalf("bindPersistentFlags() failed: %v", err)
	}

	// Set flag values
	if err := rootCmd.PersistentFlags().Set("platform", "python"); err != nil {
		t.Fatalf("Error setting platform flag: %v", err)
	}
	if err := rootCmd.PersistentFlags().Set("framework", "pytest"); err != nil {
		t.Fatalf("Error setting framework flag: %v", err)
	}
	if err := rootCmd.PersistentFlags().Set("command", "bundle exec pytest"); err != nil {
		t.Fatalf("Error setting command flag: %v", err)
	}
	if err := rootCmd.PersistentFlags().Set("tests-location", "spec/**/*_spec.rb"); err != nil {
		t.Fatalf("Error setting tests-location flag: %v", err)
	}
	if err := rootCmd.PersistentFlags().Set("tests-exclude-pattern", "spec/system/**/*_spec.rb"); err != nil {
		t.Fatalf("Error setting tests-exclude-pattern flag: %v", err)
	}
	if err := rootCmd.PersistentFlags().Set("test-discovery-cache", "/tmp/ddtest-tests.json"); err != nil {
		t.Fatalf("Error setting test-discovery-cache flag: %v", err)
	}
	if err := rootCmd.PersistentFlags().Set("ci-node-workers", "ncpu"); err != nil {
		t.Fatalf("Error setting ci-node-workers flag: %v", err)
	}
	if err := rootCmd.PersistentFlags().Set("ci-node", "3"); err != nil {
		t.Fatalf("Error setting ci-node flag: %v", err)
	}
	if err := rootCmd.PersistentFlags().Set("ci-job-overhead", "30s"); err != nil {
		t.Fatalf("Error setting ci-job-overhead flag: %v", err)
	}

	// Check that viper picks up the flag values
	if viper.GetString("platform") != "python" {
		t.Errorf("expected viper platform to be 'python', got %q", viper.GetString("platform"))
	}
	if viper.GetString("framework") != "pytest" {
		t.Errorf("expected viper framework to be 'pytest', got %q", viper.GetString("framework"))
	}
	if viper.GetString("command") != "bundle exec pytest" {
		t.Errorf("expected viper command to be 'bundle exec pytest', got %q", viper.GetString("command"))
	}
	if viper.GetString("tests_location") != "spec/**/*_spec.rb" {
		t.Errorf("expected viper tests_location to be 'spec/**/*_spec.rb', got %q", viper.GetString("tests_location"))
	}
	if viper.GetString("tests_exclude_pattern") != "spec/system/**/*_spec.rb" {
		t.Errorf("expected viper tests_exclude_pattern to be 'spec/system/**/*_spec.rb', got %q", viper.GetString("tests_exclude_pattern"))
	}
	if viper.GetString("test_discovery_cache") != "/tmp/ddtest-tests.json" {
		t.Errorf("expected viper test_discovery_cache to be '/tmp/ddtest-tests.json', got %q", viper.GetString("test_discovery_cache"))
	}
	if viper.GetString("ci_node_workers") != "ncpu" {
		t.Errorf("expected viper ci_node_workers to be 'ncpu', got %q", viper.GetString("ci_node_workers"))
	}
	if viper.GetInt("ci_node") != 3 {
		t.Errorf("expected viper ci_node to be 3, got %d", viper.GetInt("ci_node"))
	}
	if viper.GetString("parallel_runner_overhead") != "30s" {
		t.Errorf("expected viper parallel_runner_overhead to be '30s', got %q", viper.GetString("parallel_runner_overhead"))
	}
}

func TestBindPersistentFlags(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	testCmd := &cobra.Command{}
	testCmd.PersistentFlags().String("example-flag", "default", "example flag")

	err := bindPersistentFlags(testCmd, []persistentFlagBinding{
		{configKey: "example_config", flagName: "example-flag"},
	})
	if err != nil {
		t.Fatalf("bindPersistentFlags() failed: %v", err)
	}

	if err := testCmd.PersistentFlags().Set("example-flag", "configured"); err != nil {
		t.Fatalf("failed to set example flag: %v", err)
	}
	if got := viper.GetString("example_config"); got != "configured" {
		t.Fatalf("viper example_config = %q, want configured", got)
	}
}

func TestBindPersistentFlagsMissingFlag(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	err := bindPersistentFlags(&cobra.Command{}, []persistentFlagBinding{
		{configKey: "missing_config", flagName: "missing-flag"},
	})
	if err == nil || !strings.Contains(err.Error(), `flag "missing-flag" not found`) {
		t.Fatalf("bindPersistentFlags() error = %v, want missing flag", err)
	}
}

func TestCommandUsage(t *testing.T) {
	// Get all commands including root and subcommands
	allCommands := []*cobra.Command{rootCmd}
	allCommands = append(allCommands, rootCmd.Commands()...)

	// Test each command
	for _, cmd := range allCommands {
		cmdName := cmd.Use
		if cmdName == "" {
			cmdName = "root"
		}

		// Test that commands have proper usage text
		if strings.TrimSpace(cmd.Use) == "" && cmd != rootCmd {
			t.Errorf("command %q should have non-empty Use field", cmdName)
		}

		// Test that commands have help text
		if strings.TrimSpace(cmd.Short) == "" {
			t.Errorf("command %q should have non-empty Short description", cmdName)
		}

		// Test that Long description exists for commands that have it
		if cmd.Long != "" && strings.TrimSpace(cmd.Long) == "" {
			t.Errorf("command %q has Long field but it's empty", cmdName)
		}
	}

	// Verify we have the expected subcommands
	subCommands := rootCmd.Commands()
	subCommandNames := make([]string, len(subCommands))
	for i, cmd := range subCommands {
		subCommandNames[i] = cmd.Use
	}

	// Expected commands (cobra adds completion and help automatically)
	expectedCommands := []string{"plan", "run"}
	requiredCommands := []string{"completion", "help [command]", "plan", "run"}

	// Verify minimum expected commands exist
	for _, expected := range expectedCommands {
		found := false
		for _, cmd := range subCommands {
			if cmd.Use == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected to find subcommand %q", expected)
		}
	}

	// Verify we have at least the required commands (cobra adds built-ins)
	if len(subCommands) < len(requiredCommands) {
		t.Errorf("expected at least %d subcommands, got %d. Commands: %v",
			len(requiredCommands), len(subCommands), subCommandNames)
	}
}

func TestInitFunction(t *testing.T) {
	// Test that init function properly sets up the command structure
	// This is implicitly tested by the other tests, but we verify key setup

	// Verify flags are set up
	if rootCmd.PersistentFlags().Lookup("platform") == nil {
		t.Error("init should set up platform flag")
	}

	if rootCmd.PersistentFlags().Lookup("framework") == nil {
		t.Error("init should set up framework flag")
	}

	// Verify commands are added
	commands := rootCmd.Commands()
	if len(commands) == 0 {
		t.Error("init should add commands to root")
	}
}

type fakeCommandRunner struct {
	calls int
	err   error
}

func (f *fakeCommandRunner) Run(ctx context.Context) error {
	f.calls++
	return f.err
}
