// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DataDog/ddtest/internal/settings"
)

// Testdrive runs the complete configured suite. Resolve its CLI from the
// project, rather than treating the selective adapter's runtime as a command.
//
//go:embed scripts/vitest.mjs
var vitestEntrypoint string

func vitestTestdriveCommand(sessionDirectory string) (string, []string, error) {
	if strings.TrimSpace(settings.GetCommand()) != "" {
		return "", nil, fmt.Errorf("testdrive for Vitest does not support --command; move options to vitest.config.ts or select a config with --vitest-config (see docs/running.md#vitest-integration)")
	}
	args := []string{filepath.Join(sessionDirectory, "vitest.mjs")}
	if config := settings.GetVitestConfig(); config != "" {
		args = append(args, "--config", config)
	}
	return "node", args, nil
}

func writeVitestEntrypoint(sessionDirectory string) error {
	if err := os.WriteFile(filepath.Join(sessionDirectory, "vitest.mjs"), []byte(vitestEntrypoint), 0600); err != nil {
		return fmt.Errorf("save Vitest testdrive entrypoint: %w", err)
	}
	return nil
}
