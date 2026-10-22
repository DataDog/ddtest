package onboard

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/kballard/go-shellquote"
)

func resolveCypressNode(root, directory string, words []string) commandResolution {
	if slices.Contains(words, "--component") {
		return unresolvedCommand("Cypress component testing is not supported")
	}
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
	return commandResolution{Matched: true, SingleJest: true, Evidence: command, Tests: []TestCommand{{CypressPackage: filepath.ToSlash(relative), Directory: filepath.ToSlash(relative), Command: command}}}
}

// CypressValidationCommand keeps server orchestration in the original alias.
func CypressValidationCommand(root, command string) (string, error) {
	entry, err := CypressExecutionCommand(root, command)
	return entry.Command, err
}

// CypressExecutionCommand chooses one declared invocation; it never invents one.
func CypressExecutionCommand(root, command string) (TestCommand, error) {
	if command != "" {
		remaining := 256
		resolution := resolveJestSequence(root, root, command, nil, &remaining, "cypress")
		if resolution.Reason != "" {
			return TestCommand{}, fmt.Errorf("cypress command requires review: %s", resolution.Reason)
		}
		entries := resolution.Tests
		if len(entries) != 1 {
			return TestCommand{}, fmt.Errorf("cypress command requires one supported headless invocation")
		}
		entry := entries[0]
		entry.Command = command
		return entry, nil
	}
	scope, err := DiscoverValidationScope(root, "cypress")
	if err != nil {
		return TestCommand{}, err
	}
	if len(scope.Commands) == 1 {
		return scope.Commands[0], nil
	}
	if len(scope.Commands) > 1 {
		return TestCommand{}, fmt.Errorf("multiple Cypress CI commands found; select --command or use --all")
	}
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	var pkg struct{ Scripts map[string]string }
	if err != nil || json.Unmarshal(data, &pkg) != nil {
		return TestCommand{}, fmt.Errorf("no Cypress command found")
	}
	var candidates []TestCommand
	for _, name := range slices.Sorted(maps.Keys(pkg.Scripts)) {
		candidate := shellquote.Join("npm", "run", name)
		entries := ValidationCommands(root, candidate, "cypress")
		if len(entries) == 1 {
			entries[0].Command = candidate
			candidates = append(candidates, entries[0])
		}
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	if len(candidates) > 1 {
		return TestCommand{}, fmt.Errorf("multiple Cypress package scripts found; select --command")
	}
	return TestCommand{}, fmt.Errorf("no Cypress command found")
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
	if !cypressInputBool(step.With["runTests"], true) {
		for _, key := range []string{"build", "start", "start-windows", "wait-on", "install-command"} {
			if step.With[key] != "" {
				return unresolvedCommand("Cypress setup-only action requires preparation and readiness validation before later test steps")
			}
		}
		return commandResolution{}
	}

	return cypressActionPlan(root, workflow, job, step)
}

// Match cypress-io/github-action's getInputBool, including its fallback.
func cypressInputBool(value string, fallback bool) bool {
	switch strings.TrimSpace(value) {
	case "true", "1":
		return true
	case "false", "0":
		return false
	default:
		return fallback
	}
}

// A standalone literal shell exit reports CI status; it cannot run tests.
func cypressStatusGate(command string) bool {
	executable, args, env, err := ParseValidationCommand(command)
	if err != nil || executable != "exit" || len(env) > 0 || len(args) > 1 {
		return false
	}
	if len(args) == 0 {
		return true
	}
	status, err := strconv.Atoi(args[0])
	return err == nil && status >= 0 && status <= 255
}
