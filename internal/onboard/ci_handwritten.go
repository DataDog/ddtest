// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// A hand-written setup is Test Optimization a CI job sets up itself, without
// its provider's Datadog setup mechanism: the tracer comes from the project's
// own dependencies (or an install command of the job), the job's environment
// loads it into the test process (NODE_OPTIONS, RUBYOPT or the test
// framework's configuration), and DD_* variables configure it. On providers
// without the shared workflow syntax, a test command that no provider setup
// precedes is checked for one, with the rules the provider's setup follows:
// an exact tracer release, the API key from the provider's secret store (never
// written into the configuration), the selected site, a preload that reaches
// the tests, and agentless reporting. A setup missing a piece is reported
// with that piece, never replaced with the provider's setup; what ddtest
// cannot read statically, such as a Datadog Agent or a value only CI knows,
// stays unresolved and is never certified.
type handWrittenSetup struct {
	// Reference names the setup in findings, such as "hand-written dd-trace
	// setup (NODE_OPTIONS)".
	Reference string
	// Tracer is the exact tracer release the job loads, and Pin where it is
	// pinned, such as package-lock.json.
	Tracer, Pin string
	// Problems are what the setup lacks, each with the change that
	// completes it.
	Problems []string
	// Unresolved are what ddtest cannot verify statically about it.
	Unresolved []string
	// Confirmations are the facts (--ci-confirm keys) only the user can
	// state that the setup depends on, such as a Datadog Agent it reports
	// through; pending explains each one the user has not stated.
	Confirmations []string
	pending       []string
	// Unvalidated is why ddtest never certifies the setup, whatever it
	// holds: its language's hand-written setups are recognized, but no CI
	// check of that language reads them yet (see
	// discoveryLanguage.validatesHandWrittenSetup).
	Unvalidated string
}

// handWrittenAgent is the --ci-confirm fact a hand-written setup that
// reports through a Datadog Agent depends on: no CI configuration shows the
// Agent.
const handWrittenAgent = "datadog-agent"

// handWrittenAgentFact asks the user about the Datadog Agent a hand-written
// setup reports through.
var handWrittenAgentFact = ciFactDefinition{
	Key:      handWrittenAgent,
	Question: "These CI jobs set up Test Optimization by hand and report through a Datadog Agent (DD_CIVISIBILITY_AGENTLESS_ENABLED is not true). Does a Datadog Agent run where those tests run, at the address they use (DD_TRACE_AGENT_URL or DD_AGENT_HOST, otherwise localhost:8126), with an API key and the selected Datadog site?",
	Remedy:   "If not, the user decides how those jobs report: run such an Agent for them, or report agentless (DD_CIVISIBILITY_AGENTLESS_ENABLED=true with DD_API_KEY from the CI provider's secrets). Do not change how they report without the user's decision.",
	Statement: func(string) string {
		return "A Datadog Agent runs where the hand-written CI setups' tests run, at the address they use, with an API key and the selected Datadog site"
	},
}

// handWrittenPrefix starts every hand-written setup's reference.
const handWrittenPrefix = "hand-written "

// HandWrittenSetupAction reports a finding's setup (RuntimeFinding.Action)
// that its CI job writes by hand.
func HandWrittenSetupAction(action string) bool {
	return strings.HasPrefix(action, handWrittenPrefix)
}

// CIProviderHandWrittenKeyAction is the person's step that stores the
// Datadog API key a hand-written setup reads on the selected CI provider.
func CIProviderHandWrittenKeyAction() string {
	return "Store DD_API_KEY in " + activeOrDefaultCIProvider().setupTerms().SecretStore + ", where the jobs' hand-written setup reads it; never write the key into the CI configuration. Until it exists, the tracer reports nothing and those tests run untraced."
}

// CIProviderKeyServesHandWritten reports whether the selected CI provider's
// API key step also gives the jobs' hand-written setups their key: its
// setup reads DD_API_KEY from the same secret store. A CI server plugin
// configured with its own key (the Datadog Jenkins plugin) does not.
func CIProviderKeyServesHandWritten() bool {
	return !activeOrDefaultCIProvider().setupTerms().Plugin
}

// handWrittenReference names a language's hand-written setup in findings.
func handWrittenReference(tracer, mechanism string) string {
	return handWrittenPrefix + tracer + " setup (" + mechanism + ")"
}

