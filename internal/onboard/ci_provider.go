// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/kballard/go-shellquote"
	"mvdan.cc/sh/v3/syntax"
)

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
	// configurationFiles lists the repository-relative CI configuration files
	// the provider owns; none means the repository does not use it. Listing
	// never parses them.
	configurationFiles(root string) ([]string, error)
	// readWorkflows parses the provider's configuration into the shared model.
	readWorkflows(root string) ([]ciWorkflow, error)
	// findTestWorkflows lists the configuration files whose jobs run
	// framework's tests, and the subset that already set up Test Optimization
	// for language through the provider's setup mechanism.
	findTestWorkflows(root, language, framework string) (workflows, configured []string, err error)
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

// ciSetup is the Datadog Test Optimization setup one CI step runs, described
// in the setup action's terms. Each provider recognizes its own mechanism
// (see ciProvider.setupStep); shared code never inspects the mechanism.
type ciSetup struct {
	// Reference names what the step runs, such as the action and its ref.
	Reference string
	// Pinned reports a reference that selects a release.
	Pinned bool
	// Supported reports a release ddtest validates, with its integrity check
	// where the provider has one.
	Supported bool
	// DefaultLatest reports that an unset tracer version input installs the
	// latest release; otherwise the mechanism's metadata defines the default.
	DefaultLatest bool
	// Problem is why the setup cannot work where it runs, as the provider
	// found while reading it; setupProblem reports it first.
	Problem string
	// shaped reports a setup step in a form the provider recognizes entirely.
	shaped bool
	// after are shell statements that run only with the setup, inside its
	// guard.
	after []*syntax.Stmt
	// Inputs are the installer inputs keyed by the setup action's input
	// names: languages, api_key, site and <language>-tracer-version. Values
	// are verbatim; unresolved references stay as written.
	Inputs map[string]string
	// Floating reports a mechanism that cannot pin the tracer: it installs
	// the latest release when the build runs, unless the project's own
	// dependencies already pin one. Each language decides which applies and
	// reports the floating release as a caveat.
	Floating bool
	// WorkspaceRoot reports a mechanism that configures the tracer from the
	// checkout root with the build agent's own toolchain, so a tracer that
	// joins the project's dependencies must join the root ones.
	WorkspaceRoot bool
	// Confirmations are the facts (--ci-confirm keys, see ciProviderFacts)
	// the user must state before ddtest certifies this setup.
	Confirmations []string
	// Caveats are what ddtest could not check about the mechanism, for
	// final_response.
	Caveats []string
	// Exports are the loader variables the mechanism sets for the steps
	// after it besides the action's DD_TRACE_PACKAGE and DD_TRACE_ESM_IMPORT,
	// such as the Datadog Jenkins plugin's DD_TRACE_PATH. A test step's
	// NODE_OPTIONS can trust only the variables its own setup exports.
	Exports []string
}

// ciSetupRequest is the setup one language asks a provider for: the
// provider supplies the step, the language its own values.
type ciSetupRequest struct {
	// Language is the installer's token for the language (languages input).
	Language string
	// Site is the Datadog site the setup reports to.
	Site string
	// TracerInput names the exact tracer release input, such as
	// js-tracer-version, and TracerVersion its value.
	TracerInput, TracerVersion string
	// After are shell commands that must follow the setup and precede the
	// tests in the same job, such as an ESM loader registration.
	After []string
	// Notes are language rules the instructions add after the step.
	Notes []string
	// Service is the service name a setup that names one reports under: the
	// project's own name, empty when the language reads none.
	Service string
	// Framework is the framework whose tests the setup is for.
	Framework string
}

// ciSetupTerms are how a provider without the shared workflow syntax names
// its setup in the languages' setup notes, which refer to the setup's place
// in a job and to how a later command keeps a variable it exports.
type ciSetupTerms struct {
	// Installer names the setup mechanism after "the", such as "script".
	Installer string
	// Entry names the setup's place in a job, such as "the entry", and
	// SetupEntry the same place where the setup must be named, such as "the
	// setup entry".
	Entry, SetupEntry string
	// Later names what the setup's exports reach, such as "later commands of
	// the job".
	Later string
	// Variables names where a job declares its variables, such as "the job's
	// variables".
	Variables string
	// Persist describes, in a sentence, running a shell command after the
	// setup so that later test commands see what it exports; PersistEntry
	// writes it as an entry of the job's configuration (a YAML list item).
	Persist, PersistEntry func(command string) string
	// MatrixExclusion says how matrix entries leave a job, such as "with
	// rules (when: never on their matrix variables)".
	MatrixExclusion string
	// SharedMatrix names a matrix whose entries share one job's commands,
	// such as "a parallel:matrix shares the script".
	SharedMatrix string
	// ImageGuard writes an entry that runs command only where the job's
	// image, written as template, is image; empty when the template selects
	// no image through a variable or parameter.
	ImageGuard func(template, image, command string) string
	// Plugin reports a setup that is a CI server plugin, enabled in the
	// configuration, that installs the tracers on the build agent and sets
	// their variables for every step (the Datadog Jenkins plugin), instead of
	// an installer the job runs: languages describe that mechanism.
	Plugin bool
}

