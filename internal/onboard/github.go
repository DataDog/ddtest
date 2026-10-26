// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	_ "embed"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/template"

	"github.com/DataDog/ddtest/internal/environment"
)

//go:embed instructions/webdriverio.md
var webdriverIOInstructions string

//go:embed instructions/cucumber.md
var cucumberInstructions string

//go:embed instructions/mocha.md
var mochaInstructions string

//go:embed instructions/cypress.md
var cypressInstructions string

//go:embed instructions/playwright.md
var playwrightInstructions string

//go:embed instructions/vitest.md
var vitestInstructions string

//go:embed instructions/github.md
var gitHubInstructions string

func onboardGitHub(output io.Writer, provider *environment.GitHub, repositoryRoot, language, name string) error {
	workflows, configured, err := provider.FindTestWorkflows(repositoryRoot, language, name)
	if err != nil {
		return err
	}
	var scope *ValidationScope
	if language == "javascript" && (name == "mocha" || name == "playwright" || name == "cypress" || name == "cucumber" || name == "webdriverio") {
		discovered, err := DiscoverValidationScope(repositoryRoot, name)
		if err != nil {
			return err
		}
		scope = &discovered
		for _, entry := range discovered.Commands {
			for _, location := range entry.locations {
				if !slices.Contains(workflows, location.Workflow) {
					workflows = append(workflows, location.Workflow)
				}
			}
		}
		slices.Sort(workflows)
	}
	if len(workflows) == 0 {
		if language != "javascript" {
			return fmt.Errorf("onboard could not find a GitHub Actions workflow that runs %s", name)
		}
		_, _ = fmt.Fprintf(output, "CI discovery is incomplete: no GitHub Actions test workflow was resolved for %s. The guidance below is manual; it does not certify CI. Run ddtest testdrive --framework %s --command <your-test-command> --check-only --yes to inspect the local setup.\n\n", name, name)
		_, _ = fmt.Fprintln(output, strings.TrimSpace(instructions(language, name)))
		return nil
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
		command := "ddtest testdrive --framework " + name
		// A plain run needs one CI command; with several, --all checks each.
		if scope, err := DiscoverValidationScope(repositoryRoot, name); err == nil && len(scope.Commands) > 1 {
			command += " --all"
		}
		_, _ = fmt.Fprintf(output, "Run `%s` to check the setup locally.\n", command)
		_, _ = fmt.Fprintln(output, "After it finishes, post every `Open report:` link to the user so they can open the local Test Optimization report.")
		if name != "webdriverio" && name != "cypress" && name != "cucumber" {
			return nil
		}
	}

	_, _ = fmt.Fprintln(output)
	if scope != nil && RedactsCommandKeys(name) {
		WriteValidationScope(output, RedactCypressScope(*scope))
	} else if scope != nil {
		WriteValidationScope(output, *scope)
	}
	_, _ = fmt.Fprintln(output, strings.TrimSpace(instructions(language, name)))
	return nil
}

func instructions(language, name string) string {
	if language == "javascript" && name == "webdriverio" {
		return strings.ReplaceAll(webdriverIOInstructions, "ddtest testdrive", "ddtest testdrive --framework webdriverio")
	}
	if language == "javascript" && name == "cucumber" {
		return strings.ReplaceAll(cucumberInstructions, "ddtest testdrive", "ddtest testdrive --framework cucumber")
	}
	if language == "javascript" && name == "cypress" {
		return strings.ReplaceAll(cypressInstructions, "ddtest testdrive", "ddtest testdrive --framework cypress")
	}
	if language == "javascript" && name == "playwright" {
		return strings.ReplaceAll(playwrightInstructions, "ddtest testdrive", "ddtest testdrive --framework playwright")
	}
	if language == "javascript" && name == "mocha" {
		return strings.ReplaceAll(mochaInstructions, "ddtest testdrive", "ddtest testdrive --framework mocha")
	}
	if language == "javascript" && name == "vitest" {
		heading, rest, _ := strings.Cut(vitestInstructions, "\n\n")
		return strings.ReplaceAll(heading+"\n\n"+javascriptAutomaticSetup+"\n\n"+rest, "ddtest testdrive", "ddtest testdrive --framework vitest")
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
	// Empty inputs override the action's pinned defaults and request the latest release.
	tracerSetting := actionLanguage + "-tracer-version: '' # Latest release"
	text := strings.NewReplacer("__FRAMEWORK__", name, "__LANGUAGE__", actionLanguage, "__BOOTSTRAP__", bootstrap, "__TRACER_SETTING__", tracerSetting, "__AUTOMATIC_SETUP__", javascriptAutomaticSetup).Replace(renderGitHubInstructions(language))
	return strings.ReplaceAll(text, "ddtest testdrive", "ddtest testdrive --framework "+name)
}

// javascriptAutomaticSetup is the agent paragraph shared by the generic and
// Vitest instructions. It points at the preview before the applying command.
const javascriptAutomaticSetup = "For automatic setup, preview the recognized edits with `ddtest onboard --apply`, which writes nothing, then apply them with `ddtest onboard --apply --yes`. ddtest resolves the actual CI commands, tracer and supported runtime scope, applies recognized edits, then validates locally. Follow its structured next action for unsupported setup. Run validation in the foreground and wait for its exit; never use nohup, shell background jobs, sleep loops or process-name polling. If the host returns a task/session ID, wait using that host tool. Do not run parallel validation or change the selected framework/command to bypass a blocker. Use `ddtest testdrive --agent --all --yes` to validate existing setup. Preserve the generated final verdict and report links; after any source/configuration edits or configuration-only checks, run full validation again. `--instructions` prints this manual fallback."

const javascriptBootstrap = "Use Node.js 22 or newer. GitHub Actions cannot set NODE_OPTIONS for later steps, so merge this into the existing test step, preserving any current Node options:\n\n```yaml\nenv:\n  NODE_OPTIONS: -r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}\n```\n\nThe --import loader is required for Vitest and other ESM tests."

const pythonBootstrap = "The action exports PYTHONPATH and PYTEST_ADDOPTS=--ddtrace for pytest. Preserve these variables on the existing test step; do not replace its current arguments. Activate the same Python environment used for the tests before the action. If CI uses tox or nox, pass DD_*, PYTHONPATH, and PYTEST_ADDOPTS into the test environment."

const rubyBootstrap = "The action installs datadog-ci into the CI bundle and exports RUBYOPT=-rbundler/setup -rdatadog/ci/auto_instrument. Preserve RUBYOPT on the RSpec or Minitest step and merge any existing Ruby options. Keep the existing bundle exec or binstub entry point."

var gitHubInstructionsTemplate = template.Must(template.New("github").Delims("[[", "]]").Parse(gitHubInstructions))

func renderGitHubInstructions(language string) string {
	var output strings.Builder
	if err := gitHubInstructionsTemplate.Execute(&output, language); err != nil {
		panic(err)
	}
	return output.String()
}
