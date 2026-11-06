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

	"mvdan.cc/sh/v3/syntax"
)

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
	// fallback reports ddtest's setup without Bash or curl (see
	// fallbackSetupAt), which every provider validates the same way.
	fallback bool
	// after are shell statements that run only with the setup, inside its
	// guard.
	after []*syntax.Stmt
	// before are shell statements a setup snippet runs before itself,
	// whether or not it installs anything (see snippetSetupAt).
	before []string
	// shellLocal reports a setup whose exports reach only the later
	// commands of the shell it runs in, such as a setup snippet in a
	// Jenkinsfile sh step: a step of its job in another shell does not have
	// them (see ciShellOrigin).
	shellLocal bool
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
	// Beside names another Datadog setup mechanism the build enables for
	// the same language in other jobs that share its workspace, such as the
	// Datadog Jenkins plugin beside a container stage's setup snippet;
	// languages whose tracer joins the project's dependencies check that it
	// cannot change the release this setup installs.
	Beside string
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
	// Jobs are the jobs the setup is for, when the provider sets its jobs up
	// with several mechanisms (see runtimeJob.SetupProvider); empty for all.
	Jobs []string
	// Shared are the job definitions of these jobs whose other invocations
	// run no tests (see circleSharedJobs): the setup is enabled only for the
	// invocations that run the tests.
	Shared []ciSharedJob
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
	// Beside names the provider's other setup mechanism that the same build
	// runs for the language in other jobs of its workspace (see
	// runtimeJob.SetupProvider), such as the Datadog Jenkins plugin beside
	// container stages' setup snippet; empty when there is none.
	Beside string
	// SecretStore names where the provider keeps the DD_API_KEY a job reads
	// without writing it, such as "a masked GitLab CI/CD variable", for
	// setups written by hand.
	SecretStore string
	// KeyBinding reports a provider that gives a job the stored key only
	// where its configuration binds it (a Jenkins credential), not merely
	// because the secret store holds it: a hand-written setup without
	// DD_API_KEY in its environment then reports nothing.
	KeyBinding bool
	// Fallback reports the setup without Bash or curl (see imageFallback),
	// which installs the tracer with the job's own tools: languages describe
	// what it exports.
	Fallback bool
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

// setupProviderFor is the provider whose setup mechanism job uses: its
// own, unless the job runs another of its mechanisms (see
// runtimeJob.SetupProvider), whose names, inputs and instructions then
// apply.
func setupProviderFor(provider ciProvider, job runtimeJob) ciProvider {
	if job.SetupProvider != nil {
		return job.SetupProvider
	}
	return provider
}

// jobSetups are the jobs that set Test Optimization up with one of a
// provider's mechanisms.
type jobSetups struct {
	provider ciProvider
	jobs     []string
}

// testJobSetups groups the jobs that run framework's tests by the setup
// mechanism each uses (see setupProviderFor), the provider's own first; a
// provider with one mechanism, or no such job, gives that one alone.
func testJobSetups(root string, provider ciProvider, language, framework string) []jobSetups {
	groups := []jobSetups{{provider: provider}}
	workflows, err := readProviderWorkflows(provider, root)
	if err != nil {
		return groups
	}
	for _, workflow := range workflows {
		for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			job := workflow.Jobs[name]
			if disabledCondition(job.If) || !slices.ContainsFunc(job.Steps, func(step runtimeStep) bool {
				return resolveTestStep(root, workflow, job, step, language, framework).Matched
			}) {
				continue
			}
			mechanism := setupProviderFor(provider, job)
			i := slices.IndexFunc(groups, func(group jobSetups) bool { return group.provider == mechanism })
			if i < 0 {
				groups, i = append(groups, jobSetups{provider: mechanism}), len(groups)
			}
			groups[i].jobs = append(groups[i].jobs, workflow.Path+" / "+name)
		}
	}
	if len(groups) > 1 && len(groups[0].jobs) == 0 {
		groups = groups[1:]
	}
	return groups
}