// ciComponent is a reusable CI component a language knows, such as a
// runtime or dependency installer published for one provider, modeled in the
// provider's own syntax so the provider reads it like the repository's
// configuration.
type ciComponent struct {
	// Name is the component's registry name, such as an orb's
	// namespace/name.
	Name string
	// Majors are the major versions the model describes.
	Majors []string
	// Definition is the model: for CircleCI an inline orb's commands, jobs
	// and executors.
	Definition string
}

// ciSetupTarget is one test step that needs the setup before it.
type ciSetupTarget struct {
	Workflow, Job string
	// Step indexes the job's steps.
	Step int
	// After overrides the request's commands for this step.
	After []string
}

// datadogSetup recognizes the Datadog setup a step runs, whichever provider
// read it.
func datadogSetup(step runtimeStep) (ciSetup, bool) {
	if step.Setup != nil {
		return *step.Setup, true
	}
	for _, provider := range ciProviders {
		if setup, ok := provider.setupStep(step); ok {
			return setup, true
		}
	}
	return ciSetup{}, false
}

// setupSelectsLanguage reports whether setup installs the tracer of the
// language named by its languages input, after resolving row's matrix values.
func setupSelectsLanguage(setup ciSetup, row map[string]any, language string) (bool, error) {
	languages, err := runtimeValue(setup.Inputs["languages"], row)
	if err != nil {
		return false, err
	}
	return languages == "all" || slices.Contains(strings.Fields(languages), language), nil
}

// CIProviderSetupName names the selected CI provider's setup mechanism when
// it is not written in the shared workflow syntax; empty otherwise, where
// languages name the setup action themselves.
func CIProviderSetupName() string {
	if provider := activeOrDefaultCIProvider(); !provider.sharedSyntax() {
		return provider.setupName()
	}
	return ""
}

// CIProviderTracerSetting is how the selected CI provider's setup pins
// the language's tracer release, such as js-tracer-version: 5.133.0, or
// DD_SET_TRACER_VERSION_JS="5.133.0" in the Buildkite setup snippet.
func CIProviderTracerSetting(language, version string) string {
	provider := activeOrDefaultCIProvider()
	input := provider.setupInput(language + "-tracer-version")
	switch provider.name() {
	case buildkiteProvider:
		return input + `="` + version + `"`
	case jenkinsProvider:
		return "release " + version + " where the CI setup can pin it (the " + input + ")"
	}
	return input + ": " + version
}

// CIProviderAPIKeyAction is the person's step that stores the Datadog API key
// for the selected CI provider.
func CIProviderAPIKeyAction(skipped bool) string {
	return activeOrDefaultCIProvider().apiKeyAction(skipped)
}

// CIProviderChoice is a user decision: several CI providers contain tests and
// ddtest configures one at a time. No command is suggested until it is made.
type CIProviderChoice struct{ Providers []string }

func (e *CIProviderChoice) Error() string {
	return "Several CI providers contain test commands: " + strings.Join(e.Providers, ", ") + ". Ask the user which provider to install Test Optimization on, then rerun with --ci-provider <provider>."
}

// CIProviderUnsupportedError reports a language without CI setup support on
// the selected provider, or (Reason) a repository whose every test job is
// outside what the provider's setup mechanism instruments. ddtest
// prescribes nothing for that combination.
type CIProviderUnsupportedError struct {
	Language, Framework, Provider string
	// Reason names the jobs the provider's setup cannot instrument, and
	// why; Setup names that setup.
	Reason, Setup string
}

