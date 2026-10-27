// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import "slices"

// discoveryLanguage is everything onboarding needs from one language. Shared
// discovery and instructions call only these hooks; each language keeps its
// specifics in its own files and registers in languages.go.
type discoveryLanguage interface {
	name() string
	// frameworks lists the test consumers this language can recognize.
	frameworks() []string
	// actionLanguage is the language's value in the setup action's languages input.
	actionLanguage() string
	// bootstrap explains how the existing test step loads the tracer.
	bootstrap(framework string) string
	// resolvesTestSteps reports whether resolveTestStep statically resolves
	// framework's CI commands. Other steps are matched by command shape.
	resolvesTestSteps(framework string) bool
	// coordinated reports whether agent onboarding hands off to controlled
	// validation and automatic CI setup.
	coordinated() bool

	// frameworks lists the test frameworks whose CI the language discovers.
	frameworks() []string
	// resolveStep resolves one CI step for framework with the language's own
	// rules. ok is false when the shared resolver applies.
	resolveStep(root string, workflow ciWorkflow, job runtimeJob, step runtimeStep, framework string) (resolution commandResolution, ok bool)
	// localCommands resolves a selected local command for scope accounting.
	// ok is false when the shared resolver applies.
	localCommands(root, command, framework string) (commands []TestCommand, ok bool)
	// variantValue rewrites one inherited CI environment value for local
	// replay. handled is false when the shared handling applies.
	variantValue(workflow ciWorkflow, job runtimeJob, step runtimeStep, row map[string]any, key, value string) (rewritten string, handled bool, err error)
	// skipsUnsupportedNode reports whether CI rows on a Node no tracer release
	// supports are left out of validation.
	skipsUnsupportedNode() bool
	// instructions returns the language's own onboarding instructions for
	// framework; empty uses the shared GitHub template.
	instructions(framework string) string
	// onboardingScope reports whether onboarding prints framework's discovered
	// CI scope, and whether it still prints instructions once every workflow
	// already runs the Datadog action.
	onboardingScope(framework string) (discover, instructionsWhenConfigured bool)
	// automaticCISetup reports whether onboard --apply writes the language's
	// CI setup. Otherwise a coding agent applies the setup instructions and
	// hands off to coordinated validation.
	automaticCISetup() bool
	// ciSetupConfigured reports whether every discovered CI test step of
	// framework already runs the Datadog setup action, with its mandatory
	// inputs and the selected site (empty: not selected), or is explicitly
	// excluded from it, with at least one instrumented step.
	ciSetupConfigured(root, framework, site string) bool
	// agentInstructions are framework's setup instructions for a coding agent
	// whose validation ddtest runs separately, for the selected Datadog site
	// (empty: the default); empty uses instructions.
	agentInstructions(framework, site string) string
}

// discoveryLanguageFor returns the language that discovers framework's CI, or nil.
func discoveryLanguageFor(framework string) discoveryLanguage {
	for _, language := range discoveryLanguages {
		if slices.Contains(language.frameworks(), framework) {
			return language
		}
	}
	return nil
}

// discoveryLanguageName names the language that discovers framework's CI.
// Frameworks no language claims are discovered as JavaScript, as before.
func discoveryLanguageName(framework string) string {
	if language := discoveryLanguageFor(framework); language != nil {
		return language.name()
	}
	return "javascript"
}

// discoveryLanguageNamed returns a registered language, or nil.
func discoveryLanguageNamed(name string) discoveryLanguage {
	for _, language := range discoveryLanguages {
		if language.name() == name {
			return language
		}
	}
	return nil
}

// setupActionLanguage is language's value in the setup action's languages input.
func setupActionLanguage(language string) string {
	if registered := discoveryLanguageNamed(language); registered != nil {
		return registered.actionLanguage()
	}
	return language
}

// resolvesTestSteps reports whether language statically resolves framework's CI commands.
func resolvesTestSteps(language, framework string) bool {
	registered := discoveryLanguageNamed(language)
	return registered != nil && registered.resolvesTestSteps(framework)
}

// discoveryLanguageAutomaticSetup reports whether onboard --apply writes
// language's CI setup.
func discoveryLanguageAutomaticSetup(language string) bool {
	registered := discoveryLanguageNamed(language)
	return registered == nil || registered.automaticCISetup()
}

// coordinatedLanguage reports whether agent onboarding supports language.
func coordinatedLanguage(language string) bool {
	registered := discoveryLanguageNamed(language)
	return registered != nil && registered.coordinated()
}
