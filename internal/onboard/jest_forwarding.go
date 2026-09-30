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
	resolution := resolveJestCommand(root, command, nil)
	if !resolution.Matched || resolution.Reason != "" {
		return command, nil // Explicit custom commands remain the caller's choice.
	}
	words, err := shellquote.Split(command)
	if err != nil {
		return "", err
	}
	if resolution.SingleJest && len(words) > 0 && !strings.Contains(words[0], "=") {
		if words[0] == "jest" {
			words[0] = filepath.Join(root, "node_modules", ".bin", "jest")
			return shellquote.Join(words...), nil
		}
		return command, nil
	}
	count := 0
	body, err := forwardJestScript(root, command, 0, &count)
	if err != nil || count != 1 {
		return "", fmt.Errorf("cannot forward probe options through %q: %v (Jest invocations: %d); use --command with the actual Jest executable, original options and required setup", command, err, count)
	}
	body = "PATH=" + shellquote.Join(filepath.Join(root, "node_modules", ".bin")) + ":\"$PATH\"; export PATH; " + body
	return shellquote.Join("sh", "-c", body, "ddtest-jest"), nil
}

func forwardJestScript(root, command string, depth int, count *int) (string, error) {
	if depth >= 16 || strings.ContainsAny(command, ";\n") {
		return "", fmt.Errorf("only bounded literal && sequences can be forwarded")
	}
	commands, err := staticCommands(command)
	if err != nil {
		return "", err
	}
	var parts []string
	for _, words := range commands {
		for _, word := range words {
			if strings.ContainsAny(word, "*?[") {
				return "", fmt.Errorf("shell glob expansion requires review")
			}
		}
		prefix := []string{}
		for len(words) > 0 && strings.Contains(words[0], "=") {
			prefix = append(prefix, words[0])
			words = words[1:]
		}
		if len(words) == 0 {
			return "", fmt.Errorf("standalone environment assignment requires review")
		}
		part := shellquote.Join(words...)
		resolution := resolveJestCommand(root, part, nil)
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
				data, err := os.ReadFile(filepath.Join(root, "package.json"))
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
				if len(args) > 0 && args[0] == "--" {
					args = args[1:]
				}
				if len(args) > 0 {
					script += " " + shellquote.Join(args...)
				}
				part, err = forwardJestScript(root, script, depth+1, count)
				if err != nil {
					return "", err
				}
			default:
				return "", fmt.Errorf("wrapper %q requires review", words[0])
			}
		}
		if !resolution.Matched && lintOnly(root, shellquote.Join(words...), 0) {
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
func lintOnly(root, command string, depth int) bool {
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
		data, err := os.ReadFile(filepath.Join(root, "package.json"))
		if err != nil {
			return false
		}
		var manifest struct{ Scripts map[string]string }
		if json.Unmarshal(data, &manifest) != nil || manifest.Scripts["pre"+args[0]] != "" || manifest.Scripts["post"+args[0]] != "" || !lintOnly(root, manifest.Scripts[args[0]], depth+1) {
			return false
		}
	}
	return true
}
