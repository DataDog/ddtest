// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"maps"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// onboard --apply enables the Datadog Jenkins plugin in the Jenkinsfiles
// whose jobs run the tests: the only language input is the plugin's language
// token (and the service name the language reads from its project). In a
// declarative pipeline it adds datadog(testOptimization: …) to the top-level
// options directive, creating the directive after agent or environment, or,
// when the pipeline runs steps in containers, which that option's scope
// would include, to the options of each stage that runs the tests on a host
// agent; in a scripted pipeline it wraps each outermost node block that runs
// the tests.
// An existing datadog(…) call is merged instead: enabled: true, the language
// added to languages, every other argument kept. Every edit is placed with
// the parser's byte spans, keeps the file's indentation and line endings,
// and leaves every other byte unchanged; the result is read again and must
// be a complete setup for every target job, or nothing is written.

// jenkinsServiceName is a service name the edit writes as a literal.
var jenkinsServiceName = regexp.MustCompile(`^[A-Za-z0-9@][A-Za-z0-9@._/-]*$`)

// repositoryName is the name of the repository at root: its origin
// remote's last path segment, otherwise the checkout directory's name.
func repositoryName(root string) string {
	command := exec.Command("git", "config", "--get", "remote.origin.url")
	command.Dir = root
	if output, err := command.Output(); err == nil {
		url := strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(string(output)), "/"), ".git")
		if name := url[strings.LastIndexAny(url, "/:")+1:]; jenkinsServiceName.MatchString(name) {
			return name
		}
	}
	return filepath.Base(root)
}

// jenkinsEdit replaces source[start:end] with text.
type jenkinsEdit struct {
	start, end int
	text       string
}

// jenkinsEditor plans the edits of one Jenkinsfile.
type jenkinsEditor struct {
	file, source, newline string
	tree                  *groovyBlock
	// multiline are the string literals that span lines, whose content
	// re-indenting must not change.
	multiline []groovySpan
	token     string
	service   string
	edits     []jenkinsEdit
	merged    map[*groovyCall]bool
	wrapped   map[*groovyCall]bool
	// containers are the jobs that run steps in a container, and staged the
	// stages whose options ddtest enables the plugin in instead of the
	// pipeline's.
	containers []string
	staged     map[*groovyCall]bool
}

// jenkinsShTarget is a statement of an sh script that runs a target job's
// tests: the sh call (its offset in the file) and the statement (its offset
// in the script), with the commands that must precede it in that script.
// Each sh step runs a shell of its own, so every one that runs the tests
// needs them.
type jenkinsShTarget struct {
	job             string
	line            int
	call, statement int
	after           []string
}

// jenkinsSnippetTarget is an sh step that runs a container job's tests,
// where the setup snippet goes before its first test command: lines before the statement at
// statement (an offset in its script), and wrap reports a step DD_API_KEY
// does not reach yet, which withCredentials then encloses.
type jenkinsSnippetTarget struct {
	job       string
	call      int
	statement int
	lines     []string
	wrap      bool
}

// planSetupEdits sets up every target job, or explains why it cannot do so
// safely: it enables the Datadog Jenkins plugin for the jobs on host agents,
// and adds the setup snippet (see jenkinsContainer) to the sh step that runs
// the tests of each job in a container.
func (j jenkins) planSetupEdits(root string, workflows []ciWorkflow, targets []ciSetupTarget, request ciSetupRequest) ([]setupEdit, []string, error) {
	token := jenkinsPluginToken(request.Language)
	byFile := map[string][]jenkinsShTarget{}
	snippets := map[string][]jenkinsSnippetTarget{}
	// The sh steps of each Jenkinsfile a snippet is planned for, by call:
	// the cells of a matrix run the same literal sh step, which one snippet
	// sets up for all of them; the check below reads every cell again.
	planned := map[string]map[int]bool{}
	jobs := map[string][]string{}
	language := discoveryLanguageName(request.Framework)
	// The plugin set up on host stages runs beside the snippets.
	plugin := slices.ContainsFunc(targets, func(target ciSetupTarget) bool {
		job, _, ok := gitLabTarget(workflows, target)
		return ok && !jenkinsContainerJob(job)
	})
	for _, target := range targets {
		job, _, ok := gitLabTarget(workflows, target)
		workflow := jenkinsWorkflowAt(workflows, target.Workflow)
		if !ok {
			return nil, nil, &SetupGap{target.Workflow + " / " + target.Job, "The test step is not a Jenkinsfile sh step ddtest can edit."}
		}
		if jenkinsContainerJob(job) {
			jobs[target.Workflow] = append(jobs[target.Workflow], target.Job)
			// The snippet's exports reach only its own sh step's shell, so
			// every sh step that runs the tests gets one, before its first
			// test statement.
			if planned[target.Workflow] == nil {
				planned[target.Workflow] = map[int]bool{}
			}
			for i := target.Step; i < len(job.Steps); i++ {
				current := job.Steps[i]
				if i > target.Step && !resolveTestStep(root, workflow, job, current, language, request.Framework).Matched {
					continue
				}
				origin, located := current.origin.(jenkinsShellStep)
				if located && planned[target.Workflow][origin.call] || jenkinsSnippetBefore(job, i) {
					continue // That shell's setup is checked with the others below.
				}
				planned[target.Workflow][origin.call] = true
				snippet, err := jenkinsContainerTarget(root, workflows, target, job, current, request, plugin)
				if err != nil {
					return nil, nil, err
				}
				snippets[target.Workflow] = append(snippets[target.Workflow], snippet)
			}
			continue
		}
		after := request.After
		if target.After != nil {
			after = target.After
		}
		// The first test step, and every later one: each sh step is a
		// shell of its own.
		for i := target.Step; i < len(job.Steps); i++ {
			current := job.Steps[i]
			resolution := resolveTestStep(root, workflow, job, current, language, request.Framework)
			if i > target.Step && !resolution.Matched {
				continue
			}
			line, err := strconv.Atoi(current.Source[strings.LastIndex(current.Source, ":")+1:])
			origin, located := current.origin.(jenkinsShellStep)
			if err != nil || !located || !strings.HasPrefix(current.Source, target.Workflow+":") {
				return nil, nil, &SetupGap{target.Workflow + " / " + target.Job, "The test step's position in the Jenkinsfile could not be located."}
			}
			byFile[target.Workflow] = append(byFile[target.Workflow], jenkinsShTarget{job: target.Job, line: line, call: origin.call, statement: origin.statement, after: jenkinsStepAfter(after, workflow, job, current, request.Framework, resolution)})
		}
		jobs[target.Workflow] = append(jobs[target.Workflow], target.Job)
	}
	service := request.Service
	if !jenkinsServiceName.MatchString(service) {
		service = repositoryName(root)
	}
	if len(byFile) > 0 && !jenkinsServiceName.MatchString(service) {
		return nil, nil, &SetupGap{"Jenkins", "ddtest could not derive a service name for serviceName from the project or the checkout directory (" + strconv.Quote(service) + "); add the Datadog Jenkins plugin setup by hand with a serviceName of your choice, as onboard --instructions shows, then validate with testdrive --all."}
	}
	var edits []setupEdit
	for _, file := range slices.Sorted(maps.Keys(jobs)) {
		edit, err := readSetupEdit(root, file)
		if err != nil {
			return nil, nil, err
		}
		source := string(edit.before)
		after, err := planJenkinsfileSetup(file, source, token, service, byFile[file], snippets[file], jenkinsContainerJobs(workflows, file))
		if err != nil {
			return nil, nil, err
		}
		if err := checkJenkinsfileSetup(root, j, workflows, file, after, jobs[file], request); err != nil {
			return nil, nil, err
		}
		if after == source {
			continue
		}
		edit.after = []byte(after)
		edits = append(edits, edit)
	}
	return edits, nil, nil
}

