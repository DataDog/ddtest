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

// ForwardJestCommand expands only statically understood root package scripts.
// Additional options go to the single Jest invocation, never a lint/setup step.
// CI files are untouched, and && order, environment and Jest options survive.
func ForwardJestCommand(root, command string) (string, error) {
	plan, err := ForwardJestExecution(root, command)
	return plan.Command, err
}

func ForwardJestExecution(root, command string) (TestCommand, error) {
	sourceCommand := command
	resolution := resolveJestCommand(root, command, nil)
	plan := func(command string, lint bool) TestCommand {
		return TestCommand{Command: command, ScopeCommand: sourceCommand, ProbeLintSkipped: lint, ReviewReason: resolution.Reason}
	}
	if !resolution.Matched || resolution.Reason != "" {
		return plan(command, false), nil // Explicit custom commands remain the caller's choice.
	}
	commands, err := expandedStaticCommands(root, root, command)
	if err != nil {
		return TestCommand{}, err
	}
	var words []string
	if len(commands) == 1 {
		words = commands[0]
		command = shellCommand(words)
	}
	for _, test := range resolution.Tests {
		runnerWords, err := shellquote.Split(test.Command)
		if err != nil {
			return TestCommand{}, err
		}
		if slices.Contains(runnerWords, "--") {
			return TestCommand{}, fmt.Errorf("positional Jest selection requires an explicit command")
		}
	}
	if resolution.SingleJest && len(words) > 0 && !strings.Contains(words[0], "=") && appendsToRunner(root, words, 0) {
		if words[0] == "npm" && !slices.Contains(words, "--") {
			return plan(command+" --", false), nil
		}
		if words[0] == "jest" {
			words[0] = filepath.Join(root, "node_modules", ".bin", "jest")
			return plan(shellquote.Join(words...), false), nil
		}
		return plan(command, false), nil
	}
	count := 0
	skippedLint := false
	body, err := forwardJestScript(root, root, command, 0, &count, &skippedLint)
	if err == nil && count != 1 {
		err = fmt.Errorf("found %d Jest invocations; exactly one is required", count)
	}
	if err != nil {
		return TestCommand{}, fmt.Errorf("cannot forward probe options through %q: %v; use --command with the actual Jest executable, original options and required setup", command, err)
	}
	body = "PATH=" + shellquote.Join(filepath.Join(root, "node_modules", ".bin")) + ":\"$PATH\"; export PATH; " + body
	return plan(shellquote.Join("sh", "-c", body, "ddtest-jest"), skippedLint), nil
}

// expandedStaticCommands is staticCommands for a command run from directory:
// unquoted pathname patterns are replaced by the files the shell would pass.
func expandedStaticCommands(root, directory, command string) ([][]string, error) {
	fragments, err := discoverCommands(command)
	if err != nil {
		return nil, err
	}
	var commands [][]string
	for _, fragment := range fragments {
		if fragment.Reason != "" {
			return nil, fmt.Errorf("%s", fragment.Reason)
		}
		words, err := expandPathnames(root, directory, fragment)
		if err != nil {
			return nil, err
		}
		commands = append(commands, words)
	}
	return commands, nil
}

// Package managers append forwarded options to the raw script text. A comment
// or separator there turns probe options into prose or a separate command.
func appendsToRunner(directory string, words []string, depth int) bool {
	if depth >= 16 || len(words) == 0 {
		return false
	}
	if !slices.Contains([]string{"npm", "yarn", "pnpm", "bun"}, words[0]) {
		return true
	}
	args := words[1:]
	if len(args) > 0 && (args[0] == "run" || args[0] == "run-script") {
		args = args[1:]
	}
	if len(args) == 0 {
		return false
	}
	name := args[0]
	if words[0] == "npm" && name == "t" {
		name = "test"
	}
	data, err := os.ReadFile(filepath.Join(directory, "package.json"))
	if err != nil {
		return false
	}
	var manifest struct{ Scripts map[string]string }
	if json.Unmarshal(data, &manifest) != nil {
		return false
	}
	script, ok := manifest.Scripts[name]
	if !ok || strings.ContainsAny(script, "#;\n&|<>()`$") {
		return false
	}
	commands, err := expandedStaticCommands(directory, directory, script)
	if err != nil || len(commands) != 1 {
		return false
	}
	words, _, err = commandEnvironment(commands[0])
	return err == nil && appendsToRunner(directory, words, depth+1)
}

