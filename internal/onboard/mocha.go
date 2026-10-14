// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kballard/go-shellquote"
)

// Keep coverage wrappers intact. Unknown options are unresolved rather than
// guessing which following word is the executable or discarding coverage flags.
func resolveMochaCoverage(root, directory string, words, assignments, stack []string, remaining *int) commandResolution {
	i := 1
	for i < len(words) {
		word := words[i]
		if word == "--" {
			i++
			break
		}
		if !strings.HasPrefix(word, "-") {
			break
		}
		key, _, equals := strings.Cut(word, "=")
		switch key {
		case "--reporter", "-r", "--report-dir", "--reports-dir", "--temp-dir", "--cwd", "--include", "-n", "--exclude", "-x", "--extension", "-e", "--lines", "--functions", "--branches", "--statements", "--exclude-after-remap":
			if !equals {
				i++
				if i >= len(words) {
					return unresolvedCommand("Missing coverage option value")
				}
			}
		case "--all", "--check-coverage", "--per-file", "--clean", "--no-clean", "--silent", "--instrument", "--no-instrument", "--source-map", "--no-source-map", "--exclude-node-modules", "--no-exclude-node-modules":
		default:
			return unresolvedCommand("Coverage option requires review: " + word)
		}
		i++
	}
	if i >= len(words) {
		return unresolvedCommand("Coverage wrapper has no test command")
	}
	result := resolveJestWords(root, directory, words[i:], stack, remaining, "mocha")
	if result.Reason != "" || !result.SingleJest || len(result.Tests) != 1 || len(result.Builds) > 0 {
		return unresolvedCommand("Coverage wrapper needs one statically resolved Mocha invocation: " + result.Reason)
	}
	result.Tests[0].usesNYC = result.Tests[0].usesNYC || filepath.Base(words[0]) == "nyc"
	result.Tests[0].Command = shellquote.Join(append(slices.Clone(assignments), words...)...)
	return result
}

func MochaValidationCommand(root, command string) (string, error) {
	if strings.TrimSpace(command) == "" {
		scope, err := DiscoverValidationScope(root, "mocha")
		if err != nil {
			return "", err
		}
		if len(scope.Commands) > 1 {
			return "", fmt.Errorf("multiple Mocha CI commands discovered; use --all to validate each configuration")
		}
		if len(scope.Commands) == 0 {
			return "", nil
		}
		item := scope.Commands[0]
		if filepath.Clean(item.Directory) != "." || len(item.Prerequisites) > 0 || len(item.Environment) > 0 || len(item.UnvalidatedSetup) > 0 {
			return "", fmt.Errorf("mocha CI setup requires --all and review of unresolved setup")
		}
		command = item.Command
	}
	remaining := 256
	resolved := resolveJestSequence(root, root, command, nil, &remaining, "mocha")
	if resolved.Reason != "" || !resolved.SingleJest || len(resolved.Tests) != 1 || len(resolved.Builds) > 0 {
		return "", fmt.Errorf("mocha validation requires one resolved invocation, preserving coverage and setup: %s. Use --all for separate CI commands; --command is not an implicit shell", resolved.Reason)
	}
	return command, nil
}

// MochaUsesNYC follows resolved package aliases so local validation does not
// stack a second coverage wrapper around the customer's existing invocation.
func MochaUsesNYC(root, command string) bool {
	remaining := 256
	result := resolveJestSequence(root, root, command, nil, &remaining, "mocha")
	return result.Reason == "" && len(result.Tests) == 1 && result.Tests[0].usesNYC
}
