package onboard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

const webdriverIOScopedOptions = `NODE_OPTIONS="${NODE_OPTIONS:-} ${DD_WDIO_NODE_OPTIONS:-}"`

// Only this documented optional preload is normalized for discovery. The
// original alias is executed, including its existing Node options.
func webdriverIODiscoveryScript(script string) string {
	return strings.ReplaceAll(script, webdriverIOScopedOptions, "")
}

var webdriverIOStandaloneImport = regexp.MustCompile(`(?m)^\s*import\s+[^;\n]+\s+from\s*['"](?:webdriverio|webdriver|devtools)['"]`)
var webdriverIORunnerImport = regexp.MustCompile(`['"](?:@wdio/cli|@wdio/globals|mocha|jasmine|jasmine-core)['"]`)

// Standalone automation is a separate entry point, not a WDIO test runner.
// This is a discovery hint; unknown wrappers remain unresolved and visible.
func resolveWebdriverIONode(root, directory string, words []string) commandResolution {
	unknown := unresolvedCommand("Cannot identify this Node script as a WDIO runner or standalone automation: " + strings.Join(words, " "))
	if len(words) != 2 || !filepath.IsLocal(words[1]) || !slices.Contains([]string{".js", ".cjs", ".mjs"}, filepath.Ext(words[1])) {
		return unknown
	}
	path, err := filepath.EvalSymlinks(filepath.Join(directory, words[1]))
	canonical, _ := filepath.EvalSymlinks(root)
	relative, relErr := filepath.Rel(canonical, path)
	if err != nil || relErr != nil || !filepath.IsLocal(relative) {
		return unknown
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return unknown
	}
	source, err := os.ReadFile(path)
	if err != nil || !webdriverIOStandaloneImport.Match(source) || webdriverIORunnerImport.Match(source) {
		return unknown
	}
	return commandResolution{OtherFramework: true, ReviewReason: "Standalone WebDriver automation is outside WDIO test-runner validation; preserve this script without the WDIO preload: " + strings.Join(words, " ")}
}

func checkWebdriverIOBootstrap(root string, workflow ciWorkflow, job runtimeJob, step runtimeStep, row map[string]any) string {
	remaining := 256
	resolution := resolveJestSequence(root, filepath.Join(root, stepDirectory(workflow, job, step)), step.Run, nil, &remaining, "webdriverio")
	scoped := ""
	for _, layer := range []map[string]string{workflow.Env, job.Env, step.Env} {
		if value, ok := layer["DD_WDIO_NODE_OPTIONS"]; ok {
			scoped = value
		}
	}
	if resolution.OtherFramework && (scoped == "" || checkJestBootstrap(workflow, job, step, row) == "") {
		return "This command also runs standalone automation or another runner. Preserve the command; set DD_WDIO_NODE_OPTIONS on the CI step and consume it only in WDIO package leaves, preserving existing NODE_OPTIONS."
	}
	if scoped == "" {
		return checkJestBootstrap(workflow, job, step, row, "webdriverio")
	}
	var pkg struct{ Scripts map[string]string }
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil || json.Unmarshal(data, &pkg) != nil {
		return "Cannot inspect scoped WDIO package scripts"
	}
	for _, command := range resolution.Tests {
		parts, err := staticCommands(command.Command)
		if err != nil || len(parts) != 1 {
			return "Scoped WDIO instrumentation requires an identified npm leaf consuming DD_WDIO_NODE_OPTIONS"
		}
		name := ""
		if slices.Equal(parts[0], []string{"npm", "test"}) {
			name = "test"
		} else if len(parts[0]) == 3 && parts[0][0] == "npm" && parts[0][1] == "run" {
			name = parts[0][2]
		} else {
			return "Scoped WDIO instrumentation requires an identified npm leaf consuming DD_WDIO_NODE_OPTIONS"
		}
		script := pkg.Scripts[name]
		if !strings.HasPrefix(script, webdriverIOScopedOptions+" ") {
			return "WDIO leaf " + command.Command + " must start with " + webdriverIOScopedOptions + " while preserving its existing command"
		}
	}
	copy := step
	copy.Env = map[string]string{}
	for key, value := range step.Env {
		copy.Env[key] = value
	}
	copy.Env["NODE_OPTIONS"] = scoped
	return checkJestBootstrap(workflow, job, copy, row, "webdriverio")
}

// WebdriverIOValidationCommand keeps the actual package/CLI entry point.
func WebdriverIOValidationCommand(root, command string) (string, error) {
	if strings.TrimSpace(command) == "" {
		scope, err := DiscoverValidationScope(root, "webdriverio")
		if err != nil {
			return "", err
		}
		if len(scope.Commands) == 0 {
			return "", nil
		}
		if len(scope.Commands) != 1 {
			return "", fmt.Errorf("multiple WebdriverIO invocations; use --all or --command for a focused comparison")
		}
		command = scope.Commands[0].Command
	}
	commands := ValidationCommands(root, command, "webdriverio")
	if len(commands) != 1 || len(commands[0].UnvalidatedSetup) > 0 {
		return "", fmt.Errorf("WebdriverIO validation requires one resolved wdio run invocation; preserve configuration and setup")
	}
	return command, nil
}

func webdriverIOJobCandidate(root string, workflow ciWorkflow, job runtimeJob) bool {
	for _, step := range job.Steps {
		r := resolveTestStep(root, workflow, job, step, "javascript", "webdriverio")
		if r.Matched || r.Reason != "" && (strings.Contains(step.Run, "npm ") || strings.Contains(step.Run, "pnpm ") || strings.Contains(step.Run, "yarn ") || strings.Contains(step.Run, "wdio")) {
			return true
		}
	}
	return false
}

func findWebdriverIOWorkflows(root string, workflows []ciWorkflow) (workflowDiscovery, error) {
	scope, err := DiscoverValidationScope(root, "webdriverio")
	if err != nil {
		return workflowDiscovery{}, err
	}
	result := workflowDiscovery{Unresolved: scope.Unresolved, Review: scope.Review}
	for _, workflow := range workflows {
		found, configured := false, true
		for _, job := range workflow.Jobs {
			if !webdriverIOJobCandidate(root, workflow, job) {
				continue
			}
			found = true
			hasAction := false
			for _, step := range job.Steps {
				uses, _, _ := strings.Cut(strings.ToLower(step.Uses), "@")
				hasAction = hasAction || uses == githubAction
			}
			configured = configured && hasAction
		}
		if found {
			result.Workflows = append(result.Workflows, workflow.Path)
			if configured {
				result.Configured = append(result.Configured, workflow.Path)
			}
		}
	}
	return result, nil
}
