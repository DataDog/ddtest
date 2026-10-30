// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
)

// User statements fill what a CI configuration cannot show ddtest: the runtime
// of a job without a runtime image or installer (an agent host, a GitLab job
// without an image, a CircleCI machine executor, a self-hosted runner), and
// facts a provider defines, such as a plugin installed on the CI server. They
// never override what ddtest read: a statement that contradicts the
// configuration is an error, and every statement used is reported in
// final_response as stated by the user, not verified.

// CIRuntimeStatement is one --ci-runtime [<job>:]<runtime>=<version>.
type CIRuntimeStatement struct {
	// Job is the job key ddtest prints (workflow / job, or the job name
	// alone); empty applies to every job whose runtime is unknown.
	Job string
	// Runtime is the language's installer token: js or ruby.
	Runtime string
	// Version is exact or a major/minor selector, compared like an image tag.
	Version string
}

// String is the statement as --ci-runtime takes it.
func (s CIRuntimeStatement) String() string {
	value := statedRuntimeFlagName(s.Runtime) + "=" + s.Version
	if s.Job != "" {
		value = s.Job + ":" + value
	}
	return value
}

// CIFact is one --ci-confirm <fact>[=<value>].
type CIFact struct{ Key, Value string }

// String is the fact as --ci-confirm takes it.
func (f CIFact) String() string {
	if f.Value == "" {
		return f.Key
	}
	return f.Key + "=" + f.Value
}

// ciFactDefinition is a fact a provider asks the user to confirm because its
// configuration cannot show it, such as a plugin installed on the CI server.
type ciFactDefinition struct {
	// Key is the --ci-confirm name.
	Key string
	// Values describes the value the fact takes, such as agentless:<site>|agent;
	// empty for a fact confirmed without a value.
	Values string
	// valid checks a value; nil accepts any non-empty value.
	valid func(string) bool
	// Statement describes the confirmed fact in final_response.
	Statement func(value string) string
}

// ciProviderFacts lists the facts each provider (by --ci-provider name) may
// ask the user to confirm. None does yet.
var ciProviderFacts = map[string][]ciFactDefinition{}

// statedRuntimeSource is the NodeSource of a runtime the user stated.
const statedRuntimeSource = "stated by the user (--ci-runtime)"

// statedRuntime is how a language takes a user's statement of a CI job's
// runtime (--ci-runtime): how it is named and written, and where the
// language reads runtimes from CI.
type statedRuntime struct {
	// Names are the --ci-runtime runtime names, the first canonical, such
	// as node.
	Names []string
	// Display names the runtime in output, such as Node.
	Display string
	// Version matches a stated version, and Example shows valid ones.
	Version *regexp.Regexp
	Example string
	// base is the version a job's image selects, or "" (also for an image
	// of an engine the tracer cannot instrument: it is not unknown).
	base func(job runtimeJob) string
	// configured is the first version a job's steps select statically,
	// such as through a runtime installer, and the step's description.
	configured func(root string, job runtimeJob) (version, source string)
	// check inspects the language's CI findings offline; finding.RuntimeHost
	// marks a runtime only the user can state.
	check func(ctx context.Context, root, framework string) RuntimeCheck
	// runtime is the version a finding records.
	runtime func(finding RuntimeFinding) string
}

// statedRuntimeLanguages lists the languages that take runtime statements.
func statedRuntimeLanguages() []discoveryLanguage {
	var languages []discoveryLanguage
	for _, language := range discoveryLanguages {
		if language.statedRuntime() != nil {
			languages = append(languages, language)
		}
	}
	return languages
}

// statedRuntimeFor returns the statement rules of the language with the
// installer token, or nil.
func statedRuntimeFor(token string) *statedRuntime {
	for _, language := range statedRuntimeLanguages() {
		if language.actionLanguage() == token {
			return language.statedRuntime()
		}
	}
	return nil
}

