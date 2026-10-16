// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package onboard prints the smallest supported Test Optimization setup.
package onboard

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/DataDog/ddtest/internal/platform"
	"github.com/kballard/go-shellquote"
	"github.com/pelletier/go-toml/v2"
	"go.yaml.in/yaml/v3"
)

const githubAction = "datadog/test-visibility-github-action"

//go:embed instructions/github.md
var gitHubInstructions string

// Run detects the first supported onboarding path and prints its instructions.
func Run(output io.Writer) error {
	repositoryRoot, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("find repository root: %w", err)
	}
	detectedPlatform, err := platform.DetectPlatform()
	if err != nil {
		return err
	}
	runner, err := detectedPlatform.DetectFramework()
	if err != nil {
		return err
	}
	language := detectedPlatform.Name()
	name := runner.Name()

	workflows, configured, err := findWorkflows(repositoryRoot, language, name)
	if err != nil {
		return err
	}
	if len(workflows) == 0 {
		return fmt.Errorf("onboard could not find a GitHub Actions workflow that runs %s", name)
	}

	_, _ = fmt.Fprintf(output, "DDTest found %s, %s, and GitHub Actions.\n", language, name)
	_, _ = fmt.Fprintln(output)
	_, _ = fmt.Fprintln(output, "Test workflow(s):")
	for _, workflow := range workflows {
		_, _ = fmt.Fprintf(output, "  - %s\n", workflow)
	}

	if len(configured) == len(workflows) {
		_, _ = fmt.Fprintln(output)
		_, _ = fmt.Fprintln(output, "Datadog Test Optimization already appears in every detected test workflow.")
		_, _ = fmt.Fprintf(output, "Run `ddtest testdrive --framework %s` to check the setup locally.\n", name)
		_, _ = fmt.Fprintln(output, "After it finishes, post every `Open report:` link to the user so they can open the local Test Optimization report.")
		return nil
	}

	_, _ = fmt.Fprintln(output)
	_, _ = fmt.Fprintln(output, strings.TrimSpace(instructions(language, name)))
	return nil
}

func findWorkflows(repositoryRoot, language, name string) ([]string, []string, error) {
	var workflows []string
	var configured []string
	directory := filepath.Join(repositoryRoot, ".github", "workflows")
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return workflows, configured, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("find GitHub Actions workflows: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || (filepath.Ext(entry.Name()) != ".yml" && filepath.Ext(entry.Name()) != ".yaml") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		contents, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", path, err)
		}
		foundTests, isConfigured, err := inspectTestJobs(repositoryRoot, contents, language, name)
		if err != nil {
			return nil, nil, fmt.Errorf("parse %s: %w", path, err)
		}
		if !foundTests {
			continue
		}

		relativePath, err := filepath.Rel(repositoryRoot, path)
		if err != nil {
			return nil, nil, fmt.Errorf("make workflow path relative: %w", err)
		}
		relativePath = filepath.ToSlash(relativePath)
		workflows = append(workflows, relativePath)
		if isConfigured {
			configured = append(configured, relativePath)
		}
	}

	sort.Strings(workflows)
	sort.Strings(configured)
	return workflows, configured, nil
}

