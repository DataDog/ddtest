// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/DataDog/ddtest/internal/testdrive"
	"github.com/kballard/go-shellquote"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var testdriveCmd = newTestdriveCommand()

func newTestdriveCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "testdrive",
		Short: "Try Test Optimization on the local test suite",
		Long:  fmt.Sprintf("For %[1]s, compares native test results without and with instrumentation, then checks advanced features using temporary probes under the original configuration. Matching existing test failures are acceptable. Other frameworks run reporting-only checks against a local intake. No Datadog API key is required. With --check-only, inspect %[1]s configuration and static CI/tracer compatibility without installing a tracer or running tests.", testdrive.ValidationFrameworkNames()),
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.NoArgs(cmd, args); err != nil {
				return err
			}
			cmd.SilenceUsage = true
			return nil
		},
	}
	var version string
	var checkOnly bool
	var all bool
	command.Flags().BoolVar(&all,"all",false,"Validate every discovered "+testdrive.ValidationFrameworkNames()+" CI configuration, preserving environment and known build prerequisites")
	command.Flags().BoolVar(&checkOnly, "check-only", false, "Check "+testdrive.ValidationFrameworkNames()+" configuration, tracer selection and static CI runtimes without installing a tracer or running tests")
	command.Flags().StringVar(&version, "tracer-version", "latest", "Fallback tracer release or git:<commit-or-ref>, used only when the project has no tracer")
	command.Flags().Bool("yes", false, "Run after printing the changes and commands")
	command.RunE = func(cmd *cobra.Command, _ []string) error {
		execution, err := testdrive.Prepare(testdrive.PrepareOptions{TracerVersion: version, CheckOnly: checkOnly, All: all})
		if err != nil {
			return err
		}

		output := cmd.OutOrStdout()
		execution.Preview(output)
		yes, err := cmd.Flags().GetBool("yes")
		if err != nil {
			return err
		}
		if !yes {
			confirmed, err := confirmTestdrive(cmd.InOrStdin(), output, isTerminal(cmd.InOrStdin()), rerunWithYes(cmd))
			if err != nil {
				return err
			}
			if !confirmed {
				_, _ = fmt.Fprintln(output, "Testdrive cancelled.")
				return nil
			}
		}

		return execution.Run(commandContext(cmd), output)
	}
	return command
}

// The rerun hint repeats every flag the user set, so --check-only is never lost.
func rerunWithYes(cmd *cobra.Command) string {
	words := []string{"ddtest", cmd.Name()}
	cmd.Flags().Visit(func(flag *pflag.Flag) {
		switch {
		case flag.Name == "yes":
		case flag.Value.Type() == "bool" && flag.Value.String() == "true":
			words = append(words, "--"+flag.Name)
		case flag.Value.Type() == "bool":
			words = append(words, "--"+flag.Name+"="+flag.Value.String())
		default:
			words = append(words, "--"+flag.Name, flag.Value.String())
		}
	})
	return shellquote.Join(append(words, "--yes")...)
}

func confirmTestdrive(input io.Reader, output io.Writer, interactive bool, rerun string) (bool, error) {
	if !interactive {
		return false, fmt.Errorf("standard input is not interactive; review the preview and run `%s`", rerun)
	}

	_, _ = fmt.Fprint(output, "\nContinue? [y/N] (coding agents can rerun with --yes): ")
	answer, err := bufio.NewReader(input).ReadString('\n')
	if err != nil && err != io.EOF {
		return false, fmt.Errorf("read confirmation: %w", err)
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes", nil
}

func isTerminal(input io.Reader) bool {
	file, ok := input.(*os.File)
	if !ok {
		return false
	}
	return fileIsTerminal(file)
}
