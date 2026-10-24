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
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive"
	"github.com/spf13/cobra"
)

var onboardCmd = newOnboardCommand(func(output io.Writer) error {
	return onboard.Run(output)
})

func newOnboardCommand(run func(io.Writer) error) *cobra.Command {
	var apply, yes, allowDirty bool
	var version, site string
	command := &cobra.Command{
		Use:   "onboard",
		Short: "Start here: onboard this repository to Test Optimization",
		Long:  "Detects the supported test setup and prints the smallest Datadog Test Optimization onboarding instructions. Plain onboard does not edit files. --apply previews exact edits without executing tests. --apply --yes applies recognized CI setup and runs full local validation.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if apply {
				cmd.SilenceUsage = true
				detected, err := platform.DetectPlatform()
				if err != nil {
					return err
				}
				if detected.Name() != "javascript" {
					return fmt.Errorf("--apply supports JavaScript frameworks (%s); %s users should use onboard instructions and testdrive", testdrive.AutomaticSetupFrameworkNames(), detected.Name())
				}
				if site == "" {
					site = os.Getenv("DD_SITE")
				}
				drive, err := testdrive.Prepare(testdrive.PrepareOptions{TracerVersion: version, All: true})
				if err != nil {
					return err
				}
				return drive.Apply(cmd.Context(), cmd.OutOrStdout(), testdrive.ApplyOptions{Site: site, ExplicitTracer: cmd.Flags().Changed("tracer-version"), Preview: !yes, AllowDirty: allowDirty})
			}
			if yes || allowDirty {
				return fmt.Errorf("--yes and --allow-dirty require --apply")
			}
			return run(cmd.OutOrStdout())
		},
	}
	command.Flags().BoolVar(&apply, "apply", false, "Apply recognized JavaScript CI setup and run full local validation")
	command.Flags().BoolVar(&yes, "yes", false, "Allow declared source edits and local test execution")
	command.Flags().StringVar(&version, "tracer-version", "latest", "Fallback tracer selector; existing project and CI selections remain authoritative")
	command.Flags().StringVar(&site, "site", "", "Site for a new Datadog action (default DD_SITE, then datadoghq.com)")
	command.Flags().BoolVar(&allowDirty, "allow-dirty", false, "Explicitly extend uncommitted target-file edits after showing their exact diff")

	return command

}