// complete reports a setup ddtest can certify.
func (s *handWrittenSetup) complete() bool {
	return s.Unvalidated == "" && len(s.Problems)+len(s.Unresolved)+len(s.pending) == 0
}

// unconfirmed lists the facts the setup depends on that the user has not
// stated, when they are all it lacks; nil otherwise.
func (s *handWrittenSetup) unconfirmed() []string {
	if s.Unvalidated != "" || len(s.Problems)+len(s.Unresolved) > 0 {
		return nil
	}
	var keys []string
	for _, key := range s.Confirmations {
		if _, stated := statedFact(key); !stated {
			keys = append(keys, key)
		}
	}
	return keys
}

// problem explains why the setup is not certified, keeping it: empty when
// it is complete.
func (s *handWrittenSetup) problem() string {
	if s.complete() {
		return ""
	}
	return "This job sets up Test Optimization by hand (" + s.Reference + "); keep it, without adding a second setup. " + s.gaps()
}

// code classifies an incomplete setup's finding: a missing piece, or only
// what ddtest cannot verify statically.
func (s *handWrittenSetup) code() string {
	if s.Unvalidated != "" || len(s.Problems) == 0 {
		return CodeUnverifiedInitialization
	}
	return CodeIncompleteCISetup
}

// gaps lists what the setup lacks and what ddtest cannot verify about it.
func (s *handWrittenSetup) gaps() string {
	if s.Unvalidated == "" {
		return s.lacks()
	}
	return strings.TrimSpace(s.Unvalidated + " " + s.lacks())
}

// lacks lists what the setup lacks and what ddtest cannot verify about it,
// apart from Unvalidated; empty when nothing is missing.
func (s *handWrittenSetup) lacks() string {
	var text []string
	if len(s.Problems) > 0 {
		text = append(text, "It is incomplete: "+strings.Join(s.Problems, " "))
	}
	if len(s.Unresolved) > 0 {
		text = append(text, "ddtest cannot verify statically: "+strings.Join(s.Unresolved, " ")+" It stays unresolved until the configuration shows it.")
	}
	if len(s.pending) > 0 {
		text = append(text, "Only the user can confirm: "+strings.Join(s.pending, " "))
	}
	return strings.Join(text, " ")
}

func (s *handWrittenSetup) missing(problem string) {
	if !slices.Contains(s.Problems, problem) {
		s.Problems = append(s.Problems, problem)
	}
}

func (s *handWrittenSetup) unresolved(reason string) {
	if !slices.Contains(s.Unresolved, reason) {
		s.Unresolved = append(s.Unresolved, reason)
	}
}

// handWrittenEnvironment is the environment a step's commands start with:
// the workflow's, the job's and the step's variables, the step's last.
func handWrittenEnvironment(workflow ciWorkflow, job runtimeJob, step runtimeStep) map[string]string {
	env := map[string]string{}
	for _, layer := range []map[string]string{workflow.Env, job.Env, step.Env} {
		maps.Copy(env, layer)
	}
	return env
}

// handWrittenVariableReference is a value that reads another variable or a
// secret as a whole: $NAME, ${NAME} or ${{ secrets.NAME }}.
var handWrittenVariableReference = regexp.MustCompile(`^(?:\$[A-Za-z_][A-Za-z0-9_]*|\$\{[A-Za-z_][A-Za-z0-9_]*\}|\$\{\{\s*secrets\.[A-Za-z_][A-Za-z0-9_]*\s*\}\})$`)

// handWrittenReads classifies a variable's value as the configuration
// records it: a whole read of another variable or secret, a value ddtest
// cannot evaluate, or a literal.
func handWrittenReads(value string) (reference, dynamic bool) {
	value = strings.TrimSpace(value)
	if match := unresolvedCIMarker.FindStringSubmatch(value); match != nil {
		written, err := strconv.Unquote(match[1])
		return err == nil && handWrittenVariableReference.MatchString(strings.TrimSpace(written)), true
	}
	if handWrittenVariableReference.MatchString(value) {
		return true, false
	}
	return false, strings.ContainsAny(value, "$`") || strings.Contains(value, "${{")
}

