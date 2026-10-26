// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package cmd

import "github.com/spf13/cobra"

// Onboarding entry points do not print unrelated planner/worker tuning flags.
func agentHelp(command *cobra.Command) {
	command.SetHelpTemplate(`{{.Long}}

Usage: {{.UseLine}}

Flags:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}

Scope: --framework <name>, --platform <name>, --command '<actual test command>'.
Other global options: ddtest --help.
`)
}
