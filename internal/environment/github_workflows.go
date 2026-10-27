// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package environment

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/kballard/go-shellquote"
	"github.com/pelletier/go-toml/v2"
	"go.yaml.in/yaml/v3"
)

const githubAction = "datadog/test-visibility-github-action"

func gitHubWorkflowPaths(repositoryRoot string) ([]string, error) {
	directory := filepath.Join(repositoryRoot, ".github", "workflows")
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find GitHub Actions workflows: %w", err)
	}
	var paths []string
	for _, entry := range entries {
		if !entry.IsDir() && (filepath.Ext(entry.Name()) == ".yml" || filepath.Ext(entry.Name()) == ".yaml") {
			paths = append(paths, filepath.Join(directory, entry.Name()))
		}
	}
	return paths, nil
}

// FindTestWorkflows returns the workflows that run the selected framework and
// the subset with an active Datadog action before every matching test step.
func (g *GitHub) FindTestWorkflows(repositoryRoot, language, name string) ([]string, []string, error) {
	actionLanguage := language
	if language == "javascript" {
		actionLanguage = "js"
	}
	return findTestWorkflows(repositoryRoot, actionLanguage, func(directory, run string) bool {
		return looksLikeTestJob(directory, run, language, name, 0)
	})
}

// FindTestWorkflowsMatching returns the workflows with a step that runs tests
// according to matches, and the subset with an active Datadog action for
// actionLanguage before every matching step. matches receives the words of
// one literal shell command at a time.
func (g *GitHub) FindTestWorkflowsMatching(repositoryRoot, actionLanguage string, matches func(args []string) bool) ([]string, []string, error) {
	return findTestWorkflows(repositoryRoot, actionLanguage, func(_, run string) bool {
		return shellCommands(run, matches)
	})
}

