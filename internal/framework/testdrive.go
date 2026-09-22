// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package framework

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/settings"
	"github.com/kballard/go-shellquote"
)

// TestdriveCommand uses a project's package script when available, preserving
// its setup and runner configuration. Otherwise it uses the existing runner.
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
	manifest, found, err := readPackageManifest(root)
	if err != nil {
		return "", nil, err
	}
	if found {
		var scripts []string
		for name, script := range manifest.Scripts {
			if strings.Contains(script, runner.Name()) {
				scripts = append(scripts, name)
			}
		}
		slices.Sort(scripts)
		if slices.Contains(scripts, "test") {
			scripts = []string{"test"}
		}
		if len(scripts) == 1 {
			manager := "npm"
			for _, lock := range []struct{ file, manager string }{{"pnpm-lock.yaml", "pnpm"}, {"yarn.lock", "yarn"}, {"bun.lock", "bun"}, {"bun.lockb", "bun"}} {
				if _, err := os.Stat(filepath.Join(root, lock.file)); err == nil {
					manager = lock.manager
					break
				}
			}
			args := []string{"run", scripts[0]}
			if manager == "npm" && scripts[0] == "test" {
				args = []string{"test"}
			}
			extra := []string{}
			if runner.Name() == "jest" {
				extra = append(extra, "--runInBand")
			}
			if runner.Name() == "vitest" {
				extra = append(extra, "--run")
			}
			if len(extra) > 0 && manager == "npm" {
				args = append(args, "--")
			}
			return manager, append(args, extra...), nil
		}
		if len(scripts) > 1 {
			return "", nil, fmt.Errorf("multiple %s scripts (%s); choose the test entry point with --command", runner.Name(), strings.Join(scripts, ", "))
		}
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
	case *PyTest:
		interpreter := "python"
		if _, err := exec.LookPath(interpreter); err != nil {
			interpreter = "python3"
		}
		return interpreter, []string{"-m", "pytest"}, nil
	case *RSpec:
		command, args := f.getRSpecCommand()
		return command, args, nil
	case *Minitest:
		if _, err := os.Stat(filepath.Join(root, "bin", "rails")); err == nil {
			return "bin/rails", []string{"test"}, nil
		}
		return "bundle", []string{"exec", "rake", "test"}, nil
	default:
		return "", nil, fmt.Errorf("unsupported testdrive framework: %s", runner.Name())
	}
}
