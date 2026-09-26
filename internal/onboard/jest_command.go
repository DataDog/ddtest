// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kballard/go-shellquote"
)

func stepDirectory(workflow ciWorkflow, job runtimeJob, step runtimeStep) string {
	directory := workflow.Defaults.Run.WorkingDirectory
	for _, value := range []string{job.Defaults.Run.WorkingDirectory, step.WorkingDirectory} {
		if value != "" {
			directory = value
		}
	}
	return directory
}

// JestValidationCommand selects a single root-level CI command without executing
// code. Package scripts must forward added Jest flags to exactly one runner.
// An empty result leaves repositories without a known script on the default.
func JestValidationCommand(root string) (string, error) {
	workflows, err := readWorkflows(root)
	if err != nil {
		return "", err
	}
	candidates := map[string]bool{}
	for _, workflow := range workflows {
		for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			job := workflow.Jobs[name]
			if strings.TrimSpace(job.If) == "false" {
				continue
			}
			for _, step := range job.Steps {
				if strings.TrimSpace(step.If) == "false" {
					continue
				}
				resolution := resolveTestStep(root, workflow, job, step, "javascript", "jest")
				if resolution.Reason != "" && !resolution.Review {
					return "", fmt.Errorf("CI command %q requires review before selecting a Jest invocation: %s; use --command after reviewing required setup", step.Run, resolution.Reason)
				}
				if !resolution.Matched {
					continue
				}
				words, err := shellquote.Split(step.Run)
				if err != nil || !resolution.SingleJest || resolution.Reason != "" || len(words) == 0 || strings.Contains(words[0], "=") || filepath.Clean(stepDirectory(workflow, job, step)) != "." {
					return "", fmt.Errorf("cannot automatically forward Jest options through CI command %q; use --command with its working directory and required setup", step.Run)
				}
				candidates[shellquote.Join(words...)] = true
			}
		}
	}
	if len(candidates) > 1 {
		return "", fmt.Errorf("multiple Jest CI commands found: %s; choose one with --command and validate the others separately", strings.Join(slices.Sorted(maps.Keys(candidates)), "; "))
	}
	for command := range candidates {
		return command, nil
	}
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return "", err
	}
	var manifest struct {
		Scripts        map[string]string `json:"scripts"`
		PackageManager string            `json:"packageManager"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", err
	}
	manager, _, _ := strings.Cut(manifest.PackageManager, "@")
	if manager == "" {
		manager = "npm"
		for _, entry := range []struct{ file, manager string }{{"pnpm-lock.yaml", "pnpm"}, {"yarn.lock", "yarn"}} {
			if _, err := os.Stat(filepath.Join(root, entry.file)); err == nil {
				manager = entry.manager
				break
			}
		}
	}
	if !slices.Contains([]string{"npm", "yarn", "pnpm", "bun"}, manager) {
		return "", fmt.Errorf("unknown package manager %q; select the Jest command with --command", manager)
	}
	for _, name := range []string{"test:ci", "test"} {
		if manifest.Scripts[name] == "" {
			continue
		}
		command := manager + " run " + name
		resolution := resolveJestCommand(root, command, nil)
		if resolution.SingleJest && resolution.Reason == "" {
			return command, nil
		}
		if resolution.Matched || resolution.Reason != "" {
			return "", fmt.Errorf("package script %q requires review before forwarding Jest options; use --command with the actual Jest invocation and setup", name)
		}
	}
	return "", nil
}
