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
	"regexp"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/environment"
	"github.com/kballard/go-shellquote"
)

// Recognize only the documented scoped preload during discovery. Execution
// keeps the original alias; arbitrary instrumentation overrides remain unresolved.
const cucumberScopedOptions = `NODE_OPTIONS="${NODE_OPTIONS:-} ${DD_CUCUMBER_NODE_OPTIONS:-}"`

func cucumberDiscoveryScript(script string) string {
	return strings.ReplaceAll(script, cucumberScopedOptions, "")
}

func CucumberValidationCommand(root, command string) (string, error) {
	if strings.TrimSpace(command) == "" {
		scope, err := DiscoverValidationScope(root, "cucumber")
		if err != nil {
			return "", err
		}
		if len(scope.Commands) == 0 {
			return "", nil
		}
		// A CI environment alone is applied to the run, as for Jest and Playwright.
		if len(scope.Commands) != 1 || filepath.Clean(scope.Commands[0].Directory) != "." || len(scope.Commands[0].Prerequisites) > 0 {
			return "", fmt.Errorf("cucumber CI commands or build prerequisites require --all")
		}
		command = scope.Commands[0].Command
	}
	parts, err := staticCommands(command)
	if err != nil || len(parts) != 1 {
		return "", fmt.Errorf("cucumber command must forward formatter options to one invocation; review compound setup separately")
	}
	resolved := ValidationCommands(root, command, "cucumber")
	if len(resolved) != 1 || len(resolved[0].UnvalidatedSetup) > 0 {
		return "", fmt.Errorf("cucumber validation needs one resolved invocation with its profile and setup; use --all for CI commands")
	}
	return command, nil
}

// CucumberProbeCommand returns the selected Cucumber leaf, resolved through
// its npm aliases. Probes use it only to locate the coverage wrapper, the
// runner and --parallel; an owned config replaces its other Cucumber options.
func CucumberProbeCommand(root, command string) (string, error) {
	remaining := 256
	result := resolveJestSequence(root, root, command, nil, &remaining, "cucumber")
	if result.Reason != "" || len(result.Tests) != 1 || result.Tests[0].cucumberProbeCommand == "" {
		return "", fmt.Errorf("cucumber probe command could not be resolved: %s", result.Reason)
	}
	return result.Tests[0].cucumberProbeCommand, nil
}

// LifecycleScript is a package lifecycle hook that npm runs around an alias.
type LifecycleScript struct{ Directory, Name string }

// CucumberProbeLifecycle returns the npm pre and post scripts that run before
// and after the selected Cucumber leaf, in npm's order. The scripts holding
// the leaf itself, and the aliases' main scripts, are not included.
func CucumberProbeLifecycle(root, command string) (before, after []LifecycleScript, err error) {
	remaining := 256
	result := resolveJestSequence(root, root, command, nil, &remaining, "cucumber")
	if result.Reason != "" || len(result.Tests) != 1 {
		return nil, nil, fmt.Errorf("cucumber probe lifecycle could not be resolved: %s", result.Reason)
	}
	return result.Tests[0].cucumberProbeBefore, result.Tests[0].cucumberProbeAfter, nil
}

func CucumberUsesNYC(root, command string) bool {
	commands := ValidationCommands(root, command, "cucumber")
	return len(commands) == 1 && commands[0].usesNYC
}

// CucumberCoverageWrapper names the coverage tool wrapping the resolved
// Cucumber leaf, such as nyc or c8, or returns "" when there is none.
func CucumberCoverageWrapper(root, command string) string {
	commands := ValidationCommands(root, command, "cucumber")
	if len(commands) != 1 {
		return ""
	}
	return commands[0].cucumberCoverageWrapper
}

