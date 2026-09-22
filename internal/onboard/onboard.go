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
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/tracer"
)

const githubAction = "datadog/test-visibility-github-action"

//go:embed instructions/github.md
var gitHubInstructions string

// Run detects the first supported onboarding path and prints its instructions.
func Run(repositoryRoot string, output io.Writer) error {
	return RunWithFramework(repositoryRoot, "", output)
}

func RunWithFramework(repositoryRoot, hint string, output io.Writer) error {
	language, runner, err := platform.DetectTestProject(repositoryRoot, hint)
	if err != nil {
		return err
	}
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
	patterns := []string{
		filepath.Join(repositoryRoot, ".github", "workflows", "*.yml"),
		filepath.Join(repositoryRoot, ".github", "workflows", "*.yaml"),
	}

	var workflows []string
	var configured []string
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, nil, fmt.Errorf("find GitHub Actions workflows: %w", err)
		}
		for _, path := range matches {
			contents, err := os.ReadFile(path)
			if err != nil {
				return nil, nil, fmt.Errorf("read %s: %w", path, err)
			}
			text := strings.ToLower(string(contents))
			if !looksLikeTestWorkflow(text, language, name) {
				continue
			}

			relativePath, err := filepath.Rel(repositoryRoot, path)
			if err != nil {
				return nil, nil, fmt.Errorf("make workflow path relative: %w", err)
			}
			relativePath = filepath.ToSlash(relativePath)
			workflows = append(workflows, relativePath)
			if strings.Contains(text, githubAction) {
				configured = append(configured, relativePath)
			}
		}
	}

	sort.Strings(workflows)
	sort.Strings(configured)
	return workflows, configured, nil
}

func looksLikeTestWorkflow(workflow, language, name string) bool {
	markers := []string{name, githubAction}
	switch language {
	case "javascript":
		markers = append(markers, "npm test", "npm run test", "yarn test", "yarn run test", "pnpm test", "pnpm run test", "bun test", "bun run test")
	case "ruby":
		markers = append(markers, "bundle exec rake", "rake test", "rails test")
	case "python":
		markers = append(markers, "tox", "nox")
	}
	for _, marker := range markers {
		if strings.Contains(workflow, marker) {
			return true
		}
	}
	return false
}

func instructions(language, name string) string {
	actionLanguage := language
	var bootstrap, tracerSetting string
	switch language {
	case "javascript":
		actionLanguage = "js"
		tracerSetting = "js-tracer-version: " + tracer.JavaScriptVersion
		bootstrap = javascriptBootstrap
		if name == "cypress" {
			bootstrap += "\n\n" + cypressBootstrap
		}
		if name == "cucumber" {
			bootstrap += "\n\nFor the pinned tracer, also set DD_CIVISIBILITY_IMPACTED_TESTS_DETECTION_ENABLED=false on the test step. This avoids a tracer crash on Cucumber Background/Rule nodes; basic reporting is unaffected."
		}
	case "python":
		bootstrap = pythonBootstrap
		tracerSetting = "python-tracer-version: " + tracer.PythonVersion
	case "ruby":
		bootstrap = rubyBootstrap
		tracerSetting = "ruby-tracer-version: " + tracer.RubyVersion
	}
	text := strings.NewReplacer("__FRAMEWORK__", name, "__LANGUAGE__", actionLanguage, "__BOOTSTRAP__", bootstrap, "__TRACER_SETTING__", tracerSetting).Replace(gitHubInstructions)
	return strings.ReplaceAll(text, "ddtest testdrive", "ddtest testdrive --framework "+name)
}

const javascriptBootstrap = "Use Node.js 22 or newer. GitHub Actions cannot set NODE_OPTIONS for later steps, so merge this into the existing test step, preserving any current Node options:\n\n```yaml\nenv:\n  NODE_OPTIONS: -r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}\n```\n\nThe --import loader is required for Vitest and other ESM tests."

const pythonBootstrap = "The action exports PYTHONPATH and PYTEST_ADDOPTS=--ddtrace for pytest. Preserve these variables on the existing test step; do not replace its current arguments. Activate the same Python environment used for the tests before the action. If CI uses tox or nox, pass DD_*, PYTHONPATH, and PYTEST_ADDOPTS into the test environment."

const rubyBootstrap = "The action installs datadog-ci into the CI bundle and exports RUBYOPT=-rbundler/setup -rdatadog/ci/auto_instrument. Preserve RUBYOPT on the RSpec or Minitest step and merge any existing Ruby options. Keep the existing bundle exec or binstub entry point."

const cypressBootstrap = `Cypress also needs browser-side instrumentation; NODE_OPTIONS alone is insufficient:

1. In the existing Cypress config, resolve the tracer root from the action's DD_TRACE_PACKAGE value: path.dirname(path.dirname(process.env.DD_TRACE_PACKAGE)). Only enable this configuration when that variable is present.
2. Compose the existing setupNodeEvents callback with require(path.join(tracerRoot, 'ci/cypress/plugin')). Preserve existing event handlers, including after:run and after:spec; do not replace them.
3. In setupNodeEvents, generate a support wrapper under RUNNER_TEMP. Write a literal require of path.join(tracerRoot, 'ci/cypress/support'), followed by a literal require of the existing resolved supportFile (unless it is false). Set the returned config.supportFile to that wrapper. Generating literal absolute imports lets Cypress's browser bundler resolve the isolated tracer without adding it to package.json.
4. Keep the project's existing application startup, browser installation, and cypress run command. Do not use cypress open.

Use the same pattern for e2e or component configuration, whichever the selected job runs. The local testdrive creates equivalent wrappers inside its session automatically. See https://docs.datadoghq.com/tests/setup/javascript/ for the Cypress plugin contract.`