// ParseCIRuntime reads one --ci-runtime value. The job part may contain
// colons: the runtime name follows the last one.
func ParseCIRuntime(value string) (CIRuntimeStatement, error) {
	var names, examples []string
	for _, language := range statedRuntimeLanguages() {
		names = append(names, language.statedRuntime().Names[0])
		examples = append(examples, language.statedRuntime().Names[0]+"="+strings.Fields(language.statedRuntime().Example)[0])
	}
	index := strings.LastIndex(value, "=")
	if index < 0 || strings.TrimSpace(value[index+1:]) == "" {
		return CIRuntimeStatement{}, fmt.Errorf("--ci-runtime %q must be [<job>:]<runtime>=<version>, such as %s", value, strings.Join(examples, " or "))
	}
	left, version := value[:index], strings.TrimPrefix(strings.TrimSpace(value[index+1:]), "v")
	job, name, scoped := "", left, false
	if colon := strings.LastIndex(left, ":"); colon >= 0 {
		job, name, scoped = strings.TrimSpace(left[:colon]), left[colon+1:], true
	}
	name = strings.ToLower(strings.TrimSpace(name))
	var language discoveryLanguage
	for _, candidate := range statedRuntimeLanguages() {
		if slices.Contains(candidate.statedRuntime().Names, name) {
			language = candidate
		}
	}
	switch {
	case language == nil:
		return CIRuntimeStatement{}, fmt.Errorf("--ci-runtime %q names runtime %q; supported runtimes: %s", value, name, strings.Join(names, ", "))
	case scoped && job == "":
		return CIRuntimeStatement{}, fmt.Errorf("--ci-runtime %q names an empty job", value)
	case !language.statedRuntime().Version.MatchString(version):
		return CIRuntimeStatement{}, fmt.Errorf("--ci-runtime %q: the %s version must be numeric, such as %s", value, language.statedRuntime().Display, language.statedRuntime().Example)
	}
	return CIRuntimeStatement{Job: job, Runtime: language.actionLanguage(), Version: version}, nil
}

// ParseCIConfirm reads one --ci-confirm value; UseCIStatements checks it
// against the selected provider's facts.
func ParseCIConfirm(value string) (CIFact, error) {
	key, fact, _ := strings.Cut(strings.TrimSpace(value), "=")
	if key == "" {
		return CIFact{}, fmt.Errorf("--ci-confirm %q must be <fact>[=<value>]", value)
	}
	return CIFact{Key: key, Value: fact}, nil
}

type ciStatements struct {
	runtimes []CIRuntimeStatement
	facts    []CIFact
}

var activeStatements atomic.Value

// UseCIStatements records the user's statements for the rest of the command,
// after UseCIProvider: facts must be ones the selected provider defines.
func UseCIStatements(runtimes []CIRuntimeStatement, facts []CIFact) error {
	activeStatements.Store(ciStatements{})
	provider := activeOrDefaultCIProvider()
	definitions := ciProviderFacts[provider.name()]
	seen := map[string]bool{}
	for _, fact := range facts {
		index := slices.IndexFunc(definitions, func(definition ciFactDefinition) bool { return definition.Key == fact.Key })
		if index < 0 {
			if len(definitions) == 0 {
				return fmt.Errorf("--ci-confirm %s: %s defines no facts to confirm", fact.Key, provider.displayName())
			}
			var keys []string
			for _, definition := range definitions {
				keys = append(keys, definition.Key)
			}
			return fmt.Errorf("--ci-confirm %s is not a %s fact; facts: %s", fact.Key, provider.displayName(), strings.Join(keys, ", "))
		}
		definition := definitions[index]
		switch {
		case seen[fact.Key]:
			return fmt.Errorf("--ci-confirm %s is given more than once", fact.Key)
		case definition.Values == "" && fact.Value != "":
			return fmt.Errorf("--ci-confirm %s takes no value", fact.Key)
		case definition.Values != "" && (fact.Value == "" || definition.valid != nil && !definition.valid(fact.Value)):
			return fmt.Errorf("--ci-confirm %s needs a value: %s=%s", fact.Key, fact.Key, definition.Values)
		}
		seen[fact.Key] = true
	}
	for i, statement := range runtimes {
		for _, other := range runtimes[:i] {
			if other.Job == statement.Job && other.Runtime == statement.Runtime {
				return fmt.Errorf("--ci-runtime %s and %s state the same runtime twice", other, statement)
			}
		}
	}
	activeStatements.Store(ciStatements{runtimes: slices.Clone(runtimes), facts: slices.Clone(facts)})
	return nil
}