func (e *CIProviderUnsupportedError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("ddtest cannot set up %s CI for %s on %s in this repository: every CI job that runs the tests is outside what the %s instruments. %s The user's options are to run those tests where the %s instruments them, or to set up Test Optimization for them by hand, outside ddtest, which ddtest cannot validate. ddtest will not prescribe CI edits or validate this CI configuration.", e.Framework, e.Language, e.Provider, e.Setup, e.Reason, e.Setup)
	}
	return fmt.Sprintf("ddtest does not support %s CI setup for %s on %s; it will not prescribe CI edits or validate this CI configuration. Rerun with --ci-provider <provider> to choose another provider.", e.Framework, e.Language, e.Provider)
}

// activeProvider is the provider selected for this command; empty selects one
// automatically.
var activeProvider atomic.Value

// UseCIProvider selects the CI provider onboarding and testdrive read for the
// rest of the command, normally the result of SelectCIProvider. Empty selects
// it automatically from the repository again.
func UseCIProvider(name string) { activeProvider.Store(name) }

// ActiveCIProvider names the provider selected with UseCIProvider, or empty.
func ActiveCIProvider() string {
	name, _ := activeProvider.Load().(string)
	return name
}

// CIProviderNames lists the supported --ci-provider values.
func CIProviderNames() []string {
	names := make([]string, 0, len(ciProviders))
	for _, provider := range ciProviders {
		names = append(names, provider.name())
	}
	return names
}

// UnknownCIProviderError rejects a --ci-provider value no provider registers.
func UnknownCIProviderError(name string) error {
	return fmt.Errorf("unsupported --ci-provider %q; supported CI providers: %s", name, strings.Join(CIProviderNames(), ", "))
}

func ciProviderNamed(name string) ciProvider {
	for _, provider := range ciProviders {
		if provider.name() == name {
			return provider
		}
	}
	return nil
}

// defaultCIProvider applies when a repository has no CI configuration: the
// first registered provider, as onboarding behaved before providers were
// selectable.
func defaultCIProvider() ciProvider { return ciProviders[0] }

// SelectCIProvider resolves the CI provider to configure. A requested provider
// is used as given. Otherwise the only provider with configuration files is
// used without parsing them; with several, the ones whose configuration runs
// tests or leaves steps unresolved are candidates, and more than one candidate
// returns a *CIProviderChoice. A configuration that cannot be parsed never
// fails selection: that provider is not a candidate, and its files stay
// visible as review notes of the selected provider's scope. Empty means no
// provider has configuration files.
func SelectCIProvider(root, requested string) (string, error) {
	if requested != "" {
		if ciProviderNamed(requested) == nil {
			return "", UnknownCIProviderError(requested)
		}
		return requested, nil
	}
	var configured []ciProvider
	for _, provider := range ciProviders {
		// A listing error is reported when that provider is read.
		if files, err := provider.configurationFiles(root); err != nil || len(files) > 0 {
			configured = append(configured, provider)
		}
	}
	switch len(configured) {
	case 0:
		return "", nil
	case 1:
		return configured[0].name(), nil
	}
	var candidates []string
	for _, provider := range configured {
		workflows, err := provider.readWorkflows(root)
		if err == nil && slices.ContainsFunc(workflows, func(workflow ciWorkflow) bool { return workflowHasTests(root, workflow) }) {
			candidates = append(candidates, provider.name())
		}
	}
	switch len(candidates) {
	case 0:
		return configured[0].name(), nil
	case 1:
		return candidates[0], nil
	}
	return "", &CIProviderChoice{Providers: candidates}
}

// currentCIProvider is the provider selected with UseCIProvider, otherwise
// the one SelectCIProvider resolves, otherwise the default.
func currentCIProvider(root string) (ciProvider, error) {
	name := ActiveCIProvider()
	if name == "" {
		var err error
		if name, err = SelectCIProvider(root, ""); err != nil {
			return nil, err
		}
	}
	if name == "" {
		return defaultCIProvider(), nil
	}
	if provider := ciProviderNamed(name); provider != nil {
		return provider, nil
	}
	return nil, UnknownCIProviderError(name)
}

// activeOrDefaultCIProvider is the selected provider without consulting the
// repository: the default until UseCIProvider selects another.
func activeOrDefaultCIProvider() ciProvider {
	if provider := ciProviderNamed(ActiveCIProvider()); provider != nil {
		return provider
	}
	return defaultCIProvider()
}

// readWorkflows reads the selected provider's CI configuration.
func readWorkflows(root string) ([]ciWorkflow, error) {
	provider, err := currentCIProvider(root)
	if err != nil {
		return nil, err
	}
	return readProviderWorkflows(provider, root)
}

