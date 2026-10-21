// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Recognize only the documented scoped preload during discovery. Execution
// keeps the original alias; arbitrary instrumentation overrides remain unresolved.
const cucumberScopedOptions = `NODE_OPTIONS="${NODE_OPTIONS:-} ${DD_CUCUMBER_NODE_OPTIONS:-}"`

func cucumberDiscoveryScript(script string) string {
	return strings.ReplaceAll(script, cucumberScopedOptions, "")
}

func CucumberValidationCommand(root, command string) (string, error) {
	if strings.TrimSpace(command) == "" {
		scope, err := DiscoverValidationScope(root, "cucumber")
		if err != nil {
			return "", err
		}
		if len(scope.Commands) == 0 {
			return "", nil
		}
		if len(scope.Commands) != 1 || filepath.Clean(scope.Commands[0].Directory) != "." || len(scope.Commands[0].Prerequisites) > 0 || len(scope.Commands[0].Environment) > 0 {
			return "", fmt.Errorf("cucumber CI commands or build prerequisites require --all")
		}
		command = scope.Commands[0].Command
	}
	parts, err := staticCommands(command)
	if err != nil || len(parts) != 1 {
		return "", fmt.Errorf("cucumber command must forward formatter options to one invocation; review compound setup separately")
	}
	resolved := ValidationCommands(root, command, "cucumber")
	if len(resolved) != 1 || len(resolved[0].UnvalidatedSetup) > 0 {
		return "", fmt.Errorf("cucumber validation needs one resolved invocation with its profile and setup; use --all for CI commands")
	}
	return command, nil
}

// CucumberProbeCommand preserves the selected leaf's CLI configuration and
// support imports while avoiding unrelated npm test consumers.
func CucumberProbeCommand(root, command string) (string, error) {
	remaining := 256
	result := resolveJestSequence(root, root, command, nil, &remaining, "cucumber")
	if result.Reason != "" || len(result.Tests) != 1 || result.Tests[0].cucumberProbeCommand == "" {
		return "", fmt.Errorf("cucumber probe command could not be resolved: %s", result.Reason)
	}
	return result.Tests[0].cucumberProbeCommand, nil
}

func CucumberUsesNYC(root, command string) bool {
	commands := ValidationCommands(root, command, "cucumber")
	return len(commands) == 1 && commands[0].usesNYC
}

func checkCucumberBootstrap(root string, workflow ciWorkflow, job runtimeJob, step runtimeStep, row map[string]any, commands []TestCommand) string {
	// Scope checks use the same statically discovered command tree. Unknown
	// wrappers remain inconclusive rather than being treated as Cucumber-only.
	remaining := 256
	resolution := resolveJestSequence(root, filepath.Join(root, stepDirectory(workflow, job, step)), step.Run, nil, &remaining, "cucumber")
	scoped := ""
	for _, layer := range []map[string]string{workflow.Env, job.Env, step.Env} {
		if value, ok := layer["DD_CUCUMBER_NODE_OPTIONS"]; ok {
			scoped = value
		}
	}
	if resolution.OtherFramework && (scoped == "" || checkJestBootstrap(workflow, job, step, row) == "") {
		return "Cucumber-only scope is not preserved: this command also runs another framework. Keep the original command and pass DD_CUCUMBER_NODE_OPTIONS only to the Cucumber leaf."
	}
	directory := filepath.Join(root, stepDirectory(workflow, job, step))
	if len(commands) == 1 && filepath.IsLocal(commands[0].Directory) {
		directory = filepath.Join(root, commands[0].Directory)
	}
	data, err := os.ReadFile(filepath.Join(directory, "package.json"))
	var pkg struct {
		Type string
	}
	if err != nil || json.Unmarshal(data, &pkg) != nil {
		return "Cannot inspect Cucumber package scripts"
	}
	if scoped != "" {
		if len(commands) == 0 {
			return "Cannot identify a Cucumber leaf consuming DD_CUCUMBER_NODE_OPTIONS"
		}
		for _, command := range commands {
			if !command.cucumberScoped {
				return "Selected Cucumber leaf does not consume scoped DD_CUCUMBER_NODE_OPTIONS while preserving existing NODE_OPTIONS"
			}
		}
		copy := step
		copy.Env = map[string]string{}
		for k, v := range step.Env {
			copy.Env[k] = v
		}
		copy.Env["NODE_OPTIONS"] = scoped
		if reason := checkJestBootstrap(workflow, job, copy, row, "vitest"); reason != "" {
			return reason
		}
	} else if reason := checkJestBootstrap(workflow, job, step, row, "vitest"); reason != "" {
		return reason
	}
	if pkg.Type == "module" {
		options := scoped
		for _, env := range []map[string]string{workflow.Env, job.Env, step.Env} {
			if value, ok := env["NODE_OPTIONS"]; ok {
				options = scoped + " " + value
			}
		}
		if !strings.Contains(options, "--experimental-loader @istanbuljs/esm-loader-hook") && !strings.Contains(options, "--loader @istanbuljs/esm-loader-hook") {
			return "Cannot verify Istanbul instrumentation for native ESM Cucumber CI code. Include --experimental-loader @istanbuljs/esm-loader-hook in the scoped Cucumber Node options, or review custom/pre-instrumented source coverage separately."
		}
	}
	for _, command := range commands {
		if !command.usesNYC {
			return "Cucumber CI source coverage is not configured. Preserve the command and wrap its Cucumber leaf with nyc; native ESM also requires Istanbul instrumentation such as @istanbuljs/esm-loader-hook. Local coverage does not configure CI."
		}
	}
	return ""
}