func currentStatements() ciStatements {
	statements, _ := activeStatements.Load().(ciStatements)
	return statements
}

// CIStatementArguments are the --ci-runtime and --ci-confirm arguments that
// follow-up commands repeat, in the order given.
func CIStatementArguments() []string {
	statements := currentStatements()
	var arguments []string
	for _, statement := range statements.runtimes {
		arguments = append(arguments, "--ci-runtime", statement.String())
	}
	for _, fact := range statements.facts {
		arguments = append(arguments, "--ci-confirm", fact.String())
	}
	return arguments
}

// CIStatementsFingerprint is the statements' part of an input fingerprint;
// empty without statements, so fingerprints without them are unchanged.
func CIStatementsFingerprint() string {
	arguments := CIStatementArguments()
	if len(arguments) == 0 {
		return ""
	}
	return strings.Join(arguments, "\x00")
}

func statedRuntimeFlagName(token string) string {
	if runtime := statedRuntimeFor(token); runtime != nil {
		return runtime.Names[0]
	}
	return token
}

func statedRuntimeDisplay(token string) string {
	if runtime := statedRuntimeFor(token); runtime != nil {
		return runtime.Display
	}
	return token
}

// jobMatches reports whether a statement's job names the job: its full key or
// its name alone.
func (s CIRuntimeStatement) jobMatches(workflow, job string) bool {
	return s.Job == "" || s.Job == job || s.Job == workflow+" / "+job
}

// fillStatedRuntimes records, on each job whose image selects no runtime,
// the version the user stated for it: a statement for the job wins over one
// for every job.
func fillStatedRuntimes(workflows []ciWorkflow) {
	statements := currentStatements().runtimes
	if len(statements) == 0 {
		return
	}
	for _, workflow := range workflows {
		for name, job := range workflow.Jobs {
			job.Stated = nil
			for _, language := range statedRuntimeLanguages() {
				token, runtime := language.actionLanguage(), language.statedRuntime()
				if runtime.base(job) != "" || runtimeHost(job, runtime.Display) == "" {
					continue
				}
				version := ""
				for _, statement := range statements {
					if statement.Runtime == token && statement.jobMatches(workflow.Path, name) && (version == "" || statement.Job != "") {
						version = statement.Version
					}
				}
				if version != "" {
					if job.Stated == nil {
						job.Stated = map[string]string{}
					}
					job.Stated[token] = version
				}
			}
			workflow.Jobs[name] = job
		}
	}
}

// readProviderWorkflows reads a provider's configuration with the user's
// runtime statements recorded on its jobs.
func readProviderWorkflows(provider ciProvider, root string) ([]ciWorkflow, error) {
	workflows, err := provider.readWorkflows(root)
	if err == nil {
		fillStatedRuntimes(workflows)
	}
	return workflows, err
}

// runtimeHost describes where a job whose configuration selects no runtime
// runs, for a person who knows that host's runtime; empty when ddtest does not
// take the user's statement for it: a GitHub-hosted runner or job container,
// whose runtime GitHub defines, and an image ddtest cannot resolve keep their
// own findings.
func runtimeHost(job runtimeJob, runtime string) string {
	if activeOrDefaultCIProvider().sharedSyntax() {
		var labels []string
		switch runsOn := job.RunsOn.(type) {
		case string:
			labels = []string{runsOn}
		case []any:
			for _, label := range runsOn {
				labels = append(labels, fmt.Sprint(label))
			}
		}
		if job.Container != nil || !slices.ContainsFunc(labels, func(label string) bool { return strings.EqualFold(label, "self-hosted") }) {
			return ""
		}
		return "the self-hosted runner " + strings.Join(labels, ", ")
	}
	switch {
	case job.ImageProblem != "":
		// Its image is in the configuration: the remedy is to make it readable.
		return ""
	case job.Image != "":
		return "image " + job.Image + ", whose name selects no " + runtime + " version"
	}
	return "the " + activeOrDefaultCIProvider().displayName() + " " + agentHost
}

// agentHost ends the runtimeHost of a job without an image: its commands run
// on the CI agent's machine, or in the runner's default environment.
const agentHost = "runner or agent environment, with no image in the configuration"

