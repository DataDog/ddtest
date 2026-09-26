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

	"github.com/DataDog/ddtest/internal/platform"
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

	if len(discovery.Review) > 0 {
		_, _ = fmt.Fprintln(output, "\nOther CI entry points to review separately (not identified as Jest):")
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
		_, _ = fmt.Fprintf(output, "This is configuration detection, not completed onboarding. Run `ddtest testdrive --framework %s` to validate locally. Jest also checks CI runtime compatibility; other frameworks remain unvalidated.\n", name)
		_, _ = fmt.Fprintln(output, "After it finishes, share the validation verdict, CI runtime compatibility, feature results, and the Results JSON path with the user.")
		_, _ = fmt.Fprintln(output, "Keep .testoptimization/testdrive.json after cleanup, even on failure. Do not declare validation complete while any required check is failed or inconclusive.")
		return nil
	}

	_, _ = fmt.Fprintln(output)
	_, _ = fmt.Fprintln(output, strings.TrimSpace(instructions(language, name)))
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
	for _, workflow := range workflows {
		found, configured := false, true
		for _, jobName := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			job := workflow.Jobs[jobName]
			hasAction := false
			for i, step := range job.Steps {
				uses, _, _ := strings.Cut(strings.ToLower(step.Uses), "@")
				hasAction = hasAction || uses == githubAction
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

func looksLikeTestJob(commands, language, name string) bool {
	markers := []string{name}
	switch language {
	case "javascript":
		markers = append(markers, "npm test", "npm run test", "yarn test", "yarn run test", "pnpm test", "pnpm run test", "bun test", "bun run test")
	case "ruby":
		markers = append(markers, "bundle exec rake", "rake test", "rails test")
	case "python":
		markers = append(markers, "tox", "nox")
	}
	for _, marker := range markers {
		if strings.Contains(commands, marker) {
			return true
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
	text := strings.NewReplacer("__FRAMEWORK__", name, "__LANGUAGE__", actionLanguage, "__BOOTSTRAP__", bootstrap).Replace(gitHubInstructions)
	return strings.ReplaceAll(text, "ddtest testdrive", "ddtest testdrive --framework "+name)
}

const javascriptBootstrap = "For every instrumented CI matrix entry, use a Node.js version supported by the tracer selected by the action. The Jest testdrive checks the action's actual tracer metadata; do not infer CI compatibility from the local Node version. GitHub Actions cannot set NODE_OPTIONS for later steps, so merge this into the existing test step, preserving any current Node options:\n\n```yaml\nenv:\n  NODE_OPTIONS: -r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}\n```\n\nThe --import loader is required for Vitest and other ESM tests."

const pythonBootstrap = "The action exports PYTHONPATH and PYTEST_ADDOPTS=--ddtrace for pytest. Preserve these variables on the existing test step; do not replace its current arguments. Activate the same Python environment used for the tests before the action. If CI uses tox or nox, pass DD_*, PYTHONPATH, and PYTEST_ADDOPTS into the test environment."

const rubyBootstrap = "The action installs datadog-ci into the CI bundle and exports RUBYOPT=-rbundler/setup -rdatadog/ci/auto_instrument. Preserve RUBYOPT on the RSpec or Minitest step and merge any existing Ruby options. Keep the existing bundle exec or binstub entry point."

const cypressBootstrap = `Cypress also needs browser-side instrumentation; NODE_OPTIONS alone is insufficient:

1. In the existing Cypress config, resolve the tracer root from the action's DD_TRACE_PACKAGE value: path.dirname(path.dirname(process.env.DD_TRACE_PACKAGE)). Only enable this configuration when that variable is present. Use import syntax in an ESM config and require in a CommonJS config.
2. Compose the existing setupNodeEvents callback with the module at path.join(tracerRoot, 'ci/cypress/plugin'), loaded with import in ESM or require in CommonJS. Preserve existing event handlers, including after:run and after:spec; do not replace them.
3. In setupNodeEvents, generate a support wrapper under RUNNER_TEMP. Write a literal require of path.join(tracerRoot, 'ci/cypress/support'), followed by a literal require of the existing resolved supportFile (unless it is false). Set the returned config.supportFile to that wrapper. Generating literal absolute imports lets Cypress's browser bundler resolve the isolated tracer without adding it to package.json.
4. Keep the project's existing application startup, browser installation, and cypress run command. Do not use cypress open.

Use the same pattern for e2e or component configuration, whichever the selected job runs. The local testdrive creates equivalent wrappers inside its session automatically. See https://docs.datadoghq.com/tests/setup/javascript/ for the Cypress plugin contract.`
