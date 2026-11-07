// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package cmd

import (
	"context"
	"io"

	"github.com/DataDog/ddtest/internal/buildinfo"
	"github.com/DataDog/ddtest/internal/usagetelemetry"
	"github.com/spf13/cobra"
)

var newUsageAttempt = func(command, mode string) *usagetelemetry.Attempt {
	return usagetelemetry.Start(command, mode, usagetelemetry.Config{
		Version: buildinfo.CurrentVersion(), ClientToken: usagetelemetry.ClientToken, IntakeSite: usagetelemetry.IntakeSite,
	})
}

type usageCommandKey struct{}

type usageCommand struct {
	command *cobra.Command
	attempt *usagetelemetry.Attempt
}

type usageOutput struct {
	io.Writer
	*usagetelemetry.Attempt
}

// executeWithUsage owns the attempt through argument, prerequisite and RunE
// failures. Cobra post-run hooks do not run when any of those return an error.
func executeWithUsage(ctx context.Context, root *cobra.Command) error {
	usage := &usageCommand{}
	ctx = context.WithValue(ctx, usageCommandKey{}, usage)
	err := root.ExecuteContext(ctx)
	if usage.command != nil {
		usage.attempt.Finish(ExitCode(err), err != nil && ctx.Err() != nil)
	}
	return err
}

// beginUsage runs during argument validation (after help/version handling), or
// in the flag-error handler. Both precede prerequisite checks and source edits.
func beginUsage(command *cobra.Command) {
	usage, _ := commandContext(command.Root()).Value(usageCommandKey{}).(*usageCommand)
	if usage == nil || usage.command != nil {
		return
	}
	usage.command = command
	usage.attempt = newUsageAttempt(command.Name(), usageMode(command))
}

func usageMode(command *cobra.Command) string {
	flag := func(name string) bool { value, _ := command.Flags().GetBool(name); return value }
	if command.Name() == "onboard" {
		if flag("instructions") {
			return "instructions"
		}
		if flag("apply") {
			return "apply"
		}
		return "discovery"
	}
	if flag("report") {
		return "report"
	}
	if flag("check-only") {
		return "configuration_check"
	}
	return "validation"
}

func observeUsage(command *cobra.Command, status, phase, code string) {
	if usage, ok := commandContext(command.Root()).Value(usageCommandKey{}).(*usageCommand); ok {
		usage.attempt.ObserveResult(status, phase, code)
	}
}

// usageWriter adds typed result observation without changing Cobra's writer or
// its terminal detection. It forwards only writes, never reads rendered output.
func usageWriter(command *cobra.Command) io.Writer {
	output := command.OutOrStdout()
	usage, _ := commandContext(command.Root()).Value(usageCommandKey{}).(*usageCommand)
	if usage == nil || usage.attempt == nil {
		return output
	}
	return &usageOutput{Writer: output, Attempt: usage.attempt}
}