// CheckCIRuntimeStatements rejects a statement for a named job whose runtime
// the configuration already selects with a different version. A statement
// for every job never conflicts: it fills only unknown runtimes.
func CheckCIRuntimeStatements(root string) error {
	var scoped []CIRuntimeStatement
	for _, statement := range currentStatements().runtimes {
		if statement.Job != "" {
			scoped = append(scoped, statement)
		}
	}
	if len(scoped) == 0 {
		return nil
	}
	provider, err := currentCIProvider(root)
	if err != nil {
		return err
	}
	workflows, err := provider.readWorkflows(root)
	if err != nil {
		return nil // Reading reports the configuration error itself.
	}
	var conflicts []string
	for _, statement := range scoped {
		for _, workflow := range workflows {
			for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
				if !statement.jobMatches(workflow.Path, name) {
					continue
				}
				runtime := statedRuntimeFor(statement.Runtime)
				version, source := runtime.base(workflow.Jobs[name]), "image "+workflow.Jobs[name].Image
				if version == "" {
					version, source = runtime.configured(root, workflow.Jobs[name])
				}
				if version != "" && !sameVersionLine(version, statement.Version) {
					conflicts = append(conflicts, fmt.Sprintf("--ci-runtime %s contradicts %s %s, which %s selects in %s / %s", statement, statedRuntimeDisplay(statement.Runtime), version, source, workflow.Path, name))
				}
			}
		}
	}
	if len(conflicts) > 0 {
		return &CIRuntimeConflict{Conflicts: conflicts}
	}
	return nil
}

// CIRuntimeConflict is a statement that contradicts the CI configuration.
type CIRuntimeConflict struct{ Conflicts []string }

func (e *CIRuntimeConflict) Error() string {
	return strings.Join(e.Conflicts, "; ") + ". A statement only fills a runtime ddtest cannot read; fix the CI configuration or the statement, then rerun."
}