// WithCIProviderDefaults adds the variables the selected CI provider sets in
// every job, without overriding the configuration's own.
func WithCIProviderDefaults(env map[string]string) map[string]string {
	return mergeEnvironment(activeOrDefaultCIProvider().environment(), env)
}

// CIProviderConfigurationFiles lists the selected CI provider's configuration
// files in root without parsing them.
func CIProviderConfigurationFiles(root string) ([]string, error) {
	return activeOrDefaultCIProvider().configurationFiles(root)
}

// CIConfigurationFiles lists every repository file the selected CI
// provider's configuration is read from, included files too. A
// configuration that cannot be read lists its configuration files only.
func CIConfigurationFiles(root string) []string {
	provider := activeOrDefaultCIProvider()
	files, _ := provider.configurationFiles(root)
	workflows, err := provider.readWorkflows(root)
	if err != nil {
		return files
	}
	for _, workflow := range workflows {
		for _, file := range append([]string{workflow.Path}, workflow.Files...) {
			if !slices.Contains(files, file) {
				files = append(files, file)
			}
		}
	}
	return files
}

// ciProviderFollowUpFlag returns the --ci-provider argument human follow-up
// commands need: only when several providers have configuration in root, so
// a rerun would ask again.
func ciProviderFollowUpFlag(root string) string {
	return ciProviderFlag(root) + ciStatementsFollowUp()
}

// ciStatementsFollowUp repeats the user's CI statements in a human follow-up
// command, which reports are bound to.
func ciStatementsFollowUp() string {
	if arguments := CIStatementArguments(); len(arguments) > 0 {
		return " " + shellquote.Join(arguments...)
	}
	return ""
}

func ciProviderFlag(root string) string {
	selected := ActiveCIProvider()
	if selected == "" {
		return ""
	}
	configured := 0
	for _, provider := range ciProviders {
		if files, err := provider.configurationFiles(root); err != nil || len(files) > 0 {
			configured++
		}
	}
	if configured < 2 {
		return ""
	}
	return " --ci-provider " + selected
}

// CIProviderDisplayName names the selected CI provider in output.
func CIProviderDisplayName() string { return activeOrDefaultCIProvider().displayName() }

// languageSupportsCIProvider reports whether language sets up Test
// Optimization on provider. Unregistered languages support none.
func languageSupportsCIProvider(language string, provider ciProvider) bool {
	registered := discoveryLanguageNamed(language)
	return registered != nil && registered.supportsCIProvider(provider.name())
}

// CheckCIProviderSupport returns a *CIProviderUnsupportedError when the
// selected CI provider has no setup support for language's framework.
func CheckCIProviderSupport(root, language, framework string) error {
	provider, err := currentCIProvider(root)
	if err != nil {
		return err
	}
	// Without the provider's configuration there is no CI to consult or set up.
	if files, err := provider.configurationFiles(root); err == nil && len(files) == 0 {
		return nil
	}
	if !languageSupportsCIProvider(language, provider) {
		return &CIProviderUnsupportedError{Language: language, Framework: framework, Provider: provider.displayName()}
	}
	if reason := unsupportedTestJobs(root, provider, language, framework); reason != "" {
		return &CIProviderUnsupportedError{Language: language, Framework: framework, Provider: provider.displayName(), Reason: reason, Setup: provider.setupName()}
	}
	return nil
}

// unsupportedTestJobs explains, when every CI job that runs framework's
// tests is one the provider's setup mechanism cannot instrument (see
// runtimeJob.SetupUnsupported), why; empty when any can, or none runs them.
func unsupportedTestJobs(root string, provider ciProvider, language, framework string) string {
	workflows, err := readProviderWorkflows(provider, root)
	if err != nil {
		return ""
	}
	var reasons []string
	for _, workflow := range workflows {
		for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			job := workflow.Jobs[name]
			if disabledCondition(job.If) {
				continue
			}
			tests := slices.ContainsFunc(job.Steps, func(step runtimeStep) bool {
				resolution := resolveTestStep(root, workflow, job, step, language, framework)
				return !resolution.Review && (resolution.Matched || resolution.Reason != "" && step.ResolutionError == "" && stepMayStartTests(root, workflow, job, step, framework))
			})
			if !tests {
				continue
			}
			if job.SetupUnsupported == "" {
				return ""
			}
			reasons = append(reasons, workflow.Path+" / "+name+": "+job.SetupUnsupported+".")
		}
	}
	return strings.Join(reasons, " ")
}