// handWrittenTrue reads a boolean setting the way the tracers do: true or 1.
// known is false for a value ddtest cannot evaluate, such as a read of
// another variable ($TRACE_TESTS) only CI knows.
func handWrittenTrue(value string) (enabled, known bool) {
	if reference, dynamic := handWrittenReads(value); reference || dynamic {
		return false, false
	}
	value = strings.ToLower(strings.TrimSpace(value))
	return value == "true" || value == "1", true
}

// handWrittenFalse reports a boolean setting set to false or 0.
func handWrittenFalse(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return value == "false" || value == "0"
}

// handWrittenFrameworkDisabled records a setting in env that turns the
// tracer's integration for framework off, DD_TRACE_<FRAMEWORK>_ENABLED=false,
// or one ddtest cannot read.
func handWrittenFrameworkDisabled(setup *handWrittenSetup, env map[string]string, framework string) {
	key := "DD_TRACE_" + strings.ToUpper(framework) + "_ENABLED"
	value, set := env[key]
	if !set {
		return
	}
	if _, known := handWrittenTrue(value); !known {
		setup.unresolved(key + " comes from " + value + ", which ddtest cannot read.")
	} else if handWrittenFalse(value) {
		setup.missing(key + "=" + value + " turns the tracer's " + framework + " integration off for these tests: remove it.")
	}
}

// handWrittenConfiguration checks the Datadog configuration every
// language's hand-written setup shares, in the environment its test command
// starts with: Test Optimization not turned off (enabled requires it turned
// on, as datadog-ci does), agentless reporting, the API key from the
// provider's secret store and the selected site (empty: any). agentless
// reports agentless mode the framework's configuration enables.
func handWrittenConfiguration(setup *handWrittenSetup, provider ciProvider, job runtimeJob, env map[string]string, site string, enabled, agentless bool) {
	terms := provider.setupTerms()
	for _, key := range []string{"DD_TRACE_ENABLED", "DD_CIVISIBILITY_ENABLED"} {
		value, set := env[key]
		if !set {
			continue
		}
		if _, known := handWrittenTrue(value); !known {
			// CI may set it to false.
			setup.unresolved(key + " comes from " + value + ", which ddtest cannot read.")
		} else if handWrittenFalse(value) {
			setup.missing(key + "=" + value + " turns the tracer off for these tests: remove it.")
		}
	}
	if !enabled {
		value, set := env["DD_CIVISIBILITY_ENABLED"]
		switch on, known := handWrittenTrue(value); {
		case !set:
			setup.missing("Test Optimization stays off until DD_CIVISIBILITY_ENABLED is true: set DD_CIVISIBILITY_ENABLED=true in " + terms.Variables + ".")
		case !known:
			setup.unresolved("DD_CIVISIBILITY_ENABLED comes from " + value + ", which ddtest cannot read.")
		case !on && !handWrittenFalse(value):
			setup.missing("DD_CIVISIBILITY_ENABLED=" + value + " leaves Test Optimization off: set it to true.")
		}
	}
	if !agentless {
		for _, key := range []string{"DD_CIVISIBILITY_AGENTLESS_ENABLED", "DD_AGENTLESS_ENABLED"} {
			value, set := env[key]
			if !set {
				continue
			}
			on, known := handWrittenTrue(value)
			if !known {
				setup.unresolved(key + " comes from " + value + ", which ddtest cannot read.")
				return
			}
			agentless = agentless || on
		}
	}
	if !agentless {
		// The tracers report through a Datadog Agent then: one the job reaches
		// (DD_TRACE_AGENT_URL, DD_AGENT_HOST or localhost:8126), with its own
		// key and site, which no CI configuration shows.
		agent := "localhost:8126"
		for _, key := range []string{"DD_TRACE_AGENT_URL", "DD_AGENT_HOST"} {
			if value, set := env[key]; set {
				agent = key + "=" + value
			}
		}
		setup.Confirmations = append(setup.Confirmations, handWrittenAgent)
		if _, stated := statedFact(handWrittenAgent); !stated {
			setup.pending = append(setup.pending, "the tracer reports through a Datadog Agent ("+agent+") because DD_CIVISIBILITY_AGENTLESS_ENABLED is not true, and no CI configuration shows whether that Agent runs where the tests do, with an API key and the selected site; ddtest asks the user to confirm it (--ci-confirm "+handWrittenAgent+"). Keep reporting through the Agent unless the user decides otherwise.")
		}
		return
	}
	if job.KeyProblem != "" {
		setup.missing(job.KeyProblem)
	}
	bound := false
	for _, key := range []string{"DD_API_KEY", "DATADOG_API_KEY"} {
		value, set := env[key]
		if !set {
			continue
		}
		bound = true
		reference, dynamic := handWrittenReads(value)
		switch {
		case reference:
		case strings.TrimSpace(value) == "":
			setup.missing(key + " is set to an empty value in the configuration, which replaces the secret: remove it and store DD_API_KEY in " + terms.SecretStore + ".")
		case dynamic:
			setup.unresolved(key + " comes from " + value + ", which ddtest cannot tell is a secret.")
		default:
			setup.missing("The configuration writes " + key + " as a literal; never commit the key: remove it and store DD_API_KEY in " + terms.SecretStore + ".")
		}
	}
	if terms.KeyBinding && !bound {
		setup.missing("No DD_API_KEY reaches these tests, so the tracer reports nothing: the CI server gives a job a stored credential only where its configuration binds it. Use " + terms.SecretStore + ".")
	}
	configured, set := env["DD_SITE"]
	if !set {
		configured = defaultDatadogSite
	}
	if _, dynamic := handWrittenReads(configured); dynamic {
		setup.unresolved("DD_SITE comes from " + configured + ", which ddtest cannot read, so it cannot tell which Datadog site the tests report to.")
		return
	}
	if site != "" && strings.TrimSpace(configured) != site {
		reason := "The tracer reports to " + configured
		if !set {
			reason += " (the default, without DD_SITE)"
		}
		setup.missing(reason + ", but the selected Datadog site is " + site + ": set DD_SITE=" + site + " in " + terms.Variables + ".")
	}
}