// sameVersionLine reports whether two versions agree on every component both
// name, as an image tag's major selects its releases: 22 and 22.11.0 agree.
func sameVersionLine(a, b string) bool {
	left, right := strings.Split(strings.TrimPrefix(a, "v"), "."), strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := range min(len(left), len(right)) {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

// CIRuntimeRequired lists the CI jobs whose runtime only the user can tell.
type CIRuntimeRequired struct {
	// Runtime names it, such as Node.
	Runtime string
	// Flag is the --ci-runtime runtime name, such as node.
	Flag string
	// Jobs are the job keys (workflow / job), and Hosts where each runs.
	Jobs, Hosts []string
}

func (e *CIRuntimeRequired) Error() string {
	var jobs []string
	for i, job := range e.Jobs {
		jobs = append(jobs, job+" ("+e.Hosts[i]+")")
	}
	return "ddtest cannot read which " + e.Runtime + " version these CI jobs run: their configuration selects none (no runtime image or installer step): " + strings.Join(jobs, "; ") + "."
}

// CIRuntimeRequiredResult asks the user for the runtime of those jobs.
func CIRuntimeRequiredResult(err *CIRuntimeRequired) AgentResult {
	var required []string
	for _, job := range err.Jobs {
		required = append(required, "ci_runtime:"+job)
	}
	example := "'" + err.Jobs[0] + ":" + err.Flag + "=<version>'"
	return AgentResult{
		Status: "needs_input", Phase: "discovery", Code: "ci_runtime_required", NextAction: "ask_user",
		Reason: err.Error(), Location: strings.Join(err.Jobs, "; "), RequiredInput: required,
		Instruction: "Stop and ask the user which " + err.Runtime + " version each listed CI job runs (for example `" + strings.ToLower(err.Runtime) + " --version` in that job); run no other ddtest command until they answer. Never guess it, take it from this machine or another job, or edit CI to pin one without the user's request. After their answer, rerun the same command adding --ci-runtime " + err.Flag + "=<version> when every listed job runs that version, or one --ci-runtime " + example + " per job. ddtest checks the stated version like a CI image's and reports it as stated by the user, not verified.",
	}
}

// RequiredCIRuntimes returns the instrumented CI jobs of language whose
// runtime ddtest cannot read and the user has not stated, or nil. It reads
// the configuration offline.
func RequiredCIRuntimes(ctx context.Context, root, language, framework string) *CIRuntimeRequired {
	registered := discoveryLanguageNamed(language)
	if registered == nil || registered.statedRuntime() == nil {
		return nil
	}
	token, stated := registered.actionLanguage(), registered.statedRuntime()
	check := stated.check(ctx, root, framework)
	required := &CIRuntimeRequired{Runtime: stated.Display, Flag: statedRuntimeFlagName(token)}
	for _, finding := range check.Jobs {
		runtime := stated.runtime(finding)
		if finding.RuntimeHost == "" || runtime != "" || finding.Status == StatusExcluded || slices.Contains([]string{CodeExcludedRuntime, CodeUnsupportedNode, CodeUnresolvedCommand}, finding.Code) {
			continue
		}
		key := finding.Workflow + " / " + finding.Job
		if !slices.Contains(required.Jobs, key) {
			required.Jobs = append(required.Jobs, key)
			required.Hosts = append(required.Hosts, finding.RuntimeHost)
		}
	}
	if len(required.Jobs) == 0 {
		return nil
	}
	return required
}

// StatedFacts lists, for final_response, what the user stated and ddtest
// used without verifying it, and the statements that matched nothing: the
// runtimes check's findings name the jobs that ran on a stated runtime.
func StatedFacts(check *RuntimeCheck, language string) (stated, unused []string) {
	token := setupActionLanguage(language)
	runtime := statedRuntimeFor(token)
	statements := currentStatements()
	hostSetup := !activeOrDefaultCIProvider().sharedSyntax()
	used := map[string]bool{}
	type group struct {
		version string
		jobs    []string
		hosts   bool
	}
	var groups []*group
	if check != nil && runtime != nil {
		for _, finding := range check.Jobs {
			if !finding.UserStated {
				continue
			}
			version := runtime.runtime(finding)
			key := finding.Workflow + " / " + finding.Job
			for _, statement := range statements.runtimes {
				if statement.Runtime == token && statement.Version == version && statement.jobMatches(finding.Workflow, finding.Job) {
					used[statement.String()] = true
				}
			}
			index := slices.IndexFunc(groups, func(g *group) bool { return g.version == version })
			if index < 0 {
				groups = append(groups, &group{version: version})
				index = len(groups) - 1
			}
			if !slices.Contains(groups[index].jobs, key) {
				groups[index].jobs = append(groups[index].jobs, key)
			}
			groups[index].hosts = groups[index].hosts || strings.HasSuffix(finding.RuntimeHost, agentHost)
		}
	}
	for _, g := range groups {
		line := runtime.Display + " " + g.version + " for " + strings.Join(g.jobs, ", ") + " (--ci-runtime). ddtest checked the tracer against it as it checks a CI image's version."
		if g.hosts && hostSetup {
			line += " These jobs run in a runner or agent environment the configuration does not describe, so the Bash and curl or wget the Datadog setup script needs there are not checked either."
		}
		stated = append(stated, line)
	}
	for _, statement := range statements.runtimes {
		if !used[statement.String()] {
			unused = append(unused, "--ci-runtime "+statement.String()+" was not used: no validated "+statedRuntimeDisplay(statement.Runtime)+" CI job it names has a runtime ddtest could not read.")
		}
	}
	definitions := ciProviderFacts[activeOrDefaultCIProvider().name()]
	for _, fact := range statements.facts {
		line := fact.String()
		if index := slices.IndexFunc(definitions, func(definition ciFactDefinition) bool { return definition.Key == fact.Key }); index >= 0 && definitions[index].Statement != nil {
			line = definitions[index].Statement(fact.Value)
		}
		stated = append(stated, line+" (--ci-confirm)")
	}
	return stated, unused
}

// withImplicitTag writes an image reference without a tag or digest with the
// latest tag a registry pulls for it.
func withImplicitTag(image string) string {
	image = strings.TrimSpace(image)
	if image == "" || strings.Contains(image, "@") || strings.Contains(image[strings.LastIndex(image, "/")+1:], ":") {
		return image
	}
	return image + ":latest"
}
