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
	Tests             []TestCommand
	Builds            []BuildCommand
	Matched           bool
	Benchmark         bool
	SingleJest        bool
	Evidence          string
	Reason            string
	Review            bool
	UnknownExecutable bool
	ReviewReason      string
}

func unresolvedCommand(reason string) commandResolution {
	return commandResolution{Reason: reason}
}

func resolveTestStep(root string, workflow ciWorkflow, job runtimeJob, step runtimeStep, language, framework string) commandResolution {
	if step.ResolutionError != "" {
		return unresolvedCommand(step.ResolutionError)
	}
	if strings.TrimSpace(step.Run) == "" {
		return commandResolution{}
	}
	if language != "javascript" || (framework != "jest" && framework != "vitest") {
		return commandResolution{Matched: looksLikeTestJob(strings.ToLower(step.Run), language, framework)}
	}
	directory := stepDirectory(workflow, job, step)
	shell := workflow.Defaults.Run.Shell
	for _, defaults := range []runDefaults{job.Defaults.Run, {WorkingDirectory: step.WorkingDirectory, Shell: step.Shell}} {
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
	if result, ok := resolveReleaseInput(filepath.Join(root, directory), step.Run, framework); ok {
		return result
	}
	if jsonPackagingStep(step.Run) {
		return commandResolution{Review: true, Reason: "JSON packaging transformation is outside " + framework + " validation; review separately if its output is used by tests."}
	}
	if strings.TrimSpace(step.Run) == "bash <(curl -s https://codecov.io/bash)" {
		return commandResolution{Review: true, Reason: "Legacy Codecov upload is outside identified " + framework + " entry points; the downloaded script is not inspected or validated."}
	}
	remaining := 256
	result := resolveJestSequence(root, filepath.Join(root, directory), step.Run, nil, &remaining, framework)
	if _, err := staticCommands(step.Run); err != nil {
		if metadata, ok := resolveMetadataStep(root, workflow, job, step, framework); ok {
			return metadata
		}
	}
	if !result.Matched && result.Reason == "" && result.ReviewReason != "" {
		result.Review, result.Reason = true, result.ReviewReason
	}
	if result.UnknownExecutable && !result.Matched && separateCIEntryPoint(workflow, job, step) {
		result.Review = true
		result.Reason = "Build, publishing, or documentation entry point was not identified as " + framework + "; review separately if it also runs tests. " + result.Reason
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
	if words[0] == "npm" {
		_, args, _ = npmPrefix(args)
	}
	if len(args) == 0 {
		return false
	}
	if args[0] == "run" || args[0] == "run-script" {
		args = args[1:]
	} else if words[0] == "npm" {
		return false
	}
	if len(args) != 1 {
		return false
	}
	if slices.Contains([]string{"test:perf", "perf", "benchmark"}, args[0]) {
		return true
	}
	name, _, _ := strings.Cut(args[0], ":")
	return slices.Contains([]string{"build", "build-storybook", "storybook", "docs", "release", "api-extractor", "bundlewatch"}, name)
}

// Resolve only ordinary static commands and package script aliases. Never run
// a shell, load JavaScript, or rewrite the CI entry point during discovery.
func resolveJestCommand(directory, command string, stack []string) commandResolution {
	remaining := 256
	return resolveJestSequence(directory, directory, command, stack, &remaining)
}

func resolveJestSequence(root, directory, command string, stack []string, remaining *int, frameworks ...string) commandResolution {
	if len(stack) >= 16 {
		return unresolvedCommand("Package script nesting exceeds 16 levels")
	}
	commands, err := discoverCommands(command)
	if err != nil {
		return unresolvedCommand(err.Error())
	}
	var result commandResolution
	var setup []string
	for _, fragment := range commands {
		words := fragment.Words
		if fragment.Reason != "" {
			result.Reason = fragment.Reason
			result.UnknownExecutable = false
			continue
		}
		*remaining--
		if *remaining < 0 {
			return unresolvedCommand("Package script expansion exceeds 256 commands")
		}
		if len(words) > 0 && words[0] == "cd" {
			if len(words) != 2 {
				result.Reason = "cd requires one static repository directory"
				return result
			}
			next, err := repositoryDirectory(root, directory, words[1])
			if err != nil {
				result.Reason = err.Error()
				return result
			}
			directory = next
			continue
		}
		if preparation, ok := localPreparation(root, directory, words); ok {
			result.Builds = append(result.Builds, preparation)
			continue
		}
		if mutatesCIFiles(shellquote.Join(words...)) {
			setup = append(setup, "CI file or dependency changes require separate validation: "+shellquote.Join(words...))
		}
		part := resolveJestWords(root, directory, words, stack, remaining, frameworks...)
		for i := range part.Tests {
			part.Tests[i].Prerequisites = append(slices.Clone(result.Builds), part.Tests[i].Prerequisites...)
			part.Tests[i].UnvalidatedSetup = append(slices.Clone(setup), part.Tests[i].UnvalidatedSetup...)
			if result.Reason != "" {
				part.Tests[i].UnvalidatedSetup = append(part.Tests[i].UnvalidatedSetup, result.Reason)
			}
		}
		result.Tests = append(result.Tests, part.Tests...)
		result.Builds = append(result.Builds, part.Builds...)
		result.SingleJest = len(commands) == 1 && part.SingleJest
		if part.ReviewReason != "" {
			result.ReviewReason = part.ReviewReason
		}
		if part.Reason != "" {
			if result.Reason == "" {
				result.Reason = part.Reason
				result.UnknownExecutable = part.UnknownExecutable
			} else {
				result.UnknownExecutable = result.UnknownExecutable && part.UnknownExecutable
			}
		}
		result.Matched = result.Matched || part.Matched
		result.Benchmark = result.Benchmark || part.Benchmark
		if part.Evidence != "" {
			if result.Evidence != "" {
				result.Evidence += "; "
			}
			result.Evidence += part.Evidence
		}
	}
	if result.Matched && result.Benchmark && result.Reason == "" {
		result.Reason = "This command combines ordinary tests and Vitest benchmarks; benchmark instrumentation remains unvalidated."
	}
	return result
}

func resolveJestWords(root, directory string, words, stack []string, remaining *int, frameworks ...string) commandResolution {
	framework := "jest"
	if len(frameworks) > 0 {
		framework = frameworks[0]
	}
	if len(words) == 0 {
		return commandResolution{}
	}
	command := shellquote.Join(words...)
	var assignments []string
	for len(words) > 0 && strings.Contains(words[0], "=") {
		key, _, _ := strings.Cut(words[0], "=")
		if key == "NODE_OPTIONS" || key == "PATH" || strings.HasPrefix(key, "DD_") {
			return unresolvedCommand("Command overrides instrumentation or executable selection: " + command)
		}
		assignments = append(assignments, words[0])
		words = words[1:]
	}
	if len(words) == 0 {
		return unresolvedCommand("Standalone environment assignment requires review: " + command)
	}
	if literalFileOperation(words) {
		return commandResolution{}
	}
	if words[0] == "npm" && len(words) > 2 && (words[1] == "-g" || words[1] == "--global") && slices.Contains([]string{"i", "install"}, words[2]) {
		return commandResolution{}
	}
	if words[0] == "npm" {
		prefix, args, err := npmPrefix(words[1:])
		if err != nil {
			return unresolvedCommand(err.Error())
		}
		if prefix != "" {
			target, err := repositoryDirectory(root, directory, prefix)
			if err != nil {
				return unresolvedCommand(err.Error())
			}
			result := resolveJestWords(root, target, append([]string{"npm"}, args...), stack, remaining, frameworks...)
			if result.Evidence != "" {
				result.Evidence = command + " (in " + target + ") -> " + result.Evidence
			}
			// Local flag forwarding from the repository root is not validated
			// for a test runner in a different package directory.
			original, _ := filepath.EvalSymlinks(directory)
			result.SingleJest = result.SingleJest && target == original
			return result
		}
	}
	if (words[0] == "npx" || words[0] == "pnpx") || (len(words) > 1 && ((words[0] == "pnpm" || words[0] == "npm") && words[1] == "exec" || words[0] == "pnpm" && words[1] == "dlx")) {
		if words[0] == "npx" || words[0] == "pnpx" {
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
	if words[0] == framework || words[0] == "./node_modules/.bin/"+framework || words[0] == "node_modules/.bin/"+framework || words[0] == filepath.Join(directory, "node_modules", ".bin", framework) {
		if framework == "vitest" && len(words) > 1 && words[1] == "bench" {
			return commandResolution{Benchmark: true, ReviewReason: "Vitest benchmark mode is not validated by the test adapter; do not add instrumentation based on ordinary test validation: " + command}
		}
		canonicalRoot, _ := filepath.EvalSymlinks(root)
		canonicalDirectory, _ := filepath.EvalSymlinks(directory)
		relative, _ := filepath.Rel(canonicalRoot, canonicalDirectory)
		return commandResolution{Matched: true, SingleJest: true, Evidence: command,
			Tests: []TestCommand{{Directory: filepath.ToSlash(relative), Command: shellquote.Join(append(assignments, append([]string{framework}, words[1:]...)...)...)}}}
	}
	if slices.Equal(words, []string{"corepack", "enable"}) {
		return commandResolution{}
	}
	switch words[0] {
	case "echo", "printf", "true", "false", "eslint", "prettier", "oxlint", "oxfmt", "mkdir", "cp":
		return commandResolution{}
	case "unbuild", "rollup", "webpack", "tsup", "tsc", "tsgo":
		if (words[0] == "tsc" || words[0] == "tsgo") && slices.Contains(words[1:], "--noEmit") {
			return commandResolution{ReviewReason: "Type checking is outside " + framework + " validation: " + command}
		}
		canonicalRoot, _ := filepath.EvalSymlinks(root)
		canonicalDirectory, _ := filepath.EvalSymlinks(directory)
		relative, _ := filepath.Rel(canonicalRoot, canonicalDirectory)
		build := BuildCommand{Directory: filepath.ToSlash(relative), Command: shellquote.Join(append(assignments, words...)...)}
		result := commandResolution{Builds: []BuildCommand{build}}
		if words[0] != "tsc" && words[0] != "tsgo" {
			result.ReviewReason = "Build/tool command is outside " + framework + " validation; review its configuration separately if it also runs tests: " + command
		}
		return result
	case "jest", "playwright", "vitest", "mocha", "cypress":
		return commandResolution{ReviewReason: "Other test framework is outside " + framework + " validation: " + command}
	case "flow", "bundlewatch", "api-extractor", "docusaurus", "documentation", "automd", "codecov":
		return commandResolution{ReviewReason: "Build/tool command is outside " + framework + " validation; review its configuration separately if it also runs tests: " + command}
	case "clean-publish":
		return reviewReleaseLifecycle(directory, command, framework, []string{"prepublishOnly", "prepack", "prepare", "postpack", "publish", "postpublish"})
	case "pkg-pr-new":
		if len(words) > 1 && words[1] == "publish" {
			return reviewReleaseLifecycle(directory, command, framework, []string{"prepublishOnly", "prepack", "prepare", "postpack", "publish", "postpublish"})
		}
		return unresolvedCommand("Unknown publication command: " + command)
	case "git":
		if gitReleaseOperation(words) {
			return commandResolution{ReviewReason: "Git release operation is outside " + framework + " validation; review separately, including any Git hooks: " + command}
		}
		if slices.Equal(words, []string{"git", "diff", "--quiet", "--exit-code"}) {
			return commandResolution{ReviewReason: "Git content comparison is outside " + framework + " validation; review separately, including configured diff helpers."}
		}
		return unresolvedCommand("Unsupported git command requires review: " + command)
	case "npm", "yarn", "pnpm", "bun":
		// Yarn without a subcommand installs dependencies. npm accepts global
		// options before its install subcommand. Neither is a Jest entry point.
		if yarnImplicitInstall(words) {
			return commandResolution{}
		}
		if words[0] == "npm" && len(words) > 2 && (words[1] == "-g" || words[1] == "--global") && slices.Contains([]string{"i", "install"}, words[2]) {
			return commandResolution{}
		}
		if len(words) < 2 {
			return unresolvedCommand("Missing package-manager subcommand: " + command)
		}
		if slices.Contains([]string{"npm", "pnpm", "yarn"}, words[0]) && words[1] == "version" {
			return reviewReleaseLifecycle(directory, command, framework, []string{"preversion", "version", "postversion"})
		}
		if (words[0] == "npm" || words[0] == "pnpm") && words[1] == "publish" {
			return resolveNpmPublish(directory, words, framework)
		}
		if slices.Equal(words, []string{"npm", "config", "get", "cache"}) {
			return commandResolution{}
		}
		// These are setup/metadata commands, not explicit test entry points.
		if slices.Contains([]string{"ci", "install", "i", "--version", "--help"}, words[1]) {
			return commandResolution{}
		}
		if words[0] == "pnpm" && words[1] == "add" {
			return unresolvedCommand("Dependency-changing setup requires separate validation: " + command)
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
		result := resolvePackageScript(root, directory, name, forwarded, stack, command, remaining, frameworks...)
		for i := range result.Tests {
			if len(assignments) > 0 {
				result.Tests[i].Command = shellquote.Join(assignments...) + " " + result.Tests[i].Command
				for j := range result.Tests[i].Prerequisites {
					result.Tests[i].Prerequisites[j].Command = shellquote.Join(assignments...) + " " + result.Tests[i].Prerequisites[j].Command
				}
			}
		}
		for i := range result.Builds {
			if len(assignments) > 0 {
				result.Builds[i].Command = shellquote.Join(assignments...) + " " + result.Builds[i].Command
			}
		}
		return result
	default:
		return commandResolution{Reason: "Cannot statically resolve command: " + command, UnknownExecutable: true}
	}
}

func resolvePackageScript(root, directory, name string, args, stack []string, command string, remaining *int, frameworks ...string) commandResolution {
	key := filepath.Join(directory, "package.json") + ":" + name
	if slices.Contains(stack, key) {
		return unresolvedCommand("Package script cycle: " + strings.Join(append(slices.Clone(stack), key), " -> "))
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
		if name == "flow" && (strings.HasPrefix(command, "yarn flow") || strings.HasPrefix(command, "pnpm flow")) {
			return resolveJestWords(root, directory, append([]string{"flow"}, args...), stack, remaining, frameworks...)
		}
		if len(frameworks) > 0 && frameworks[0] == "vitest" && name == "vitest" && (strings.HasPrefix(command, "pnpm vitest") || strings.HasPrefix(command, "yarn vitest")) {
			return resolveJestWords(root, directory, append([]string{"vitest"}, args...), stack, remaining, frameworks...)
		}
		return unresolvedCommand("No package.json script named " + name + " in " + directory)
	}
	var lifecycle string
	for _, hook := range []string{"pre" + name, "post" + name} {
		if manifest.Scripts[hook] != "" {
			lifecycle = "Package lifecycle script " + hook + " requires review alongside " + command
		}
	}
	if len(args) > 0 {
		script += " " + shellquote.Join(args...)
	}
	result := resolveJestSequence(root, directory, script, append(slices.Clone(stack), key), remaining, frameworks...)
	// Preserve the package manager's build entry point, including its lifecycle
	// and orchestration, when expansion cannot represent that build alone.
	if name == "build" && !result.Matched && orchestratedBuild(root, directory, script, manifest.Scripts, stack, remaining, frameworks...) {
		relative, _ := filepath.Rel(root, directory)
		return commandResolution{Builds: []BuildCommand{{Directory: filepath.ToSlash(relative), Command: command, Kind: "package-build"}}}
	}
	if lifecycle != "" {
		result.Reason = lifecycle
		result.UnknownExecutable = false
	}
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
