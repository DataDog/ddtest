// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package onboard prints the smallest supported Test Optimization setup.
package onboard

import (
	_ "embed"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/template"

	"github.com/DataDog/ddtest/internal/environment"
	"github.com/DataDog/ddtest/internal/platform"
	"go.yaml.in/yaml/v3"
)

const githubAction = "datadog/test-visibility-github-action"

//go:embed instructions/github.md
var gitHubInstructions string

var gitHubInstructionsTemplate = template.Must(template.New("github").Delims("[[", "]]").Parse(gitHubInstructions))

//go:embed instructions/scratch-cleanup.md
var scratchCleanupInstructions string

//go:embed instructions/vitest.md
var vitestInstructions string

//go:embed instructions/playwright.md
var playwrightInstructions string

//go:embed instructions/cypress.md
var cypressInstructions string

//go:embed instructions/mocha.md
var mochaInstructions string

//go:embed instructions/cucumber.md
var cucumberInstructions string

//go:embed instructions/webdriverio.md
var webdriverIOInstructions string

//go:embed instructions/java.md
var javaInstructions string

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
	provider, err := environment.DetectRepositoryCIProvider(repositoryRoot)
	if err != nil {
		return fmt.Errorf("onboard: %w", err)
	}
	language := detectedPlatform.Name()
	name := runner.Name()
	if language != "javascript" {
		github, ok := provider.(*environment.GitHub)
		if !ok {
			return fmt.Errorf("onboard does not support CI provider %s", provider.Name())
		}
		return onboardGitHub(output, github, repositoryRoot, language, name)
	}

	discovery, err := findWorkflows(repositoryRoot, language, name)
	workflows, configured := discovery.Workflows, discovery.Configured
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
	_, _ = fmt.Fprintln(output, "\n"+strings.TrimSpace(scratchCleanupInstructions))
	if name == "jest" || name == "vitest" || name == "playwright" || name == "mocha" || name == "cucumber" || name == "webdriverio" {
		scope, err := DiscoverValidationScope(repositoryRoot, name)
		if err != nil {
			return err
		}
		if name == "webdriverio" {
			_, _ = fmt.Fprintln(output, "\n"+strings.TrimSpace(instructions(language, name)))
		}
		WriteValidationScope(output, scope)
		if name == "webdriverio" {
			_, _ = fmt.Fprintln(output, "First inspect all configurations with `ddtest testdrive --framework webdriverio --all --check-only --yes`. Then follow the focused validation instructions below. Do not automatically execute unavailable browsers or the complete example matrix.")
			if len(scope.Commands) > 0 {
				_, _ = fmt.Fprintf(output, "First focused command: `ddtest testdrive --framework webdriverio --command %q --tracer-version <resolved-release> --yes`. This validates only that configuration; other variants stay unvalidated.\n", scope.Commands[0].Command)
			}
		} else {
			_, _ = fmt.Fprintf(output, "Run `ddtest testdrive --framework %s --all --check-only --yes`, then finish edits and review separate setup, and run `ddtest testdrive --framework %s --all --tracer-version <resolved-release> --yes`. This aggregates every discovered configuration in one report pair. A --command run validates only its selected configuration.\n", name, name)
		}
	}
	if name == "jest" {
		command, err := JestValidationCommand(repositoryRoot)
		if err != nil {
			_, _ = fmt.Fprintf(output, "\nLocal Jest command needs review: %s\n", err)
		} else if command != "" {
			_, _ = fmt.Fprintf(output, "\nLocal validation command: %s\nTestdrive selects this command automatically; preserve its config, coverage, and execution options. --command overrides this selection.\n", command)
		}
	}

	if len(discovery.Review) > 0 {
		_, _ = fmt.Fprintln(output, "\nOther CI entry points to review separately:")
		for _, reason := range discovery.Review {
			_, _ = fmt.Fprintf(output, "  - %s\n", reason)
		}
	}
	if len(discovery.Unresolved) > 0 {
		_, _ = fmt.Fprintln(output, "\nCI command discovery is inconclusive:")
		for _, reason := range discovery.Unresolved {
			_, _ = fmt.Fprintf(output, "  - %s\n", reason)
		}
		_, _ = fmt.Fprintln(output, "Identify the actual test steps before editing CI. Preserve valid commands; do not rewrite them merely to satisfy discovery. Manual review does not turn an unverified programmatic check into a pass.")
	}

	if len(discovery.Unresolved) == 0 && len(configured) == len(workflows) {
		_, _ = fmt.Fprintln(output)
		_, _ = fmt.Fprintln(output, "Datadog Test Optimization already appears in every detected test workflow.")
		_, _ = fmt.Fprintf(output, "This is configuration detection, not completed onboarding. Run `ddtest testdrive --framework %s` to validate locally. Jest, Vitest, Playwright, Mocha, Cypress and Cucumber projects also check CI runtime compatibility; configurations without a validation adapter remain unvalidated.\n", name)
		_, _ = fmt.Fprintln(output, "After it finishes, share the validation verdict, CI runtime compatibility and feature results using the final response block printed by testdrive. Copy its absolute Markdown report links verbatim, each on its own line; do not shorten them to relative paths or replace them with file:// URLs.")
		_, _ = fmt.Fprintln(output, "Keep .testoptimization/report.html and .testoptimization/testdrive.json after cleanup, even on failure. Do not declare validation complete while any required check is failed or inconclusive.")
		if name != "vitest" && name != "playwright" && name != "mocha" && name != "cucumber" && name != "webdriverio" {
			return nil
		}
	}

	if name != "webdriverio" {
		_, _ = fmt.Fprintln(output)
		_, _ = fmt.Fprintln(output, strings.TrimSpace(instructions(language, name)))
	}
	return nil
}

