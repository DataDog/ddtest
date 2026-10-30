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
	"strconv"
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
	// Question asks the user for the fact, and Remedy says what to do when
	// it does not hold.
	Question, Remedy string
}

// ciProviderFacts lists the facts each provider (by --ci-provider name) may
// ask the user to confirm.
var ciProviderFacts = map[string][]ciFactDefinition{jenkinsProvider: jenkinsFacts}

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
	// compare reports whether a version configured (by source) agrees with
	// a stated one, and false comparable when ddtest cannot evaluate the
	// configured selector; nil compares them like image tags
	// (sameVersionLine).
	compare func(configured, source, stated string) (agree, comparable bool)
	// configured is the version a job's steps select statically by its
	// first test step, such as through a runtime installer, and the step's
	// description; "" when a matrix row the job runs on keeps the host's
	// runtime (see installedRuntime and stepsThroughFirstTest).
	configured func(root string, workflow ciWorkflow, job runtimeJob) (version, source string)
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
	// uncompared names, by statement, the configured selectors
	// CheckCIRuntimeStatements could not compare with it.
	uncompared map[string][]string
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
	// The same statement twice is one statement; two versions for the same
	// key are an error, never the last one silently.
	var unique []CIRuntimeStatement
	for _, statement := range runtimes {
		index := slices.IndexFunc(unique, func(other CIRuntimeStatement) bool {
			return other.Job == statement.Job && other.Runtime == statement.Runtime
		})
		switch {
		case index < 0:
			unique = append(unique, statement)
		case unique[index].Version != statement.Version:
			return fmt.Errorf("--ci-runtime %s and %s state different versions for the same job", unique[index], statement)
		}
	}
	activeStatements.Store(ciStatements{runtimes: unique, facts: slices.Clone(facts)})
	return nil
}

func currentStatements() ciStatements {
	statements, _ := activeStatements.Load().(ciStatements)
	return statements
}