func checkCucumberBootstrap(root string, workflow ciWorkflow, job runtimeJob, step runtimeStep, row map[string]any, commands []TestCommand) string {
	// Scope checks use the same statically discovered command tree. Unknown
	// wrappers remain inconclusive rather than being treated as Cucumber-only.
	remaining := 256
	resolution := resolveJestSequence(root, filepath.Join(root, stepDirectory(workflow, job, step)), step.Run, nil, &remaining, "cucumber")
	scoped := ""
	for _, layer := range []map[string]string{workflow.Env, job.Env, step.Env} {
		if value, ok := layer["DD_CUCUMBER_NODE_OPTIONS"]; ok {
			scoped = value
		}
	}
	if resolution.OtherFramework && (scoped == "" || checkJestBootstrap(workflow, job, step, row, false) == "") {
		return "Cucumber-only scope is not preserved: this command also runs another framework. Keep the original command and pass DD_CUCUMBER_NODE_OPTIONS only to the Cucumber leaf."
	}
	directory := filepath.Join(root, stepDirectory(workflow, job, step))
	if len(commands) == 1 && filepath.IsLocal(commands[0].Directory) {
		directory = filepath.Join(root, commands[0].Directory)
	}
	data, err := os.ReadFile(filepath.Join(directory, "package.json"))
	var pkg struct {
		Type string
	}
	if err != nil || json.Unmarshal(data, &pkg) != nil {
		return "Cannot inspect Cucumber package scripts"
	}
	if scoped != "" {
		if len(commands) == 0 {
			return "Cannot identify a Cucumber leaf consuming DD_CUCUMBER_NODE_OPTIONS"
		}
		for _, command := range commands {
			if !command.cucumberScoped {
				return "Selected Cucumber leaf does not consume scoped DD_CUCUMBER_NODE_OPTIONS while preserving existing NODE_OPTIONS"
			}
		}
		configured := step
		configured.Env = maps.Clone(step.Env)
		if configured.Env == nil {
			configured.Env = map[string]string{}
		}
		configured.Env["NODE_OPTIONS"] = scoped
		if reason := checkJestBootstrap(workflow, job, configured, row, true); reason != "" {
			return reason
		}
	} else if reason := checkJestBootstrap(workflow, job, step, row, true); reason != "" {
		return reason
	}
	if pkg.Type == "module" || CucumberESMInputs(root, commands) {
		options := scoped
		for _, env := range []map[string]string{workflow.Env, job.Env, step.Env} {
			if value, ok := env["NODE_OPTIONS"]; ok {
				options = scoped + " " + value
			}
		}
		if !nodeOptionsLoad(options, "@istanbuljs/esm-loader-hook") {
			return "Cannot verify Istanbul instrumentation for native ESM Cucumber CI code. Include --experimental-loader @istanbuljs/esm-loader-hook in the scoped Cucumber Node options, or review custom/pre-instrumented source coverage separately."
		}
	}
	for _, command := range commands {
		if !command.usesNYC {
			if command.cucumberCoverageWrapper == "c8" {
				return "The selected Cucumber command uses c8 V8 coverage, which does not supply the Istanbul suite coverage required for TIA. ddtest preserves c8 and does not add a second NYC wrapper; review application instrumentation separately."
			}
			return "Cucumber CI source coverage is not configured. Preserve the command and wrap its Cucumber leaf with nyc; native ESM also requires Istanbul instrumentation such as @istanbuljs/esm-loader-hook. Local coverage does not configure CI."
		}
	}
	return ""
}

// nodeOptionsLoad reports whether NODE_OPTIONS registers module as a loader,
// in either the "--loader module" or the "--loader=module" form.
func nodeOptionsLoad(options, module string) bool {
	words, err := environment.SplitNodeOptions(options)
	if err != nil {
		return false
	}
	for i, word := range words {
		key, value, equals := strings.Cut(word, "=")
		if key != "--loader" && key != "--experimental-loader" {
			continue
		}
		if !equals && i+1 < len(words) {
			value = words[i+1]
		}
		if value == module {
			return true
		}
	}
	return false
}

// Native ESM sources, also inside a config's quoted path or glob lists.
var cucumberESMPath = regexp.MustCompile(`\.m[jt]s(?:$|["'\s,\])}])`)

// cucumberESMPathArgument reports whether a command word, or the value of a
// --option=value word, names an .mjs or .mts path.
func cucumberESMPathArgument(word string) bool {
	if strings.HasPrefix(word, "-") {
		_, value, ok := strings.Cut(word, "=")
		if !ok {
			return false
		}
		word = value
	}
	return slices.Contains([]string{".mjs", ".mts"}, filepath.Ext(word))
}

// Conservative input inspection only: executing arbitrary configuration here
// would be an onboarding side effect. Dynamic config remains subject to execution.
func CucumberESMInputs(root string, commands []TestCommand) bool {
	for _, command := range commands {
		directory := filepath.Join(root, command.Directory)
		configs := []string{"cucumber.js", "cucumber.cjs", "cucumber.mjs", "cucumber.json", "cucumber.yaml", "cucumber.yml"}
		words, _ := shellquote.Split(command.cucumberProbeCommand)
		if slices.ContainsFunc(words, cucumberESMPathArgument) {
			return true
		}
		for i, word := range words {
			if word == "--config" || word == "-c" {
				if i+1 < len(words) {
					configs = []string{words[i+1]}
					break
				}
			}
			if value, ok := strings.CutPrefix(word, "--config="); ok {
				configs = []string{value}
				break
			}
		}
		for _, name := range configs {
			file := name
			if !filepath.IsAbs(file) {
				file = filepath.Join(directory, file)
			}
			data, err := os.ReadFile(file)
			if err == nil {
				if cucumberESMPathArgument(file) || cucumberESMPath.Match(data) {
					return true
				}
				break
			}
		}
	}
	return false
}
