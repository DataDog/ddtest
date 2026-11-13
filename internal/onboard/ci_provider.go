// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

// ciProvider is everything onboarding needs from one CI provider. A provider
// parses its own configuration into the shared workflow model (jobs, steps,
// commands, environment, matrix and conditions); what to run and how to set up
// Test Optimization for a language stays behind discoveryLanguage. Providers
// keep their specifics in ci_provider_*.go and register in ci_providers.go;
// TestCIProviderFilesNameNoLanguage keeps languages out of those files.
type ciProvider interface {
	// name is the provider's --ci-provider value.
	name() string
	// displayName names the provider in output.
	displayName() string
	// checkoutReference is how the provider's configuration reads the
	// checkout directory, such as $CI_PROJECT_DIR, which the shared model
	// writes as ${{ github.workspace }} (see displayCIText).
	checkoutReference() string
	// checkoutVariable names the variable the provider sets in every job to
	// the checkout directory, such as CI_PROJECT_DIR; empty when it sets
	// none that commands can use as one (see repositoryRelativeCommands).
	checkoutVariable() string
	// configurationFiles lists the repository-relative CI configuration files
	// the provider owns; none means the repository does not use it. Listing
	// never parses them.
	configurationFiles(root string) ([]string, error)
	// readWorkflows parses the provider's configuration into the shared model.
	readWorkflows(root string) ([]ciWorkflow, error)
	// environment holds the variables the provider sets in every job.
	environment() map[string]string

	// editsSetup reports whether onboard --apply writes the provider's setup
	// with planSetupEdits; otherwise every language gets instructions that a
	// person or coding agent applies.
	editsSetup() bool
	// sharedSyntax reports whether the configuration is written in the shared
	// workflow model's own syntax, so the shared workflow editor and setup
	// instructions apply to it directly; otherwise setupInstructions and
	// planSetupEdits do.
	sharedSyntax() bool
	// setupStep recognizes the provider's Datadog setup mechanism in a step
	// of its configuration.
	setupStep(step runtimeStep) (ciSetup, bool)
	// setupName names the setup mechanism in findings, such as "Datadog action".
	setupName() string
	// setupInput names one setup input (in the setup action's terms, such as
	// ruby-tracer-version) the way the provider's configuration spells it.
	setupInput(name string) string
	// setupProblem explains why setup, with inputs resolved for one
	// configuration, is not a complete setup: a supported release, an API key
	// from the provider's secret store, an exact tracer release in tracerInput
	// (exact validates it; example is one) and the selected site (empty: any).
	// Empty when it is complete.
	setupProblem(setup ciSetup, tracerInput string, exact func(string) bool, example, site string) string
	// apiKeyAction is the one step a person takes in the provider's settings
	// so the setup can report: storing DD_API_KEY. skipped describes a setup
	// guarded to skip without the key.
	apiKeyAction(skipped bool) string
	// setupTerms are the provider's words for its setup, which languages'
	// setup notes use.
	setupTerms() ciSetupTerms
	// setupInstructions explains how to add the setup step before a job's
	// test commands, for a person or a coding agent.
	setupInstructions(request ciSetupRequest) string
	// planSetupEdits inserts the setup step before each target test step,
	// preserving every other line, or explains why it cannot do so safely.
	// impact lists consequences a person should review.
	planSetupEdits(root string, workflows []ciWorkflow, targets []ciSetupTarget, request ciSetupRequest) (edits []setupEdit, impact []string, err error)
}
