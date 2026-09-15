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

var onboardCmd = newOnboardCommand(onboard.Run)

func newOnboardCommand(run func(string, io.Writer) error) *cobra.Command {
	return &cobra.Command{
		Use:   "onboard",
		Short: "Show how to enable Datadog Test Optimization",
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