type workflowDiscovery struct {
	Workflows, Configured, Unresolved, Review []string
}

type runDefaults struct {
	WorkingDirectory string `yaml:"working-directory"`
	Shell            string `yaml:"shell"`
}

type ciDefaults struct {
	Run runDefaults `yaml:"run"`
}

type ciWorkflow struct {
	On       any `yaml:"on"`
	Path     string
	Defaults ciDefaults            `yaml:"defaults"`
	Env      map[string]string     `yaml:"env"`
	Jobs     map[string]runtimeJob `yaml:"jobs"`
}

func readWorkflows(root string) ([]ciWorkflow, error) {
	var workflows []ciWorkflow
	directory := filepath.Join(root, ".github", "workflows")
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find GitHub Actions workflows: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || (filepath.Ext(entry.Name()) != ".yml" && filepath.Ext(entry.Name()) != ".yaml") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		contents, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		var workflow ciWorkflow
		if err := yaml.Unmarshal(contents, &workflow); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		workflow.Path = ".github/workflows/" + entry.Name()
		for name, job := range workflow.Jobs {
			job.Steps = expandCompositeSteps(root, workflow.Path, job.Steps)
			workflow.Jobs[name] = job
		}
		workflows = append(workflows, workflow)
	}
	return workflows, nil
}

func findWorkflows(root, language, name string) (workflowDiscovery, error) {
	var result workflowDiscovery
	workflows, err := readWorkflows(root)
	if err != nil {
		return result, err
	}
	if name == "webdriverio" {
		return findWebdriverIOWorkflows(root, workflows)
	}
	actionLanguage := language
	if language == "javascript" {
		actionLanguage = "js"
	}
	for _, workflow := range workflows {
		found, configured := false, true
		for _, jobName := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			job := workflow.Jobs[jobName]
			if name == "webdriverio" && !webdriverIOJobCandidate(root, workflow, job) {
				continue
			}
			hasAction := false
			for i, step := range job.Steps {
				condition := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(step.If), "${{"), "}}"))
				if condition == "false" {
					continue
				}
				uses, _, hasVersion := strings.Cut(strings.ToLower(step.Uses), "@")
				languages := step.With["languages"]
				if uses == githubAction && hasVersion && (condition == "" || condition == "true" || condition == "success()") && !strings.Contains(languages, "${{") {
					hasAction = hasAction || languages == "all" || slices.Contains(strings.Fields(languages), actionLanguage)
				}
				resolution := resolveTestStep(root, workflow, job, step, language, name)
				if resolution.Review {
					result.Review = append(result.Review, fmt.Sprintf("%s / %s / step %d (%s): %s", workflow.Path, jobName, stepNumber(step, i), step.Run, resolution.Reason))
					continue
				}
				if resolution.Matched || resolution.Reason != "" {
					found = true
					configured = configured && hasAction && resolution.Reason == ""
				}
				if resolution.Reason != "" {
					result.Unresolved = append(result.Unresolved, fmt.Sprintf("%s / %s / step %d (%s): %s", workflow.Path, jobName, stepNumber(step, i), step.Run, resolution.Reason))
				}
			}
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

