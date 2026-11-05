// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/testcommand"
	"go.yaml.in/yaml/v3"
)

// gitHubActionsProvider is the --ci-provider value of GitHub Actions.
const gitHubActionsProvider = "github"

// gitHubActions reads .github/workflows. Its workflow syntax is the shared
// workflow model, and its setup mechanism is the Datadog setup action.
type gitHubActions struct{}

func (gitHubActions) name() string { return gitHubActionsProvider }

func (gitHubActions) displayName() string       { return "GitHub Actions" }
func (gitHubActions) checkoutReference() string { return ciProjectDirectory }
func (gitHubActions) checkoutVariable() string  { return "GITHUB_WORKSPACE" }

func (gitHubActions) environment() map[string]string {
	return map[string]string{"CI": "true", "GITHUB_ACTIONS": "true"}
}

// workflowEntries lists the workflow files in .github/workflows.
func (gitHubActions) workflowEntries(root string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, ".github", "workflows"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find GitHub Actions workflows: %w", err)
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && (filepath.Ext(entry.Name()) == ".yml" || filepath.Ext(entry.Name()) == ".yaml") {
			names = append(names, entry.Name())
		}
	}
	return names, nil
}

func (g gitHubActions) configurationFiles(root string) ([]string, error) {
	names, err := g.workflowEntries(root)
	paths := make([]string, 0, len(names))
	for _, name := range names {
		paths = append(paths, ".github/workflows/"+name)
	}
	return paths, err
}

func (gitHubActions) readWorkflows(root string) ([]ciWorkflow, error) {
	files, err := readGitHubWorkflowFiles[ciWorkflow](root)
	if err != nil {
		return nil, err
	}
	var workflows []ciWorkflow
	for _, file := range files {
		workflow := file.workflow
		workflow.Path = file.path
		for jobName, job := range workflow.Jobs {
			job.Steps = expandCompositeSteps(root, workflow.Path, job.Steps)
			workflow.Jobs[jobName] = job
		}
		workflows = append(workflows, workflow)
	}
	return workflows, nil
}

type gitHubWorkflowFile[T any] struct {
	path     string
	workflow T
}

// readGitHubWorkflowFiles shares file loading and parse errors while letting
// manual instructions decode fewer fields than full discovery and validation.
func readGitHubWorkflowFiles[T any](root string) ([]gitHubWorkflowFile[T], error) {
	names, err := (gitHubActions{}).workflowEntries(root)
	if err != nil {
		return nil, err
	}
	var workflows []gitHubWorkflowFile[T]
	directory := filepath.Join(root, ".github", "workflows")
	for _, name := range names {
		path := filepath.Join(directory, name)
		contents, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		var workflow T
		if err := yaml.Unmarshal(contents, &workflow); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		workflows = append(workflows, gitHubWorkflowFile[T]{path: ".github/workflows/" + name, workflow: workflow})
	}
	return workflows, nil
}

// Manual instructions inspect only literal commands, directories and setup
// languages. Other fields, including expression-valued environments, must not
// prevent guidance; full validation still decodes the complete ciWorkflow.
type gitHubInstructionDefaults struct {
	Run struct {
		WorkingDirectory string `yaml:"working-directory"`
	} `yaml:"run"`
}