// handWrittenCISetup recognizes the hand-written setup of language's tests
// that the step at index of job runs on row, where no provider setup
// precedes it; nil when the step's environment loads no tracer, or on a
// provider written in the shared workflow syntax, whose setup is always
// the Datadog action.
func handWrittenCISetup(root string, workflow ciWorkflow, job runtimeJob, index int, row map[string]any, language, framework, site string) *handWrittenSetup {
	provider, err := currentCIProvider(root)
	if err != nil || provider.sharedSyntax() {
		return nil
	}
	registered := discoveryLanguageNamed(language)
	if registered == nil {
		return nil
	}
	setup := registered.handWrittenSetup(root, provider, workflow, job, index, row, framework, site)
	if setup != nil && !registered.validatesHandWrittenSetup() {
		setup.Unvalidated = handWrittenUnvalidated(language)
	}
	return setup
}

// jobHandWrittenSetup is the hand-written setup of the job's test step at
// first (see handWrittenCISetup), checked against every later step of the
// job that runs framework's tests too: a setup the first step writes inline
// or in its own variables reaches no other step, so each one must load the
// tracer itself, with the same release, for the job to be certified. nil
// when the first step has none.
func jobHandWrittenSetup(root string, workflow ciWorkflow, job runtimeJob, first int, language, framework, site string) *handWrittenSetup {
	setup := handWrittenCISetup(root, workflow, job, first, nil, language, framework, site)
	if setup == nil {
		return nil
	}
	token := setupActionLanguage(language)
	for i := first + 1; i < len(job.Steps); i++ {
		step := job.Steps[i]
		label := "step " + strconv.Itoa(stepNumber(step, i))
		if step.Source != "" {
			label = step.Source
		}
		if current, ok := datadogSetup(step); ok {
			if selected, err := setupSelectsLanguage(current, nil, token); err != nil || selected {
				setup.unresolved("the job also runs a Datadog setup (" + label + ") after its first " + framework + " test step, and ddtest does not certify a job that mixes it with a hand-written setup.")
				return setup
			}
		}
		resolution := resolveTestStep(root, workflow, job, step, language, framework)
		if resolution.Review || !resolution.Matched {
			continue
		}
		later := handWrittenCISetup(root, workflow, job, i, nil, language, framework, site)
		if later == nil {
			setup.missing("The " + framework + " test command at " + label + " (" + step.Run + ") gets none of the hand-written setup, so its tests run untraced: give that command the same setup, or set it once for the job's later commands after its dependency install.")
			continue
		}
		if later.Tracer != "" && setup.Tracer != "" && later.Tracer != setup.Tracer {
			setup.missing("The " + framework + " test command at " + label + " loads tracer release " + later.Tracer + ", not " + setup.Tracer + " as the job's first test command does: load one exact release in every test command.")
		}
		for _, problem := range later.Problems {
			setup.missing(problem)
		}
		for _, reason := range later.Unresolved {
			setup.unresolved(reason)
		}
		for _, reason := range later.pending {
			if !slices.Contains(setup.pending, reason) {
				setup.pending = append(setup.pending, reason)
			}
		}
		for _, key := range later.Confirmations {
			if !slices.Contains(setup.Confirmations, key) {
				setup.Confirmations = append(setup.Confirmations, key)
			}
		}
	}
	return setup
}