// workflowHasTests reports whether any active step of workflow may run tests
// of a registered language, or cannot be resolved well enough to tell.
func workflowHasTests(root string, workflow ciWorkflow) bool {
	for _, job := range workflow.Jobs {
		if disabledCondition(job.If) {
			continue
		}
		if _, _, err := jobMatrix(job); err != nil {
			return true // An unresolved matrix can run tests.
		}
		for _, step := range job.Steps {
			if disabledCondition(step.If) {
				continue
			}
			if step.Run == "" && step.ResolutionError != "" {
				return true
			}
			for _, language := range discoveryLanguages {
				for _, framework := range language.frameworks() {
					resolution := resolveTestStep(root, workflow, job, step, language.name(), framework)
					if resolution.Matched || (!resolution.Review && resolution.Reason != "" && stepMayStartTests(root, workflow, job, step, framework)) || namesFramework(step.Run, framework, true) {
						return true
					}
				}
			}
		}
	}
	return false
}

// disabledCondition reports whether condition can never pass, such as false or
// ${{ false }}; anything unresolved may still run.
func disabledCondition(condition string) bool {
	if strings.TrimSpace(condition) == "" {
		return false
	}
	active, err := possibleJobCondition(condition, nil)
	return err == nil && !active
}

// ProviderChoiceResult asks the user to choose the CI provider to configure.
func ProviderChoiceResult(choice *CIProviderChoice) AgentResult {
	return AgentResult{Status: "needs_input", Phase: "discovery", Code: "ci_provider_required", Reason: choice.Error(), RequiredInput: choice.Providers, NextAction: "ask_user", Instruction: "Ask the user which CI provider should receive Test Optimization. Do not choose one or install on every provider. After their answer, rerun the original command with --ci-provider <provider>."}
}

// ProviderUnsupportedResult reports a language/provider combination ddtest
// does not set up.
func ProviderUnsupportedResult(err *CIProviderUnsupportedError) AgentResult {
	result := AgentResult{Status: "unsupported", Phase: "onboarding", Code: "ci_provider_unsupported", Reason: err.Error(), NextAction: "report_result"}
	if err.Reason != "" {
		result.Code = "ci_setup_unsupported"
		result.Instruction = "Report this result to the user with its reason and stop. Do not move tests between agents, edit CI, add a tracer or set up Test Optimization by hand: those are the user's decisions, and ddtest cannot validate a manual setup."
	}
	return result
}

// CIProviderEditsSetup reports whether onboard --apply writes the selected
// CI provider's setup; otherwise every language gets instructions.
func CIProviderEditsSetup() bool { return activeOrDefaultCIProvider().editsSetup() }

// providerAutomaticSetup reports whether onboard --apply writes framework's
// setup on provider in root: the provider edits its configuration and the
// language writes that framework's setup there. A plugin setup applies only
// where the provider can make the edit; otherwise the instructions do.
func providerAutomaticSetup(root string, provider ciProvider, language discoveryLanguage, framework string) bool {
	return provider.editsSetup() && language.automaticProviderSetup(framework) && (!provider.setupTerms().Plugin || pluginSetupApplies(root, framework))
}

// testWorkflowsWithSetup lists the workflows whose jobs run framework's tests
// and those where an active setup selecting language precedes every test step.
func testWorkflowsWithSetup(root string, workflows []ciWorkflow, language, framework string) (found, configured []string) {
	token := setupActionLanguage(language)
	for _, workflow := range workflows {
		tests, complete := false, true
		for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			job := workflow.Jobs[name]
			if disabledCondition(job.If) {
				continue
			}
			setup := false
			for _, step := range job.Steps {
				if current, ok := datadogSetup(step); ok {
					selected, err := setupSelectsLanguage(current, nil, token)
					setup = setup || err == nil && selected
				}
				resolution := resolveTestStep(root, workflow, job, step, language, framework)
				if resolution.Review || !resolution.Matched && (resolution.Reason == "" || !stepMayStartTests(root, workflow, job, step, framework)) {
					continue
				}
				tests = true
				complete = complete && setup && resolution.Reason == ""
			}
		}
		if tests {
			found = append(found, workflow.Path)
			if complete {
				configured = append(configured, workflow.Path)
			}
		}
	}
	return found, configured
}
