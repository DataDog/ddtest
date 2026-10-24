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
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/environment"
	"github.com/kballard/go-shellquote"
)

// A resolution can be unrelated, matched, or unresolved. A matched command with
// an unresolved component is still inconclusive; it must not hide custom wrappers.
type commandResolution struct {
	MochaAuxiliary bool
	OtherFramework bool
	Setup          []string

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
		if framework == "cypress" {
			return resolveCypressAction(root, workflow, job, step)
		}

		return commandResolution{Review: true, Reason: "Cypress GitHub Action is outside " + framework + " validation"}
	}
	if language == "javascript" && framework == "cypress" && cypressStatusGate(step.Run) {
		return commandResolution{Review: true, Reason: "CI status gate is outside Cypress test execution"}
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
	if language != "javascript" || (framework != "jest" && framework != "vitest" && framework != "mocha" && framework != "playwright" && framework != "cypress" && framework != "cucumber" && framework != "webdriverio") {
		return commandResolution{Matched: environment.LooksLikeTestCommand(root, step.Run, language, framework)}
	}
	// GitHub expands these before the shell runs; they are not shell syntax.
	if expression := workflowExpression.FindString(step.Run); expression != "" {
		return unresolvedCommand("unresolved CI workflow expression requires review: " + expression)
	}
	if framework == "cypress" {
		if plan, ok := cypressBackgroundPlan(root, workflow, job, step); ok {
			return plan
		}
	}
	directory := stepDirectory(workflow, job, step)
	shell := workflow.Defaults.Run.Shell
	for _, defaults := range []runDefaults{job.Defaults.Run, {WorkingDirectory: step.WorkingDirectory, Shell: step.Shell}} {
		if defaults.Shell != "" {
			shell = defaults.Shell
		}
	}
	if shell == "" {
		shell = runnerDefaultShell(job, nil)
	}
	if !supportedCIShell(shell) {
		return unresolvedCommand("Unsupported CI shell: " + shell)
	}

	if strings.ContainsAny(directory, "$`~") || (directory != "" && !filepath.IsLocal(directory)) {
		return unresolvedCommand("Cannot statically resolve repository working-directory: " + directory)
	}
	resolvedDirectory, err := repositoryDirectory(root, root, directory)
	if err != nil {
		return unresolvedCommand("Cannot resolve repository working-directory: " + repositoryError(root, err))
	}
	if result, ok := resolveReleaseInput(resolvedDirectory, step.Run, framework); ok {
		return result
	}
	if literalDataRedirect(step.Run) {
		return commandResolution{Review: true, Reason: "Literal data transformation requires separate review: " + step.Run, Setup: []string{"File output changes require separate validation: " + step.Run}}
	}
	if result, ok := resolveCIShell(root, workflow, job, step, framework); ok {
		return result
	}

	if framework == "mocha" {
		if result, ok := reviewMochaBrowserStep(root, resolvedDirectory, step.Run); ok {
			return result
		}
	}
	remaining := packageScriptExpansionLimit
	result := resolveJestSequence(root, resolvedDirectory, step.Run, nil, &remaining, framework)

	if _, err := staticCommands(step.Run); err != nil {
		if metadata, ok := resolveMetadataStep(root, workflow, job, step, framework); ok {
			return metadata
		}
	}
	if result.MochaAuxiliary && (result.ReviewReason == "" || result.Matched) {
		result.Reason = joinDiagnostic(result.Reason, "Node script is not identified as Mocha outside a resolved Mocha package command; review its test scope.")
		for i := range result.Tests {
			result.Tests[i].UnvalidatedSetup = append(result.Tests[i].UnvalidatedSetup, result.Reason)
		}
	}
	if !result.Matched && result.Reason == "" && result.ReviewReason != "" {
		result.Review, result.Reason = true, result.ReviewReason
	}

	return result
}

// Bound package script and composite expansion so a cyclic or very large
// alias graph cannot hang discovery.
const packageScriptExpansionLimit = 256

