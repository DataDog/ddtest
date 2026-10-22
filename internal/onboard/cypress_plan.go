package onboard

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/kballard/go-shellquote"
)

// CypressCommand retains the execution path and native options. Cloud inputs
// are deliberately excluded from local execution; CI files are never rewritten.
type CypressCommand struct {
	Action      string         `json:"action,omitempty"`
	Options     map[string]any `json:"options,omitempty"`
	CLI         []string       `json:"cli,omitempty"`
	Start       [][]string     `json:"start,omitempty"`
	WaitOn      []string       `json:"wait_on,omitempty"`
	WaitTimeout int            `json:"wait_timeout_seconds,omitempty"`
	Platform    string         `json:"platform,omitempty"`
}

// Limits bound literal CI inputs without executing a shell or unbounded setup.
const cypressInputLimit = 16384
const cypressDefaultWaitTimeout = 60

var cypressResolvedInputs = []string{"browser", "config", "env", "expose", "project", "config-file", "spec", "headed", "quiet", "component", "working-directory", "command-prefix", "command", "build", "start", "start-windows", "wait-on", "wait-on-timeout", "runTests"}

func cypressActionPlan(root string, workflow ciWorkflow, job runtimeJob, step runtimeStep) commandResolution {
	options := map[string]any{"record": false, "parallel": false}
	plan := &CypressCommand{Action: step.Uses, Options: options, WaitTimeout: cypressDefaultWaitTimeout}
	if job.RunsOn != nil {
		runner, ok := job.RunsOn.(string)
		if !ok || !strings.HasPrefix(runner, "ubuntu-") && !strings.HasPrefix(runner, "macos-") && !strings.HasPrefix(runner, "windows-") {
			return unresolvedCommand("Cypress action runner OS requires a resolved hosted label")
		}
		switch {
		case strings.HasPrefix(runner, "windows-"):
			plan.Platform = "windows"
		case strings.HasPrefix(runner, "macos-"):
			plan.Platform = "darwin"
		case strings.HasPrefix(runner, "ubuntu-"):
			plan.Platform = "linux"
		}
	}
	for key, value := range step.With {
		if len(value) > cypressInputLimit || strings.ContainsRune(value, 0) {
			return unresolvedCommand("Cypress action input exceeds validation limits: " + key)
		}
		if slices.Contains([]string{"project", "config-file", "working-directory"}, key) && value != "" && !filepath.IsLocal(value) {
			return unresolvedCommand("Cypress action path must stay in the repository: " + key)
		}
		if value == "" {
			continue
		}
		switch key {
		case "browser", "config", "env", "expose", "project":
			// Match the action: Cypress parses these serialized Module API inputs.
			options[key] = value
		case "config-file":
			options["configFile"] = value
		case "spec":
			options[key] = strings.FieldsFunc(value, func(c rune) bool { return c == ',' || c == '\n' })
		case "headed", "quiet", "component":
			options[key] = cypressInputBool(value, false)
			if key == "component" && options[key] == true && step.With["command"] == "" {
				return unresolvedCommand("Cypress component testing is not supported")
			}
		case "working-directory":
			if !filepath.IsLocal(value) {
				return unresolvedCommand("Cypress working-directory must stay in the repository")
			}
		case "install-command":
			return unresolvedCommand("Cypress custom install-command requires separate setup validation: " + value)
		case "command": // Validated as a preserved literal CLI invocation below.
		case "command-prefix":
			return unresolvedCommand("Cypress command-prefix needs its original CLI wrapper validated separately")
		case "build", "start", "start-windows", "wait-on", "wait-on-timeout", "runTests",
			"install", "cache-key", "package-manager-cache", "publish-summary", "summary-title",
			"record", "parallel", "group", "tag", "ci-build-id", "auto-cancel-after-failures":
		default:
			return unresolvedCommand("Cypress action input requires review: " + key)
		}
	}
	start := step.With["start"]
	if plan.Platform == "windows" && step.With["start-windows"] != "" {
		start = step.With["start-windows"]
	}
	for _, command := range strings.FieldsFunc(start, func(c rune) bool { return c == ',' || c == '\n' }) {
		words, err := staticCommands(command)
		if err != nil || len(words) != 1 || len(words[0]) == 0 || strings.Contains(words[0][0], "=") {
			return unresolvedCommand("Cypress startup requires literal executable arguments")
		}
		plan.Start = append(plan.Start, words[0])
	}
	for _, address := range strings.Split(step.With["wait-on"], ",") {
		if address = strings.TrimSpace(address); address != "" {
			if !strings.HasPrefix(address, "http://") && !strings.HasPrefix(address, "https://") {
				return unresolvedCommand("Cypress wait-on requires an HTTP(S) URL")
			}
			plan.WaitOn = append(plan.WaitOn, address)
		}
	}
	if value := step.With["wait-on-timeout"]; value != "" {
		seconds, err := strconv.Atoi(value)
		if err != nil || seconds <= 0 || seconds > 3600 {
			return unresolvedCommand("Cypress wait-on timeout must be between 1 and 3600 seconds")
		}
		plan.WaitTimeout = seconds
	}
	directory := "." // Actions do not inherit defaults.run.working-directory.
	if value := step.With["working-directory"]; value != "" {
		directory = value
	}
	if directory == "" {
		directory = "."
	}
	data, _ := json.Marshal(options)
	entry := TestCommand{CypressPackage: directory, Directory: directory, Command: "Cypress.run(" + string(data) + ")", Cypress: plan}
	if custom := step.With["command"]; custom != "" {
		words, err := staticCommands(custom)
		if err != nil || len(words) != 1 || len(words[0]) == 0 {
			return unresolvedCommand("Cypress custom command requires one literal invocation")
		}
		child := step
		child.Uses = ""
		child.Run = custom
		child.WorkingDirectory = directory
		resolved := resolveTestStep(root, workflow, job, child, "javascript", "cypress")
		if resolved.Reason != "" || !resolved.Matched || resolved.OtherFramework || len(resolved.Tests) != 1 {
			return unresolvedCommand("Cypress custom command needs its original setup reviewed: " + resolved.Reason)
		}
		entry = resolved.Tests[0]
		entry.Directory = directory
		entry.Command = custom
		entry.Cypress = plan
		plan.Action = ""
		plan.Options = nil // The real action ignores Module API inputs with command.
		executable, arguments, assignments, err := ParseValidationCommand(custom)
		if err != nil {
			return unresolvedCommand(err.Error())
		}
		entry.Environment = mergeEnvironment(entry.Environment, assignments)
		plan.CLI = append([]string{executable}, arguments...)
	}
	if build := step.With["build"]; build != "" {
		commands, err := staticCommands(build)
		if err != nil || len(commands) == 0 {
			return unresolvedCommand("Cypress build requires literal executable arguments")
		}
		for _, words := range commands {
			if len(words) == 0 || strings.Contains(words[0], "=") {
				return unresolvedCommand("Cypress build environment requires review")
			}
			entry.Prerequisites = append(entry.Prerequisites, BuildCommand{Directory: directory, Command: shellquote.Join(words...), Kind: "build"})
		}
	}
	return commandResolution{Matched: true, Tests: []TestCommand{entry}, Evidence: step.Uses + " -> " + entry.Command}
}

