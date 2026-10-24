package cmd

import (
	"errors"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"os"
	"strconv"
	"strings"
)

func protocolFailure(cmd *cobra.Command, phase, code string, err error, next []string) error {
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	action := "supply_input"
	status := "needs_input"
	if code == "framework_unsupported" {
		action = "report_result"
		status = "unsupported"
		next = nil
	}
	// The confirmed command is the consent; running it is the next action.
	if code == "consent_required" && len(next) > 0 {
		action = "run_command"
	}
	return errors.Join(err, onboard.WriteAgentResult(cmd.OutOrStdout(), onboard.AgentResult{Status: status, Phase: phase, Code: code, Reason: err.Error(), NextAction: action, NextCommand: next}))
}

func confirmedCommand(cmd *cobra.Command) []string {
	words := onboard.DDTestCommand(cmd.Name())
	if len(words) == 0 {
		return nil
	}
	cmd.Flags().Visit(func(flag *pflag.Flag) {
		if flag.Name != "yes" {
			words = append(words, "--"+flag.Name+"="+flag.Value.String())
		}
	})
	return append(words, "--yes")
}

func configureProtocolErrors(command *cobra.Command) {
	original := command.Args
	command.Args = func(cmd *cobra.Command, args []string) error {
		if original == nil {
			return nil
		}
		err := original(cmd, args)
		if err != nil && usesProtocol(cmd) {
			return protocolFailure(cmd, "options", "invalid_options", err, nil)
		}
		return err
	}
	command.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		if usesProtocol(cmd) {
			return protocolFailure(cmd, "options", "invalid_options", err, nil)
		}
		return err
	})
}
func usesProtocol(cmd *cobra.Command) bool {
	agent, _ := cmd.Flags().GetBool("agent")
	return agent || cmd.Flags().Changed("format") || protocolArguments(cmd, os.Args[1:])
}

// Flag parsing can fail before it reaches --agent/--format. Inspect the original
// invocation too, skipping values of known options rather than treating a test
// command's quoted arguments as ddtest flags.
func protocolArguments(cmd *cobra.Command, args []string) bool {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		if arg == "--agent" {
			return true
		}
		if value, ok := strings.CutPrefix(arg, "--agent="); ok {
			enabled, err := strconv.ParseBool(value)
			if err != nil || enabled {
				return true
			}
		}
		if arg == "--format" || strings.HasPrefix(arg, "--format=") {
			return true
		}
		if strings.HasPrefix(arg, "--") && !strings.Contains(arg, "=") {
			flag := cmd.Flag(strings.TrimPrefix(arg, "--"))
			if flag != nil && flag.NoOptDefVal == "" {
				i++
			}
		}
	}
	return false
}
