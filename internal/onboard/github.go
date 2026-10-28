// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	_ "embed"
	"strings"
	"text/template"
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

func instructions(language, name string) string {
	if provider := activeOrDefaultCIProvider(); !provider.sharedSyntax() {
		return providerSetupInstructions(".", provider, language, name, "", false)
	}
	text := frameworkInstructions(language, name)
	if language == "javascript" {
		return pinSetupAction(text)
	}
	return text
}

func frameworkInstructions(language, name string) string {
	if registered := discoveryLanguageNamed(language); registered != nil {
		if text := registered.instructions(name); text != "" {
			return text
		}
	}
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
	actionLanguage := setupActionLanguage(language)
	var bootstrap string
	if registered := discoveryLanguageNamed(language); registered != nil {
		bootstrap = registered.bootstrap(name)
	}
	// Empty inputs override the action's pinned defaults and request the latest release.
	tracerSetting := actionLanguage + "-tracer-version: '' # Latest release"
	text := strings.NewReplacer("__FRAMEWORK__", name, "__LANGUAGE__", actionLanguage, "__BOOTSTRAP__", bootstrap, "__TRACER_SETTING__", tracerSetting, "__AUTOMATIC_SETUP__", javascriptAutomaticSetup).Replace(renderGitHubInstructions(language))
	return strings.ReplaceAll(text, "ddtest testdrive", "ddtest testdrive --framework "+name)
}

// javascriptAutomaticSetup is the agent paragraph shared by the generic and
// Vitest instructions. It points at the preview before the applying command.
const javascriptAutomaticSetup = "For automatic setup, preview the recognized edits with `ddtest onboard --apply`, which writes nothing, then apply them with `ddtest onboard --apply --yes`. ddtest resolves the actual CI commands, tracer and supported runtime scope, applies recognized edits, then validates locally. Follow its structured next action for unsupported setup. Run validation in the foreground and wait for its exit; never use nohup, shell background jobs, sleep loops or process-name polling. If the host returns a task/session ID, wait using that host tool. Do not run parallel validation or change the selected framework/command to bypass a blocker. Use `ddtest testdrive --agent --all --yes` to validate existing setup. Preserve the generated final verdict and report links; after any source/configuration edits or configuration-only checks, run full validation again. `--instructions` prints this manual fallback."

var gitHubInstructionsTemplate = template.Must(template.New("github").Delims("[[", "]]").Parse(gitHubInstructions))

func renderGitHubInstructions(language string) string {
	var output strings.Builder
	if err := gitHubInstructionsTemplate.Execute(&output, language); err != nil {
		panic(err)
	}
	return output.String()
}