// statedFact is the value of a fact the user confirmed, and whether they did.
func statedFact(key string) (string, bool) {
	for _, fact := range currentStatements().facts {
		if fact.Key == key {
			return fact.Value, true
		}
	}
	return "", false
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

// fillStatedRuntimes records, on each job whose image selects no runtime and
// that runs on a host the user can describe on some matrix row, the version
// the user stated for it: a statement for the job wins over one for every
// job. The checks apply it only on those rows (runtimeHost).
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
				if runtime.base(job) != "" || jobRuntimeHost(job, runtime.Display) == "" {
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
// runs on a matrix row (nil: the job without a row), for a person who knows
// that host's runtime; empty when ddtest does not take the user's statement
// for it: a GitHub-hosted runner or job container, whose runtime GitHub
// defines, and an image ddtest cannot resolve keep their own findings.
func runtimeHost(job runtimeJob, row map[string]any, runtime string) string {
	if activeOrDefaultCIProvider().sharedSyntax() {
		// A runner from the matrix is the row's; one ddtest cannot resolve is
		// taken as written, as GitHub-hosted unless a label says otherwise.
		labels, resolved := rowRunnerLabels(job, row)
		if !resolved {
			labels = runnerLabels(job.RunsOn)
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
	case job.Host != "":
		return job.Host
	}
	return "the " + activeOrDefaultCIProvider().displayName() + " " + agentHost
}

// jobRuntimeHost is the runtimeHost of the job's first matrix row that has
// one: a statement for the job fills only those rows (see runtimeHost), and a
// matrix ddtest cannot expand keeps the job's runner as written.
func jobRuntimeHost(job runtimeJob, runtime string) string {
	rows, _, err := jobMatrix(job)
	if err != nil || len(rows) == 0 {
		return runtimeHost(job, nil, runtime)
	}
	for _, row := range rows {
		if host := runtimeHost(job, row, runtime); host != "" {
			return host
		}
	}
	return ""
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
	// Different keys can name the same job (workflow / job, or the job name
	// alone): their versions must agree.
	for _, workflow := range workflows {
		for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			for i, statement := range scoped {
				for _, other := range scoped[:i] {
					if other.Runtime == statement.Runtime && other.Version != statement.Version && other.jobMatches(workflow.Path, name) && statement.jobMatches(workflow.Path, name) {
						return &CIRuntimeAmbiguity{fmt.Errorf("--ci-runtime %s and %s state different versions for the same job, %s / %s", other, statement, workflow.Path, name)}
					}
				}
			}
		}
	}
	var conflicts []string
	uncompared := map[string][]string{}
	for _, statement := range scoped {
		for _, workflow := range workflows {
			for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
				if !statement.jobMatches(workflow.Path, name) {
					continue
				}
				runtime := statedRuntimeFor(statement.Runtime)
				version, source := runtime.base(workflow.Jobs[name]), "image "+workflow.Jobs[name].Image
				if version == "" {
					version, source = runtime.configured(root, workflow, workflow.Jobs[name])
				}
				if version == "" {
					continue
				}
				agree, comparable := sameVersionLine(version, statement.Version), true
				if runtime.compare != nil {
					agree, comparable = runtime.compare(version, source, statement.Version)
				}
				switch {
				case !comparable:
					// Not a conflict ddtest can show: the statement is
					// accepted, and reported unused with this selector.
					uncompared[statement.String()] = append(uncompared[statement.String()], fmt.Sprintf("%s selects %s %s in %s / %s", source, statedRuntimeDisplay(statement.Runtime), version, workflow.Path, name))
				case !agree:
					conflicts = append(conflicts, fmt.Sprintf("--ci-runtime %s contradicts %s %s, which %s selects in %s / %s", statement, statedRuntimeDisplay(statement.Runtime), version, source, workflow.Path, name))
				}
			}
		}
	}
	if len(uncompared) > 0 {
		statements := currentStatements()
		statements.uncompared = uncompared
		activeStatements.Store(statements)
	}
	if len(conflicts) > 0 {
		return &CIRuntimeConflict{Conflicts: conflicts}
	}
	return nil
}

// installedRuntime is the version a job's steps select on every matrix row
// the job runs on, as rowRuntime reads one row with the conditions the
// language's runtime check evaluates (active is false for a row the job skips).
// It is "" when a row selects none, such as one whose installer a condition
// skips, or when rows select different versions: that job runs on its host's
// runtime, or a statement for it changes nothing, so neither is a conflict.
func installedRuntime(rows []map[string]any, err error, rowRuntime func(row map[string]any) (version, source string, active bool)) (string, string) {
	if err != nil {
		return "", ""
	}
	version, source := "", ""
	for _, row := range rows {
		rowVersion, rowSource, active := rowRuntime(row)
		switch {
		case !active:
			continue
		case rowVersion == "" || version != "" && rowVersion != version:
			return "", ""
		case version == "":
			version, source = rowVersion, rowSource
		}
	}
	return version, source
}

// stepsThroughFirstTest are a row's steps up to and including its first test
// step, as isTest identifies them: the runtime those tests start on is the
// one selected by then, and an installer after them selects nothing they run
// on. It is every step when isTest identifies none, so a job whose tests
// ddtest cannot recognize keeps every installer.
func stepsThroughFirstTest(steps []runtimeStep, isTest func(runtimeStep) bool) []runtimeStep {
	for i, step := range steps {
		if isTest(step) {
			return steps[:i+1]
		}
	}
	return steps
}

// CIRuntimeAmbiguity is two statements that give one job different
// versions: an invalid option, not a conflict with the configuration.
type CIRuntimeAmbiguity struct{ error }

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

// nodeSelector reads a setup-node version selector in the single-term forms
// ddtest evaluates: a version whose components may be wildcards (x, X or *),
// after an optional ^, ~, ~> or = and a v, such as 20.x, ^20, ~20.18,
// =20.18.0 or v20.
var nodeSelector = regexp.MustCompile(`^(\^|~>?|=)?[v=]*([0-9]+|[xX*])(?:\.([0-9]+|[xX*]))?(?:\.([0-9]+|[xX*]))?$`)

// nodeSelectorAgrees reports whether a setup-node selector, which follows
// node-semver, can select a release of a stated version's line (22 is any
// 22.x.y). comparable is false for a selector it cannot evaluate: a range
// (>=18 <21, 18 || 20), an alias (lts/*, node, latest, current) or one that
// takes the latest release (*, x).
func nodeSelectorAgrees(selector, stated string) (agree, comparable bool) {
	match := nodeSelector.FindStringSubmatch(strings.TrimSpace(selector))
	if match == nil {
		return false, false
	}
	// A wildcard component makes those after it wildcards too.
	var named []int
	for _, component := range match[2:] {
		value, err := strconv.Atoi(component)
		if err != nil {
			break
		}
		named = append(named, value)
	}
	if len(named) == 0 {
		return false, false
	}
	lower, upper := versionRange(named)
	switch match[1] {
	case "~", "~>":
		// A tilde allows patch releases, or minor ones when only the major
		// is named.
		_, upper = versionRange(named[:min(len(named), 2)])
	case "^":
		// A caret allows changes right of the first non-zero component.
		position := slices.IndexFunc(named, func(value int) bool { return value != 0 })
		if position < 0 {
			position = len(named) - 1
		}
		_, upper = versionRange(named[:position+1])
	}
	var line []int
	for _, component := range strings.Split(stated, ".") {
		value, err := strconv.Atoi(component)
		if err != nil {
			return false, false
		}
		line = append(line, value)
	}
	statedLower, statedUpper := versionRange(line)
	return slices.Compare(statedLower, upper) < 0 && slices.Compare(lower, statedUpper) < 0, true
}

// versionRange is the half-open range of releases a version's named
// components select: [20.18.0, 20.19.0) for 20.18.
func versionRange(named []int) (lower, upper []int) {
	lower, upper = make([]int, 3), make([]int, 3)
	copy(lower, named)
	copy(upper, named)
	upper[len(named)-1]++
	return lower, upper
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
	return "ddtest cannot read which " + e.Runtime + " version these CI jobs run: their configuration selects none (no runtime image, or installer step that selects a version): " + strings.Join(jobs, "; ") + "."
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

// CIConfirmationRequired lists the provider facts the user must confirm
// before ddtest certifies the setup of these CI jobs.
type CIConfirmationRequired struct {
	// Provider names the CI provider.
	Provider string
	// Facts are the facts to confirm, in the provider's order.
	Facts []ciFactDefinition
	// Jobs are the job keys (workflow / job) whose setup depends on them.
	Jobs []string
}

func (e *CIConfirmationRequired) Error() string {
	var keys []string
	for _, fact := range e.Facts {
		keys = append(keys, fact.Key)
	}
	return "The " + e.Provider + " setup of these CI jobs depends on facts no CI configuration shows, so ddtest cannot certify it until the user confirms them (" + strings.Join(keys, ", ") + "): " + strings.Join(e.Jobs, "; ") + "."
}

// CIConfirmationRequiredResult asks the user to confirm those facts.
func CIConfirmationRequiredResult(err *CIConfirmationRequired) AgentResult {
	var required, questions, flags []string
	for _, fact := range err.Facts {
		flag := fact.Key
		if fact.Values != "" {
			flag += "=" + fact.Values
		}
		required = append(required, "ci_confirm:"+flag)
		questions = append(questions, strings.TrimSpace(fact.Key+": "+fact.Question+" "+fact.Remedy))
		flags = append(flags, "--ci-confirm "+flag)
	}
	return AgentResult{
		Status: "needs_input", Phase: "discovery", Code: "ci_setup_confirmation_required", NextAction: "ask_user",
		Reason: err.Error(), Location: strings.Join(err.Jobs, "; "), RequiredInput: required,
		Instruction: "Stop and ask the user to confirm each fact; run no other ddtest command until they answer, and never assume, guess or check it from this machine. " + strings.Join(questions, " ") + " After their answer, rerun the same command adding " + strings.Join(flags, " ") + " with the values they confirmed. ddtest reports each confirmation as stated by the user, not verified.",
	}
}

// RequiredCIConfirmations returns the facts the user has not confirmed that
// a setup before framework's tests depends on (see ciSetup.Confirmations),
// or nil. Only a setup without another problem asks: a problem is reported
// first. It reads the configuration offline.
func RequiredCIConfirmations(root, language, framework string) *CIConfirmationRequired {
	provider, err := currentCIProvider(root)
	if err != nil {
		return nil
	}
	definitions := ciProviderFacts[provider.name()]
	if len(definitions) == 0 {
		return nil
	}
	workflows, err := readProviderWorkflows(provider, root)
	if err != nil {
		return nil
	}
	token := setupActionLanguage(language)
	needed := map[string]bool{}
	var jobs []string
	for _, workflow := range workflows {
		for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			job := workflow.Jobs[name]
			if disabledCondition(job.If) {
				continue
			}
			var setup *ciSetup
			for _, step := range job.Steps {
				if current, ok := datadogSetup(step); ok {
					if selected, err := setupSelectsLanguage(current, nil, token); err == nil && selected {
						setup = &current
					}
				}
				resolution := resolveTestStep(root, workflow, job, step, language, framework)
				if resolution.Review || !resolution.Matched && (resolution.Reason == "" || !stepMayStartTests(root, workflow, job, step, framework)) {
					continue
				}
				if setup == nil || setup.Problem != "" {
					break
				}
				missing := false
				for _, key := range setup.Confirmations {
					if _, stated := statedFact(key); !stated {
						needed[key], missing = true, true
					}
				}
				if key := workflow.Path + " / " + name; missing && !slices.Contains(jobs, key) {
					jobs = append(jobs, key)
				}
				break
			}
		}
	}
	if len(needed) == 0 {
		return nil
	}
	required := &CIConfirmationRequired{Provider: provider.displayName(), Jobs: jobs}
	for _, definition := range definitions {
		if needed[definition.Key] {
			required.Facts = append(required.Facts, definition)
		}
	}
	return required
}

// CISetupCaveats lists, for final_response, what ddtest could not check about
// the setup mechanism of the validated CI jobs (see ciSetup.Caveats), such as
// a tracer release the mechanism cannot pin.
func CISetupCaveats(check *RuntimeCheck) []string {
	var caveats []string
	if check == nil {
		return nil
	}
	for _, finding := range check.Jobs {
		for _, caveat := range finding.SetupCaveats {
			if !slices.Contains(caveats, caveat) {
				caveats = append(caveats, caveat)
			}
		}
	}
	return caveats
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
			line := "--ci-runtime " + statement.String() + " was not used: no validated " + statedRuntimeDisplay(statement.Runtime) + " CI job it names has a runtime ddtest could not read."
			if selectors := statements.uncompared[statement.String()]; len(selectors) > 0 {
				line += " " + strings.Join(selectors, "; ") + ": a selector ddtest cannot compare with a stated version, so the statement was accepted without checking that it agrees."
			}
			unused = append(unused, line)
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
