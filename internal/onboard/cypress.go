package onboard

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
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

// CypressExecutionCommand chooses one declared invocation; it never invents one.
func CypressExecutionCommand(root, command string) (TestCommand, error) {
	var scope ValidationScope
	var err error
	if command == "" {
		scope, err = DiscoverValidationScope(root, "cypress")
	}
	return CypressExecutionCommandInScope(root, command, scope, err)
}

// CypressExecutionCommandInScope selects from an already discovered scope.
func CypressExecutionCommandInScope(root, command string, scope ValidationScope, scopeErr error) (TestCommand, error) {
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
		// The original command is relative to the caller, even when it selects
		// another package (npm --prefix); CypressPackage keeps that package.
		entry.Command = command
		entry.Directory = "."
		return entry, nil
	}
	if scopeErr != nil {
		return TestCommand{}, scopeErr
	}
	if len(scope.Commands) == 1 {
		return scope.Commands[0], nil
	}
	if len(scope.Commands) > 1 {
		// Rows for other runner OSes stay unvalidated; they never make a host
		// configuration ambiguous.
		host := slices.DeleteFunc(slices.Clone(scope.Commands), func(entry TestCommand) bool {
			return entry.Platform != "" && entry.Platform != runtime.GOOS
		})
		if len(host) == 1 {
			return host[0], nil
		}
		return TestCommand{}, fmt.Errorf("multiple Cypress CI commands found; select --command or use --all")
	}
	// A package script stands in only when CI has no Cypress command at all.
	if len(scope.Unresolved) > 0 {
		return TestCommand{}, fmt.Errorf("the Cypress CI command could not be resolved; select the command to validate with --command: %s", strings.Join(scope.Unresolved, "; "))
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
	var startup, readiness, test string
	switch words[0] {
	case "start-test", "start-server-and-test":
		if len(words) == 3 {
			startup, readiness, test = "start", words[1], words[2]
		} else if len(words) == 4 {
			startup, readiness, test = words[1], words[2], words[3]
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
	if len(stack) == 0 {
		// A CI step runs the orchestration itself; replay its server startup and
		// readiness for every run, as the action's start/wait-on inputs are.
		// Inside a package alias the full alias already runs it.
		return cypressOrchestrationPlan(root, directory, words, startup, readiness, result)
	}
	return result
}

// start-server-and-test's own default readiness timeout (WAIT_ON_TIMEOUT).
const cypressOrchestrationWaitTimeout = 300

func cypressOrchestrationPlan(root, directory string, words []string, startup, readiness string, result commandResolution) commandResolution {
	var waitOn []string
	if readiness != "" {
		address := readiness
		if port, err := strconv.Atoi(strings.TrimPrefix(address, ":")); err == nil && port > 0 && port < 65536 {
			address = "http://127.0.0.1:" + strconv.Itoa(port)
		}
		if !strings.HasPrefix(address, "http://") && !strings.HasPrefix(address, "https://") || strings.Contains(address, "|") {
			return unresolvedCommand("Cypress server orchestration readiness requires one HTTP(S) URL or port: " + readiness)
		}
		waitOn = []string{address}
	}
	entry := result.Tests[0]
	executable, arguments, assignments, err := ParseValidationCommand(entry.Command)
	if err != nil {
		return unresolvedCommand(err.Error())
	}
	entry.Environment = mergeEnvironment(entry.Environment, assignments)
	entry.Directory = repositoryPath(root, directory)
	entry.Command = shellquote.Join(words...)
	entry.Cypress = &CypressCommand{CLI: append([]string{executable}, arguments...), Start: [][]string{{"npm", "run", startup}}, WaitOn: waitOn, WaitTimeout: cypressOrchestrationWaitTimeout}
	result.Tests = []TestCommand{entry}
	result.Evidence = "Managed startup npm run " + startup + " -> " + result.Evidence
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
