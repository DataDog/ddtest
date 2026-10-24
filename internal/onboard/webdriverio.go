package onboard

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Match the shared resolver budget; bound source reads used only for discovery hints.
const webdriverIOScriptBudget = 256
const webdriverIOMaxHintSourceBytes = 1 << 20

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
	if err != nil || !info.Mode().IsRegular() || info.Size() > webdriverIOMaxHintSourceBytes {
		return unknown
	}
	source, err := os.ReadFile(path)
	if err != nil || !webdriverIOStandaloneImport.Match(source) || webdriverIORunnerImport.Match(source) {
		return unknown
	}
	return commandResolution{OtherFramework: true, ReviewReason: "Standalone WebDriver automation is outside WDIO test-runner validation; preserve this script without the WDIO preload: " + strings.Join(words, " ")}
}

func checkWebdriverIOBootstrap(root string, workflow ciWorkflow, job runtimeJob, step runtimeStep, row map[string]any) string {
	remaining := webdriverIOScriptBudget
	resolution := resolveJestSequence(root, filepath.Join(root, stepDirectory(workflow, job, step)), step.Run, nil, &remaining, "webdriverio")
	scoped := ""
	for _, layer := range []map[string]string{workflow.Env, job.Env, step.Env} {
		if value, ok := layer["DD_WDIO_NODE_OPTIONS"]; ok {
			scoped = value
		}
	}
	if resolution.OtherFramework && (scoped == "" || checkJestBootstrap(workflow, job, step, row, false) == "") {
		return "This command also runs standalone automation or another runner. Preserve the command; set DD_WDIO_NODE_OPTIONS on the CI step and consume it only in WDIO package leaves, preserving existing NODE_OPTIONS."
	}
	if scoped == "" {
		return checkJestBootstrap(workflow, job, step, row, true)
	}
	for _, command := range resolution.Tests {
		if !command.webdriverIOScoped {
			return "WDIO leaf " + command.Command + " must start with " + webdriverIOScopedOptions + " while preserving its existing command"
		}
	}

	scopedStep := step
	scopedStep.Env = maps.Clone(step.Env)
	if scopedStep.Env == nil {
		scopedStep.Env = map[string]string{}
	}
	scopedStep.Env["NODE_OPTIONS"] = scoped
	return checkJestBootstrap(workflow, job, scopedStep, row, true)
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
		environment := maps.Clone(scope.Commands[0].Environment)
		if environment["NODE_OPTIONS"] == "" {
			delete(environment, "NODE_OPTIONS")
		}
		if filepath.Clean(scope.Commands[0].Directory) != "." || len(scope.Commands[0].Prerequisites) > 0 || len(environment) > 0 {
			return "", fmt.Errorf("multiple WebdriverIO invocations; use --all or --command for a focused comparison")
		}
		command = scope.Commands[0].Command
	}
	remaining := webdriverIOScriptBudget
	resolution := resolveJestSequence(root, root, command, nil, &remaining, "webdriverio")
	commands := resolution.Tests
	if resolution.Reason != "" || resolution.OtherFramework || len(commands) != 1 || len(commands[0].UnvalidatedSetup) > 0 {
		return "", fmt.Errorf("WebdriverIO validation requires one resolved wdio run invocation; preserve configuration and setup")
	}
	return command, nil
}