func renderGitHubInstructions(language string) string {
	var output strings.Builder
	if err := gitHubInstructionsTemplate.Execute(&output, language); err != nil {
		panic(err) // The embedded template has only string comparisons and writes to a strings.Builder.
	}
	return output.String()
}

func instructions(language, name string) string {
	if language == "java" {
		return strings.ReplaceAll(javaInstructions, "__FRAMEWORK__", name)
	}
	if language == "javascript" && name == "webdriverio" {
		return strings.ReplaceAll(webdriverIOInstructions, "ddtest testdrive", "ddtest testdrive --framework webdriverio")
	}
	if language == "javascript" && name == "cucumber" {
		return strings.ReplaceAll(cucumberInstructions, "ddtest testdrive", "ddtest testdrive --framework cucumber")
	}
	if language == "javascript" && name == "cypress" {
		text := strings.NewReplacer("For Jest and supported Vitest projects", "For Cypress projects", "Jest configuration", "Cypress configuration", "Jest CI discovery", "Cypress CI discovery", "Identified Jest steps", "Identified Cypress steps", "__FRAMEWORK__", "cypress", "__LANGUAGE__", "js", "__BOOTSTRAP__", javascriptBootstrap).Replace(renderGitHubInstructions(language))
		return strings.ReplaceAll(text+"\n\n"+cypressInstructions, "ddtest testdrive", "ddtest testdrive --framework cypress")
	}
	if language == "javascript" && name == "mocha" {
		return strings.ReplaceAll(mochaInstructions, "ddtest testdrive", "ddtest testdrive --framework mocha")
	}
	if language == "javascript" && name == "playwright" {
		return strings.ReplaceAll(playwrightInstructions, "ddtest testdrive", "ddtest testdrive --framework playwright")
	}
	if language == "javascript" && name == "vitest" {
		return strings.ReplaceAll(vitestInstructions, "ddtest testdrive", "ddtest testdrive --framework vitest")
	}
	actionLanguage := language
	var bootstrap string
	switch language {
	case "javascript":
		actionLanguage = "js"
		bootstrap = javascriptBootstrap
		if name == "cucumber" {
			bootstrap += "\n\nFor Cucumber, also set DD_CIVISIBILITY_IMPACTED_TESTS_DETECTION_ENABLED=false on the test step. This avoids a tracer crash on Cucumber Background/Rule nodes; basic reporting is unaffected."
		}
	case "python":
		bootstrap = pythonBootstrap
	case "ruby":
		bootstrap = rubyBootstrap
	}
	tracerSetting := actionLanguage + "-tracer-version: '' # Latest release"
	text := strings.NewReplacer("__FRAMEWORK__", name, "__LANGUAGE__", actionLanguage, "__BOOTSTRAP__", bootstrap, "__TRACER_SETTING__", tracerSetting).Replace(renderGitHubInstructions(language))
	return strings.ReplaceAll(text, "ddtest testdrive", "ddtest testdrive --framework "+name)
}

const javascriptBootstrap = "For every instrumented CI matrix entry, use a Node.js version supported by the tracer selected by the action. The Jest and Vitest testdrives check the action's actual tracer metadata; do not infer CI compatibility from the local Node version. GitHub Actions cannot set NODE_OPTIONS for later steps, so merge this into the existing test step, preserving any current Node options:\n\n```yaml\nenv:\n  NODE_OPTIONS: -r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}\n```\n\nThe --import loader is required for Vitest and other ESM tests."

const pythonBootstrap = "The action exports PYTHONPATH and PYTEST_ADDOPTS=--ddtrace for pytest. Preserve these variables on the existing test step; do not replace its current arguments. Activate the same Python environment used for the tests before the action. If CI uses tox or nox, pass DD_*, PYTHONPATH, and PYTEST_ADDOPTS into the test environment."

const rubyBootstrap = "The action installs datadog-ci into the CI bundle and exports RUBYOPT=-rbundler/setup -rdatadog/ci/auto_instrument. Preserve RUBYOPT on the RSpec or Minitest step and merge any existing Ruby options. Keep the existing bundle exec or binstub entry point."