// jenkinsWorkflowAt is the workflow read from file.
func jenkinsWorkflowAt(workflows []ciWorkflow, file string) ciWorkflow {
	for _, workflow := range workflows {
		if workflow.Path == file {
			return workflow
		}
	}
	return ciWorkflow{}
}

// jenkinsStepAfter is what must run before a test step of job in its sh
// script: after, the commands that follow the setup, when the language
// needs them for that step (see discoveryLanguage.ciShellAfter).
func jenkinsStepAfter(after []string, workflow ciWorkflow, job runtimeJob, step runtimeStep, framework string, resolution commandResolution) []string {
	language := discoveryLanguageFor(framework)
	if len(after) == 0 || language == nil {
		return nil
	}
	if needed, _ := language.ciShellAfter(workflow, job, step, framework, resolution); !needed {
		return nil
	}
	return after
}

// jenkinsContainerTarget plans the setup snippet for a container job's
// test step, the first of its sh step: the snippet for request's language in the Jenkins form,
// installing request's tracer release (or the one the project's
// dependencies pin), with the language's commands that follow the setup.
// plugin reports host stages the plugin sets up beside it, whose own
// install of a tracer the dependencies do not pin would change the release.
func jenkinsContainerTarget(root string, workflows []ciWorkflow, target ciSetupTarget, job runtimeJob, step runtimeStep, request ciSetupRequest, plugin bool) (jenkinsSnippetTarget, error) {
	location := target.Workflow + " / " + target.Job
	origin, ok := step.origin.(jenkinsShellStep)
	if !ok {
		return jenkinsSnippetTarget{}, &SetupGap{location, "The test step's position in the Jenkinsfile could not be located."}
	}
	language := discoveryLanguageFor(request.Framework)
	if language == nil {
		return jenkinsSnippetTarget{}, &SetupGap{location, "ddtest cannot set up " + request.Framework + " in this container stage."}
	}
	container := setupProviderFor(jenkins{}, job).(jenkinsContainer)
	site := request.Site
	if site == "" {
		site = defaultDatadogSite
	}
	snippet := language.ciSetupRequest(root, request.Framework, site, container.setupTerms())
	snippet.Framework = request.Framework
	if target.After != nil {
		snippet.After = target.After
	}
	release := request.TracerVersion
	if release == "" || strings.HasPrefix(release, "<") {
		if locked, ok := language.(ciLockedTracer); ok {
			release = locked.lockedTracer(root)
		}
	}
	// A tracer no dependency carries has the exact release ddtest
	// recommends, as the setup instructions do. The plugin that host stages
	// run beside it downloads the latest release, and one validation cannot
	// certify both, so it is pinned to that release first.
	if release == "" || strings.HasPrefix(release, "<") {
		if recommended, ok := language.(ciRecommendedTracer); ok {
			if plugin {
				return jenkinsSnippetTarget{}, &EditThenApplyGap{SetupGap{location, "The Datadog Jenkins plugin that sets up the host stages downloads the latest tracer, but this container stage's snippet installs " + recommended.recommendedTracer() + ", and one validation cannot certify both. Pin the plugin to that release, as the setup instructions show, then rerun onboard --apply."}}
			}
			release = recommended.recommendedTracer()
		}
	}
	if release == "" || strings.HasPrefix(release, "<") {
		reason := "The setup snippet for this container stage installs an exact tracer release, which ddtest selects only from the project's dependencies, and they pin none."
		if plugin {
			// The other mechanism of the build changes the release.
			return jenkinsSnippetTarget{}, &EditThenApplyGap{SetupGap{location, reason + " Pin the tracer in the project's dependencies, as the setup instructions show, then rerun onboard --apply, which writes the snippet with that release."}}
		}
		return jenkinsSnippetTarget{}, &SetupGap{location, reason + " Add the setup snippet by hand with an exact release, as onboard --instructions shows, then validate with testdrive --all."}
	}
	snippet.TracerVersion = release
	key := mergeEnvironment(job.Env, step.Env)["DD_API_KEY"]
	if key != "" && key != jenkinsSecret("DD_API_KEY") {
		return jenkinsSnippetTarget{}, &SetupGap{location, "DD_API_KEY reaches this stage from " + key + ", not from a Jenkins credential ddtest can see; bind it with withCredentials(" + jenkinsCredentialBinding + ") { … } or credentials('" + jenkinsCredentialID + "') instead, then rerun onboard --apply."}
	}
	written := jenkinsSnippet(snippet, container.reset)
	lines := strings.Split(written.shell(), "\n")
	// An image without Bash runs the language's setup for a POSIX shell.
	if shell, ok := language.(ciShellSetup); ok && job.SetupImageShellOnly {
		lines = append(slices.Clone(written.before), strings.Split(shell.shellSetup(site, release, jenkinsSnippetForm), "\n")...)
	}
	return jenkinsSnippetTarget{job: target.Job, call: origin.call, statement: origin.statement, lines: lines, wrap: key == ""}, nil
}

// jenkinsSnippetBefore reports a setup snippet before step among a
// container job's steps in step's own shell (its sh step): that shell is set
// up already, completely or not, which the edit then checks without adding a
// second one. A snippet in another sh step does not reach it.
func jenkinsSnippetBefore(job runtimeJob, step int) bool {
	return slices.ContainsFunc(job.Steps[:step], func(current runtimeStep) bool {
		return current.Setup != nil && jenkinsSnippetSetup(*current.Setup) && sameShell(current, job.Steps[step])
	})
}

// ciLockedTracer is a language whose project dependencies can pin its
// tracer, which a setup snippet then installs.
// ciRecommendedTracer is a language whose tracer no project dependency
// carries, as dd-java-agent, so ddtest recommends an exact release itself.
type ciRecommendedTracer interface {
	// recommendedTracer is the exact release ddtest's setup instructions
	// recommend.
	recommendedTracer() string
}

type ciLockedTracer interface {
	// lockedTracer is the exact release the dependencies at root pin, or
	// empty.
	lockedTracer(root string) string
}