// setupProviderNames names the setup mechanisms of groups.
func setupProviderNames(groups []jobSetups) string {
	names := make([]string, 0, len(groups))
	for _, group := range groups {
		if name := group.provider.setupName(); !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return strings.Join(names, " or the ")
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

// CIProviderSiteSetting names site in place of the example Datadog site the
// manual setup instructions show, which they render for the default site:
// empty for that default, which needs no substitution.
func CIProviderSiteSetting(site string) string {
	if site == "" || site == defaultDatadogSite {
		return ""
	}
	return "the Datadog site " + site + " in place of the example site " + defaultDatadogSite
}

// CIProviderAPIKeyAction is the person's step that stores the Datadog API key
// for the selected CI provider.
func CIProviderAPIKeyAction(skipped bool) string {
	return activeOrDefaultCIProvider().apiKeyAction(skipped)
}

// ciAPIKeyActions is a provider whose jobs set Test Optimization up with
// several mechanisms (see runtimeJob.SetupProvider), each receiving the
// API key its own way.
type ciAPIKeyActions interface {
	// apiKeyActions are the person's steps for the mechanisms named by
	// references (ciSetup.Reference of the validated jobs' setups).
	apiKeyActions(references []string, skipped bool) string
}

// CIProviderAPIKeyActionFor is CIProviderAPIKeyAction for the setups of
// check's jobs, on a provider whose jobs use several mechanisms.
func CIProviderAPIKeyActionFor(check *RuntimeCheck, skipped bool) string {
	provider := activeOrDefaultCIProvider()
	several, ok := provider.(ciAPIKeyActions)
	if !ok && check != nil {
		// Jobs on the setup without Bash or curl skip it without the key.
		fallback := slices.ContainsFunc(check.Jobs, func(job RuntimeFinding) bool { return job.Action == fallbackSetupName })
		others := slices.ContainsFunc(check.Jobs, func(job RuntimeFinding) bool { return job.Action != "" && job.Action != fallbackSetupName })
		switch {
		case fallback && others:
			return provider.apiKeyAction(skipped) + " " + newImageFallback(provider, "", "").apiKeyAction(skipped)
		case fallback:
			return newImageFallback(provider, "", "").apiKeyAction(skipped)
		}
	}
	if !ok || check == nil {
		return provider.apiKeyAction(skipped)
	}
	var references []string
	for _, job := range check.Jobs {
		if job.Action != "" {
			references = append(references, job.Action)
		}
	}
	return several.apiKeyActions(references, skipped)
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
	// HandWritten names the jobs that set up Test Optimization by hand in
	// a way ddtest recognizes but does not validate for Language yet (see
	// handWrittenSetup.Unvalidated), each with its setup.
	HandWritten []string
}

func (e *CIProviderUnsupportedError) Error() string {
	if len(e.HandWritten) > 0 {
		return fmt.Sprintf("ddtest cannot validate %s CI for %s on %s in this repository: ddtest does not validate hand-written %s setups yet, and these CI jobs set up Test Optimization by hand: %s ddtest neither certifies those setups nor replaces them with the %s. Keep them as they are, and do not add the %s to those jobs, remove their setup or change how they report; what a setup lacks is for the user to decide. ddtest will not prescribe CI edits or validate this CI configuration.", e.Framework, e.Language, e.Provider, handWrittenLanguageName(e.Language), strings.TrimSuffix(strings.Join(e.HandWritten, "; "), ".")+".", e.Setup, e.Setup)
	}
	if e.Reason != "" {
		return fmt.Sprintf("ddtest cannot set up %s CI for %s on %s in this repository: every CI job that runs the tests is outside what the %s instruments. %s The user's options are to run those tests where the %s instruments them, or %s. ddtest will not prescribe CI edits or validate this CI configuration as it is.", e.Framework, e.Language, e.Provider, e.Setup, e.Reason, e.Setup, handWrittenSetupOption(e.Language, e.Setup))
	}
	return fmt.Sprintf("ddtest does not support %s CI setup for %s on %s; it will not prescribe CI edits or validate this CI configuration. Rerun with --ci-provider <provider> to choose another provider.", e.Framework, e.Language, e.Provider)
}

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
	return providerSupportError(root, provider, language, framework)
}

// providerSupportError is CheckCIProviderSupport's result once the
// provider's configuration exists. A hand-written setup ddtest recognizes
// but does not validate for language comes first, whatever the provider
// supports, so it is never replaced or certified.
func providerSupportError(root string, provider ciProvider, language, framework string) error {
	if jobs := unvalidatedHandWrittenJobs(root, provider, language, framework); len(jobs) > 0 {
		return &CIProviderUnsupportedError{Language: language, Framework: framework, Provider: provider.displayName(), Setup: provider.setupName(), HandWritten: jobs}
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
			// A job that sets up the tracer by hand needs no provider setup.
			if job.SetupUnsupported == "" || firstHandWrittenSetup(root, workflow, job, language, framework, "") != nil {
				return ""
			}
			reasons = append(reasons, workflow.Path+" / "+name+": "+job.SetupUnsupported+".")
		}
	}
	return strings.Join(reasons, " ")
}

