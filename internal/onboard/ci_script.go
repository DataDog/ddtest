// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Follow repository-owned shell wrappers with the same bounded resolver used
// for package aliases. Reading a wrapper is never permission to execute setup.
func resolveRepositoryShell(root, directory string, words, stack []string, remaining *int, frameworks ...string) (commandResolution, bool) {
	if len(words) == 0 {
		return commandResolution{}, false
	}
	name := words[0]
	if slices.Contains([]string{"sh", "bash"}, name) {
		if len(words) != 2 || strings.HasPrefix(words[1], "-") {
			return commandResolution{}, false
		}
		name = words[1]
	} else if len(words) != 1 || !strings.HasPrefix(name, "./") {
		return commandResolution{}, false
	}
	if filepath.Ext(name) != ".sh" {
		return commandResolution{}, false
	}
	info, ok := repositoryFile(root, directory, name)
	if !ok || info.Size() > 16384 || (len(words) == 1 && info.Mode().Perm()&0o111 == 0) {
		return unresolvedCommand("Repository shell wrapper is unavailable or exceeds 16 KiB: " + name), true
	}
	key := "shell:" + repositoryPath(root, filepath.Join(directory, name))
	if slices.Contains(stack, key) || len(stack) >= 16 {
		return unresolvedCommand("Repository shell wrapper cycle/depth requires review: " + name), true
	}
	data, err := os.ReadFile(filepath.Join(directory, name))
	if err != nil {
		return unresolvedCommand(err.Error()), true
	}
	file, ok := parseCIShell(string(data))
	if !ok {
		return unresolvedCommand("Repository shell wrapper syntax requires review: " + name), true
	}
	var commands []string
	for _, stmt := range file.Stmts {
		call, ok := stmt.Cmd.(*syntax.CallExpr)
		metadata := ok && len(stmt.Redirs) == 0 && !stmt.Background && !stmt.Negated && len(call.Assigns) == 0 && len(call.Args) > 0
		if metadata && outputOnlyCall(call) {
			for _, arg := range call.Args {
				if _, err := literalShellWord(arg); err != nil {
					metadata = false
				}
			}
			if metadata {
				continue
			}
		}
		if metadata && call.Args[0].Lit() == "set" {
			allowed := true
			for _, arg := range call.Args[1:] {
				allowed = allowed && slices.Contains([]string{"-x", "+x", "-e", "-u", "-eu", "-eux", "-ex", "-euo", "pipefail"}, arg.Lit())
			}
			if allowed {
				continue
			}
		}
		commands = append(commands, shellText(stmt))
	}
	r := resolveJestSequence(root, directory, strings.Join(commands, "\n"), append(slices.Clone(stack), key), remaining, frameworks...)
	if !r.Matched {
		return unresolvedCommand("Repository shell setup requires declared output facts: " + name), true
	}
	r.SingleJest = false
	if r.Evidence != "" {
		r.Evidence = name + " -> " + r.Evidence
	}
	return r, true
}