// handWrittenLanguageName names language in messages, such as Python.
func handWrittenLanguageName(language string) string {
	if registered := discoveryLanguageNamed(language); registered != nil {
		return registered.displayName()
	}
	return language
}

// handWrittenUnvalidated explains why a hand-written setup of language is
// never certified, when ddtest does not validate that language's
// hand-written setups yet.
func handWrittenUnvalidated(language string) string {
	return "ddtest does not validate hand-written " + handWrittenLanguageName(language) + " setups yet: it recognizes this one and lists what it lacks, but never certifies it, and never replaces it with the provider's setup."
}

// validatesHandWrittenSetups reports whether ddtest validates language's
// hand-written setups on providers without the shared workflow syntax.
func validatesHandWrittenSetups(language string) bool {
	registered := discoveryLanguageNamed(language)
	return registered != nil && registered.validatesHandWrittenSetup()
}

// handWrittenValidatedLanguages names, in a sentence, the languages whose
// hand-written setups ddtest validates, such as "JavaScript and Ruby".
func handWrittenValidatedLanguages() string {
	var names []string
	for _, language := range discoveryLanguages {
		if language.validatesHandWrittenSetup() {
			names = append(names, language.displayName())
		}
	}
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// handWrittenSetupOption is the user's option, in a message about CI jobs
// the provider's setup (named setup) cannot instrument, of setting Test
// Optimization up by hand in those jobs, and whether ddtest then checks
// that setup for language.
func handWrittenSetupOption(language, setup string) string {
	if validatesHandWrittenSetups(language) {
		return "to set up Test Optimization for them by hand in the job itself (the tracer from the project's dependencies, loaded through the job's environment), which ddtest then checks like the " + setup
	}
	return "to set up Test Optimization for them by hand in the job itself, which ddtest does not validate for " + handWrittenLanguageName(language) + " yet"
}

// unvalidatedHandWrittenJobs lists the CI jobs that run framework's tests
// and set up Test Optimization by hand in a way ddtest recognizes but does
// not validate yet (see handWrittenSetup.Unvalidated), each with its setup
// and what it lacks; empty when there are none. Such jobs are never
// certified, and the provider's setup is never prescribed for them.
func unvalidatedHandWrittenJobs(root string, provider ciProvider, language, framework string) []string {
	if provider.sharedSyntax() || validatesHandWrittenSetups(language) {
		return nil
	}
	workflows, err := readProviderWorkflows(provider, root)
	if err != nil {
		return nil
	}
	var jobs []string
	for _, workflow := range workflows {
		for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			job := workflow.Jobs[name]
			if disabledCondition(job.If) {
				continue
			}
			setup := firstHandWrittenSetup(root, workflow, job, language, framework, "")
			if setup == nil || setup.Unvalidated == "" {
				continue
			}
			entry := workflow.Path + " / " + name + " (" + setup.Reference + ")"
			if lacks := setup.lacks(); lacks != "" {
				entry += ": " + lacks
			}
			jobs = append(jobs, entry)
		}
	}
	return jobs
}

