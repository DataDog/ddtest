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
	"slices"
	"strings"

	"github.com/kballard/go-shellquote"
)

// A resolution can be unrelated, matched, or unresolved. A matched command with
// an unresolved component is still inconclusive; it must not hide custom wrappers.
type commandResolution struct {
	Matched           bool
	Evidence          string
	Reason            string
	Review            bool
	UnknownExecutable bool
}

func unresolvedCommand(reason string) commandResolution {
	return commandResolution{Reason: reason}
}

func resolveTestStep(root string, workflow ciWorkflow, job runtimeJob, step runtimeStep, language, framework string) commandResolution {
	if strings.TrimSpace(step.Run) == "" {
		return commandResolution{}
	}
	if language != "javascript" || framework != "jest" {
		return commandResolution{Matched: looksLikeTestJob(strings.ToLower(step.Run), language, framework)}
	}
	directory := workflow.Defaults.Run.WorkingDirectory
	shell := workflow.Defaults.Run.Shell
	for _, defaults := range []runDefaults{job.Defaults.Run, {WorkingDirectory: step.WorkingDirectory, Shell: step.Shell}} {
		if defaults.WorkingDirectory != "" {
			directory = defaults.WorkingDirectory
		}
		if defaults.Shell != "" {
			shell = defaults.Shell
		}
	}
	if shell != "" && shell != "bash" && shell != "sh" {
		return unresolvedCommand("Unsupported CI shell: " + shell)
	}
	if strings.ContainsAny(directory, "$`~") || (directory != "" && !filepath.IsLocal(directory)) {
		return unresolvedCommand("Cannot statically resolve repository working-directory: " + directory)
	}
	result := resolveJestCommand(filepath.Join(root, directory), step.Run, nil)
	if result.UnknownExecutable && !result.Matched && separateCIEntryPoint(workflow, job, step) {
		result.Review = true
		result.Reason = "Build, publishing, or documentation entry point was not identified as Jest; review separately if it also runs tests. " + result.Reason
	}
	return result
}

// Scope by explicit commands, never job/step names or comments. These unresolved
// entry points remain visible for review; this is not proof they cannot run tests.
// Instrumented steps and unknown test wrappers must still block validation.
func separateCIEntryPoint(workflow ciWorkflow, job runtimeJob, step runtimeStep) bool {
	for _, env := range []map[string]string{workflow.Env, job.Env, step.Env} {
		if env["NODE_OPTIONS"] != "" {
			return false
		}
	}
	commands, err := staticCommands(step.Run)
	if err != nil || len(commands) != 1 {
		return false
	}
	words := commands[0]
	if len(words) == 2 && words[0] == "npx" && words[1] == "semantic-release" {
		return true
	}
	if len(words) < 2 || !slices.Contains([]string{"npm", "yarn", "pnpm", "bun"}, words[0]) {
		return false
	}
	args := words[1:]
	if args[0] == "run" || args[0] == "run-script" {
		args = args[1:]
	} else if words[0] == "npm" {
		return false
	}
	return len(args) == 1 && slices.Contains([]string{"build", "build-storybook", "storybook", "release"}, args[0])
}

// Resolve only ordinary static commands and package script aliases. Never run
// a shell, load JavaScript, or rewrite the CI entry point during discovery.
func resolveJestCommand(directory, command string, stack []string) commandResolution {
	remaining := 256
	return resolveJestSequence(directory, command, stack, &remaining)
}

func resolveJestSequence(directory, command string, stack []string, remaining *int) commandResolution {
	if len(stack) >= 16 {
		return unresolvedCommand("Package script nesting exceeds 16 levels")
	}
	commands, err := staticCommands(command)
	if err != nil {
		return unresolvedCommand(err.Error())
	}
	var result commandResolution
	for _, words := range commands {
		*remaining--
		if *remaining < 0 {
			return unresolvedCommand("Package script expansion exceeds 256 commands")
		}
		part := resolveJestWords(directory, words, stack, remaining)
		if part.Reason != "" {
			if result.Reason == "" {
				result.Reason = part.Reason
				result.UnknownExecutable = part.UnknownExecutable
			} else {
				result.UnknownExecutable = result.UnknownExecutable && part.UnknownExecutable
			}
		}
		result.Matched = result.Matched || part.Matched
		if part.Evidence != "" {
			if result.Evidence != "" {
				result.Evidence += "; "
			}
			result.Evidence += part.Evidence
		}
	}
	return result
}

