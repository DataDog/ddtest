// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package framework

import (
	"fmt"
	"strings"

	"github.com/DataDog/ddtest/internal/settings"
	"github.com/kballard/go-shellquote"
)

// TestdriveCommand uses the explicit --command override or the framework's
// normal command, without inferring overrides from package scripts.
// Detection and preview must not launch subprocesses.
func TestdriveCommand(root string, runner Framework) (string, []string, error) {
	if command := strings.TrimSpace(settings.GetCommand()); command != "" {
		override, err := shellquote.Split(command)
		if err != nil {
			return "", nil, fmt.Errorf("parse testdrive --command: %w", err)
		}
		if len(override) == 0 {
			return "", nil, fmt.Errorf("testdrive --command is empty")
		}
		return override[0], override[1:], nil
	}
	switch f := runner.(type) {
	case *Jest:
		command, args := f.getJestCommand()
		return command, args, nil
	case *Mocha:
		command, args := f.getMochaCommand()
		return command, args, nil
	case *Cucumber:
		command, args := f.getCucumberCommand()
		return command, args, nil
	case *Vitest:
		command, args := f.getVitestCommand()
		return command, vitestArgsForSubcommand(args, "run"), nil
	case *Playwright:
		command, args := f.getPlaywrightCommand()
		return command, playwrightRunArgs(command, args, nil), nil
	case *Cypress:
		command, args := f.getCypressCommand()
		return command, cypressRunArgs(command, args, nil), nil

	default:
		return "", nil, fmt.Errorf("unsupported testdrive framework: %s", runner.Name())
	}
}
