// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package cmd

import (
	"io"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/spf13/cobra"
)

var onboardCmd = newOnboardCommand(func(output io.Writer) error {
	return onboard.Run(output)
})

func newOnboardCommand(run func(io.Writer) error) *cobra.Command {
	return &cobra.Command{
		Use:   "onboard",
		Short: "Start here: onboard this repository to Test Optimization",
		Long:  "Detects the supported test setup and prints the smallest Datadog Test Optimization onboarding instructions. It does not edit files.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd.OutOrStdout())
		},
	}
}