func resolveJestWords(directory string, words, stack []string, remaining *int) commandResolution {
	if len(words) == 0 {
		return commandResolution{}
	}
	command := shellquote.Join(words...)
	for len(words) > 0 && strings.Contains(words[0], "=") {
		key, _, _ := strings.Cut(words[0], "=")
		if key == "NODE_OPTIONS" || key == "PATH" || strings.HasPrefix(key, "DD_") {
			return unresolvedCommand("Command overrides instrumentation or executable selection: " + command)
		}
		words = words[1:]
	}
	if len(words) == 0 {
		return commandResolution{}
	}
	if words[0] == "npx" || (len(words) > 1 && (words[0] == "pnpm" || words[0] == "npm") && words[1] == "exec") {
		if words[0] == "npx" {
			words = words[1:]
		} else {
			words = words[2:]
		}
		if len(words) > 0 && words[0] == "--" {
			words = words[1:]
		}
		if len(words) == 0 {
			return unresolvedCommand("Missing executable: " + command)
		}
	}
	switch words[0] {
	case "jest", "./node_modules/.bin/jest", "node_modules/.bin/jest":
		return commandResolution{Matched: true, Evidence: command}
	case "echo", "printf", "true", "false", "eslint", "prettier", "tsc", "mkdir", "cp":
		return commandResolution{}
	case "npm", "yarn", "pnpm", "bun":
		if len(words) < 2 {
			return unresolvedCommand("Missing package-manager subcommand: " + command)
		}
		// These are setup/metadata commands, not explicit test entry points.
		if slices.Contains([]string{"ci", "install", "i", "--version", "--help"}, words[1]) {
			return commandResolution{}
		}
		args := words[1:]
		if words[0] == "bun" && args[0] == "test" {
			// bun test invokes Bun's own runner, not the package's Jest script.
			return commandResolution{}
		}
		if args[0] == "run" || args[0] == "run-script" {
			args = args[1:]
		} else if words[0] == "npm" && args[0] != "test" && args[0] != "t" {
			return unresolvedCommand("Unsupported npm subcommand: " + command)
		}
		if len(args) == 0 || strings.HasPrefix(args[0], "-") {
			return unresolvedCommand("Unsupported package script selection: " + command)
		}
		name := args[0]
		if words[0] == "npm" && words[1] == "t" {
			name = "test"
		}
		forwarded := args[1:]
		if len(forwarded) > 0 && forwarded[0] == "--" {
			forwarded = forwarded[1:]
		} else if words[0] == "npm" && len(forwarded) > 0 {
			return unresolvedCommand("npm script arguments without -- require review: " + command)
		}
		return resolvePackageScript(directory, name, forwarded, stack, command, remaining)
	default:
		return commandResolution{Reason: "Cannot statically resolve command: " + command, UnknownExecutable: true}
	}
}

func resolvePackageScript(directory, name string, args, stack []string, command string, remaining *int) commandResolution {
	if slices.Contains(stack, name) {
		return unresolvedCommand("Package script cycle: " + strings.Join(append(slices.Clone(stack), name), " -> "))
	}
	data, err := os.ReadFile(filepath.Join(directory, "package.json"))
	if err != nil {
		return unresolvedCommand("Cannot read package.json for " + command + ": " + err.Error())
	}
	var manifest struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return unresolvedCommand("Cannot parse package.json: " + err.Error())
	}
	script, ok := manifest.Scripts[name]
	if !ok {
		return unresolvedCommand("No package.json script named " + name + " in " + directory)
	}
	for _, hook := range []string{"pre" + name, "post" + name} {
		if manifest.Scripts[hook] != "" {
			return unresolvedCommand("Package lifecycle script " + hook + " requires review alongside " + command)
		}
	}
	if len(args) > 0 {
		script += " " + shellquote.Join(args...)
	}
	result := resolveJestSequence(directory, script, append(slices.Clone(stack), name), remaining)
	if result.Reason != "" {
		result.Reason = command + " -> " + result.Reason
	} else if result.Matched {
		result.Evidence = command + " -> " + result.Evidence
	}
	return result
}

// Split a deliberately small shell subset: literal words, quotes, comments,
// newlines and &&/; sequences. Expansion, pipelines and control flow require
// review. shellquote handles word quoting; this scanner only finds boundaries.
func staticCommands(command string) ([][]string, error) {
	var commands [][]string
	start := 0
	var quote byte
	add := func(end int) error {
		words, err := shellquote.Split(command[start:end])
		if err != nil {
			return err
		}
		if len(words) > 0 {
			commands = append(commands, words)
		}
		if len(commands) > 64 {
			return fmt.Errorf("command sequence exceeds 64 entries")
		}
		return nil
	}
	for i := 0; i < len(command); i++ {
		c := command[i]
		if c == '\\' && quote != '\'' {
			i++
			continue
		}
		if quote != 0 {
			if c == quote {
				quote = 0
			} else if quote == '"' && (c == '$' || c == '`') {
				return nil, fmt.Errorf("dynamic shell expansion requires review")
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '$', '`', '|', '<', '>', '(', ')', '{', '}':
			return nil, fmt.Errorf("unsupported or dynamic shell syntax requires review")
		case '#':
			if i > start && !strings.ContainsRune(" \t\n", rune(command[i-1])) {
				continue
			}
			if err := add(i); err != nil {
				return nil, err
			}
			for i < len(command) && command[i] != '\n' {
				i++
			}
			start = i + 1
		case '&', ';', '\n':
			if err := add(i); err != nil {
				return nil, err
			}
			if c == '&' {
				if i+1 >= len(command) || command[i+1] != '&' {
					return nil, fmt.Errorf("background commands require review")
				}
				i++
			}
			start = i + 1
		}
	}
	if start < len(command) {
		if err := add(len(command)); err != nil {
			return nil, err
		}
	}
	return commands, nil
}
