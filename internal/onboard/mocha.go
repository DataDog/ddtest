// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/kballard/go-shellquote"
)

// Keep coverage wrappers intact. Unknown options are unresolved rather than
// guessing which following word is the executable or discarding coverage flags.
func resolveMochaCoverage(root, directory string, words, stack []string, remaining *int, frameworks ...string) commandResolution {
	framework := "mocha"
	if len(frameworks) > 0 {
		framework = frameworks[0]
	}
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
	result := resolveJestWords(root, directory, words[i:], stack, remaining, framework)
	if result.Reason == "" && !result.Matched && result.ReviewReason != "" {
		return result
	}
	if result.Reason != "" || !result.SingleJest || len(result.Tests) != 1 || len(result.Builds) > 0 {
		return unresolvedCommand("Coverage wrapper needs one statically resolved Mocha invocation: " + result.Reason)
	}
	if probe := result.Tests[0].mochaProbeCommand; probe != "" {
		result.Tests[0].mochaProbeCommand = shellquote.Join(words[:i]...) + " " + probe
	}
	result.Tests[0].usesNYC = result.Tests[0].usesNYC || filepath.Base(words[0]) == "nyc"
	result.Tests[0].Command = shellquote.Join(words...)
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

// This is a discovery hint, not a JavaScript interpreter or proof of execution.
// The native observer must subsequently see a complete Mocha run. Never execute
// a script during discovery, and never replace its programmatic runner with CLI.
var mochaImport = regexp.MustCompile(`(?m)(?:^|[;\n])\s*(?:(?:const|let|var)\s+[A-Za-z_$][\w$]*\s*=\s*require\(\s*['"]mocha['"]\s*\)|import\s+[^;\n]+\s+from\s*['"]mocha['"])`)

var browserRunnerImport = regexp.MustCompile(`(?m)(?:^|[;\n])\s*import\s+[^;\n]+\s+from\s*['"]puppeteer['"]`)

func resolveMochaNode(root, directory string, words []string) commandResolution {
	if len(words) == 3 && (words[1] == "-e" || words[1] == "--eval") {
		// A literal inline helper is retained only inside a package command with
		// an identified Mocha consumer; it is never itself proof of test scope.
		return commandResolution{MochaAuxiliary: true}
	}
	if len(words) < 2 || !filepath.IsLocal(words[1]) || !slices.Contains([]string{".js", ".cjs", ".mjs"}, filepath.Ext(words[1])) {
		return unresolvedCommand("Mocha Node entry point requires a literal repository script: " + shellquote.Join(words...))
	}
	path, err := filepath.EvalSymlinks(filepath.Join(directory, words[1]))
	canonicalRoot, _ := filepath.EvalSymlinks(root)
	relative, relErr := filepath.Rel(canonicalRoot, path)
	if err != nil || relErr != nil || !filepath.IsLocal(relative) {
		return unresolvedCommand("Cannot resolve repository Node script: " + words[1])
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return unresolvedCommand("Cannot inspect repository Node script: " + words[1])
	}
	source, err := os.ReadFile(path)
	if err != nil {
		return unresolvedCommand(err.Error())
	}
	command := shellquote.Join(words...)
	if !mochaImport.Match(source) {
		if browserRunnerImport.Match(source) {
			return commandResolution{ReviewReason: "Puppeteer browser runner is outside Node Mocha validation: " + command}
		}
		return commandResolution{MochaAuxiliary: true}
	}
	directoryRelative, _ := filepath.Rel(root, directory)
	return commandResolution{Matched: true, SingleJest: true, Evidence: command, Tests: []TestCommand{{mochaESM: mochaESMInputs(directory, words[1:]), Directory: filepath.ToSlash(directoryRelative), Command: command}}}
}

// MochaProbeCommand omits companion lint/assertion checks only for synthetic
// probes. The paired real-suite runs always execute the entire original alias.
func MochaProbeCommand(root, command string) string {
	remaining := 256
	result := resolveJestSequence(root, root, command, nil, &remaining, "mocha")
	if result.Reason == "" && len(result.Tests) == 1 && result.Tests[0].mochaProbeCommand != "" {
		return result.Tests[0].mochaProbeCommand
	}
	return command
}
