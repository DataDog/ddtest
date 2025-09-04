package cmd

import (
	"bytes"
	"os"
	"strings"
	"testing"

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

	// Check default values
	if platformFlag.DefValue != "ruby" {
		t.Errorf("expected platform default to be 'ruby', got %q", platformFlag.DefValue)
	}

	if frameworkFlag.DefValue != "rspec" {
		t.Errorf("expected framework default to be 'rspec', got %q", frameworkFlag.DefValue)
	}
}

func TestCommandHierarchy(t *testing.T) {
	// Verify that setupCmd, runCmd and serverCmd are added to rootCmd
	commands := rootCmd.Commands()
	var foundSetup, foundRun, foundServer bool
	for _, cmd := range commands {
		if cmd.Use == "setup" {
			foundSetup = true
		}
		if cmd.Use == "run" {
			foundRun = true
		}
		if cmd.Use == "server" {
			foundServer = true
		}
	}

	if !foundSetup {
		t.Error("setup command should be added to root command")
	}
	if !foundRun {
		t.Error("run command should be added to root command")
	}
	if !foundServer {
		t.Error("server command should be added to root command")
	}
}

func TestExecute(t *testing.T) {
	// Save original args
	originalArgs := os.Args
	defer func() { os.Args = originalArgs }()

	// Test with help flag to avoid actual execution
	os.Args = []string{"ddruntest", "--help"}

	// Capture output
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)

	err := Execute()
	if err != nil {
		t.Errorf("Execute() with --help should not return error, got %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "ddruntest") {
		t.Error("help output should contain command name 'ddruntest'")
	}
}

func TestFlagBinding(t *testing.T) {
	// Reset viper
	viper.Reset()

	// Flags are already defined in init(), so we can use them directly
	// Rebind flags to ensure they work with viper
	if err := viper.BindPFlag("platform", rootCmd.PersistentFlags().Lookup("platform")); err != nil {
		t.Fatalf("Error binding platform flag: %v", err)
	}
	if err := viper.BindPFlag("framework", rootCmd.PersistentFlags().Lookup("framework")); err != nil {
		t.Fatalf("Error binding framework flag: %v", err)
	}

	// Set flag values
	if err := rootCmd.PersistentFlags().Set("platform", "python"); err != nil {
		t.Fatalf("Error setting platform flag: %v", err)
	}
	if err := rootCmd.PersistentFlags().Set("framework", "pytest"); err != nil {
		t.Fatalf("Error setting framework flag: %v", err)
	}

	// Check that viper picks up the flag values
	if viper.GetString("platform") != "python" {
		t.Errorf("expected viper platform to be 'python', got %q", viper.GetString("platform"))
	}
	if viper.GetString("framework") != "pytest" {
		t.Errorf("expected viper framework to be 'pytest', got %q", viper.GetString("framework"))
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
	expectedCommands := []string{"setup", "run", "server"}
	requiredCommands := []string{"completion", "help [command]", "setup", "run", "server"}

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
