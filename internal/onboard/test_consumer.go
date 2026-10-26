// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// Executable names of each framework, as they appear in CI commands.
var frameworkExecutables = map[string][]string{
	"jest": {"jest"}, "vitest": {"vitest"}, "mocha": {"mocha", "_mocha"}, "playwright": {"playwright"},
	"cypress": {"cypress"}, "cucumber": {"cucumber", "cucumber-js"}, "webdriverio": {"wdio", "webdriverio"},
}

// Task runners and package binaries runners that can start any test suite.
var taskRunners = []string{"npx", "bunx", "make", "just", "task", "nx", "turbo", "lerna", "rush", "gulp", "grunt"}

var interpreters = []string{"node", "bash", "sh", "zsh", "deno", "tsx", "ts-node"}

// Package-manager subcommands that start a package script or binary.
var scriptSubcommands = []string{"test", "t", "tst", "run", "run-script", "exec", "x", "dlx", "start"}

// Package-manager subcommands that never start a script or test binary.
var managerOnlySubcommands = []string{"install", "i", "ci", "add", "remove", "uninstall", "audit", "config", "publish", "pack", "why", "list", "ls", "outdated", "dedupe", "store", "version", "view", "info", "cache", "stage", "login", "whoami", "dist-tag"}

var commandSeparator = regexp.MustCompile(`\n|&&|\|\||;|\||\$\(|` + "`" + `|\bthen\b|\bdo\b|\belse\b|\belif\b`)

var heredocStart = regexp.MustCompile(`<<-?\s*['"]?([A-Za-z_][A-Za-z0-9_]*)['"]?`)

// mayStartTests reports whether an unresolved step could hide a test of the
// selected framework. Only steps made of shell builtins, known non-test tools
// (system packages, coverage merging, linters), package-manager setup and
// publishing, or another framework are outside the test path; an unknown
// executable, action, package script or repository file may wrap a test.
func mayStartTests(step runtimeStep, framework string) bool {
	if strings.TrimSpace(step.Run) == "" {
		return step.Uses != "" // An unresolved action can run anything.
	}
	var lines []string
	terminator := ""
	for line := range strings.SplitSeq(step.Run, "\n") {
		if terminator != "" {
			if strings.TrimSpace(line) == terminator {
				terminator = ""
			}
			continue // An inline script body is data for its interpreter.
		}
		if match := heredocStart.FindStringSubmatch(line); match != nil {
			terminator = match[1]
		}
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		lines = append(lines, line)
	}
	text := strings.ToLower(strings.Join(lines, "\n"))
	if namesFramework(text, framework, true) {
		return true
	}
	text = shellCommandText(text)
	for _, segment := range commandSeparator.Split(text, -1) {
		words := strings.Fields(segment)
		for len(words) > 0 && (strings.Contains(words[0], "=") || slices.Contains([]string{"sudo", "env", "time", "exec", "xargs", "command", "if", "while", "until", "!", "{", "("}, words[0])) {
			words = words[1:]
		}
		if len(words) > 0 {
			words[0] = strings.TrimLeft(words[0], "({!")
		}
		for len(words) > 0 && strings.HasPrefix(words[0], "-") {
			words = words[1:] // xargs/env options before the command.
		}
		if len(words) == 0 {
			continue
		}
		// A segment that runs another framework is outside this one's scope.
		other := namesFramework(segment, framework, false)
		command := strings.Trim(words[0], `"'`)
		base := filepath.Base(command)
		switch {
		case strings.HasPrefix(command, "./") || strings.HasPrefix(command, "../") || slices.Contains([]string{".sh", ".js", ".mjs", ".cjs", ".ts"}, filepath.Ext(command)):
			return true
		case slices.Contains(taskRunners, base):
			if !other {
				return true
			}
		case slices.Contains(interpreters, base):
			// Interpreters start tests only when they execute a file.
			if len(words) > 1 && !strings.HasPrefix(words[1], "-") && !strings.HasPrefix(words[1], "<") {
				return true
			}
		case slices.Contains([]string{"npm", "pnpm", "yarn", "bun"}, base):
			starts := len(words) == 1 || slices.Contains(scriptSubcommands, words[1])
			// pnpm/yarn/bun <name> run a script or binary of that name.
			starts = starts || base != "npm" && !strings.HasPrefix(words[1], "-") && !slices.Contains(managerOnlySubcommands, words[1])
			if starts && !other {
				return true
			}
		case base == "" || slices.Contains(nonTestTools, base):
		default:
			if !other {
				return true // An unknown executable may wrap tests.
			}
		}
	}
	return false
}