func inspectTestJobs(root string, contents []byte, language, name string) (foundTests, configured bool, err error) {
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Run  string `yaml:"run"`
				Uses string `yaml:"uses"`
				If   string `yaml:"if"`
				With struct {
					Languages string `yaml:"languages"`
				} `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(contents, &workflow); err != nil {
		return false, false, err
	}
	actionLanguage := language
	if language == "javascript" {
		actionLanguage = "js"
	}
	for _, job := range workflow.Jobs {
		jobConfigured := false
		for _, step := range job.Steps {
			condition := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(step.If), "${{"), "}}"))
			if condition == "false" {
				continue
			}
			if looksLikeTestJob(root, step.Run, language, name, 0) {
				foundTests = true
				if !jobConfigured {
					return true, false, nil
				}
			}
			action, _, hasVersion := strings.Cut(strings.ToLower(step.Uses), "@")
			if action == githubAction && hasVersion && (condition == "" || condition == "true" || condition == "success()") && !strings.Contains(step.With.Languages, "${{") {
				languages := strings.Fields(step.With.Languages)
				jobConfigured = jobConfigured || step.With.Languages == "all" || slices.Contains(languages, actionLanguage)
			}
		}
	}
	return foundTests, foundTests, nil
}

// Resolve literal invocations without running project code. Limit script recursion
// so cycles in package.json cannot hang onboarding.
func looksLikeTestJob(root, commands, language, name string, depth int) bool {
	if depth > 10 {
		return false
	}
	for _, command := range strings.Split(commands, "\n") {
		command = strings.TrimSpace(command)
		if command == "" || strings.HasPrefix(command, "#") {
			continue
		}
		// Handle simple shell command lists. Leave quoted shell expressions opaque.
		if !strings.ContainsAny(command, "\"'`") && strings.ContainsAny(command, ";&|") {
			for _, part := range strings.FieldsFunc(command, func(r rune) bool { return r == ';' || r == '&' || r == '|' }) {
				if looksLikeTestJob(root, part, language, name, depth+1) {
					return true
				}
			}
			continue
		}
		args, err := shellquote.Split(command)
		if err != nil {
			continue
		}
		for len(args) > 0 && strings.Contains(args[0], "=") {
			args = args[1:]
		}
	unwrap:
		for len(args) > 1 {
			switch args[0] + " " + args[1] {
			case "bundle exec", "python -m", "python3 -m", "{envpython} -m", "poetry run":
				args = args[2:]
			case "uv run":
				args = args[2:]
				for len(args) > 0 && slices.Contains([]string{"--locked", "--frozen", "--no-sync"}, args[0]) {
					args = args[1:]
				}
			default:
				break unwrap
			}
		}
		if len(args) == 0 {
			continue
		}
		executable := filepath.Base(args[0])
		if language == "javascript" && slices.Contains([]string{"npm", "pnpm", "yarn", "bun", "npx", "bunx"}, executable) && len(args) > 1 {
			args = args[1:]
			direct := executable == "npx" || executable == "bunx" || args[0] == "exec" || args[0] == "dlx"
			explicitScript := args[0] == "run" || args[0] == "run-script"
			if !direct && !explicitScript && (slices.Contains([]string{"install", "add", "remove", "update"}, args[0]) || (executable == "npm" && args[0] != "test")) {
				continue
			}
			if args[0] == "run" || args[0] == "run-script" || args[0] == "exec" || args[0] == "dlx" {
				args = args[1:]
			}
			for len(args) > 0 && slices.Contains([]string{"--", "--silent", "--yes", "-y", "--no-install"}, args[0]) {
				args = args[1:]
			}
			if len(args) == 0 {
				continue
			}
			if direct {
				executable = filepath.Base(args[0])
			} else {
				var manifest struct {
					Scripts map[string]string `json:"scripts"`
				}
				contents, err := os.ReadFile(filepath.Join(root, "package.json"))
				if err != nil || json.Unmarshal(contents, &manifest) != nil {
					continue
				}
				if script, ok := manifest.Scripts[args[0]]; ok {
					if looksLikeTestJob(root, script, language, name, depth+1) {
						return true
					}
					continue
				}
				// Preserve conventional test entry points when the project has no script.
				if args[0] == "test" {
					return true
				}
				if executable == "npm" {
					continue
				}
				executable = filepath.Base(args[0])
			}
		}
		if executable == name || (name == "cucumber" && executable == "cucumber-js") {
			if len(args) > 1 && slices.Contains([]string{"install", "install-deps"}, args[1]) {
				continue
			}
			return true
		}
		if language == "ruby" && (executable == "rake" || (executable == "rails" && len(args) > 1 && args[1] == "test")) {
			return true
		}
		if language == "python" {
			if executable == "tox" && toxRunsTests(root, args[1:], name) {
				return true
			}
			if executable == "nox" {
				return true
			}
		}
	}
	return false
}

