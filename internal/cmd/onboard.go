// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package cmd

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testdrive"
	"github.com/spf13/cobra"
)

var onboardCmd = newOnboardCommand(nil)

func newOnboardCommand(run func(io.Writer) error) *cobra.Command {
	var apply, yes, instructions, allowDirty, agent bool
	var version, facts, site, format, resume, ciProvider string
	var timeout time.Duration
	var reviewed, substitute bool
	var ciRuntimes, ciFacts []string
	command := &cobra.Command{
		Use:   "onboard",
		Short: "Start here: onboard this repository to Test Optimization",
		Long:  "Detects the test setup and returns one structured next action. --apply --yes owns recognized Node CLI CI setup, full testdrive, package exclusions and cleanup. Unfamiliar integrations return a precise blocker.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			structured := agent || cmd.Flags().Changed("format")
			fail := func(code string, err error) error {
				if structured {
					return protocolFailure(cmd, "preparation", code, err, nil)
				}
				return err
			}
			if structured {
				cmd.SilenceErrors = true
			}
			if format != "agent" && format != "json" {
				return fail("invalid_options", fmt.Errorf("--format must be agent or json"))
			}
			if resume != "" && !apply {
				return fail("invalid_options", fmt.Errorf("--resume requires --apply --yes"))
			}
			if apply && instructions {
				return fail("invalid_options", fmt.Errorf("--apply and --instructions are mutually exclusive"))
			}
			if err := resolveCIProvider(cmd, ciProvider); err != nil {
				// Without --instructions, onboarding answers non-terminals in the agent protocol.
				return ciProviderFailure(cmd, structured || (!instructions && !apply && !isTerminalWriter(cmd.OutOrStdout())), err)
			}
			if err := resolveCIStatements(ciRuntimes, ciFacts); err != nil {
				return ciStatementFailure(cmd, structured || (!instructions && !apply && !isTerminalWriter(cmd.OutOrStdout())), err)
			}
			// start returns the onboarding handoff: the next command, or for a
			// language whose CI setup the agent applies, the setup to apply first.
			start := func() error {
				selectedFormat := ""
				if structured {
					selectedFormat = format
				}
				selectedTracer := ""
				if cmd.Flags().Changed("tracer-version") {
					selectedTracer = version
				}
				selectedTimeout := ""
				if cmd.Flags().Changed("timeout") {
					selectedTimeout = timeout.String()
				}
				return onboard.StartWithOptions(cmd.OutOrStdout(), onboard.StartOptions{Timeout: selectedTimeout, AllowReviewedBuilds: reviewed, AcceptSubstituteRuntime: substitute, Format: selectedFormat, Site: site, Tracer: selectedTracer, Facts: facts, Platform: settings.GetPlatform(), Command: settings.GetCommand(), Human: isTerminalWriter(cmd.OutOrStdout()) && !structured})
			}
			if instructions {
				if structured || facts != "" || site != "" || cmd.Flags().Changed("tracer-version") {
					return fail("invalid_options", fmt.Errorf("--instructions cannot be combined with structured output or setup options"))
				}
				return onboard.Run(cmd.OutOrStdout())
			}
			if apply {
				cmd.SilenceUsage = true
				detected, err := platform.DetectPlatform()
				if err != nil {
					return fail("validation_preparation", err)
				}
				if detected.Name() != "javascript" && testdrive.Coordinated(detected.Name()) || detected.Name() == "javascript" && !onboard.CIProviderEditsSetup() {
					// ddtest writes no CI setup for this language, or on this
					// provider; the agent applies it from the handoff, which then
					// resumes coordinated validation.
					return start()
				}
				if detected.Name() != "javascript" {
					return fail("framework_unsupported", fmt.Errorf("--apply supports JavaScript frameworks (%s); %s users should use onboard instructions and testdrive", testdrive.AutomaticSetupFrameworkNames(), detected.Name()))
				}
				if site == "" {
					site = os.Getenv("DD_SITE")
				}
				execution, err := testdrive.Prepare(testdrive.PrepareOptions{TracerVersion: version, All: true, Timeout: timeout, AllowReviewedBuilds: reviewed, AcceptSubstituteRuntime: substitute})
				if err != nil {
					return fail("validation_preparation", err)
				}
				if !yes {
					if structured {
						return protocolFailure(cmd, "consent", "consent_required", fmt.Errorf("review setup with plain onboard --apply, then confirm with --yes"), confirmedCommand(cmd))
					}
					return execution.Apply(commandContext(cmd), cmd.OutOrStdout(), testdrive.ApplyOptions{Site: site, ExplicitTracer: cmd.Flags().Changed("tracer-version"), Preview: true, FactsFile: facts})
				}
				cmd.SilenceErrors = true
				mode := format
				if !structured {
					mode = "verbose"
				}
				return execution.Workflow(commandContext(cmd), cmd.OutOrStdout(), testdrive.WorkflowOptions{AllowDirty: allowDirty, Resume: resume, Apply: true, ExplicitVersion: cmd.Flags().Changed("tracer-version"), FactsFile: facts, Site: site, Format: mode})
			}
			if yes || allowDirty {
				return fail("invalid_options", fmt.Errorf("--yes and --allow-dirty require --apply"))
			}
			if run != nil {
				return run(cmd.OutOrStdout())
			}
			return start()
		},
	}
	command.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "Maximum duration per test or preparation command")
	command.Flags().BoolVar(&reviewed, "allow-reviewed-builds", false, "Allow explicitly reviewed preparation in isolated copies")
	command.Flags().BoolVar(&substitute, "accept-substitute-runtime", false, "Complete validation on a compatible local Node when CI uses a different major; ask the user first")
	command.Flags().BoolVar(&agent, "agent", false, "Return exactly one structured result for a coding agent")
	command.Flags().BoolVar(&apply, "apply", false, "Apply recognized Node CLI CI setup and run full local validation; unsupported layouts return a precise blocker")
	command.Flags().BoolVar(&yes, "yes", false, "Allow the declared source edits and local test execution")
	command.Flags().BoolVar(&instructions, "instructions", false, "Print the detailed manual onboarding guidance")
	command.Flags().StringVar(&version, "tracer-version", "latest", "Fallback tracer selector; installed project and existing CI tracers remain authoritative")
	command.Flags().StringVar(&resume, "resume", "", "Resume a bounded workflow checkpoint; tests always execute fresh")
	command.Flags().StringVar(&facts, "facts", "", "JSON build/service facts bound to input_fingerprint; must be outside the checkout")
	command.Flags().StringVar(&site, "site", "", "Datadog site for a new action (default DD_SITE, otherwise datadoghq.com)")
	command.Flags().StringVar(&format, "format", "agent", "Result rendering: agent or json for direct host consumption")
	command.Flags().StringVar(&ciProvider, "ci-provider", "", ciProviderFlagUsage())
	ciStatementFlags(command, &ciRuntimes, &ciFacts)

	command.Flags().BoolVar(&allowDirty, "allow-dirty", false, "Explicitly extend uncommitted target-file edits after showing their exact diff")
	agentHelp(command)
	configureProtocolErrors(command)
	return command
}

func isTerminalWriter(output io.Writer) bool {
	file, ok := output.(*os.File)
	return ok && fileIsTerminal(file)
}
