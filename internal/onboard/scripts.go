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

	"github.com/DataDog/ddtest/internal/environment"
	"github.com/kballard/go-shellquote"
)

// A resolution can be unrelated, matched, or unresolved. A matched command with
// an unresolved component is still inconclusive; it must not hide custom wrappers.
type commandResolution struct {
	OtherFramework bool

	Tests   []TestCommand
	Builds  []BuildCommand
	Matched bool

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

	uses, _, _ := strings.Cut(strings.ToLower(step.Uses), "@")
	if language == "javascript" && uses == "cypress-io/github-action" {

		return commandResolution{Review: true, Reason: "Cypress GitHub Action is outside " + framework + " validation"}
	}
	if step.ResolutionError != "" {
		return unresolvedCommand(step.ResolutionError)
	}
	if strings.TrimSpace(step.Run) == "" {
		if step.Uses != "" && len(step.Env) > 0 {
			return commandResolution{Review: true, Reason: "Action implementation and its private environment are outside local " + framework + " execution; no CI secret is resolved or forwarded."}
		}
		return commandResolution{}
	}
	if language != "javascript" || framework != "jest" {
		return commandResolution{Matched: environment.LooksLikeTestCommand(root, step.Run, language, framework)}
	}
	directory := stepDirectory(workflow, job, step)
	shell := workflow.Defaults.Run.Shell
	for _, defaults := range []runDefaults{job.Defaults.Run, {WorkingDirectory: step.WorkingDirectory, Shell: step.Shell}} {
		if defaults.Shell != "" {
			shell = defaults.Shell
		}
	}
	if !supportedCIShell(shell) {
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
	if legacyCodecovUpload(step.Run) {
		return commandResolution{Review: true, Reason: "Legacy Codecov upload is outside identified " + framework + " entry points; the downloaded script is not inspected or validated."}
	}
	if result, ok := resolveCIShell(root, workflow, job, step, framework); ok {
		return result
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
	previousChain := 0
	for _, fragment := range commands {
		if previousChain != 0 && previousChain != fragment.Chain && len(result.Builds) > 0 {
			setup = append(setup, "Builds separated from tests by ; or a newline require shell exit-policy review; they are not conditional prerequisites.")
			result.Builds = nil
		}
		previousChain = fragment.Chain
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
		result.OtherFramework = result.OtherFramework || part.OtherFramework
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
	runner := framework

	if words[0] == runner || words[0] == "./node_modules/.bin/"+runner || words[0] == "node_modules/.bin/"+runner || words[0] == filepath.Join(directory, "node_modules", ".bin", runner) {
		canonicalRoot, _ := filepath.EvalSymlinks(root)
		canonicalDirectory, _ := filepath.EvalSymlinks(directory)
		relative, _ := filepath.Rel(canonicalRoot, canonicalDirectory)
		return commandResolution{Matched: true, SingleJest: true, Evidence: command,
			Tests: []TestCommand{{Directory: filepath.ToSlash(relative), Command: shellquote.Join(append(assignments, append([]string{runner}, words[1:]...)...)...)}}}
	}

	if slices.Equal(words, []string{"corepack", "enable"}) {
		return commandResolution{}
	}
	switch words[0] {
	case "echo", "printf", "true", "false", "eslint", "standard", "prettier", "oxlint", "oxfmt", "mkdir", "cp", "knip":
		return commandResolution{}
	case "unbuild", "rollup", "webpack", "tsup", "tsc", "tsgo", "bunchee":
		if words[0] == "bunchee" {
			return commandResolution{Reason: "Cannot statically resolve command: " + command, UnknownExecutable: true}
		}
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
	case "jest", "playwright", "vitest", "mocha", "cypress", "cucumber-js", "tap", "mochify":
		return commandResolution{OtherFramework: true, ReviewReason: "Other test framework is outside " + framework + " validation: " + command}
	case "flow", "bundlewatch", "api-extractor", "docusaurus", "documentation", "automd", "codecov", "vitepress", "ls-engines", "lockfile-lint", "remark", "ban", "publint", "gherkin-lint":
		return commandResolution{ReviewReason: "Build/tool command is outside " + framework + " validation; review its configuration separately if it also runs tests: " + command}
	case "clean-publish":
		return reviewReleaseLifecycle(directory, command, framework, []string{"prepublishOnly", "prepack", "prepare", "postpack", "publish", "postpublish"})
	case "pkg-pr-new":
		if len(words) > 1 && words[1] == "publish" {
			return reviewReleaseLifecycle(directory, command, framework, []string{"prepublishOnly", "prepack", "prepare", "postpack", "publish", "postpublish"})
		}
		return unresolvedCommand("Unknown publication command: " + command)
	case "turbo":

		return unresolvedCommand("Cannot statically resolve command: " + command)
	case "git":
		if gitReleaseOperation(words) {
			return commandResolution{ReviewReason: "Git release operation is outside " + framework + " validation; review separately, including any Git hooks: " + command}
		}
		if slices.Equal(words, []string{"git", "diff", "--quiet", "--exit-code"}) {
			return commandResolution{ReviewReason: "Git content comparison is outside " + framework + " validation; review separately, including configured diff helpers."}
		}
		return unresolvedCommand("Unsupported git command requires review: " + command)
	case "start-test", "start-server-and-test", "run-p":

		return unresolvedCommand("Unsupported script orchestration: " + command)
	case "npm-run-all", "npm-run-all2", "run-s":

		return unresolvedCommand("Unsupported script orchestration: " + command)

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
		if (words[0] == "npm" && slices.Contains([]string{"ci", "clean-install"}, words[1])) || slices.Contains([]string{"install", "i", "--version", "--help"}, words[1]) {
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

// Reuse AST discovery for callers that require every command to be literal.
func staticCommands(command string) ([][]string, error) {
	fragments, err := discoverCommands(command)
	if err != nil {
		return nil, err
	}
	var commands [][]string
	for _, fragment := range fragments {
		if fragment.Reason != "" {
			return nil, fmt.Errorf("%s", fragment.Reason)
		}
		commands = append(commands, fragment.Words)
	}
	return commands, nil
}
