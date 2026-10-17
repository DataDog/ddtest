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
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kballard/go-shellquote"
)

// These optional values affect debug logging and scoped preloading, not test
// selection. Only normalize them for discovery; execute the original npm alias.
const cucumberScopedOptions = `NODE_OPTIONS="${NODE_OPTIONS:-} ${DD_CUCUMBER_NODE_OPTIONS:-}"`

func cucumberDiscoveryScript(script string) string {
	if strings.TrimSpace(script) == "npm ls >/dev/null" {
		return "true"
	}
	return strings.NewReplacer(`DEBUG="any,$DEBUG"`, "", cucumberScopedOptions, "").Replace(script)
}

func resolveNpmRunAll(root, directory string, words, stack []string, remaining *int, framework string) commandResolution {
	data, err := os.ReadFile(filepath.Join(directory, "package.json"))
	var pkg struct{ Scripts map[string]string }
	if err != nil || json.Unmarshal(data, &pkg) != nil {
		return unresolvedCommand("Cannot read Cucumber package scripts")
	}
	var commands []string
	for _, value := range words[1:] {
		if slices.Contains([]string{"--print-label", "--parallel", "--serial", "-p", "-s"}, value) {
			continue
		}
		args, err := shellquote.Split(value)
		if err != nil || len(args) == 0 || strings.HasPrefix(args[0], "-") || strings.Contains(args[0], "**") {
			return unresolvedCommand("Unsupported Cucumber script orchestration: " + value)
		}
		matched := false
		for _, name := range slices.Sorted(maps.Keys(pkg.Scripts)) {
			match, err := path.Match(strings.ReplaceAll(args[0], ":", "/"), strings.ReplaceAll(name, ":", "/"))
			if err != nil {
				return unresolvedCommand("Invalid Cucumber script pattern: " + value)
			}
			if match {
				matched = true
				commands = append(commands, shellquote.Join(append([]string{"npm", "run", name}, args[1:]...)...))
			}
		}
		if !matched {
			return unresolvedCommand("No package script matches " + value)
		}
	}
	return resolveJestSequence(root, directory, strings.Join(commands, " && "), stack, remaining, framework)
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
		if len(scope.Commands) != 1 || len(scope.Commands[0].Prerequisites) > 0 || len(scope.Commands[0].Environment) > 0 {
			return "", fmt.Errorf("cucumber CI commands or build prerequisites require --all")
		}
		command = scope.Commands[0].Command
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
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	var pkg struct {
		Type    string
		Scripts map[string]string
	}
	if err != nil || json.Unmarshal(data, &pkg) != nil {
		return "Cannot inspect Cucumber package scripts"
	}
	if scoped != "" {
		found := false
		for _, script := range pkg.Scripts {
			if strings.Contains(script, "cucumber-js") {
				if !strings.Contains(script, cucumberScopedOptions) {
					return "Cucumber leaf does not consume scoped DD_CUCUMBER_NODE_OPTIONS while preserving existing NODE_OPTIONS"
				}
				found = true
			}
		}
		if !found {
			return "Cannot identify a Cucumber leaf consuming DD_CUCUMBER_NODE_OPTIONS"
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
				options += " " + value
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
