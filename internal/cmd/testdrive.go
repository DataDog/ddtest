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
	"time"

	"github.com/DataDog/ddtest/internal/platform"
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
	var version, facts, format, resume, detail, site string
	var checkOnly, agent, report bool
	var all bool
	var allowReviewedBuilds, acceptSubstituteRuntime bool
	var timeout time.Duration
	command.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "Maximum duration of each subprocess, including each full test-suite run")
	command.Flags().BoolVar(&allowReviewedBuilds, "allow-reviewed-builds", false, "Allow reviewed bundlers, package build lifecycle scripts and dependency changes in isolated copies (may use the network)")
	command.Flags().BoolVar(&acceptSubstituteRuntime, "accept-substitute-runtime", false, "Complete validation on a compatible local Node when CI uses a different major; ask the user first")
	command.Flags().BoolVar(&all, "all", false, "Validate every discovered "+testdrive.ValidationFrameworkNames()+" CI configuration, preserving environment and known build prerequisites")
	command.Flags().BoolVar(&checkOnly, "check-only", false, "Check "+testdrive.ValidationFrameworkNames()+" configuration, tracer selection and static CI runtimes without installing a tracer or running tests")
	command.Flags().StringVar(&version, "tracer-version", "latest", "Fallback tracer release or git:<commit-or-ref>, used only when the project has no tracer; defaults to the release every instrumented CI job pins, else latest")
	command.Flags().Bool("yes", false, "Run after printing the changes and commands")
	command.Flags().BoolVar(&agent, "agent", false, "Use the shared coordinator and compact agent result protocol")
	command.Flags().BoolVar(&report, "report", false, "Render the saved result directly, verifying its source/runtime fingerprint; run no tests")
	command.Flags().StringVar(&format, "format", "agent", "Result rendering: agent or json for host consumption")
	command.Flags().StringVar(&detail, "detail", "", "With --report, inspect the saved plan or a configuration ID without executing tests")
	command.Flags().StringVar(&resume, "resume", "", "Resume the same repository/framework/scope; tests always execute fresh")
	command.Flags().StringVar(&facts, "facts", "", "JSON build/service facts bound to input_fingerprint (requires --all)")
	command.Flags().StringVar(&site, "site", "", "Datadog site retained in the resolved validation plan")

	command.RunE = func(cmd *cobra.Command, _ []string) error {
		structured := agent || cmd.Flags().Changed("format")
		fail := func(code string, err error) error {
			if structured {
				return protocolFailure(cmd, "preparation", code, err, nil)
			}
			return err
		}
		if structured {
			cmd.SilenceErrors, cmd.SilenceUsage = true, true
		}

		if format != "agent" && format != "json" {
			return fail("invalid_options", fmt.Errorf("--format must be agent or json"))
		}
		if detail != "" && !report {
			return fail("invalid_options", fmt.Errorf("--detail requires --report"))
		}
		if resume != "" && detail == "" && (!structured || report || checkOnly) {
			return fail("invalid_options", fmt.Errorf("--resume requires --agent and full execution"))
		}
		if report && (all || checkOnly) {
			return fail("invalid_options", fmt.Errorf("--report cannot be combined with --all or --check-only"))
		}
		if report {
			cmd.SilenceErrors = true
			if detail != "" {
				return testdrive.RenderWorkflowDetail(commandContext(cmd), cmd.OutOrStdout(), resume, detail)
			}
			return testdrive.RenderReport(commandContext(cmd), cmd.OutOrStdout(), format)
		}
		if facts != "" && !all {
			return fail("invalid_options", fmt.Errorf("--facts requires --all; facts cannot replace or narrow the discovered scope"))
		}
		if structured {
			if detected, err := platform.DetectPlatform(); err == nil && detected.Name() != "javascript" && detected.Name() != "ruby" {
				return fail("framework_unsupported", fmt.Errorf("controlled validation is not available for %s", detected.Name()))
			}
		}

		if structured {
			yes, _ := cmd.Flags().GetBool("yes")
			if !yes {
				return protocolFailure(cmd, "consent", "consent_required", fmt.Errorf("--yes is required for declared local execution"), confirmedCommand(cmd))
			}
		}

		execution, err := testdrive.Prepare(testdrive.PrepareOptions{TracerVersion: version, DefaultTracerVersion: !cmd.Flags().Changed("tracer-version"), CheckOnly: checkOnly, All: all, Timeout: timeout, AllowReviewedBuilds: allowReviewedBuilds, AcceptSubstituteRuntime: acceptSubstituteRuntime})
		if err != nil {
			return fail("validation_preparation", err)
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
			confirmed, err := confirmTestdrive(cmd.InOrStdin(), output, isTerminal(cmd.InOrStdin()), rerunWithYes(cmd))
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
		if mode == "verbose" && facts == "" {
			return execution.Run(commandContext(cmd), output)
		}
		return execution.Workflow(commandContext(cmd), output, testdrive.WorkflowOptions{Resume: resume, ExplicitVersion: cmd.Flags().Changed("tracer-version"), FactsFile: facts, Site: site, Format: mode})
	}
	agentHelp(command)
	configureProtocolErrors(command)
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