// jenkinsContainerJobs are the jobs of a Jenkinsfile that run steps in a
// container, which the plugin's scope must not include.
func jenkinsContainerJobs(workflows []ciWorkflow, file string) []string {
	var containers []string
	for _, workflow := range workflows {
		if workflow.Path != file {
			continue
		}
		for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			job := workflow.Jobs[name]
			if jenkinsContainerJob(job) && !disabledCondition(job.If) && slices.ContainsFunc(job.Steps, func(step runtimeStep) bool { return step.Setup == nil }) {
				containers = append(containers, file+" / "+name)
			}
		}
	}
	return containers
}

// checkJenkinsfileSetup reads the edited Jenkinsfile again: it must keep
// every job, and set up every target job completely.
func checkJenkinsfileSetup(root string, provider ciProvider, workflows []ciWorkflow, file, source string, jobs []string, request ciSetupRequest) error {
	gap := func(reason string) error {
		return &SetupGap{file, "The planned edit would not leave a complete Datadog setup, so ddtest wrote nothing: " + reason + " Fix it, or add the setup by hand as onboard --instructions shows, then validate with testdrive --all."}
	}
	edited, err := readJenkinsfile(root, file, source)
	if err != nil {
		return gap("the edited Jenkinsfile cannot be read (" + err.Error() + ").")
	}
	fillStatedRuntimes([]ciWorkflow{edited})
	for _, workflow := range workflows {
		if workflow.Path == file && !slices.Equal(slices.Sorted(maps.Keys(workflow.Jobs)), slices.Sorted(maps.Keys(edited.Jobs))) {
			return gap("the edited Jenkinsfile reads as other jobs than the original.")
		}
	}
	// The plugin's variables reach every step in the option's scope, so a
	// job there that runs steps in a container refuses the edit.
	var containers []string
	for _, name := range slices.Sorted(maps.Keys(edited.Jobs)) {
		job := edited.Jobs[name]
		if jenkinsContainerJob(job) && !disabledCondition(job.If) && len(job.Steps) > 1 && job.Steps[0].Setup != nil && !jenkinsSnippetSetup(*job.Steps[0].Setup) {
			containers = append(containers, file+" / "+name)
		}
	}
	if len(containers) > 0 {
		return &SetupGap{file, "ddtest does not enable the Datadog Jenkins plugin here: its option would also apply to " + strings.Join(containers, ", ") + ", which run steps in a container. The plugin sets its tracer variables in every step of its scope, container steps included, but installs the tracer only on the build agent, with the agent's toolchain. In those containers, " + jenkinsContainerEffects([]string{jenkinsPluginToken(setupActionLanguage(discoveryLanguageName(request.Framework)))}) + ". The user's options are to run those stages on a host agent, or to keep them out of the option's scope (a container block in a stage of its own, or in a scripted pipeline, a node block of its own outside the wrap), then rerun onboard --apply; or " + handWrittenSetupOption(discoveryLanguageName(request.Framework), jenkinsPluginReference) + ". Do not move stages without the user's approval."}
	}
	language := discoveryLanguageName(request.Framework)
	for _, name := range jobs {
		problem := providerJobSetupProblem(root, provider, edited, name, language, request.Framework, request.Site)
		if problem == "" {
			problem = jenkinsAfterProblem(root, edited, name, language, request)
		}
		if problem != "" {
			return gap(file + " / " + name + ": " + problem)
		}
	}
	return nil
}

// jenkinsAfterProblem explains a test step of a job whose sh script does not
// have the commands that follow the setup (request's After) in effect
// before it, in its own shell; empty when every one that needs them does.
// The reader follows the script's statements in order, so a command after
// the test, in a comment, or undone before it, leaves the step without it.
func jenkinsAfterProblem(root string, workflow ciWorkflow, name, language string, request ciSetupRequest) string {
	registered := discoveryLanguageFor(request.Framework)
	if len(request.After) == 0 || registered == nil {
		return ""
	}
	job := workflow.Jobs[name]
	after := request.After
	if jenkinsContainerJob(job) {
		// A container stage's setup snippet runs its own commands.
		after = registered.ciSetupRequest(root, request.Framework, request.Site, setupProviderFor(jenkins{}, job).setupTerms()).After
	}
	for _, step := range job.Steps {
		resolution := resolveTestStep(root, workflow, job, step, language, request.Framework)
		if !resolution.Matched {
			continue
		}
		if needed, problem := registered.ciShellAfter(workflow, job, step, request.Framework, resolution); needed && problem != "" {
			return "the sh step at " + step.Source + " runs " + OneLineExcerpt(step.Run) + " without " + strings.Join(after, "; ") + " in effect before it in its shell: " + problem + "; add it inside that sh script, before the test command."
		}
	}
	return ""
}

// planJenkinsfileSetup returns source with the plugin enabled for targets.
// containers are the jobs that run steps in a container: a declarative
// pipeline with any then enables the plugin in the options of each stage
// that runs the tests instead of the pipeline's, so its scope leaves them
// out.
func planJenkinsfileSetup(file, source, token, service string, targets []jenkinsShTarget, snippets []jenkinsSnippetTarget, containers []string) (string, error) {
	tree, err := parseGroovy(source)
	if err != nil {
		return "", &SetupGap{file, "The Jenkinsfile cannot be parsed: " + err.Error()}
	}
	tokens, _ := lexGroovy(source)
	e := &jenkinsEditor{file: file, source: source, newline: "\n", tree: tree, token: token, service: service, containers: containers, merged: map[*groovyCall]bool{}, wrapped: map[*groovyCall]bool{}, staged: map[*groovyCall]bool{}}
	if strings.Contains(source, "\r\n") {
		e.newline = "\r\n"
	}
	for _, token := range tokens {
		if token.kind == groovyStringToken && strings.Contains(token.text, "\n") {
			e.multiline = append(e.multiline, groovySpan{token.start, token.end})
		}
	}
	if err := e.plan(targets); err != nil {
		return "", err
	}
	for _, snippet := range snippets {
		if err := e.snippet(snippet); err != nil {
			return "", err
		}
	}
	return e.apply()
}

// gap refuses the edit at node, pointing at instructions.
func (e *jenkinsEditor) gap(node groovyNode, reason string) error {
	location := e.file
	if node != nil {
		start, _ := node.span()
		location += ":" + strconv.Itoa(groovyLine(e.source, start))
	}
	return &SetupGap{location, reason + " Add the Datadog Jenkins plugin setup by hand as onboard --instructions shows, then validate with testdrive --all."}
}

