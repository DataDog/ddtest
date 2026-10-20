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
	"github.com/spf13/cobra"
)

var testdriveCmd = newTestdriveCommand()

func newTestdriveCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "testdrive",
		Short: "Try Test Optimization on the local test suite",
		Long:  "Validates supported JavaScript and Maven JVM frameworks with paired runs and controlled feature scenarios against a local intake. Maven CI compatibility and other profiles require separate review. Python/Ruby retain the existing execution checks: exit 0 means the command succeeded and sent test events; agent/JSON workflows are unsupported. No Datadog API key is required.",
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.NoArgs(cmd, args); err != nil {
				return err
			}
			cmd.SilenceUsage = true
			return nil
		},
	}
	var version, facts, format, resume, detail string
	var checkOnly, all, agent, report bool
	command.Flags().BoolVar(&all, "all", false, "Run known chained build prerequisites, validate every discovered Jest/Vitest/Playwright/Mocha/Cypress/Cucumber/WebdriverIO CI command, and aggregate one report pair")
	command.Flags().BoolVar(&checkOnly, "check-only", false, "Inspect supported JavaScript or Maven configuration without installing a tracer or running tests; JavaScript also checks static CI runtimes")
	command.Flags().StringVar(&version, "tracer-version", "latest", "Fallback tracer release or git:<commit-or-ref>, used only when the project has no tracer")
	command.Flags().Bool("yes", false, "Run after printing the changes and commands")
	command.Flags().BoolVar(&agent, "agent", false, "Use the shared coordinator and compact agent result protocol")
	command.Flags().BoolVar(&report, "report", false, "Render the saved result directly, verifying its source/runtime fingerprint; run no tests")
	command.Flags().StringVar(&format, "format", "agent", "Result rendering: agent or json for host consumption")
	command.Flags().StringVar(&detail, "detail", "", "With --report, inspect the saved plan or a configuration ID without executing tests")
	command.Flags().StringVar(&resume, "resume", "", "Resume the same repository/framework/scope; tests always execute fresh")
	command.Flags().StringVar(&facts, "facts", "", "JSON build/service facts bound to input_fingerprint (requires --all)")
	command.MarkFlagsMutuallyExclusive("report", "check-only")
	command.MarkFlagsMutuallyExclusive("report", "all")
	command.RunE = func(cmd *cobra.Command, _ []string) error {
		if format != "agent" && format != "json" {
			return fmt.Errorf("--format must be agent or json")
		}
		if detail != "" && !report {
			return fmt.Errorf("--detail requires --report")
		}
		if resume != "" && detail == "" && (!agent || report || checkOnly) {
			return fmt.Errorf("--resume requires --agent and full execution")
		}
		if report {
			cmd.SilenceErrors = true
			if detail != "" {
				return testdrive.RenderWorkflowDetail(commandContext(cmd), cmd.OutOrStdout(), resume, detail)
			}
			return testdrive.RenderReport(commandContext(cmd), cmd.OutOrStdout(), format)
		}
		if facts != "" && !all {
			return fmt.Errorf("--facts requires --all; facts cannot replace or narrow the discovered scope")
		}
		if agent {
			yes, _ := cmd.Flags().GetBool("yes")
			if !yes {
				return fmt.Errorf("--agent requires --yes for declared test execution, loopback listeners and owned temporary storage")
			}
		}
		execution, err := testdrive.Prepare(version, checkOnly, all)
		if err != nil {
			return err
		}

		output := cmd.OutOrStdout()
		if !agent && !cmd.Flags().Changed("format") {
			execution.Preview(output)
		}
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

		mode := "verbose"
		if agent || cmd.Flags().Changed("format") {
			mode = format
			cmd.SilenceErrors = true
		}
		return execution.Workflow(commandContext(cmd), output, testdrive.WorkflowOptions{Resume: resume, ExplicitVersion: cmd.Flags().Changed("tracer-version"), FactsFile: facts, Format: mode})
	}
	agentHelp(command)
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