// Shell builtins and tools that never start a JavaScript test suite.
var nonTestTools = []string{
	"echo", "printf", "cd", "pushd", "popd", "export", "set", "unset", "shopt", "exit", "return", "true", "false", ":", "[", "[[", "test", "fi", "then", "else", "elif", "done", "esac", "for", "case", "select", "function", "}", ")", "read", "sleep", "wait", "trap", "source", ".", "eval", "local", "declare",
	"mkdir", "cp", "mv", "rm", "rmdir", "cat", "ls", "touch", "chmod", "chown", "ln", "tee", "sed", "awk", "grep", "find", "jq", "yq", "tar", "zip", "unzip", "gzip", "curl", "wget", "git", "gh", "head", "tail", "sort", "uniq", "wc", "cut", "tr", "base64", "sha256sum", "shasum", "md5sum", "date", "uname", "whoami", "hostname", "nproc", "df", "du", "basename", "dirname", "realpath", "which", "type",
	"apt", "apt-get", "dpkg", "brew", "choco", "winget", "yum", "dnf", "apk", "pip", "pip3", "pipx", "corepack", "nvm", "volta", "fnm", "asdf", "rustup", "cargo", "go",
	"lcov", "genhtml", "codecov", "coveralls", "cspell", "markdownlint", "markdownlint-cli2", "prettier", "eslint", "tsc", "stylelint", "commitlint", "biome", "oxlint",
}

// namesFramework reports whether text names an executable of the selected
// framework (selected) or of any other framework (!selected).
func namesFramework(text, framework string, selected bool) bool {
	var words []string
	for _, token := range strings.FieldsFunc(text, func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune("\"'`;|&()=,", r) }) {
		if strings.HasPrefix(token, "@") {
			continue // A scoped package spec is an install argument, not an executable.
		}
		word, _, _ := strings.Cut(filepath.Base(token), "@")
		words = append(words, word)
	}
	for name, executables := range frameworkExecutables {
		if (name == framework) == selected && slices.ContainsFunc(words, func(word string) bool { return slices.Contains(executables, word) }) {
			return true
		}
	}
	return false
}

// shellCommandText keeps the command structure of a script: quoted text becomes
// one placeholder word in its position (command substitutions inside double
// quotes start their own segment) and line continuations are joined.
func shellCommandText(script string) string {
	script = strings.ReplaceAll(script, "\\\n", " ")
	var out strings.Builder
	var quote rune
	substitution := 0 // Open $( levels inside a double-quoted string.
	backtick := false // Inside a `...` substitution of a double-quoted string.
	for i := 0; i < len(script); i++ {
		c := rune(script[i])
		switch {
		case backtick:
			if c == '`' {
				backtick = false
				out.WriteString("\n")
			} else {
				out.WriteRune(c)
			}
		case quote == '"' && substitution == 0 && c == '`':
			backtick = true
			out.WriteString("\n")
		case quote == '\'':
			if c == '\'' {
				quote = 0
			}
		case quote == '"' && substitution == 0:
			switch {
			case c == '\\':
				i++
			case c == '"':
				quote = 0
			case strings.HasPrefix(script[i:], "$("):
				substitution = 1
				i++
				out.WriteString("\n")
			}
		case quote == '"' && c == '(':
			substitution++
			out.WriteRune(c)
		case quote == '"' && c == ')':
			substitution--
			if substitution > 0 {
				out.WriteRune(c)
			} else {
				out.WriteString("\n")
			}
		case c == '\'' || c == '"':
			if quote == 0 {
				quote = c
				// The quoted word, in its position; NAME="..." stays one assignment.
				if text := out.String(); text != "" && !unicode.IsSpace(rune(text[len(text)-1])) {
					out.WriteString("q ")
				} else {
					out.WriteString(" q ")
				}
			} else {
				out.WriteRune(c)
			}
		default:
			out.WriteRune(c)
		}
	}
	return out.String()
}