// findTestWorkflows passes matches each run script with its working directory.
func findTestWorkflows(repositoryRoot, actionLanguage string, matches func(directory, run string) bool) ([]string, []string, error) {
	var workflows []string
	var configured []string
	paths, err := gitHubWorkflowPaths(repositoryRoot)
	if err != nil {
		return nil, nil, err
	}
	for _, path := range paths {
		contents, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", path, err)
		}
		foundTests, isConfigured, err := inspectTestJobs(repositoryRoot, contents, actionLanguage, matches)
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

func inspectTestJobs(root string, contents []byte, actionLanguage string, matches func(directory, run string) bool) (foundTests, configured bool, err error) {
	type defaults struct {
		Run struct {
			WorkingDirectory string `yaml:"working-directory"`
		} `yaml:"run"`
	}
	var workflow struct {
		Defaults defaults `yaml:"defaults"`
		Jobs     map[string]struct {
			Defaults defaults `yaml:"defaults"`
			Steps    []struct {
				Run              string `yaml:"run"`
				Uses             string `yaml:"uses"`
				If               string `yaml:"if"`
				WorkingDirectory string `yaml:"working-directory"`
				With             struct {
					Languages string `yaml:"languages"`
				} `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(contents, &workflow); err != nil {
		return false, false, err
	}
	for _, job := range workflow.Jobs {
		jobConfigured := false
		for _, step := range job.Steps {
			condition := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(step.If), "${{"), "}}"))
			if condition == "false" {
				continue
			}
			directory := step.WorkingDirectory
			if directory == "" {
				directory = job.Defaults.Run.WorkingDirectory
			}
			if directory == "" {
				directory = workflow.Defaults.Run.WorkingDirectory
			}
			if !filepath.IsAbs(directory) {
				directory = filepath.Join(root, directory)
			}
			if matches(directory, step.Run) {
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

// LooksLikeTestCommand recognizes literal framework invocations without running project code.
func LooksLikeTestCommand(root, commands, language, name string) bool {
	return looksLikeTestJob(root, commands, language, name, 0)
}

// shellCommands reports whether visit accepts the words of any literal
// command in a run script, after leading VAR=value assignments. Simple ;, &
// and | lists are split; quoted shell expressions stay opaque.
func shellCommands(commands string, visit func(args []string) bool) bool {
	for _, command := range strings.Split(commands, "\n") {
		command = strings.TrimSpace(command)
		if command == "" || strings.HasPrefix(command, "#") {
			continue
		}
		if !strings.ContainsAny(command, "\"'`") && strings.ContainsAny(command, ";&|") {
			for _, part := range strings.FieldsFunc(command, func(r rune) bool { return r == ';' || r == '&' || r == '|' }) {
				if shellCommands(part, visit) {
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
		if len(args) > 0 && visit(args) {
			return true
		}
	}
	return false
}

// Resolve literal invocations without running project code. Limit script recursion
// so cycles in package.json cannot hang onboarding.
func looksLikeTestJob(root, commands, language, name string, depth int) bool {
	if depth > 10 {
		return false
	}
	return shellCommands(commands, func(args []string) bool {
		return looksLikeTestInvocation(root, args, language, name, depth)
	})
}

func looksLikeTestInvocation(root string, args []string, language, name string, depth int) bool {
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
		return false
	}
	executable := filepath.Base(args[0])
	if language == "javascript" && slices.Contains([]string{"npm", "pnpm", "yarn", "bun", "npx", "bunx"}, executable) && len(args) > 1 {
		args = args[1:]
		direct := executable == "npx" || executable == "bunx" || args[0] == "exec" || args[0] == "dlx"
		explicitScript := args[0] == "run" || args[0] == "run-script"
		if !direct && !explicitScript && (slices.Contains([]string{"install", "add", "remove", "update"}, args[0]) || (executable == "npm" && args[0] != "test")) {
			return false
		}
		if args[0] == "run" || args[0] == "run-script" || args[0] == "exec" || args[0] == "dlx" {
			args = args[1:]
		}
		for len(args) > 0 && slices.Contains([]string{"--", "--silent", "--yes", "-y", "--no-install"}, args[0]) {
			args = args[1:]
		}
		if len(args) == 0 {
			return false
		}
		if direct {
			executable = filepath.Base(args[0])
		} else {
			var manifest struct {
				Scripts map[string]string `json:"scripts"`
			}
			contents, err := os.ReadFile(filepath.Join(root, "package.json"))
			if err != nil || json.Unmarshal(contents, &manifest) != nil {
				return false
			}
			if script, ok := manifest.Scripts[args[0]]; ok {
				return looksLikeTestJob(root, script, language, name, depth+1)
			}
			// Preserve conventional test entry points when the project has no script.
			if args[0] == "test" {
				return true
			}
			if executable == "npm" {
				return false
			}
			executable = filepath.Base(args[0])
		}
	}
	if executable == name || (name == "cucumber" && executable == "cucumber-js") {
		if len(args) > 1 && (slices.Contains([]string{"install", "install-deps"}, args[1]) || (name == "cypress" && slices.Contains([]string{"verify", "cache", "info", "version", "help"}, args[1]))) {
			return false
		}
		return true
	}
	if language == "ruby" && executable == "rake" {
		var tasks []string
		listing := false
		for i := 1; i < len(args); i++ {
			arg := args[i]
			if slices.Contains([]string{"-T", "--tasks", "-D", "--describe", "-P", "--prereqs", "-W", "--where", "-V", "--version", "-h", "--help"}, arg) {
				listing = true
			}
			if slices.Contains([]string{"-f", "--rakefile", "-I", "--libdir", "-r", "--require"}, arg) {
				i++
				continue
			}
			if !strings.HasPrefix(arg, "-") && !strings.Contains(arg, "=") {
				tasks = append(tasks, arg)
			}
		}
		if listing {
			return false
		}
		// Keep default and custom tasks as candidates, but exclude known
		// maintenance tasks that do not execute tests.
		if len(tasks) == 0 {
			return true
		}
		for _, task := range tasks {
			task, _, _ = strings.Cut(task, ":")
			task, _, _ = strings.Cut(task, "[")
			if !slices.Contains([]string{"rubocop", "standard", "standardrb", "lint", "format", "style", "build", "install", "release", "assets", "clobber", "clean"}, task) {
				return true
			}
		}
	}
	if language == "ruby" && executable == "rails" && len(args) > 1 && args[1] == "test" {
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
