// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

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

// coordinatedLanguage reports whether agent onboarding supports language.
func coordinatedLanguage(language string) bool {
	registered := discoveryLanguageNamed(language)
	return registered != nil && registered.coordinated()
}
