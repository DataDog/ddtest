// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/testdrive"
	"github.com/spf13/cobra"
)

var onboardCmd = newOnboardCommand(func(output io.Writer) error {
	return onboard.Start(output)
})

func newOnboardCommand(run func(io.Writer) error) *cobra.Command {
	var apply, yes, instructions bool
	var version, facts, site, format, resume string
	command := &cobra.Command{
		Use:   "onboard",
		Short: "Start here: onboard this repository to Test Optimization",
		Long:  "Detects the test setup and returns one structured next action. --apply --yes owns recognized Node CLI CI setup, full testdrive, package exclusions and cleanup. Unfamiliar integrations return a precise blocker.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			if format != "agent" && format != "json" {
				return fmt.Errorf("--format must be agent or json")
			}
			if resume != "" && !apply {
				return fmt.Errorf("--resume requires --apply --yes")
			}
			if instructions {
				return onboard.Run(cmd.OutOrStdout())
			}
			if apply {
				if site == "" {
					site = os.Getenv("DD_SITE")
				}
				if !yes {
					return fmt.Errorf("--apply edits recognized CI/ignore files and runs repository tests locally; specify --yes to execute")
				}
				execution, err := testdrive.Prepare(version, false, true)
				if err != nil {
					return err
				}
				cmd.SilenceErrors = true
				return execution.Workflow(commandContext(cmd), cmd.OutOrStdout(), testdrive.WorkflowOptions{Resume: resume, Apply: true, ExplicitVersion: cmd.Flags().Changed("tracer-version"), FactsFile: facts, Site: site, Format: format})
			}
			return run(cmd.OutOrStdout())
		},
	}
	command.Flags().BoolVar(&apply, "apply", false, "Apply recognized Node CLI CI setup and run full local validation; unsupported layouts return a precise blocker")
	command.Flags().BoolVar(&yes, "yes", false, "Allow the declared source edits and local test execution")
	command.Flags().BoolVar(&instructions, "instructions", false, "Print the detailed manual onboarding guidance")
	command.Flags().StringVar(&version, "tracer-version", "latest", "Fallback tracer selector; installed project and existing CI tracers remain authoritative")
	command.Flags().StringVar(&resume, "resume", "", "Resume a bounded workflow checkpoint; tests always execute fresh")
	command.Flags().StringVar(&facts, "facts", "", "JSON build/service facts bound to input_fingerprint; must be outside the checkout")
	command.Flags().StringVar(&site, "site", "", "Datadog site for a new action (default DD_SITE, otherwise datadoghq.com)")
	command.Flags().StringVar(&format, "format", "agent", "Result rendering: agent or json for direct host consumption")
	command.MarkFlagsMutuallyExclusive("apply", "instructions")
	command.MarkFlagsRequiredTogether("apply", "yes")
	agentHelp(command)
	return command
}
