// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/cobra"
)

func chooseCIProvider(cmd *cobra.Command) error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	_, err = onboard.SelectCIProvider(root, settings.GetCIProvider())
	var choice *onboard.CIProviderChoice
	if !errors.As(err, &choice) || usesProtocol(cmd) || !isTerminal(cmd.InOrStdin()) || !isTerminalWriter(cmd.OutOrStdout()) {
		return err
	}
	for i, provider := range choice.Providers {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%d. %s\n", i+1, provider)
	}
	_, _ = fmt.Fprint(cmd.OutOrStdout(), "Which CI provider should Test Optimization be installed on? ")
	answer, readErr := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if readErr != nil {
		return readErr
	}
	selected := strings.TrimSpace(answer)
	if number, e := strconv.Atoi(selected); e == nil && number > 0 && number <= len(choice.Providers) {
		selected = choice.Providers[number-1]
	}
	if !slices.Contains(choice.Providers, selected) {
		return fmt.Errorf("choose one of %s with --ci-provider", strings.Join(choice.Providers, ", "))
	}
	settings.Get().CIProvider = selected
	return nil
}