// groovyVisit calls visit for node and every node inside it, with the
// nodes that enclose it, outermost first.
func groovyVisit(node groovyNode, ancestors []groovyNode, visit func(node groovyNode, ancestors []groovyNode)) {
	if node == nil {
		return
	}
	visit(node, ancestors)
	inner := append(slices.Clone(ancestors), node)
	children := []groovyNode{}
	switch node := node.(type) {
	case *groovyBlock:
		children = node.Statements
	case *groovyCall:
		if node.Receiver != nil {
			children = append(children, node.Receiver)
		}
		for _, arg := range node.Args {
			children = append(children, arg.Value)
		}
		if node.Closure != nil {
			children = append(children, node.Closure)
		}
	case *groovyClosure:
		children = append(children, node.Body)
	case *groovyControl:
		children = append(children, node.Condition)
		for _, block := range append(append(slices.Clone(node.Blocks), node.Catches...), node.Finally) {
			if block != nil {
				children = append(children, block)
			}
		}
	case *groovyAssign:
		children = append(children, node.Target, node.Value)
	case *groovyFunction:
		children = append(children, node.Body)
	case *groovyReturn:
		children = append(children, node.Value)
	case *groovyBinary:
		children = append(children, node.Left, node.Right)
	case *groovyList:
		children = node.Items
	case *groovyMap:
		for _, entry := range node.Entries {
			children = append(children, entry.Value)
		}
	}
	for _, child := range children {
		groovyVisit(child, inner, visit)
	}
}

// jenkinsStep reports a call of the Pipeline step name.
func jenkinsStep(node groovyNode, name string) (*groovyCall, bool) {
	call, ok := node.(*groovyCall)
	return call, ok && call.Receiver == nil && call.Name == name
}

func (e *jenkinsEditor) plan(targets []jenkinsShTarget) error {
	var pipeline *groovyCall
	// A scripted pipeline's node blocks inside functions stay unwrapped.
	var functions []error
	for _, statement := range e.tree.Statements {
		switch node := statement.(type) {
		case *groovyAnnotation:
			if node.Name == "Library" {
				return e.gap(node, "The Jenkinsfile loads a shared library (@Library), which may declare the pipeline, its options or its node blocks itself; ddtest does not edit pipelines that use shared libraries.")
			}
		case *groovyCall:
			if node.Receiver == nil && node.Name == "library" {
				return e.gap(node, "The Jenkinsfile loads a shared library (library …), which may declare the pipeline, its options or its node blocks itself; ddtest does not edit pipelines that use shared libraries.")
			}
			if node.Receiver == nil && node.Name == "pipeline" && node.Closure != nil {
				if pipeline != nil {
					return e.gap(node, "The Jenkinsfile declares several pipeline blocks; ddtest cannot tell which one Jenkins runs.")
				}
				pipeline = node
			}
		case *groovyAssign:
			if _, ok := jenkinsStep(node.Value, "library"); ok {
				return e.gap(node, "The Jenkinsfile loads a shared library (library …), which may declare the pipeline, its options or its node blocks itself; ddtest does not edit pipelines that use shared libraries.")
			}
		case *groovyFunction:
			problem := ""
			groovyVisit(node.Body, nil, func(inner groovyNode, _ []groovyNode) {
				if call, ok := jenkinsStep(inner, "node"); ok && problem == "" && call.Closure != nil && jenkinsRunsCommands(call.Closure.Body) {
					problem = "The Groovy function " + node.Name + "() runs a node block with shell steps, which may run the tests; ddtest wraps only node blocks the script runs directly, so it would leave that one uninstrumented."
				}
			})
			if problem != "" {
				functions = append(functions, e.gap(node, problem))
			}
		}
	}
	if pipeline == nil && len(functions) > 0 {
		return functions[0]
	}
	if pipeline != nil {
		if libraries, ok := jenkinsDirectives(pipeline.Closure.Body)["libraries"]; ok {
			return e.gap(libraries, "The pipeline loads shared libraries (libraries directive), which may declare its options themselves; ddtest does not edit pipelines that use shared libraries.")
		}
	}
	// The sh calls, by their offset, with what encloses them: a target's
	// call is the one its step was read from, whichever others share its
	// line.
	type located struct {
		call      *groovyCall
		ancestors []groovyNode
	}
	byStart := map[int][]located{}
	groovyVisit(e.tree, nil, func(node groovyNode, ancestors []groovyNode) {
		if call, ok := jenkinsStep(node, "sh"); ok {
			byStart[call.Start] = append(byStart[call.Start], located{call, ancestors})
		}
	})
	options := false
	// The commands each sh call's script needs, and the statements they
	// must precede.
	esm := map[*groovyCall][]string{}
	statements := map[*groovyCall][]int{}
	for _, target := range targets {
		calls := byStart[target.call]
		if len(calls) != 1 {
			return e.gap(nil, "The sh step of "+target.job+" at line "+strconv.Itoa(target.line)+" could not be located as one sh call in the Jenkinsfile, so ddtest cannot tell which one runs the tests.")
		}
		current := calls[0]
		if len(target.after) > 0 {
			esm[current.call] = target.after
			statements[current.call] = append(statements[current.call], target.statement)
		}
		var datadogs []*groovyCall
		var node, stage, staged *groovyCall
		inPipeline := false
		for _, ancestor := range current.ancestors {
			if call, ok := jenkinsStep(ancestor, "datadog"); ok && call.Closure != nil {
				datadogs = append(datadogs, call)
			}
			if call, ok := jenkinsStep(ancestor, "node"); ok && node == nil {
				node = call
			}
			if call, ok := jenkinsStep(ancestor, "stage"); ok && inPipeline && call.Closure != nil {
				// The innermost stage, and the innermost one whose
				// options enable the plugin already.
				stage = call
				if jenkinsStageDatadog(call) != nil {
					staged = call
				}
			}
			if ancestor == pipeline {
				inPipeline = true
			}
		}
		switch {
		case len(datadogs) > 1:
			return e.gap(datadogs[1], "Several datadog(…) calls enclose the tests of "+target.job+"; ddtest cannot tell which arguments the plugin applies.")
		case len(datadogs) == 1:
			if err := e.merge(datadogs[0]); err != nil {
				return err
			}
		case pipeline != nil && !inPipeline:
			return e.gap(current.call, "The tests of "+target.job+" run outside the declarative pipeline block, where its options do not apply.")
		case staged != nil:
			if err := e.merge(jenkinsStageDatadog(staged)); err != nil {
				return err
			}
		case pipeline != nil && len(e.containers) > 0 && stage != nil:
			if err := e.stageOptions(pipeline, stage); err != nil {
				return err
			}
		case pipeline != nil:
			options = true
		case node == nil:
			return e.gap(current.call, "The tests of "+target.job+" run outside a node block ddtest can wrap.")
		default:
			if err := e.wrap(node); err != nil {
				return err
			}
		}
	}
	if options {
		if err := e.options(pipeline); err != nil {
			return err
		}
	}
	for _, call := range slices.SortedFunc(maps.Keys(esm), func(a, b *groovyCall) int { return a.Start - b.Start }) {
		if err := e.prepend(call, esm[call], statements[call]); err != nil {
			return err
		}
	}
	return nil
}