// Resolve only ordinary static commands and package script aliases. Never run
// a shell, load JavaScript, or rewrite the CI entry point during discovery.
func resolveJestCommand(directory, command string, stack []string) commandResolution {
	remaining := packageScriptExpansionLimit
	return resolveJestSequence(directory, directory, command, stack, &remaining)
}

func resolveJestSequence(root, directory, command string, stack []string, remaining *int, frameworks ...string) commandResolution {
	if len(stack) >= 16 {
		return unresolvedCommand("Package script nesting exceeds 16 levels")
	}

	if len(frameworks) > 0 && frameworks[0] == "playwright" {
		if inner, ok := playwrightFailureGuard(command); ok {
			result := resolveJestSequence(root, directory, inner, stack, remaining, frameworks...)
			if result.Reason != "" || !result.SingleJest || len(result.Tests) != 1 || len(result.Builds) > 0 {
				return unresolvedCommand("Playwright failure guard requires a single test invocation")
			}
			result.Tests[0].ExitOnFailure = true
			// Package-manager arguments would be appended to exit, not Playwright.
			result.SingleJest = false
			return result
		}
	}
	commands, err := discoverCommands(command)
	if err != nil {
		return unresolvedCommand(repositoryError(root, err))
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
			result.Reason = joinDiagnostic(result.Reason, fragment.Reason)
			result.UnknownExecutable = false
			continue
		}
		*remaining--
		if *remaining < 0 {
			return unresolvedCommand(fmt.Sprintf("Package script expansion exceeds %d commands", packageScriptExpansionLimit))
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
			setup = append(setup, "CI file or dependency changes require separate validation: "+shellCommand(words))
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
		result.MochaAuxiliary = result.MochaAuxiliary || part.MochaAuxiliary
		setup = append(setup, part.Setup...)

		result.Builds = append(result.Builds, part.Builds...)
		result.SingleJest = len(commands) == 1 && part.SingleJest
		if part.ReviewReason != "" {
			result.ReviewReason = joinDiagnostic(result.ReviewReason, part.ReviewReason)
		}
		if part.Reason != "" {
			if result.Reason == "" {
				result.Reason = part.Reason
				result.UnknownExecutable = part.UnknownExecutable
			} else {
				result.Reason = joinDiagnostic(result.Reason, part.Reason)
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

	result.Setup = append(result.Setup, setup...)
	return result
}

func resolveJestWords(root, directory string, words, stack []string, remaining *int, frameworks ...string) (result commandResolution) {
	framework := "jest"
	if len(frameworks) > 0 {
		framework = frameworks[0]
	}

	if len(words) == 0 {
		return commandResolution{}
	}
	words, assignments, err := commandEnvironment(words)
	if err != nil {
		return unresolvedCommand(err.Error())
	}
	defer func() { applyCommandEnvironment(&result, assignments) }()
	command := shellCommand(words)
	if literalFileOperation(words) {
		return commandResolution{}
	}
	if words[0] == "npm" && len(words) > 2 && (words[1] == "-g" || words[1] == "--global") && slices.Contains([]string{"i", "install"}, words[2]) {
		return commandResolution{}
	}
	if words[0] == "npm" {
		prefix, args, err := npmPrefix(words[1:])
		if err != nil {
			return unresolvedCommand(repositoryError(root, err))
		}
		if prefix != "" {
			target, err := repositoryDirectory(root, directory, prefix)
			if err != nil {
				return unresolvedCommand(repositoryError(root, err))
			}
			result := resolveJestWords(root, target, append([]string{"npm"}, args...), stack, remaining, frameworks...)
			if result.Evidence != "" {
				result.Evidence = command + " (in " + repositoryPath(root, target) + ") -> " + result.Evidence
			}
			// Local flag forwarding from the repository root is not validated
			// for a test runner in a different package directory.
			original, _ := filepath.EvalSymlinks(directory)
			result.SingleJest = result.SingleJest && target == original
			return result
		}
	}
	if (words[0] == "npx" || words[0] == "pnpx") || (len(words) > 1 && ((words[0] == "pnpm" || words[0] == "npm") && words[1] == "exec" || words[0] == "pnpm" && words[1] == "dlx")) {
		npmExec := words[0] == "npm"
		if words[0] == "npx" || words[0] == "pnpx" {
			words = words[1:]
			// testdrive itself adds --no-install, so the executed command
			// must still match the discovered CI configuration.
			for len(words) > 0 && slices.Contains([]string{"--no-install", "--no", "--yes", "-y"}, words[0]) {
				words = words[1:]
			}
		} else {
			words = words[2:]
		}

		if (framework == "mocha" || framework == "cypress" || framework == "cucumber" || framework == "webdriverio") && len(words) > 0 && slices.Contains([]string{"--no-install", "--no"}, words[0]) {
			words = words[1:]
		}
		if npmExec {
			separator := slices.Index(words, "--")
			for i, word := range words {
				if i == separator {
					break
				}
				if strings.HasPrefix(word, "-") {
					return unresolvedCommand("npm exec options before -- require review: " + command)
				}
			}
			if separator >= 0 {
				words = slices.Delete(words, separator, separator+1)
			}
		} else if len(words) > 0 && words[0] == "--" {
			words = words[1:]
		}
		if len(words) == 0 {
			return unresolvedCommand("Missing executable: " + command)
		}
	}

	if framework != "webdriverio" && filepath.Base(words[0]) == "wdio" {
		return commandResolution{OtherFramework: true}
	}
	runner := framework
	if framework == "webdriverio" {
		runner = "wdio"
	}
	if framework == "cucumber" {
		runner = "cucumber-js"
	}

	if words[0] == runner || words[0] == "./node_modules/.bin/"+runner || words[0] == "node_modules/.bin/"+runner || words[0] == filepath.Join(directory, "node_modules", ".bin", runner) {
		if framework == "mocha" && mochaWatchMode(directory, words[1:]) {
			return unresolvedCommand("Mocha watch mode is not supported; select a finite test command")
		}
		if framework == "jest" && jestMetadataOptions(words[1:]) {
			return commandResolution{}
		}
		// Any other first argument is a config path, whatever its file name.
		if framework == "webdriverio" && len(words) > 1 && slices.Contains([]string{"config", "install", "repl"}, words[1]) {
			return commandResolution{ReviewReason: "WebdriverIO setup/tool command is outside test execution: " + command}
		}
		if framework == "cypress" && (len(words) < 2 || words[1] != "run") {
			return commandResolution{ReviewReason: "Cypress setup/interactive command is outside headless validation: " + command}
		}
		if framework == "playwright" && (len(words) < 2 || words[1] != "test") {
			return commandResolution{ReviewReason: "Playwright setup/tool command is outside test execution: " + command}
		}
		if framework == "playwright" && playwrightInteractiveMode(words[2:]) {
			return unresolvedCommand("Playwright UI and debug modes are interactive and are not supported; select a finite test command")
		}
		probeCommand := ""
		if framework == "cucumber" {
			probeCommand = shellquote.Join(append([]string{runner}, words[1:]...)...)
		}

		canonicalRoot, _ := filepath.EvalSymlinks(root)
		canonicalDirectory, _ := filepath.EvalSymlinks(directory)
		relative, _ := filepath.Rel(canonicalRoot, canonicalDirectory)
		entry := TestCommand{cucumberProbeCommand: probeCommand, Directory: filepath.ToSlash(relative), Command: shellquote.Join(append([]string{runner}, words[1:]...)...)}
		if framework == "cypress" {
			if slices.Contains(words, "--component") {
				return unresolvedCommand("Cypress component testing is not supported")
			}
			entry.CypressPackage = filepath.ToSlash(relative)
		}
		if framework == "mocha" {
			entry.mochaIstanbul = MochaBabelIstanbulConfigured(directory)
			entry.mochaRunnerArgs = slices.Clone(words[1:])
			entry.mochaProbeCommand = shellquote.Join(words...)
			entry.mochaLookupDirectory = filepath.ToSlash(relative)
			entry.mochaProbeDirectory = filepath.ToSlash(relative)
			entry.mochaESM = mochaESMInputs(directory, words[1:])
		}
		return commandResolution{Matched: true, SingleJest: true, Evidence: command, Tests: []TestCommand{entry}}
	}

	if framework == "webdriverio" && words[0] == "node" {
		return resolveWebdriverIONode(root, directory, words)
	}
	if framework == "cypress" && words[0] == "node" {
		return resolveCypressNode(root, directory, words)
	}

	if framework == "cucumber" && (filepath.Base(words[0]) == "nyc" || filepath.Base(words[0]) == "c8") {
		return resolveMochaCoverage(root, directory, words, stack, remaining, framework)
	}
	if framework == "mocha" {
		if words[0] == "mochify" || mochaBrowserShell(root, directory, words[0]) {
			return commandResolution{ReviewReason: "Mochify browser tests are outside Node Mocha validation: " + command}
		}
		if words[0] == "node" {
			return resolveMochaNode(root, directory, words)
		}
		if filepath.Base(words[0]) == "c8" || filepath.Base(words[0]) == "nyc" {
			return resolveMochaCoverage(root, directory, words, stack, remaining, framework)
		}
	}
	if tool, ok := discoveryTools[words[0]]; ok {
		return resolveDiscoveryTool(root, directory, words, framework, tool)
	}
	if args, ok := nodeJestArguments(words); framework == "jest" && ok {
		if jestMetadataOptions(args) {
			return commandResolution{}
		}
		relative := repositoryPath(root, directory)
		return commandResolution{Matched: true, SingleJest: true, Evidence: command, Tests: []TestCommand{{Directory: relative, Command: shellquote.Join(words...)}}}
	}
	if framework == "vitest" && IsDDTestCommand(words[0]) {
		if _, err := ParseVitestRunnerCommand(words[1:]); err != nil {
			return unresolvedCommand(err.Error())
		}
		return commandResolution{Matched: true, SingleJest: true, Evidence: command, Tests: []TestCommand{{Directory: repositoryPath(root, directory), Command: shellquote.Join(words...)}}}
	}
	if slices.Equal(words, []string{"corepack", "enable"}) {
		return commandResolution{}
	}
	switch words[0] {
	case "turbo":
		if framework == "playwright" {
			return reviewPlaywrightTurbo(root, directory, words, stack, remaining)
		}
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
		if framework == "cypress" {
			return resolveCypressOrchestration(root, directory, words, stack, remaining)
		}

		return unresolvedCommand("Unsupported script orchestration: " + command)
	case "npm-run-all", "npm-run-all2", "run-s":
		if (framework != "mocha" && framework != "cucumber" && framework != "webdriverio") || len(words) < 2 {
			return unresolvedCommand("Unsupported script orchestration: " + command)
		}
		var scripts []string
		for _, name := range words[1:] {
			if strings.ContainsAny(name, "*?[]{} ") || strings.HasPrefix(name, "-") {
				return unresolvedCommand("Script orchestration needs literal script names: " + command)
			}
			scripts = append(scripts, shellquote.Join("npm", "run", name))
		}
		return resolveJestSequence(root, directory, strings.Join(scripts, " && "), stack, remaining, framework)

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
		if len(forwarded) > 0 && forwarded[0] == "--" && words[0] != "pnpm" {
			forwarded = forwarded[1:]
		} else if words[0] == "npm" && len(forwarded) > 0 {
			return unresolvedCommand("npm script arguments without -- require review: " + command)
		}
		result := resolvePackageScript(root, directory, name, forwarded, stack, shellCommand(words), remaining, frameworks...)

		return result
	default:
		return commandResolution{Reason: "Cannot statically resolve command: " + command, UnknownExecutable: true}
	}
}

func resolvePackageScript(root, directory, name string, args, stack []string, command string, remaining *int, frameworks ...string) commandResolution {

	if len(frameworks) > 0 && frameworks[0] == "webdriverio" && strings.HasPrefix(command, "npm ") {
		canonicalRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			return unresolvedCommand("Cannot resolve repository root")
		}
		for {
			if _, err := os.Stat(filepath.Join(directory, "package.json")); err == nil {
				break
			}
			relative, err := filepath.Rel(canonicalRoot, directory)
			if err != nil || !filepath.IsLocal(relative) || relative == "." {
				break
			}
			directory = filepath.Dir(directory)
		}
	}
	key := repositoryPath(root, filepath.Join(directory, "package.json")) + ":" + name
	if slices.Contains(stack, key) {
		return unresolvedCommand("Package script cycle: " + strings.Join(append(slices.Clone(stack), key), " -> "))
	}
	data, err := os.ReadFile(filepath.Join(directory, "package.json"))
	if err != nil {
		return unresolvedCommand("Cannot read package.json for " + command + ": " + repositoryError(root, err))
	}
	var manifest struct {
		Scripts         map[string]string `json:"scripts"`
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return unresolvedCommand("Cannot parse package.json: " + err.Error())
	}
	script, ok := manifest.Scripts[name]

	if !ok {
		if len(frameworks) > 0 && frameworks[0] == "playwright" && name == "playwright" && (strings.HasPrefix(command, "pnpm playwright") || strings.HasPrefix(command, "yarn playwright")) {
			return resolveJestWords(root, directory, append([]string{"playwright"}, args...), stack, remaining, frameworks...)
		}
		if len(frameworks) > 0 && frameworks[0] == "vitest" && name == "vitest" && (strings.HasPrefix(command, "pnpm vitest") || strings.HasPrefix(command, "yarn vitest")) {
			return resolveJestWords(root, directory, append([]string{"vitest"}, args...), stack, remaining, frameworks...)
		}
		if strings.HasPrefix(command, "yarn ") && name == "jest" {
			if manifest.Dependencies["jest"] != "" || manifest.DevDependencies["jest"] != "" {
				return resolveJestWords(root, directory, append([]string{"jest"}, args...), stack, remaining, frameworks...)
			}
			return unresolvedCommand("Cannot establish Yarn binary jest from repository dependencies in " + repositoryPath(root, directory))
		}
		return unresolvedCommand("No package.json script named " + name + " in " + repositoryPath(root, directory))
	}
	var lifecycle string
	for _, hook := range []string{"pre" + name, "post" + name} {
		if manifest.Scripts[hook] != "" {
			lifecycle = joinDiagnostic(lifecycle, "Package lifecycle script "+hook+" requires review alongside "+command)
		}
	}
	if len(args) > 0 {
		script += " " + shellquote.Join(args...)
	}

	mocha := len(frameworks) > 0 && frameworks[0] == "mocha"
	cucumber := len(frameworks) > 0 && frameworks[0] == "cucumber"
	cucumberScoped := cucumber && strings.HasPrefix(script, cucumberScopedOptions+" ")
	if cucumber {
		script = cucumberDiscoveryScript(script)
		parts, err := staticCommands(script)
		if variable := shellVariable.FindStringSubmatch(script); err != nil && variable != nil {
			return unresolvedCommand("Cucumber alias uses unresolved environment variable $" + variable[1] + "; select the command with its value expanded: " + command)
		}
		if err != nil || len(parts) != 1 {
			return unresolvedCommand("Cucumber aliases must forward formatter options to one invocation; compound scripts require explicit setup review: " + command)
		}
	}
	webdriverIOScoped := len(frameworks) > 0 && frameworks[0] == "webdriverio" && strings.HasPrefix(script, webdriverIOScopedOptions+" ")
	if len(frameworks) > 0 && frameworks[0] == "webdriverio" {
		script = webdriverIODiscoveryScript(script)
	}
	var result commandResolution
	if (mocha || cucumber || (len(frameworks) > 0 && (frameworks[0] == "cypress" || frameworks[0] == "webdriverio"))) && strings.HasPrefix(command, "npm ") {
		// npm starts each lifecycle script in a fresh shell at the package root.
		// Resolve them independently so an inner cd/export cannot affect the next hook.
		singleInvocation := true
		leaf := -1
		for i, part := range []string{manifest.Scripts["pre"+name], script, manifest.Scripts["post"+name]} {
			if part == "" {
				continue
			}
			resolved := resolveJestSequence(root, directory, part, append(slices.Clone(stack), key), remaining, frameworks...)
			if resolved.Matched {
				singleInvocation = singleInvocation && resolved.SingleJest
			}
			if len(resolved.Tests) > 0 {
				leaf = i
			}
			result.Tests = append(result.Tests, resolved.Tests...)
			result.Builds = append(result.Builds, resolved.Builds...)
			result.Setup = append(result.Setup, resolved.Setup...)
			result.Matched = result.Matched || resolved.Matched
			result.OtherFramework = result.OtherFramework || resolved.OtherFramework
			result.MochaAuxiliary = result.MochaAuxiliary || resolved.MochaAuxiliary
			result.Reason = joinDiagnostic(result.Reason, resolved.Reason)
			result.ReviewReason = joinDiagnostic(result.ReviewReason, resolved.ReviewReason)
			result.Evidence = joinDiagnostic(result.Evidence, resolved.Evidence)
		}
		lifecycle = ""
		result.SingleJest = singleInvocation && len(result.Tests) == 1 && manifest.Scripts["pre"+name] == "" && manifest.Scripts["post"+name] == ""
		if cucumber && len(result.Tests) == 1 {
			// Probes call the runner directly. Record the hooks npm runs around
			// the Cucumber leaf, outermost first, so probes can replay them.
			test := &result.Tests[0]
			if leaf > 0 && manifest.Scripts["pre"+name] != "" {
				test.cucumberProbeBefore = append([]LifecycleScript{{Directory: repositoryPath(root, directory), Name: "pre" + name}}, test.cucumberProbeBefore...)
			}
			if leaf < 2 && manifest.Scripts["post"+name] != "" {
				test.cucumberProbeAfter = append(test.cucumberProbeAfter, LifecycleScript{Directory: repositoryPath(root, directory), Name: "post" + name})
			}
		}
	} else {
		result = resolveJestSequence(root, directory, script, append(slices.Clone(stack), key), remaining, frameworks...)
	}

	if webdriverIOScoped && result.Reason == "" && !result.OtherFramework && len(result.Tests) == 1 {
		result.Tests[0].webdriverIOScoped = true
	}
	if cucumberScoped && result.Reason == "" && !result.OtherFramework && len(result.Tests) == 1 {
		result.Tests[0].cucumberScoped = true
	}
	if mocha && lifecycle == "" && result.Reason == "" && !result.Matched && result.MochaAuxiliary && name == "build" {
		return commandResolution{Builds: []BuildCommand{{Directory: repositoryPath(root, directory), Command: command, Kind: "package-build"}}}
	}
	if len(frameworks) > 0 && frameworks[0] == "cypress" && len(result.Tests) > 1 {
		return unresolvedCommand("Multiple Cypress invocations in one package alias require ordered execution review; preserve the original alias: " + command)
	}
	if len(frameworks) > 0 && frameworks[0] == "webdriverio" && len(result.Tests) > 1 {
		return unresolvedCommand("Multiple WebdriverIO invocations in one package alias require ordered execution review; preserve the original alias: " + command)
	}
	if (mocha || cucumber) && len(result.Tests) > 1 {
		label := "Mocha"
		if cucumber {
			label = "Cucumber"
		}
		return unresolvedCommand("Multiple " + label + " invocations in one package alias require ordered execution review; preserve the original alias: " + command)
	}
	if (mocha || cucumber || (len(frameworks) > 0 && (frameworks[0] == "cypress" || frameworks[0] == "webdriverio"))) && lifecycle == "" && result.Reason == "" && !result.OtherFramework && len(result.Tests) == 1 {
		if !result.SingleJest && result.Tests[0].mochaProbeCommand == "" {
			result.Tests[0].mochaProbeCommand = result.Tests[0].Command
		}
		result.SingleJest = true
		result.MochaAuxiliary = false
		if cucumber && (result.Tests[0].Directory != repositoryPath(root, directory) || len(result.Tests[0].Environment) > 0) {
			return unresolvedCommand("Cucumber alias changes its inner working directory or environment; use an explicit scoped invocation after reviewing that setup: " + command)
		}
		// The full alias starts at the caller for lifecycle fidelity; probes
		// retain their resolved runner directory and scoped environment.
		result.Tests[0].Directory = repositoryPath(root, directory)
		if len(result.Tests[0].Environment) > 0 {
			// Outer assignments apply, but an inner script's own assignment
			// (MODE=inner under MODE=outer npm run unit) wins, as in the shell.
			merged := maps.Clone(result.Tests[0].Environment)
			maps.Copy(merged, result.Tests[0].mochaProbeEnvironment)
			result.Tests[0].mochaProbeEnvironment = merged
		}
		for _, hook := range []string{"pre" + name, "post" + name} {
			if value := manifest.Scripts[hook]; value != "" {
				result.Tests[0].LifecycleHooks = append(result.Tests[0].LifecycleHooks, repositoryPath(root, directory)+": "+hook+" -> "+value)
			}
		}
		result.Tests[0].Environment = nil
		result.Tests[0].Resolution = command + " -> " + result.Evidence
		result.Tests[0].Command = command
		result.Tests[0].Prerequisites = nil
		result.Builds = nil
	}

	// An npm alias without -- consumes newly appended runner flags. Expand
	// this boundary when forwarding, while retaining already-safe aliases.
	if result.SingleJest && !mocha {
		commands, err := staticCommands(script)
		if err == nil && len(commands) == 1 {
			words, _, err := commandEnvironment(commands[0])
			if err == nil && words[0] == "npm" && !slices.Contains(words, "--") {
				result.SingleJest = false
			}
		}
	}

	// Preserve the package manager's build entry point, including its lifecycle
	// and orchestration, when expansion cannot represent that build alone.
	if name == "build" && !result.Matched && orchestratedBuild(root, directory, script, manifest.Scripts, stack, remaining, frameworks...) {
		relative := repositoryPath(root, directory)
		return commandResolution{Reason: lifecycle, Builds: []BuildCommand{{Directory: filepath.ToSlash(relative), Command: command, Kind: "package-build"}}}
	}

	if lifecycle != "" {
		result.Reason = joinDiagnostic(result.Reason, lifecycle)
		for i := range result.Tests {
			result.Tests[i].UnvalidatedSetup = append(result.Tests[i].UnvalidatedSetup, lifecycle)
		}
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

// Help, version and configuration output exit before running a suite,
// including their explicit =true forms. The runner's -- ends option parsing.
func jestMetadataOptions(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		option, value, explicit := strings.Cut(arg, "=")
		if slices.Contains([]string{"--help", "-h", "--version", "-v", "--showConfig", "--show-config"}, option) && (!explicit || value == "true") {
			return true
		}
	}
	return false
}

// --ui waits for the user and --debug opens the Inspector without a timeout.
func playwrightInteractiveMode(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		option, _, _ := strings.Cut(arg, "=")
		if slices.Contains([]string{"--ui", "--ui-host", "--ui-port", "--debug"}, option) {
			return true
		}
	}
	return false
}