type gitHubInstructionWorkflow struct {
	Defaults gitHubInstructionDefaults `yaml:"defaults"`
	Jobs     map[string]struct {
		Defaults gitHubInstructionDefaults `yaml:"defaults"`
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

func (gitHubActions) findTestWorkflows(root, language, framework string) ([]string, []string, error) {
	workflows, err := readGitHubWorkflowFiles[gitHubInstructionWorkflow](root)
	if err != nil {
		return nil, nil, err
	}
	var found, configured []string
	for _, file := range workflows {
		tests, setup := inspectLiteralTestJobs(root, file.workflow, language, framework)
		if tests {
			found = append(found, file.path)
			if setup {
				configured = append(configured, file.path)
			}
		}
	}
	return found, configured, nil
}

// inspectLiteralTestJobs preserves the conservative action and command checks
// used by manual onboarding. Full validation uses resolved commands instead.
func inspectLiteralTestJobs(root string, workflow gitHubInstructionWorkflow, language, name string) (foundTests, configured bool) {
	actionLanguage := setupActionLanguage(language)
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
			if testcommand.Matches(directory, step.Run, language, name) {
				foundTests = true
				if !jobConfigured {
					return true, false
				}
			}
			action, _, hasVersion := strings.Cut(strings.ToLower(step.Uses), "@")
			if action == githubAction && hasVersion && (condition == "" || condition == "true" || condition == "success()") && !strings.Contains(step.With.Languages, "${{") {
				languages := strings.Fields(step.With.Languages)
				jobConfigured = jobConfigured || step.With.Languages == "all" || slices.Contains(languages, actionLanguage)
			}
		}
	}
	return foundTests, foundTests
}

func (gitHubActions) editsSetup() bool { return true }

func (gitHubActions) sharedSyntax() bool { return true }

// The Datadog setup action, at any ref; Pinned when the ref is present.
func (gitHubActions) setupStep(step runtimeStep) (ciSetup, bool) {
	uses, _, pinned := strings.Cut(strings.ToLower(step.Uses), "@")
	if uses != githubAction {
		return ciSetup{}, false
	}
	return ciSetup{Reference: step.Uses, Pinned: pinned, Supported: setupActionSupported(step.Uses), Inputs: step.With}, true
}

func (gitHubActions) setupName() string { return "Datadog action" }

func (gitHubActions) setupInput(name string) string { return name }

func (gitHubActions) setupProblem(setup ciSetup, tracerInput string, exact func(string) bool, example, site string) string {
	if !setupActionSupported(setup.Reference) {
		return "The Datadog action " + setup.Reference + " is not a supported release; use datadog/test-visibility-github-action@v3."
	}
	if strings.TrimSpace(setup.Inputs["api_key"]) == "" {
		return "The Datadog action has no api_key input; set api_key: ${{ secrets.DD_API_KEY }}."
	}
	if tracer := setup.Inputs[tracerInput]; !exact(tracer) {
		return "The Datadog action's " + tracerInput + " " + fmt.Sprintf("%q", tracer) + " is not an exact release; set an exact version such as " + example + "."
	}
	configured := setup.Inputs["site"]
	if configured == "" {
		configured = defaultDatadogSite
	}
	if site != "" && configured != site {
		return "The Datadog action reports to " + configured + ", but the selected Datadog site is " + site + "; set site: " + site + " on the action."
	}
	return ""
}

func (gitHubActions) apiKeyAction(skipped bool) string {
	if skipped {
		return "Add the DD_API_KEY repository secret (GitHub Settings > Secrets and variables > Actions). Until it exists, the Datadog step is skipped and tests run untraced."
	}
	return "Add the DD_API_KEY repository secret (GitHub Settings > Secrets and variables > Actions). Until it exists, the Datadog step fails without failing the job and tests run untraced."
}

// GitHub Actions setup notes are the languages' workflow templates.
func (gitHubActions) setupTerms() ciSetupTerms {
	return ciSetupTerms{
		Persist:      func(command string) string { return command },
		PersistEntry: func(command string) string { return "- " + command },
		ImageGuard:   func(string, string, string) string { return "" },
	}
}

// GitHub Actions setup instructions are the languages' workflow templates.
func (gitHubActions) setupInstructions(ciSetupRequest) string { return "" }

// The shared workflow editor edits GitHub Actions workflows.
func (gitHubActions) planSetupEdits(string, []ciWorkflow, []ciSetupTarget, ciSetupRequest) ([]setupEdit, []string, error) {
	return nil, nil, errors.New("GitHub Actions workflows are edited by the shared workflow editor")
}
