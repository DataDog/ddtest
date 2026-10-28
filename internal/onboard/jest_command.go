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
func JestExecutionCommand(root string) (TestCommand, error) { return selectJestCommand(root, true) }

func JestValidationCommand(root string) (string, error) {
	entry, err := selectJestCommand(root, true)
	return entry.Command, err
}

func selectJestCommand(root string, forward bool) (TestCommand, error) {
	workflows, err := readWorkflows(root)
	if err != nil {
		return TestCommand{}, err
	}
	candidates := map[string]TestCommand{}
	add := func(entry TestCommand) { data, _ := json.Marshal(entry); candidates[string(data)] = entry }
	var unresolved string
	for _, workflow := range workflows {
		for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			job := workflow.Jobs[name]
			rows, err := runtimeMatrix(job.Strategy.Matrix)
			if err != nil {
				return TestCommand{}, err
			}
			for _, row := range rows {
				active, err := possibleJobCondition(job.If, row)
				if err != nil {
					return TestCommand{}, err
				}
				if !active {
					continue
				}
				for _, step := range job.Steps {
					active, err := stepCondition(step, row, conditionEnvironment(workflow, job, runtimeStep{}, row))
					if err != nil {
						return TestCommand{}, err
					}
					if !active {
						continue
					}
					// Select the command CI runs for this row, including its
					// environment, so consumers differing only there stay distinct.
					resolved, env, envErr := resolveVariantStep(workflow, job, step, row, "jest")
					resolution := resolveTestStep(root, workflow, job, resolved, "javascript", "jest")
					if envErr != nil && !resolution.Review {
						resolution.Reason = joinDiagnostic(resolution.Reason, envErr.Error())
					}
					if resolution.Reason != "" && !resolution.Review {
						unresolved = fmt.Sprintf("CI command %q requires review: %s", step.Run, resolution.Reason)
					}
					if !resolution.Matched {
						continue
					}
					if resolution.Reason != "" {
						return TestCommand{}, fmt.Errorf("CI command %q requires review: %s; use --command with its working directory and required setup", step.Run, resolution.Reason)
					}
					words, err := shellquote.Split(resolved.Run)
					if err != nil || len(words) == 0 || filepath.Clean(stepDirectory(workflow, job, resolved)) != "." {
						return TestCommand{}, fmt.Errorf("cannot automatically forward Jest options through CI command %q; use --command with its working directory and required setup", step.Run)
					}
					if !forward {
						entry, err := configurationEntry(resolved.Run, resolution)
						if err != nil {
							return TestCommand{}, err
						}
						entry.Environment = WithCIProviderDefaults(mergeEnvironment(env, entry.Environment))
						// CI explicitly clearing/preloading NODE_OPTIONS must not inherit local flags.
						for _, layer := range []map[string]string{workflow.Env, job.Env, step.Env} {
							if _, exists := layer["NODE_OPTIONS"]; exists {
								if entry.Environment == nil {
									entry.Environment = map[string]string{}
								}
								if _, kept := entry.Environment["NODE_OPTIONS"]; !kept {
									entry.Environment["NODE_OPTIONS"] = ""
								}
							}
						}
						add(entry)
						continue
					}
					selected, err := ForwardJestExecution(root, resolved.Run)
					if err != nil {
						return TestCommand{}, err
					}
					var assignments []string
					for _, key := range slices.Sorted(maps.Keys(env)) {
						// NODE_OPTIONS can't be a command assignment: testdrive adds its
						// own preload to it. An empty value means CI only preloaded the
						// tracer; keep its other options in the plan's environment.
						if key == "NODE_OPTIONS" {
							if env[key] == "" {
								continue
							}
							if selected.Environment == nil {
								selected.Environment = map[string]string{}
							}
							selected.Environment[key] = env[key]
							continue
						}
						assignments = append(assignments, key+"="+env[key])
					}
					if len(assignments) > 0 {
						selected.Command = shellCommand(assignments) + " " + selected.Command
					}
					add(selected)
				}
			}
		}
	}
	if len(candidates) > 1 {
		return TestCommand{}, fmt.Errorf("multiple Jest CI commands found: %s; choose one with --command and validate the others separately", strings.Join(slices.Sorted(maps.Keys(candidates)), "; "))
	}
	for _, entry := range candidates {
		return entry, nil
	}
	// A known invocation can be validated locally even when other CI entry
	// points are unknown. CheckCIRuntimes still reports those independently.
	// Without a known CI command, do not guess past an unresolved wrapper.
	if unresolved != "" {
		return TestCommand{}, fmt.Errorf("%s; use --command after reviewing required setup", unresolved)
	}
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return TestCommand{}, err
	}
	var manifest struct {
		Scripts        map[string]string `json:"scripts"`
		PackageManager string            `json:"packageManager"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return TestCommand{}, err
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
		return TestCommand{}, fmt.Errorf("unknown package manager %q; select the Jest command with --command", manager)
	}
	for _, name := range []string{"test:ci", "test"} {
		if manifest.Scripts[name] == "" {
			continue
		}
		command := manager + " run " + name
		resolution := resolveJestCommand(root, command, nil)
		if resolution.Matched && resolution.Reason == "" {
			if !forward {
				return configurationEntry(command, resolution)
			}
			return ForwardJestExecution(root, command)
		}
		if resolution.Matched || resolution.Reason != "" {
			return TestCommand{}, fmt.Errorf("package script %q requires review before forwarding Jest options; use --command with the actual Jest invocation and setup", name)
		}
	}
	return TestCommand{}, nil
}