// ProviderUnsupportedResult reports a language/provider combination ddtest
// does not set up.
func ProviderUnsupportedResult(err *CIProviderUnsupportedError) AgentResult {
	result := AgentResult{Status: "unsupported", Phase: "onboarding", Code: "ci_provider_unsupported", Reason: err.Error(), NextAction: "report_result"}
	switch {
	case len(err.HandWritten) > 0:
		result.Code = "ci_setup_unsupported"
		result.Instruction = "Report this result to the user with its reason and stop. Keep the hand-written setups as they are: do not add the " + err.Setup + ", edit CI, add or change a tracer, or remove a setup; those are the user's decisions, and ddtest cannot validate hand-written " + handWrittenLanguageName(err.Language) + " setups yet."
	case err.Reason != "":
		result.Code = "ci_setup_unsupported"
		result.Instruction = "Report this result to the user with its reason and stop. Do not move tests between agents, edit CI, add a tracer or set up Test Optimization by hand: those are the user's decisions."
		if validatesHandWrittenSetups(err.Language) {
			result.Instruction += " Once the user sets it up by hand, rerun onboarding, which checks that setup."
		} else {
			result.Instruction += " ddtest does not validate hand-written " + handWrittenLanguageName(err.Language) + " setups yet."
		}
	}
	return result
}

// CIProviderEditsSetup reports whether onboard --apply writes the selected
// CI provider's setup; otherwise every language gets instructions.
func CIProviderEditsSetup() bool { return activeOrDefaultCIProvider().editsSetup() }

// providerAutomaticSetup reports whether onboard --apply writes framework's
// setup on provider in root, reporting to site (empty: any): the provider
// edits its configuration and the language writes that framework's setup
// there. A plugin setup applies only where the provider can make the edit
// and leave it complete for site; otherwise the instructions do.
func providerAutomaticSetup(root string, provider ciProvider, language discoveryLanguage, framework, site string) bool {
	return provider.editsSetup() && language.automaticProviderSetup(framework) && (!provider.setupTerms().Plugin || pluginSetupApplies(root, framework, site))
}

// ciConfirmationProblem lists the provider facts the user has not
// confirmed (--ci-confirm) that this entry's setup depends on, and how to
// confirm them; empty when every one is confirmed.
func ciConfirmationProblem(provider ciProvider, setup ciSetup) string {
	var missing []ciFactDefinition
	for _, definition := range ciProviderFacts[provider.name()] {
		if _, stated := statedFact(definition.Key); slices.Contains(setup.Confirmations, definition.Key) && !stated {
			missing = append(missing, definition)
		}
	}
	if len(missing) == 0 {
		return ""
	}
	var questions, flags []string
	for _, fact := range missing {
		flag := fact.Key
		if fact.Values != "" {
			flag += "=" + fact.Values
		}
		questions = append(questions, strings.TrimSpace(fact.Key+": "+fact.Question+" "+fact.Remedy))
		flags = append(flags, "--ci-confirm "+flag)
	}
	return "The " + provider.setupName() + " setup depends on facts no CI configuration shows, so ddtest cannot certify it until the user confirms them. Ask the user; never assume or check them from this machine. " + strings.Join(questions, " ") + " Then rerun testdrive adding " + strings.Join(flags, " ") + " with the values they confirmed."
}