// apply makes the planned edits, which must not overlap.
func (e *jenkinsEditor) apply() (string, error) {
	slices.SortFunc(e.edits, func(a, b jenkinsEdit) int { return b.start - a.start })
	text := e.source
	end := len(text) + 1
	for _, edit := range e.edits {
		if edit.end > end || edit.start > edit.end {
			return "", &SetupGap{e.file, "The planned edits overlap; add the Datadog Jenkins plugin setup by hand as onboard --instructions shows."}
		}
		text = text[:edit.start] + edit.text + text[edit.end:]
		end = edit.start
	}
	return text, nil
}

func (e *jenkinsEditor) add(start, end int, text string) {
	e.edits = append(e.edits, jenkinsEdit{start, end, text})
}

// optionsMap is the testOptimization argument ddtest writes.
func (e *jenkinsEditor) optionsMap() string {
	return "[enabled: true, serviceName: " + groovyQuote(e.service, "'") + ", languages: [" + groovyQuote(e.token, "'") + "]]"
}

// datadogCall is the datadog(…) call ddtest writes.
func (e *jenkinsEditor) datadogCall() string {
	return "datadog(testOptimization: " + e.optionsMap() + ")"
}

// groovyQuote writes text as a Groovy string with quote, ' or ".
func groovyQuote(text, quote string) string {
	if quote == "'" {
		return "'" + strings.NewReplacer(`\`, `\\`, "'", `\'`).Replace(text) + "'"
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`).Replace(text) + `"`
}

// lineStart is the offset where the line holding offset starts.
func (e *jenkinsEditor) lineStart(offset int) int {
	return strings.LastIndex(e.source[:offset], "\n") + 1
}

// lineEnd is the offset of the line break after offset (before a \r\n), or
// the end of the file.
func (e *jenkinsEditor) lineEnd(offset int) int {
	end := strings.Index(e.source[offset:], "\n")
	if end < 0 {
		return len(e.source)
	}
	end += offset
	if end > offset && e.source[end-1] == '\r' {
		end--
	}
	return end
}

// indent is the leading whitespace of the line holding offset.
func (e *jenkinsEditor) indent(offset int) string {
	line := e.source[e.lineStart(offset):]
	return line[:len(line)-len(strings.TrimLeft(line, " \t"))]
}

// startsLine reports whether only whitespace precedes offset on its line.
func (e *jenkinsEditor) startsLine(offset int) bool {
	return strings.TrimLeft(e.source[e.lineStart(offset):offset], " \t") == ""
}

// endsLine reports whether only whitespace or a line comment follows offset
// on its line.
func (e *jenkinsEditor) endsLine(offset int) bool {
	rest := strings.TrimLeft(e.source[offset:e.lineEnd(offset)], " \t")
	return rest == "" || strings.HasPrefix(rest, "//")
}

func (e *jenkinsEditor) sameLine(a, b int) bool {
	return groovyLine(e.source, a) == groovyLine(e.source, b)
}

// unit is the file's indentation step: inner's indentation beyond outer's
// when it is deeper, otherwise the smallest indentation of the file's code
// lines (a tab when they indent with tabs), or four spaces.
func (e *jenkinsEditor) unit(outer, inner string) string {
	if strings.HasPrefix(inner, outer) && len(inner) > len(outer) {
		return inner[len(outer):]
	}
	unit := ""
	offset := 0
	for _, line := range strings.SplitAfter(e.source, "\n") {
		content := strings.TrimRight(line, "\r\n")
		trimmed := strings.TrimLeft(content, " \t")
		indent := content[:len(content)-len(trimmed)]
		code := trimmed != "" && !strings.HasPrefix(trimmed, "*") && !e.inString(offset)
		offset += len(line)
		switch {
		case !code || indent == "":
		case indent[0] == '\t':
			return "\t"
		case len(indent) >= 2 && (unit == "" || len(indent) < len(unit)):
			unit = indent
		}
	}
	if unit == "" || len(unit) > 8 {
		return "    "
	}
	return unit
}

// inString reports an offset inside a multi-line string literal.
func (e *jenkinsEditor) inString(offset int) bool {
	return slices.ContainsFunc(e.multiline, func(span groovySpan) bool { return offset > span.Start && offset < span.End })
}

// jenkinsStageDatadog is the datadog(…) call of a stage's options directive
// that configures Test Optimization, or nil.
func jenkinsStageDatadog(stage *groovyCall) *groovyCall {
	options, ok := jenkinsDirectives(stage.Closure.Body)["options"]
	if !ok || options.Closure == nil {
		return nil
	}
	for _, statement := range options.Closure.Body.Statements {
		if call, ok := jenkinsStep(statement, "datadog"); ok {
			if _, _, configures := jenkinsTestOptimization(call); configures {
				return call
			}
		}
	}
	return nil
}

// stageOptions enables the plugin in the options directive of a stage that
// runs the tests, once per stage, because the pipeline runs steps in
// containers, which the pipeline's option would include. A pipeline option
// that already enables it keeps them in its scope, so the edit is refused.
func (e *jenkinsEditor) stageOptions(pipeline, stage *groovyCall) error {
	if e.staged[stage] {
		return nil
	}
	e.staged[stage] = true
	if options, ok := jenkinsDirectives(pipeline.Closure.Body)["options"]; ok && options.Closure != nil {
		for _, statement := range options.Closure.Body.Statements {
			if call, ok := jenkinsStep(statement, "datadog"); ok {
				if _, _, configures := jenkinsTestOptimization(call); configures {
					return e.gap(call, "The pipeline's datadog(…) option enables Test Optimization for every stage, including "+strings.Join(e.containers, ", ")+", which run steps in a container, where "+jenkinsContainerUnsupported+". Move the option into the options directive of each host-agent stage that runs the tests, every one with the same arguments, then rerun onboard --apply.")
				}
			}
		}
	}
	return e.optionsIn(stage, "stage")
}

// options enables the plugin in the declarative pipeline's top-level options
// directive, creating it after agent or environment.
func (e *jenkinsEditor) options(pipeline *groovyCall) error {
	return e.optionsIn(pipeline, "pipeline")
}

// optionsIn enables the plugin in the options directive of a pipeline or
// stage block (what names it), creating the directive after agent or
// environment.
func (e *jenkinsEditor) optionsIn(pipeline *groovyCall, what string) error {
	body := pipeline.Closure.Body
	var options, anchor groovyNode
	for _, statement := range body.Statements {
		name := ""
		switch node := statement.(type) {
		case *groovyCall:
			if node.Receiver == nil {
				name = node.Name
			}
		case *groovyName:
			if len(node.Path) == 1 {
				name = node.Path[0]
			}
		}
		switch name {
		case "options":
			if options != nil {
				return e.gap(statement, "The "+what+" has several options directives.")
			}
			options = statement
		case "agent", "environment":
			anchor = statement
		}
	}
	if options != nil {
		call, ok := options.(*groovyCall)
		if !ok || call.Closure == nil || len(call.Args) > 0 {
			return e.gap(options, "The "+what+"'s options directive is not a block ddtest can read.")
		}
		return e.optionsBlock(call)
	}
	if len(body.Statements) == 0 || e.sameLine(pipeline.Closure.Start, pipeline.Closure.End-1) {
		return e.gap(pipeline, "The "+what+" block is written on one line, where ddtest cannot place an options directive.")
	}
	first, _ := body.Statements[0].span()
	if anchor != nil {
		first, _ = anchor.span()
	}
	sibling := e.indent(first)
	block := sibling + "options {" + e.newline + sibling + e.unit(e.indent(pipeline.Start), sibling) + e.datadogCall() + e.newline + sibling + "}"
	if anchor == nil {
		if !e.startsLine(first) {
			return e.gap(pipeline, "The "+what+"'s first directive does not start its line, where ddtest cannot place an options directive.")
		}
		e.add(e.lineStart(first), e.lineStart(first), block+e.newline)
		return nil
	}
	if _, end := anchor.span(); !e.endsLine(end) {
		return e.gap(anchor, "Another statement follows the "+what+"'s agent or environment directive on its line, where ddtest cannot place an options directive.")
	} else {
		position := e.lineEnd(end)
		e.add(position, position, e.newline+block)
	}
	return nil
}

// optionsBlock enables the plugin in an existing options directive: it
// merges into its datadog(…) call, or adds one.
func (e *jenkinsEditor) optionsBlock(options *groovyCall) error {
	var datadogs []*groovyCall
	for _, statement := range options.Closure.Body.Statements {
		if call, ok := jenkinsStep(statement, "datadog"); ok {
			datadogs = append(datadogs, call)
		}
	}
	if len(datadogs) > 0 {
		for _, call := range datadogs[1:] {
			if groovyText(e.source, call) != groovyText(e.source, datadogs[0]) {
				return e.gap(call, "The options directive has several datadog(…) calls with different arguments; ddtest cannot tell which ones the plugin applies.")
			}
		}
		for _, call := range datadogs {
			if err := e.merge(call); err != nil {
				return err
			}
		}
		return nil
	}
	closure := options.Closure
	sibling := e.indent(options.Start)
	statements := closure.Body.Statements
	if len(statements) == 0 {
		if e.sameLine(closure.Start, closure.End-1) {
			e.add(closure.Start, closure.End, "{"+e.newline+sibling+e.unit(e.indent(options.Start), "")+e.datadogCall()+e.newline+sibling+"}")
			return nil
		}
		position := e.lineStart(closure.End - 1)
		if !e.startsLine(closure.End - 1) {
			position = closure.End - 1
			e.add(position, position, e.newline+sibling+e.unit(sibling, "")+e.datadogCall()+e.newline+sibling)
			return nil
		}
		e.add(position, position, sibling+e.unit(sibling, "")+e.datadogCall()+e.newline)
		return nil
	}
	_, end := statements[len(statements)-1].span()
	if e.endsLine(end) && !e.sameLine(closure.Start, end) {
		start, _ := statements[len(statements)-1].span()
		position := e.lineEnd(end)
		e.add(position, position, e.newline+e.indent(start)+e.datadogCall())
		return nil
	}
	// Options written on one line take another statement after a ;.
	e.add(end, end, "; "+e.datadogCall())
	return nil
}

// wrap encloses a scripted node block in datadog(…) { … }, indenting its
// lines one step; lines inside multi-line strings keep their bytes. It only
// inserts text, at line starts and around the block, so edits inside the
// block (such as an sh script's loader line) compose with it.
func (e *jenkinsEditor) wrap(node *groovyCall) error {
	if e.wrapped[node] {
		return nil
	}
	e.wrapped[node] = true
	if node.Closure == nil {
		return e.gap(node, "The node step has no block ddtest can wrap.")
	}
	if !e.startsLine(node.Start) || !e.endsLine(node.End) {
		return e.gap(node, "The node block shares its first or last line with other statements, where ddtest cannot wrap it.")
	}
	outer := e.indent(node.Start)
	inner := ""
	if statements := node.Closure.Body.Statements; len(statements) > 0 {
		if start, _ := statements[0].span(); !e.sameLine(node.Start, start) {
			inner = e.indent(start)
		}
	}
	unit := e.unit(outer, inner)
	start, end := e.lineStart(node.Start), e.lineEnd(node.End)
	e.add(start, start, outer+"datadog(testOptimization: "+e.optionsMap()+") {"+e.newline+unit)
	for offset := strings.Index(e.source[start:end], "\n"); offset >= 0; {
		line := start + offset + 1
		next := strings.Index(e.source[line:end], "\n")
		lineEnd := end
		if next >= 0 {
			lineEnd = line + next
		}
		if strings.TrimSpace(e.source[line:lineEnd]) != "" && !e.inString(line) {
			e.add(line, line, unit)
		}
		if next < 0 {
			break
		}
		offset = line + next - start
	}
	e.add(end, end, e.newline+outer+"}")
	return nil
}

// merge enables the plugin in an existing datadog(…) call: it adds
// testOptimization, or sets enabled: true and adds the language to
// languages in it (or in its legacy testVisibility), keeping every other
// argument. Arguments it would change must be literal.
func (e *jenkinsEditor) merge(call *groovyCall) error {
	if e.merged[call] {
		return nil
	}
	e.merged[call] = true
	if jenkinsPositionalArgs(call) {
		// Its arguments are a map ddtest would have to read, and a named one
		// beside it makes a call Jenkins rejects.
		return e.gap(call, "The datadog(…) call has positional or computed arguments, so ddtest cannot safely merge the setup into it: Jenkins rejects a named testOptimization argument beside them. Write its arguments as named ones, such as datadog(collectLogs: true) { … }, then rerun onboard --apply.")
	}
	key, value, ok := jenkinsTestOptimization(call)
	if !ok {
		argument := "testOptimization: " + e.optionsMap()
		switch {
		case len(call.Args) > 0:
			last := call.Args[len(call.Args)-1]
			lastStart, lastEnd := last.Value.span()
			if last.Name != "" {
				lastStart = last.NameStart
			}
			e.append(call.NameStart, lastStart, lastEnd, argument)
		case call.Parens:
			open := strings.Index(e.source[call.NameStart:], "(") + call.NameStart
			e.add(open+1, open+1, argument)
		default:
			position := call.NameStart + len(call.Name)
			e.add(position, position, "("+argument+")")
		}
		return nil
	}
	options, literal := value.(*groovyMap)
	if !literal {
		return e.gap(call, "The datadog("+key+": …) argument is computed, not a literal map, so ddtest cannot merge the setup into it.")
	}
	if len(options.Entries) == 0 {
		e.add(options.Start, options.End, e.optionsMap())
		return nil
	}
	fields := map[string]groovyArg{}
	for _, entry := range options.Entries {
		if entry.Name == "\x00" {
			return e.gap(call, "The datadog("+key+": …) map has a computed key, so ddtest cannot merge the setup into it.")
		}
		fields[entry.Name] = entry
	}
	var missing []string
	if enabled, ok := fields["enabled"]; !ok {
		missing = append(missing, "enabled: true")
	} else if literal, ok := enabled.Value.(*groovyLiteral); !ok || literal.Text != "true" && literal.Text != "false" {
		return e.gap(call, "The datadog("+key+": …) map sets enabled to the computed value "+groovyText(e.source, enabled.Value)+", so ddtest cannot set it to true.")
	} else if literal.Text == "false" {
		e.add(literal.Start, literal.End, "true")
	}
	if service, ok := fields["serviceName"]; !ok {
		missing = append(missing, "serviceName: "+groovyQuote(e.service, "'"))
	} else if text, ok := service.Value.(*groovyString); ok {
		if value, literal := text.Value.literal(); literal && strings.TrimSpace(value) == "" {
			e.add(text.Start, text.End, groovyQuote(e.service, "'"))
		}
	}
	if languages, ok := fields["languages"]; !ok {
		missing = append(missing, "languages: ["+groovyQuote(e.token, "'")+"]")
	} else if err := e.language(call, key, languages.Value); err != nil {
		return err
	}
	if len(missing) > 0 {
		last := options.Entries[len(options.Entries)-1]
		_, lastEnd := last.Value.span()
		e.append(options.Start, last.NameStart, lastEnd, strings.Join(missing, ", "))
	}
	return nil
}

// language adds the plugin's language token to a literal languages list.
func (e *jenkinsEditor) language(call *groovyCall, key string, value groovyNode) error {
	list, ok := value.(*groovyList)
	if !ok {
		return e.gap(call, "The datadog("+key+": …) map sets languages to the computed value "+groovyText(e.source, value)+", so ddtest cannot add "+e.token+" to it.")
	}
	quote := "'"
	for _, item := range list.Items {
		text, ok := item.(*groovyString)
		value, literal := "", false
		if ok {
			value, literal = text.Value.literal()
		}
		if !literal {
			return e.gap(call, "The datadog("+key+": …) languages list has the computed item "+groovyText(e.source, item)+", so ddtest cannot add "+e.token+" to it.")
		}
		if strings.EqualFold(strings.TrimSpace(value), e.token) {
			return nil
		}
		if text.Value.quote == `"` {
			quote = `"`
		}
	}
	if len(list.Items) == 0 {
		e.add(list.Start, list.End, "["+groovyQuote(e.token, "'")+"]")
		return nil
	}
	lastStart, lastEnd := list.Items[len(list.Items)-1].span()
	e.append(list.Start, lastStart, lastEnd, groovyQuote(e.token, quote))
	return nil
}

// append adds item after the last of a comma-separated sequence opened at
// open: on its own line, with the last item's indentation, when the items
// start on lines of their own; after a trailing comma, keeping one after it.
func (e *jenkinsEditor) append(open, lastStart, lastEnd int, item string) {
	rest := e.source[lastEnd:]
	trimmed := strings.TrimLeft(rest, " \t\r\n")
	trailing := strings.HasPrefix(trimmed, ",")
	multiline := !e.sameLine(open, lastStart) && e.startsLine(lastStart)
	switch {
	case trailing && multiline:
		position := lastEnd + len(rest) - len(trimmed) + 1
		e.add(position, position, e.newline+e.indent(lastStart)+item+",")
	case trailing:
		position := lastEnd + len(rest) - len(trimmed) + 1
		e.add(position, position, " "+item+",")
	case multiline:
		e.add(lastEnd, lastEnd, ","+e.newline+e.indent(lastStart)+item)
	default:
		e.add(lastEnd, lastEnd, ", "+item)
	}
}

// prepend adds commands to the start of an sh step's script, which must be
// a single-quoted literal: they then run in the test's shell, before it.
// tests are the offsets in the script of the statements that run the tests;
// commands that already run as statements of the script before every one
// of them are kept as they are. One that only follows a test, sits in a
// comment or runs inside another statement does not count: the commands
// are added at the start, and the check of the edited Jenkinsfile then
// requires them to take effect before every test.
func (e *jenkinsEditor) prepend(call *groovyCall, commands []string, tests []int) error {
	location := e.file + ":" + strconv.Itoa(groovyLine(e.source, call.Start))
	manual := func(reason string) error {
		return &EditThenApplyGap{SetupGap{location, reason + " Add these commands inside the sh script that runs the tests, before the test command, then rerun onboard --apply, which enables the Datadog Jenkins plugin:\n" + strings.Join(commands, "\n")}}
	}
	positional, named := jenkinsArgs(call)
	node := firstNode(positional)
	if value, ok := named["script"]; ok {
		node = value
	}
	script, ok := node.(*groovyString)
	if !ok || script.Value.quote != "'" && script.Value.quote != "'''" {
		return manual("The sh script that runs the tests is not a single-quoted string literal, which ddtest edits only.")
	}
	text, _ := script.Value.literal()
	file, parsed := parseCIShell(text)
	if !parsed {
		return manual("The sh script that runs the tests is not shell ddtest can parse, so it cannot tell whether these commands run before the test command.")
	}
	if jenkinsCommandsPrecede(file, commands, tests) {
		return nil
	}
	if first, _, _ := strings.Cut(strings.TrimLeft(text, " \t\r\n"), "\n"); strings.HasPrefix(first, "#!") {
		return manual("The sh script that runs the tests starts with a shebang, which ddtest does not edit.")
	}
	if slices.ContainsFunc(commands, func(command string) bool { return strings.ContainsAny(command, "'\\\n") }) {
		return manual("These commands cannot be written into a single-quoted string as they are.")
	}
	content := script.Start + len(script.Value.quote)
	body := e.source[content : script.End-len(script.Value.quote)]
	if script.Value.quote == "'''" && strings.TrimLeft(body, " \t\r\n") != body && strings.Contains(body[:len(body)-len(strings.TrimLeft(body, " \t\r\n"))], "\n") {
		// The script starts on the next line: one line per command, with its
		// first line's indentation.
		leading := body[:len(body)-len(strings.TrimLeft(body, " \t\r\n"))]
		position := content + strings.LastIndex(leading, "\n") + 1
		indent := e.indent(content + len(leading))
		var lines strings.Builder
		for _, command := range commands {
			lines.WriteString(indent + command + e.newline)
		}
		e.add(position, position, lines.String())
		return nil
	}
	e.add(content, content, strings.Join(commands, "; ")+"; ")
	return nil
}

// jenkinsCommandsPrecede reports whether each of commands runs as a
// top-level statement of an sh script before every statement at tests (its
// offsets in the script): on its own, not negated, backgrounded or
// redirected, as the shell parser reads it.
func jenkinsCommandsPrecede(file *syntax.File, commands []string, tests []int) bool {
	printed := func(command syntax.Command) string {
		var text strings.Builder
		if err := syntax.NewPrinter().Print(&text, command); err != nil {
			return ""
		}
		return text.String()
	}
	for _, command := range commands {
		parsed, ok := parseCIShell(command)
		if !ok || len(parsed.Stmts) != 1 {
			return false
		}
		want := printed(parsed.Stmts[0].Cmd)
		for _, test := range tests {
			if want == "" || !slices.ContainsFunc(file.Stmts, func(stmt *syntax.Stmt) bool {
				plain := !stmt.Negated && !stmt.Background && !stmt.Coprocess && len(stmt.Redirs) == 0
				return plain && int(stmt.Pos().Offset()) < test && printed(stmt.Cmd) == want
			}) {
				return false
			}
		}
	}
	return true
}

// snippet adds the setup snippet to the sh step of target before its test
// statement, and encloses the step in withCredentials when DD_API_KEY does
// not reach it. The script must be a single-quoted string literal without
// escapes, so its text is the source's: a multi-line one (sh ”'…”')
// gets the snippet's lines before the test statement's line, with its
// indentation; a one-line one (sh '…') becomes a multi-line one, its
// statements before the test one on a line of their own.
func (e *jenkinsEditor) snippet(target jenkinsSnippetTarget) error {
	var call *groovyCall
	var ancestors []groovyNode
	groovyVisit(e.tree, nil, func(node groovyNode, enclosing []groovyNode) {
		if current, ok := jenkinsStep(node, "sh"); ok && current.Start == target.call {
			call, ancestors = current, enclosing
		}
	})
	if call == nil {
		return e.gap(nil, "The sh step of "+target.job+" could not be located.")
	}
	if target.wrap && slices.ContainsFunc(ancestors, func(node groovyNode) bool {
		enclosing, ok := jenkinsStep(node, "withCredentials")
		if !ok {
			return false
		}
		positional, _ := jenkinsArgs(enclosing)
		return slices.ContainsFunc(jenkinsCredentialVariable.FindAllStringSubmatch(groovyText(e.source, firstNode(positional)), -1), func(match []string) bool { return match[2] == "DD_API_KEY" })
	}) {
		// A withCredentials around the sh step binds the key already, as
		// the check of the edited Jenkinsfile confirms.
		target.wrap = false
	}
	location := e.file + ":" + strconv.Itoa(groovyLine(e.source, call.Start))
	manual := func(reason string) error {
		return &EditThenApplyGap{SetupGap{location, reason + " Add the Datadog setup snippet to the sh script that runs the tests of " + target.job + ", before the test command, as onboard --instructions shows (with DD_API_KEY bound from a Jenkins credential), then rerun onboard --apply."}}
	}
	positional, named := jenkinsArgs(call)
	node := firstNode(positional)
	if value, ok := named["script"]; ok {
		node = value
	}
	script, ok := node.(*groovyString)
	if !ok || script.Value.quote != "'" && script.Value.quote != "'''" {
		return manual("The sh script that runs the tests is not a single-quoted string literal, which ddtest edits only.")
	}
	quote := script.Value.quote
	content, closing := script.Start+len(quote), script.End-len(quote)
	raw := e.source[content:closing]
	if text, literal := script.Value.literal(); !literal || text != raw {
		return manual("The sh script that runs the tests has escape sequences, which ddtest does not rewrite.")
	}
	if first, _, _ := strings.Cut(strings.TrimLeft(raw, " \t\r\n"), "\n"); strings.HasPrefix(first, "#!") {
		return manual("The sh script that runs the tests starts with a shebang, which ddtest does not edit.")
	}
	if slices.ContainsFunc(target.lines, func(line string) bool { return strings.Contains(line, "\\") || strings.Contains(line, "'''") }) {
		return manual("The setup snippet cannot be written into a single-quoted string as it is.")
	}
	offset := target.statement
	if offset < 0 || offset > len(raw) {
		return e.gap(call, "The test command's position in the sh script of "+target.job+" could not be located.")
	}
	outer := e.indent(call.Start)
	unit := e.unit(outer, "")
	if target.wrap {
		// The sh step moves one level in, into withCredentials.
		outer += unit
	}
	inner := outer + unit
	// The script with the snippet, and where it starts and ends in the file.
	var literal string
	lineStart := strings.LastIndex(raw[:offset], "\n") + 1
	switch {
	case quote == "'''" && lineStart > 0 && strings.TrimLeft(raw[lineStart:offset], " \t") == "":
		indent := raw[lineStart:offset]
		var lines strings.Builder
		for _, line := range target.lines {
			lines.WriteString(indent + line + e.newline)
		}
		literal = raw[:lineStart] + lines.String() + raw[lineStart:]
	case quote == "'":
		before := strings.TrimRight(raw[:offset], " \t")
		before = strings.TrimSpace(strings.TrimSuffix(before, ";"))
		if strings.HasSuffix(before, "&") {
			return manual("The test command follows a background command (&) in its sh script.")
		}
		var lines strings.Builder
		lines.WriteString(e.newline)
		if before != "" {
			lines.WriteString(inner + before + e.newline)
		}
		for _, line := range target.lines {
			lines.WriteString(inner + line + e.newline)
		}
		lines.WriteString(inner + strings.TrimSpace(raw[offset:]) + e.newline + outer)
		literal = lines.String()
	default:
		return manual("The test command does not start a line of its own in the sh script.")
	}
	text := "'''" + literal + "'''"
	if !target.wrap {
		e.add(script.Start, script.End, text)
		return nil
	}
	// withCredentials encloses the sh step's lines, which must be a
	// statement of their own.
	statement := len(ancestors) > 0 && slices.ContainsFunc(blockStatements(ancestors[len(ancestors)-1]), func(node groovyNode) bool { return node == call })
	if !statement || !e.startsLine(call.Start) || !e.endsLine(call.End) {
		return manual("DD_API_KEY does not reach this sh step, and it is not a statement on lines of its own that ddtest can enclose in withCredentials(" + jenkinsCredentialBinding + ") { … }.")
	}
	start, end := e.lineStart(call.Start), e.lineEnd(call.End)
	segment := e.source[start:script.Start] + text + e.source[script.End:end]
	stringStart, stringEnd := script.Start-start, script.Start-start+len(text)
	outer = e.indent(call.Start)
	var wrapped strings.Builder
	wrapped.WriteString(outer + "withCredentials(" + jenkinsCredentialBinding + ") {" + e.newline)
	for offset := 0; offset < len(segment); {
		next := strings.Index(segment[offset:], "\n")
		line := segment[offset:]
		if next >= 0 {
			line = segment[offset : offset+next+1]
		}
		if strings.TrimSpace(line) != "" && (offset <= stringStart || offset >= stringEnd) {
			wrapped.WriteString(unit)
		}
		wrapped.WriteString(line)
		offset += len(line)
	}
	wrapped.WriteString(e.newline + outer + "}")
	e.add(start, end, wrapped.String())
	return nil
}

// blockStatements are the statements of a block, or of a closure's.
func blockStatements(node groovyNode) []groovyNode {
	switch node := node.(type) {
	case *groovyBlock:
		return node.Statements
	case *groovyClosure:
		return node.Body.Statements
	}
	return nil
}