// Support the ordinary two-command startup pattern without executing shell
// syntax or discarding additional commands, redirects, or background consumers.
func cypressBackgroundPlan(root string, workflow ciWorkflow, job runtimeJob, step runtimeStep) (commandResolution, bool) {
	file, ok := parseCIShell(step.Run)
	if !ok || len(file.Stmts) != 2 {
		return commandResolution{}, false
	}
	start, test := file.Stmts[0], file.Stmts[1]
	if !start.Background || start.Negated || start.Coprocess || len(start.Redirs) != 0 ||
		test.Background || test.Negated || test.Coprocess || len(test.Redirs) != 0 {
		return commandResolution{}, false
	}
	servers, err := staticCommands(shellText(start.Cmd))
	if err != nil || len(servers) != 1 || len(servers[0]) < 2 || !slices.Contains([]string{"npm", "yarn", "pnpm", "node"}, servers[0][0]) {
		return commandResolution{}, false
	}
	commands, err := staticCommands(shellText(test.Cmd))
	if err != nil || len(commands) != 1 {
		return commandResolution{}, false
	}
	child := step
	child.Run = shellText(test.Cmd)
	resolved := resolveTestStep(root, workflow, job, child, "javascript", "cypress")
	if !resolved.Matched || resolved.Reason != "" || len(resolved.Tests) != 1 {
		return commandResolution{}, false
	}
	entry := resolved.Tests[0]
	entry.Command = strings.TrimSpace(step.Run)
	executable, arguments, assignments, err := ParseValidationCommand(shellText(test.Cmd))
	if err != nil {
		return unresolvedCommand(err.Error()), true
	}
	entry.Environment = mergeEnvironment(entry.Environment, assignments)
	entry.Cypress = &CypressCommand{CLI: append([]string{executable}, arguments...), Start: servers, WaitTimeout: cypressDefaultWaitTimeout}
	resolved.Tests, resolved.Evidence = []TestCommand{entry}, fmt.Sprintf("Managed startup %s -> %s", shellquote.Join(servers[0]...), shellquote.Join(commands[0]...))
	return resolved, true
}
