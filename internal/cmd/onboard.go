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
	"github.com/spf13/cobra"
)

var onboardCmd = newOnboardCommand(func(root string, output io.Writer) error {
	return onboard.RunWithFramework(root, onboardingFramework(), output)
})

func onboardingFramework() string {
	if rootCmd.PersistentFlags().Changed("framework") {
		name, _ := rootCmd.PersistentFlags().GetString("framework")
		return name
	}
	return ""
}

func newOnboardCommand(run func(string, io.Writer) error) *cobra.Command {
	return &cobra.Command{
		Use:   "onboard",
		Short: "Start here: onboard this repository to Test Optimization",
		Long:  "Detects the supported test setup and prints the smallest Datadog Test Optimization onboarding instructions. It does not edit files.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			repositoryRoot, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("find repository root: %w", err)
			}
			return run(repositoryRoot, cmd.OutOrStdout())
		},
	}
}
