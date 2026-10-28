// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/spf13/cobra"
)

// selectCIProvider resolves the CI provider; tests replace it.
var selectCIProvider = onboard.SelectCIProvider

// ciProviderFlagUsage describes --ci-provider on onboard and testdrive.
func ciProviderFlagUsage() string {
	return "CI provider whose configuration to discover, set up and validate (" + strings.Join(onboard.CIProviderNames(), ", ") + "); required when several contain tests"
}

// invalidCIProvider is a --ci-provider value no provider registers.
type invalidCIProvider struct{ error }

// resolveCIProvider selects the CI provider once per command, before any
// discovery, edit or validation, and keeps it for follow-up commands and
// input fingerprints. When several providers contain tests, a person at a
// terminal chooses one; otherwise the *onboard.CIProviderChoice is returned.
func resolveCIProvider(cmd *cobra.Command, requested string) error {
	onboard.UseCIProvider("")
	if requested != "" && !slices.Contains(onboard.CIProviderNames(), requested) {
		return invalidCIProvider{onboard.UnknownCIProviderError(requested)}
	}
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("find repository root: %w", err)
	}
	selected, err := selectCIProvider(root, requested)
	var choice *onboard.CIProviderChoice
	if errors.As(err, &choice) && !usesProtocol(cmd) && isTerminal(cmd.InOrStdin()) && isTerminalWriter(cmd.OutOrStdout()) {
		selected, err = promptCIProvider(cmd.InOrStdin(), cmd.OutOrStdout(), choice)
	}
	if err != nil {
		return err
	}
	onboard.UseCIProvider(selected)
	return nil
}

// promptCIProvider asks a person which provider to configure, by number or name.
func promptCIProvider(input io.Reader, output io.Writer, choice *onboard.CIProviderChoice) (string, error) {
	_, _ = fmt.Fprintln(output, "Several CI providers contain test commands:")
	for i, provider := range choice.Providers {
		_, _ = fmt.Fprintf(output, "  %d. %s\n", i+1, provider)
	}
	_, _ = fmt.Fprint(output, "Which CI provider should Test Optimization be installed on? ")
	answer, err := bufio.NewReader(input).ReadString('\n')
	if err != nil && answer == "" {
		return "", fmt.Errorf("choose one of %s with --ci-provider", strings.Join(choice.Providers, ", "))
	}
	selected := strings.TrimSpace(answer)
	if number, err := strconv.Atoi(selected); err == nil && number > 0 && number <= len(choice.Providers) {
		selected = choice.Providers[number-1]
	}
	if !slices.Contains(choice.Providers, selected) {
		return "", fmt.Errorf("choose one of %s with --ci-provider", strings.Join(choice.Providers, ", "))
	}
	return selected, nil
}

// ciProviderFailure renders a resolveCIProvider error: an invalid value is an
// invalid option, and a choice asks the user before anything runs.
func ciProviderFailure(cmd *cobra.Command, structured bool, err error) error {
	var invalid invalidCIProvider
	var choice *onboard.CIProviderChoice
	switch {
	case !structured:
		return err
	case errors.As(err, &invalid):
		return protocolFailure(cmd, "options", "invalid_options", invalid.error, nil)
	case errors.As(err, &choice):
		return protocolFailure(cmd, "discovery", "ci_provider_required", err, nil)
	default:
		return protocolFailure(cmd, "discovery", "ci_discovery", err, nil)
	}
}
