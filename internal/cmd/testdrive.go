// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/DataDog/ddtest/internal/testdrive"
	"github.com/spf13/cobra"
)

type testdriveExecution interface {
	Preview(io.Writer)
	Run(context.Context, io.Writer) error
}

type prepareTestdrive func(string, bool) (testdriveExecution, error)

var testdriveCmd = newTestdriveCommand(func(version string, checkOnly bool) (testdriveExecution, error) {
	return testdrive.Prepare(version, checkOnly)
})

func newTestdriveCommand(prepare prepareTestdrive) *cobra.Command {
	command := &cobra.Command{
		Use:   "testdrive",
		Short: "Try Test Optimization on the local test suite",
		Long:  "Validates Jest compatibility with paired runs and controlled feature scenarios against a local intake. Other frameworks collect telemetry but remain unvalidated. No Datadog API key is required.",
		Args:  cobra.NoArgs,
	}
	var version string
	var checkOnly bool
	command.Flags().BoolVar(&checkOnly, "check-only", false, "Check Jest and static CI configuration without installing a tracer or running tests")
	command.Flags().StringVar(&version, "tracer-version", "latest", "Fallback tracer release or git:<commit-or-ref>, used only when the project has no tracer")
	command.Flags().Bool("yes", false, "Run after printing the changes and commands")
	command.RunE = func(cmd *cobra.Command, _ []string) error {
		execution, err := prepare(version, checkOnly)
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
			confirmed, err := confirmTestdrive(cmd.InOrStdin(), output, isTerminal(cmd.InOrStdin()))
			if err != nil {
				return err
			}
			if !confirmed {
				_, _ = fmt.Fprintln(output, "Testdrive cancelled.")
				return nil
			}
		}

		cmd.SilenceUsage = true // Runtime validation results are not command syntax errors.
		return execution.Run(commandContext(cmd), output)
	}
	return command
}

func confirmTestdrive(input io.Reader, output io.Writer, interactive bool) (bool, error) {
	if !interactive {
		return false, fmt.Errorf("standard input is not interactive; review the preview and run `ddtest testdrive --yes`")
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
