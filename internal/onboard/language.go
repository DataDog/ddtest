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
	// supportsCIProvider reports whether the language sets up Test
	// Optimization on a CI provider (see ciProvider). Instructions, setup and
	// completeness checks apply only on providers it supports; others get a
	// clean unsupported answer.
	supportsCIProvider(provider string) bool
	// setupShellExports are the variables the Datadog installer exports for
	// the language into later commands of the same shell, on providers whose
	// setup runs in the job's shell (see ciProvider.sharedSyntax). Values use
	// the workflow model's syntax: ${{ env.NAME }} is a value only CI knows.
	// environment reaches the commands' environment; references can only be
	// expanded by later commands. current is the environment at the setup.
	setupShellExports(current map[string]string) (environment, references map[string]string)
	// ciSetupRequest is the language's part of a provider's setup step for
	// framework, reporting to site, on providers without the shared workflow
	// syntax: its installer token, exact tracer input, commands that must
	// follow the setup, and its own setup rules.
	ciSetupRequest(root, framework, site string, terms ciSetupTerms) ciSetupRequest
	// cacheVariable reports a variable that locates the language's package
	// cache or install directory, which CI caches between jobs.
	cacheVariable(name string) bool
	// runtimeVariable reports an environment variable the language's
	// runtime or tools read, which images commonly export.
	runtimeVariable(name string) bool
	// outsideFrameworkJob reports whether a CI job, on one matrix row,
	// cannot run framework's tests: nothing it runs (following package and
	// repository scripts) mentions the framework. Such a job without the
	// setup and without a recognized test is reported, not a blocker.
	// Languages without a static answer return false.
	outsideFrameworkJob(root string, workflow ciWorkflow, job runtimeJob, row map[string]any, framework string) bool
	// unsupportedImage names the runtime a CI job's container image selects
	// when the language's tracer cannot instrument it, such as another
	// language engine; empty otherwise. Such a job without the setup keeps
	// running uninstrumented and is excluded from validation.
	unsupportedImage(image string) string
	// ciComponents are the provider's reusable components the language
	// knows how they run, such as its runtime and dependency installers and
	// test commands; a provider reads only the versions modeled and leaves
	// every other component unresolved.
	ciComponents(provider string) []ciComponent
	// automaticProviderSetup reports whether onboard --apply writes
	// framework's setup on such providers.
	automaticProviderSetup(framework string) bool
	// agentInstructions are framework's setup instructions for a coding agent
	// whose validation ddtest runs separately, for the selected Datadog site
	// (empty: the default); empty uses instructions.
	agentInstructions(framework, site string) string

	// jobConsumer identifies jobs whose test commands are selected by matrix
	// values, before the shared unresolved-command scan. handled is false when
	// that scan should decide whether the job can run framework's tests.
	jobConsumer(root string, workflow ciWorkflow, job runtimeJob, framework string) (consumer, handled bool)

	// keepsCISetting reports whether a DD_* CI setting reaches local replay.
	// Other DD_* settings configure CI's agent, intake or session.
	keepsCISetting(key string) bool

	// ciPluginLanguage is the language's token in a CI server plugin's
	// languages list, such as the Datadog Jenkins plugin's.
	ciPluginLanguage() string
	// ciPluginExports are the variables a CI server plugin (the Datadog
	// Jenkins plugin) sets for the language in every step, from the job's
	// environment current, in the workflow model's syntax; references can
	// only be expanded by commands.
	ciPluginExports(current map[string]string) (environment, references map[string]string)

	// statedRuntime is how the language takes the user's statement of a CI
	// job's runtime its configuration does not select (--ci-runtime); nil
	// when it takes none.
	statedRuntime() *statedRuntime
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