// Track cd like the shell does, and run each expanded package script in its own
// subshell: the package manager's child process confines directory changes and
// adds that package's local binaries to PATH.
func forwardJestScript(root, directory, command string, depth int, count *int, skippedLint *bool) (string, error) {
	if depth >= 16 || strings.ContainsAny(command, ";\n") {
		return "", fmt.Errorf("only bounded literal && sequences can be forwarded")
	}
	fragments, err := discoverCommands(command)
	if err != nil {
		return "", err
	}
	for _, fragment := range fragments {
		if fragment.Reason != "" {
			return "", fmt.Errorf("%s", fragment.Reason)
		}
		for i, word := range fragment.Words {
			if !slices.Contains(fragment.Patterns, i) && strings.ContainsAny(word, "*?[") {
				return "", fmt.Errorf("shell glob expansion requires review")
			}
		}
	}
	var parts []string
	for _, fragment := range fragments {
		// Expand patterns where the shell would, after any preceding cd, so
		// the forwarded command passes the runner the same file list.
		words, err := expandPathnames(root, directory, fragment)
		if err != nil {
			return "", err
		}
		prefix := []string{}
		for len(words) > 0 && strings.Contains(words[0], "=") {
			prefix = append(prefix, words[0])
			words = words[1:]
		}
		if len(words) == 0 {
			return "", fmt.Errorf("standalone environment assignment requires review")
		}
		if words[0] == "cd" {
			if len(prefix) > 0 || len(words) != 2 {
				return "", fmt.Errorf("cd requires one static repository directory")
			}
			directory, err = repositoryDirectory(root, directory, words[1])
			if err != nil {
				return "", err
			}
			parts = append(parts, shellCommand(words))
			continue
		}
		part := shellCommand(words)
		remaining := &scriptBudget{left: packageScriptExpansionLimit}
		resolution := resolveJestSequence(root, directory, part, nil, remaining)
		if resolution.Matched {
			switch words[0] {
			case "jest", "./node_modules/.bin/jest", "node_modules/.bin/jest":
				if slices.Contains(words, "--") {
					return "", fmt.Errorf("positional Jest selection requires an explicit command")
				}
				*count++
				part += ` "$@"`
			case "npm", "yarn", "pnpm", "bun":
				args := words[1:]
				if len(args) > 0 && (args[0] == "run" || args[0] == "run-script") {
					args = args[1:]
				}
				if len(args) == 0 {
					return "", fmt.Errorf("missing package script")
				}
				name := args[0]
				if words[0] == "npm" && name == "t" {
					name = "test"
				}
				data, err := os.ReadFile(filepath.Join(directory, "package.json"))
				if err != nil {
					return "", err
				}
				var manifest struct{ Scripts map[string]string }
				if err := json.Unmarshal(data, &manifest); err != nil {
					return "", err
				}
				script, ok := manifest.Scripts[name]
				if !ok || manifest.Scripts["pre"+name] != "" || manifest.Scripts["post"+name] != "" {
					return "", fmt.Errorf("package script or lifecycle requires review")
				}
				args = args[1:]
				if len(args) > 0 && args[0] == "--" && words[0] != "pnpm" {
					args = args[1:]
				}
				if len(args) > 0 {
					script += " " + shellquote.Join(args...)
				}
				part, err = forwardJestScript(root, directory, script, depth+1, count, skippedLint)
				if err != nil {
					return "", err
				}
				if directory != root {
					part = "PATH=" + shellquote.Join(filepath.Join(directory, "node_modules", ".bin")) + `:"$PATH"; export PATH; ` + part
				}
				part = "( " + part + " )"
			default:
				return "", fmt.Errorf("wrapper %q requires review", words[0])
			}
		}
		if !resolution.Matched && lintOnly(directory, shellquote.Join(words...), 0) {
			*skippedLint = true
			part = `if [ "${DDTEST_JEST_PROBE:-0}" = 1 ]; then :; else ` + part + `; fi`
		}
		if len(prefix) > 0 {
			part = shellquote.Join(append([]string{"env"}, prefix...)...) + " " + shellquote.Join("sh", "-c", part, "ddtest-jest") + ` "$@"`
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, " && "), nil
}

// Lint is part of the real suite command, but must not reject a temporary
// synthetic test's formatting before Jest can discover it. Opaque setup stays.
func lintOnly(directory, command string, depth int) bool {
	if depth >= 16 {
		return false
	}
	commands, err := staticCommands(command)
	if err != nil || len(commands) == 0 {
		return false
	}
	for _, words := range commands {
		for len(words) > 0 && strings.Contains(words[0], "=") {
			words = words[1:]
		}
		if len(words) == 0 {
			return false
		}
		if words[0] == "eslint" || words[0] == "prettier" {
			continue
		}
		if !slices.Contains([]string{"npm", "yarn", "pnpm", "bun"}, words[0]) {
			return false
		}
		args := words[1:]
		if len(args) > 0 && (args[0] == "run" || args[0] == "run-script") {
			args = args[1:]
		}
		if len(args) != 1 {
			return false
		}
		data, err := os.ReadFile(filepath.Join(directory, "package.json"))
		if err != nil {
			return false
		}
		var manifest struct{ Scripts map[string]string }
		if json.Unmarshal(data, &manifest) != nil || manifest.Scripts["pre"+args[0]] != "" || manifest.Scripts["post"+args[0]] != "" || !lintOnly(directory, manifest.Scripts[args[0]], depth+1) {
			return false
		}
	}
	return true
}