// Native tox configuration can distinguish test environments from typing and
// lint environments even when both install pytest. Unknown tox configurations
// remain candidates, as before; only explicit command overrides exclude a job.
func toxRunsTests(root string, args []string, name string) bool {
	type environment struct {
		Commands [][]any `toml:"commands"`
	}
	var project struct {
		Tool struct {
			Tox struct {
				Environments map[string]environment `toml:"env"`
				Base         environment            `toml:"env_run_base"`
				Default      []string               `toml:"env_list"`
			} `toml:"tox"`
		} `toml:"tool"`
	}
	contents, err := os.ReadFile(filepath.Join(root, "pyproject.toml"))
	if err != nil || toml.Unmarshal(contents, &project) != nil {
		return true
	}
	selected := project.Tool.Tox.Default
	for i, arg := range args {
		switch {
		case (arg == "-e" || arg == "--environment") && i+1 < len(args):
			selected = strings.Split(args[i+1], ",")
		case strings.HasPrefix(arg, "--environment="):
			selected = strings.Split(strings.TrimPrefix(arg, "--environment="), ",")
		case strings.HasPrefix(arg, "-e") && len(arg) > 2:
			selected = strings.Split(strings.TrimPrefix(arg, "-e"), ",")
		}
	}
	if len(selected) == 0 {
		selected = []string{""}
	}
	for _, env := range selected {
		commands := project.Tool.Tox.Environments[env].Commands
		if commands == nil {
			commands = project.Tool.Tox.Base.Commands
		}
		if commands == nil {
			return true
		}
		for _, command := range commands {
			var literal []string
			for _, value := range command {
				text, ok := value.(string)
				if !ok {
					break
				}
				literal = append(literal, text)
			}
			if len(literal) > 0 && (filepath.Base(literal[0]) == name || (len(literal) > 2 && slices.Contains([]string{"python", "python3", "{envpython}"}, literal[0]) && literal[1] == "-m" && literal[2] == name)) {
				return true
			}
		}
	}
	return false
}

func instructions(language, name string) string {
	actionLanguage := language
	var bootstrap string
	switch language {
	case "javascript":
		actionLanguage = "js"
		bootstrap = javascriptBootstrap
		if name == "cypress" {
			bootstrap += "\n\n" + cypressBootstrap
		}
		if name == "cucumber" {
			bootstrap += "\n\nFor Cucumber, also set DD_CIVISIBILITY_IMPACTED_TESTS_DETECTION_ENABLED=false on the test step. This avoids a tracer crash on Cucumber Background/Rule nodes; basic reporting is unaffected."
		}
	case "python":
		bootstrap = pythonBootstrap
	case "ruby":
		bootstrap = rubyBootstrap
	}
	// Empty inputs override the action's pinned defaults and request the latest release.
	tracerSetting := actionLanguage + "-tracer-version: '' # Latest release"
	text := strings.NewReplacer("__FRAMEWORK__", name, "__LANGUAGE__", actionLanguage, "__BOOTSTRAP__", bootstrap, "__TRACER_SETTING__", tracerSetting).Replace(gitHubInstructions)
	return strings.ReplaceAll(text, "ddtest testdrive", "ddtest testdrive --framework "+name)
}

const javascriptBootstrap = "Use Node.js 22 or newer. GitHub Actions cannot set NODE_OPTIONS for later steps, so merge this into the existing test step, preserving any current Node options:\n\n```yaml\nenv:\n  NODE_OPTIONS: -r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}\n```\n\nThe --import loader is required for Vitest and other ESM tests."

const pythonBootstrap = "The action exports PYTHONPATH and PYTEST_ADDOPTS=--ddtrace for pytest. Preserve these variables on the existing test step; do not replace its current arguments. Activate the same Python environment used for the tests before the action. If CI uses tox or nox, pass DD_*, PYTHONPATH, and PYTEST_ADDOPTS into the test environment."

const rubyBootstrap = "The action installs datadog-ci into the CI bundle and exports RUBYOPT=-rbundler/setup -rdatadog/ci/auto_instrument. Preserve RUBYOPT on the RSpec or Minitest step and merge any existing Ruby options. Keep the existing bundle exec or binstub entry point."

const cypressBootstrap = `Cypress also needs browser-side instrumentation; NODE_OPTIONS alone is insufficient:

1. In the existing Cypress config, resolve the tracer root from the action's DD_TRACE_PACKAGE value: path.dirname(path.dirname(process.env.DD_TRACE_PACKAGE)). Only enable this configuration when that variable is present. Use import syntax in an ESM config and require in a CommonJS config.
2. Compose the existing setupNodeEvents callback with the module at path.join(tracerRoot, 'ci/cypress/plugin'), loaded with import in ESM or require in CommonJS. Preserve existing event handlers, including after:run and after:spec; do not replace them.
3. In setupNodeEvents, generate a support wrapper under RUNNER_TEMP. Write a literal require of path.join(tracerRoot, 'ci/cypress/support'), followed by a literal require of the existing resolved supportFile (unless it is false). Set the returned config.supportFile to that wrapper. Generating literal absolute imports lets Cypress's browser bundler resolve the isolated tracer without adding it to package.json.
4. Keep the project's existing application startup, browser installation, and cypress run command. Do not use cypress open.

Use the same pattern for e2e or component configuration, whichever the selected job runs. The local testdrive creates equivalent wrappers inside its session automatically. See https://docs.datadoghq.com/tests/setup/javascript/ for the Cypress plugin contract.`
