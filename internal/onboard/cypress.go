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

func resolveCypressNode(root, directory string, words []string) commandResolution {
	i := 1
	for i < len(words) && strings.HasPrefix(words[i], "-") {
		flag, _, joined := strings.Cut(words[i], "=")
		if !slices.Contains([]string{"-C", "--conditions", "-r", "--require", "--import", "--loader"}, flag) {
			return unresolvedCommand("Cypress Node flag requires review: " + words[i])
		}
		i++
		if !joined {
			i++
		}
	}
	if i+1 >= len(words) || words[i+1] != "run" {
		return unresolvedCommand("Cypress Node command must invoke its headless CLI directly")
	}
	cli := filepath.Clean(strings.TrimPrefix(words[i], "./"))
	if cli != filepath.Join("node_modules", "cypress", "bin", "cypress") && cli != filepath.Join("node_modules", ".bin", "cypress") {
		return unresolvedCommand("Cypress Node entry point requires its local CLI")
	}
	relative, _ := filepath.Rel(root, directory)
	command := shellquote.Join(words...)
	return commandResolution{Matched: true, SingleJest: true, Evidence: command, Tests: []TestCommand{{Directory: filepath.ToSlash(relative), Command: command}}}
}

// CypressValidationCommand keeps server orchestration in the original alias.
func CypressValidationCommand(root, command string) (string, error) {
	resolve := func(command string) commandResolution {
		remaining := 256
		return resolveJestSequence(root, root, command, nil, &remaining, "cypress")
	}
	if command != "" {
		resolved := resolve(command)
		if resolved.Reason != "" || len(resolved.Tests) != 1 {
			return command, fmt.Errorf("cypress command requires review: %s", resolved.Reason)
		}
		return command, nil
	}
	if scope, err := DiscoverValidationScope(root, "cypress"); err == nil && len(scope.Commands) == 1 && scope.Commands[0].Directory == "." {
		// Prefer the actual CI alias, including its application coverage/startup
		// setup, over a generic local:run or test alias. Unknown CI setup remains
		// visible in the validation scope and is not certified by this selection.
		return scope.Commands[0].Command, nil
	}
	for _, name := range []string{"local:run", "test:ci", "test:e2e", "e2e", "test", "cypress:run"} {
		candidate := shellquote.Join("npm", "run", name)
		result := resolve(candidate)
		if result.Reason == "" && len(result.Tests) == 1 {
			return candidate, nil
		}
	}
	return "npx --no-install cypress run", nil
}

func resolveCypressOrchestration(root, directory string, words, stack []string, remaining *int) commandResolution {
	data, err := os.ReadFile(filepath.Join(directory, "package.json"))
	var pkg struct{ Scripts map[string]string }
	if err != nil || json.Unmarshal(data, &pkg) != nil {
		return unresolvedCommand("Cannot read Cypress orchestration package.json")
	}
	var startup, test string
	switch words[0] {
	case "start-test", "start-server-and-test":
		if len(words) == 3 {
			startup, test = "start", words[2]
		} else if len(words) == 4 {
			startup, test = words[1], words[3]
		} else {
			return unresolvedCommand("Cypress server orchestration needs a literal start command, URL/port and test script")
		}
	case "run-p":
		if len(words) != 4 || words[1] != "--race" {
			return unresolvedCommand("Cypress parallel orchestration requires run-p --race with one server and one test script")
		}
		startup, test = words[2], words[3]
	}
	if pkg.Scripts[startup] == "" || strings.ContainsAny(startup+test, "*?[]{}$\x60") {
		return unresolvedCommand("Cypress orchestration requires literal package scripts")
	}
	testCommand := pkg.Scripts[test]
	if testCommand == "" {
		testCommand = test
	}
	result := resolveJestSequence(root, directory, testCommand, stack, remaining, "cypress")
	if result.Reason != "" || len(result.Tests) != 1 {
		return unresolvedCommand("Cannot identify one Cypress run in server orchestration: " + result.Reason)
	}
	// A second Cypress consumer in startup must not disappear from scope.
	start := resolveJestSequence(root, directory, pkg.Scripts[startup], stack, remaining, "cypress")
	if start.Matched {
		return unresolvedCommand("Cypress orchestration startup also runs tests")
	}
	result.SingleJest = false
	result.Evidence = shellquote.Join(words...) + " -> " + result.Evidence
	return result
}

// Preserve the action's API execution path instead of substituting a CLI run.
func resolveCypressAction(root string, workflow ciWorkflow, job runtimeJob, step runtimeStep) commandResolution {
	if step.With["runTests"] == "false" {
		return commandResolution{}
	}
	if command := step.With["command"]; command != "" {
		step.Uses, step.Run = "", command
		return resolveTestStep(root, workflow, job, step, "javascript", "cypress")
	}
	return cypressActionPlan(root, workflow, job, step)
}