// handWrittenSetupNote lists, for setup instructions, the CI jobs running
// framework's tests that already set up Test Optimization by hand, and what
// each still needs, so a person or coding agent keeps them; empty when
// there are none.
func handWrittenSetupNote(root, language, framework, site string) string {
	lines, _ := handWrittenSetups(root, language, framework, site)
	if len(lines) == 0 {
		return ""
	}
	return "These CI jobs already set up Test Optimization by hand. Keep that setup and do not add the " + activeOrDefaultCIProvider().setupName() + " to them; make only the changes listed for each, in the job's own setup:\n" + strings.Join(lines, "\n")
}

// handWrittenSetups describes, one line per CI job that runs framework's
// tests and sets up Test Optimization by hand, that setup and what it still
// needs; every reports that each job running those tests, at least one, does.
func handWrittenSetups(root, language, framework, site string) (lines []string, every bool) {
	provider, err := currentCIProvider(root)
	if err != nil || provider.sharedSyntax() {
		return nil, false
	}
	workflows, err := readProviderWorkflows(provider, root)
	if err != nil {
		return nil, false
	}
	every = true
	for _, workflow := range workflows {
		for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			job := workflow.Jobs[name]
			if disabledCondition(job.If) || !slices.ContainsFunc(job.Steps, func(step runtimeStep) bool {
				return resolveTestStep(root, workflow, job, step, language, framework).Matched
			}) {
				continue
			}
			setup := firstHandWrittenSetup(root, workflow, job, language, framework, site)
			if setup == nil {
				every = false
				continue
			}
			status := "complete; ddtest validates it as it is."
			if !setup.complete() {
				status = setup.gaps()
			}
			lines = append(lines, workflow.Path+" / "+name+" ("+setup.Reference+"): "+status)
		}
	}
	return lines, every && len(lines) > 0
}

// firstHandWrittenSetup is the hand-written setup before the first of
// framework's test commands in job, when no provider setup selecting
// language precedes it; nil otherwise.
func firstHandWrittenSetup(root string, workflow ciWorkflow, job runtimeJob, language, framework, site string) *handWrittenSetup {
	token := setupActionLanguage(language)
	for i, step := range job.Steps {
		if current, ok := datadogSetup(step); ok {
			if selected, err := setupSelectsLanguage(current, nil, token); err == nil && selected {
				return nil
			}
		}
		resolution := resolveTestStep(root, workflow, job, step, language, framework)
		if resolution.Review || !resolution.Matched {
			continue
		}
		return jobHandWrittenSetup(root, workflow, job, i, language, framework, site)
	}
	return nil
}

// handWrittenAssignment is a shell word that assigns a variable for its
// command, such as NODE_ENV=production.
var handWrittenAssignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// handWrittenCommand splits a static command's words (see staticCommands)
// into the variables it assigns for itself and the command they prefix.
func handWrittenCommand(words []string) (assigned map[string]string, command []string) {
	assigned = map[string]string{}
	for len(words) > 0 && handWrittenAssignment.MatchString(words[0]) {
		name, value, _ := strings.Cut(words[0], "=")
		assigned[name] = value
		words = words[1:]
	}
	return assigned, words
}

// handWrittenPreloadBeforeInstall records a preload in variable (loaded
// reads its value) that reaches the job's first dependency install
// (install) before the test step at index. The tracer is not installed yet
// there, so on a fresh checkout the install's own runtime fails to load the
// preload, and the job stops before any test runs. tracer names it.
func handWrittenPreloadBeforeInstall(setup *handWrittenSetup, workflow ciWorkflow, job runtimeJob, index int, variable, tracer string, install func(words []string) bool, loaded func(value string) bool) {
	for i, step := range job.Steps[:index] {
		commands, err := staticCommands(step.Run)
		if err != nil {
			continue
		}
		for _, words := range commands {
			assigned, command := handWrittenCommand(words)
			if !install(command) {
				continue
			}
			env := mergeEnvironment(handWrittenEnvironment(workflow, job, step), assigned)
			if value, set := env[variable]; set && loaded(value) {
				where := step.Source
				if where == "" {
					where = "step " + strconv.Itoa(stepNumber(step, i))
				}
				setup.missing(variable + " preloads " + tracer + " for " + strings.Join(command, " ") + " at " + where + " too, which runs before " + tracer + " is installed: on a fresh checkout the install cannot load the preload and fails, so no test runs. Set " + variable + " for the test commands only (inline on each, or for the commands after the install).")
			}
			return
		}
	}
}
