// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"cmp"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// The interpreters below read a Jenkinsfile's syntax tree (see
// ci_provider_jenkins_groovy.go) into the shared workflow model: the
// declarative pipeline { … } in full and the literal subset of scripted
// pipelines. They thread an immutable jenkinsScope, so what a stage selects
// (agent, environment, directory, setup) never leaks to its siblings.

const (
	// jenkinsMatrixLimit bounds the cells of one matrix, and
	// jenkinsMatrixBudget those of all of a Jenkinsfile's matrices, nested
	// ones included.
	jenkinsMatrixLimit  = 200
	jenkinsMatrixBudget = 1000
	// jenkinsValueLimit bounds a value ddtest evaluates, such as a GString
	// that interpolates others.
	jenkinsValueLimit = 1 << 16
	// jenkinsNameLimit bounds a stage's name and a matrix axis's value,
	// which the names of the jobs inside repeat, and jenkinsAxisLimit the
	// axes of one matrix.
	jenkinsNameLimit = 256
	jenkinsAxisLimit = 32
)

// jenkinsAgent is where a scope's steps run.
type jenkinsAgent struct {
	// kind is "" (no agent yet), "none", "host" or "container".
	kind string
	// label is a host agent's label expression.
	label string
	// image is a container's literal image; describe names the container
	// construct for findings.
	image, describe string
	// problem is why ddtest cannot read the agent.
	problem string
}

// jenkinsScope is what a Jenkinsfile construct selects for the steps inside it.
type jenkinsScope struct {
	stages []string
	// env holds the environment's values; unknown ones are unresolved values.
	env map[string]string
	// vars holds Groovy variables with literal values.
	vars map[string]string
	// params holds parameter defaults.
	params map[string]string
	agent  jenkinsAgent
	setup  *jenkinsSetup
	// setupProblem is why a datadog(…) in scope cannot be certified, such
	// as one in a stage's options.
	setupProblem string
	dir          string
	// pathChange names a construct that puts another toolchain on PATH, such
	// as tools { nodejs 'X' }.
	pathChange string
	disabled   bool
	// problem makes shell steps in scope unresolved.
	problem string
	// suffix names a matrix cell, such as " (linux, 22)".
	suffix string
	// overrides are variables a narrower scope (stage environment, withEnv)
	// sets, by where it sets them.
	overrides map[string]string
	// shadow are the variables an environment directive, withEnv,
	// withCredentials or a matrix axis sets around the scope: an env.X
	// assignment changes the build's value, but the scope still reads theirs.
	shadow map[string]bool
	// globals are the build's values env.X assignments set, which outlive
	// the closure that assigns them; globals["PATH"] describes a PATH change.
	globals map[string]string
	// pod names a Kubernetes podTemplate around the scope; podLabel is its
	// literal label.
	pod, podLabel string
	// pipelineEnv is the pipeline's top-level environment, which the
	// Datadog Jenkins plugin composes its NODE_OPTIONS with (see
	// pluginExports).
	pipelineEnv map[string]string
	// envUnknown names an assignment to an environment variable whose name
	// ddtest cannot tell, such as env[name] = …: no variable of the build's
	// environment is known after it.
	envUnknown string
}

func (s jenkinsScope) with(change func(*jenkinsScope)) jenkinsScope {
	next := s
	next.stages = slices.Clone(s.stages)
	next.env = maps.Clone(s.env)
	next.vars = maps.Clone(s.vars)
	next.params = maps.Clone(s.params)
	next.overrides = maps.Clone(s.overrides)
	next.shadow = maps.Clone(s.shadow)
	next.globals = maps.Clone(s.globals)
	next.pipelineEnv = maps.Clone(s.pipelineEnv)
	if next.pipelineEnv == nil {
		next.pipelineEnv = map[string]string{}
	}
	if next.shadow == nil {
		next.shadow = map[string]bool{}
	}
	if next.globals == nil {
		next.globals = map[string]string{}
	}
	if next.env == nil {
		next.env = map[string]string{}
	}
	if next.vars == nil {
		next.vars = map[string]string{}
	}
	if next.params == nil {
		next.params = map[string]string{}
	}
	if next.overrides == nil {
		next.overrides = map[string]string{}
	}
	change(&next)
	return next
}

// jenkinsJob collects the steps of one job while its construct is read.
type jenkinsJob struct {
	name  string
	scope jenkinsScope
	steps []runtimeStep
	// pathChange is a PATH or runtime change a step ran under.
	pathChange string
}

// jenkinsSlot is the job steps go to, created when the first one is read.
type jenkinsSlot struct {
	name  string
	scope jenkinsScope
	job   *jenkinsJob
	// within names the job a container block's own job splits from (see
	// containerSlot).
	within string
}

type jenkinsReader struct {
	root, file, source string
	workflow           *ciWorkflow
	// functions are the Jenkinsfile's Groovy functions, by name.
	functions       map[string]*groovyBlock
	libraries       []string
	review, nonTest []string
	// workspace is what ${WORKSPACE} reads as: $WORKSPACE in a script, which
	// the shell reads as the checkout (see jenkinsShellDialect), and the
	// shared model's checkout expression in an environment value.
	workspace string
	// setups are the datadog(…) options whose scope includes container jobs.
	setups []*jenkinsSetup
	// cells counts the matrix cells expanded so far.
	cells int
}

// readJenkinsfile reads one Jenkinsfile into a workflow.
func readJenkinsfile(root, file, source string) (ciWorkflow, error) {
	tree, err := parseGroovy(source)
	if err != nil {
		return ciWorkflow{}, err
	}
	workflow := ciWorkflow{Path: file, Jobs: map[string]runtimeJob{}}
	r := &jenkinsReader{root: root, file: file, source: source, workflow: &workflow, functions: map[string]*groovyBlock{}, workspace: "$WORKSPACE"}
	for _, statement := range tree.Statements {
		switch node := statement.(type) {
		case *groovyFunction:
			r.functions[node.Name] = cmp.Or(node.Body, &groovyBlock{})
		case *groovyAnnotation:
			r.annotation(node)
		}
	}
	scope := jenkinsScope{}.with(func(*jenkinsScope) {})
	top := &jenkinsSlot{name: "top level", scope: scope}
	// Top-level statements run in order: a declaration before a declarative
	// pipeline is visible inside it, and one after it is not.
	for _, statement := range tree.Statements {
		if call, ok := statement.(*groovyCall); ok && call.Name == "pipeline" && call.Closure != nil && call.Receiver == nil {
			scope = r.persist(scope, r.pipeline(call.Closure.Body, scope), call.Closure.Body)
			continue
		}
		r.statement(statement, &scope, top)
	}
	r.finish(top)
	// The plugin's variables reach every job in an option's scope, so one
	// container job there leaves the option uncertified for all of them.
	for _, setup := range r.setups {
		for _, step := range setup.steps {
			if step.Problem == "" {
				step.Problem = setup.containerProblem()
			}
		}
	}
	slices.Sort(r.review)
	workflow.Review = slices.Compact(r.review)
	if len(r.nonTest) > 0 {
		slices.Sort(r.nonTest)
		workflow.Caveats = []string{file + ": Jenkins steps that only check out, archive, publish or notify run no tests and are outside this validation: " + strings.Join(slices.Compact(r.nonTest), ", ") + "."}
	}
	return workflow, nil
}

func (r *jenkinsReader) line(node groovyNode) int {
	start, _ := node.span()
	return groovyLine(r.source, start)
}

func (r *jenkinsReader) where(node groovyNode) string {
	return r.file + ":" + strconv.Itoa(r.line(node))
}

func (r *jenkinsReader) text(node groovyNode) string {
	return OneLineExcerpt(strings.TrimSpace(groovyText(r.source, node)))
}

func (r *jenkinsReader) annotation(node *groovyAnnotation) {
	if node.Name != "Library" {
		return
	}
	for _, arg := range node.Args {
		r.libraries = append(r.libraries, r.literals(arg.Value)...)
	}
}

// literals reads a string or a list of strings.
func (r *jenkinsReader) literals(node groovyNode) []string {
	switch value := node.(type) {
	case *groovyString:
		if text, ok := value.Value.literal(); ok {
			return []string{text}
		}
	case *groovyList:
		var items []string
		for _, item := range value.Items {
			items = append(items, r.literals(item)...)
		}
		return items
	}
	return nil
}

// libraryNote names where an unknown step may come from.
func (r *jenkinsReader) libraryNote() string {
	slices.Sort(r.libraries)
	r.libraries = slices.Compact(r.libraries)
	if len(r.libraries) == 0 {
		return "it may come from a shared library Jenkins loads implicitly"
	}
	return "it may come from the shared library " + strings.Join(r.libraries, ", ")
}

// value evaluates a Groovy expression to text: literals, and strings whose
// interpolations read known environment values, variables or parameter
// defaults. problem explains anything else.
func (r *jenkinsReader) value(node groovyNode, scope jenkinsScope) (value, problem string) {
	switch node := node.(type) {
	case *groovyString:
		return r.interpolate(node, scope)
	case *groovyLiteral:
		if node.Text == "null" {
			return "", "null"
		}
		return node.Text, ""
	case *groovyName:
		return r.reference(strings.Join(node.Path, "."), scope)
	case *groovyIndex:
		if key, isEnv := r.envTarget(node, scope); isEnv && key != "" {
			return r.reference("env."+key, scope)
		}
	case nil:
		return "", "a missing value"
	}
	return "", "the computed value " + r.text(node)
}

// envTarget reads a reference to an environment variable: env.X, or
// env['X'] and env[name] with a name ddtest can tell. isEnv reports one in
// env; key is empty when ddtest cannot tell its name.
func (r *jenkinsReader) envTarget(node groovyNode, scope jenkinsScope) (key string, isEnv bool) {
	switch node := node.(type) {
	case *groovyName:
		if len(node.Path) == 2 && node.Path[0] == "env" {
			if !jenkinsVariableName.MatchString(node.Path[1]) {
				return "", true // env."${name}"
			}
			return node.Path[1], true
		}
	case *groovyIndex:
		if receiver, ok := node.Receiver.(*groovyName); ok && len(receiver.Path) == 1 && receiver.Path[0] == "env" {
			key, problem := r.value(node.Index, scope)
			if problem != "" || !jenkinsVariableName.MatchString(key) {
				return "", true
			}
			return key, true
		}
	}
	return "", false
}

// jenkinsEnvIndex is env['X'] in an interpolation.
var jenkinsEnvIndex = regexp.MustCompile(`^env\[\s*(?:'([A-Za-z_][A-Za-z0-9_]*)'|"([A-Za-z_][A-Za-z0-9_]*)")\s*\]$`)

// jenkinsVariableName is an environment variable's name.
var jenkinsVariableName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// jenkinsEnvMethods set an environment variable: env.setProperty('X', v).
var jenkinsEnvMethods = []string{"env.setProperty", "env.put", "env.putAt"}

// envMutation reads a statement that may set a variable: an assignment, or
// env.setProperty(name, value) as the assignment env[name] = value. isEnv
// reports one that sets an environment variable; key is empty when ddtest
// cannot tell its name.
func (r *jenkinsReader) envMutation(node groovyNode, scope jenkinsScope) (assign *groovyAssign, key string, isEnv bool) {
	switch node := node.(type) {
	case *groovyAssign:
		key, isEnv := r.envTarget(node.Target, scope)
		return node, key, isEnv
	case *groovyCall:
		if node.Receiver != nil || !slices.Contains(jenkinsEnvMethods, node.Name) {
			return nil, "", false
		}
		positional, _ := jenkinsArgs(node)
		if len(positional) != 2 {
			return &groovyAssign{groovySpan: node.groovySpan, Op: "="}, "", true
		}
		assign := &groovyAssign{groovySpan: node.groovySpan, Target: &groovyIndex{groovySpan: node.groovySpan, Receiver: &groovyName{Path: []string{"env"}}, Index: positional[0]}, Op: "=", Value: positional[1]}
		key, isEnv := r.envTarget(assign.Target, scope)
		return assign, key, isEnv
	}
	return nil, "", false
}

// reference reads env.X, params.X, a variable, or an environment variable
// by its bare name.
func (r *jenkinsReader) reference(name string, scope jenkinsScope) (string, string) {
	name = strings.TrimSpace(name)
	if match := jenkinsEnvIndex.FindStringSubmatch(name); match != nil {
		name = "env." + cmp.Or(match[1], match[2]) // ${env['X']}
	}
	if key, ok := strings.CutPrefix(name, "params."); ok {
		if value, known := scope.params[key]; known {
			return value, ""
		}
		return "", "the build parameter " + name + ", which has no default in the Jenkinsfile"
	}
	key := strings.TrimPrefix(name, "env.")
	if key == "WORKSPACE" {
		return r.workspace, ""
	}
	if value, known := scope.vars[name]; known && key == name {
		if unresolvedCIError(value) != nil {
			return "", "${" + name + "}, which only the Jenkins build knows"
		}
		return value, ""
	}
	if scope.envUnknown != "" && !scope.shadow[key] {
		return "", "${" + name + "}, which " + scope.envUnknown + " may set"
	}
	if value, known := scope.env[key]; known && unresolvedCIError(value) == nil {
		return value, ""
	}
	return "", "${" + name + "}, which only the Jenkins build knows"
}

// jenkinsUnknownVariable is the value of a Groovy variable ddtest cannot
// tell, such as one a loop or a branch sets.
func jenkinsUnknownVariable(name string) string {
	return unresolvedCIValue(name)
}

// interpolate resolves a GString.
func (r *jenkinsReader) interpolate(node *groovyString, scope jenkinsScope) (string, string) {
	var text strings.Builder
	for _, part := range node.Value.parts {
		if !part.interpolated {
			text.WriteString(part.text)
			continue
		}
		if node.Value.quote == "/" || node.Value.quote == "$/" {
			return "", "the interpolated string " + r.text(node)
		}
		value, problem := r.reference(part.expr, scope)
		if problem != "" {
			return "", problem
		}
		if text.Len()+len(value) > jenkinsValueLimit {
			return "", fmt.Sprintf("the string %s, whose value is longer than %d bytes", r.text(node), jenkinsValueLimit)
		}
		text.WriteString(value)
	}
	return text.String(), ""
}

// args reads a call's positional and named arguments.
func jenkinsArgs(call *groovyCall) (positional []groovyNode, named map[string]groovyNode) {
	named = map[string]groovyNode{}
	for _, arg := range call.Args {
		if arg.Name == "" {
			positional = append(positional, arg.Value)
		} else {
			named[arg.Name] = arg.Value
		}
	}
	return positional, named
}

// directives indexes a declarative section's calls by name.
func jenkinsDirectives(body *groovyBlock) map[string]*groovyCall {
	directives := map[string]*groovyCall{}
	for _, statement := range body.Statements {
		switch node := statement.(type) {
		case *groovyCall:
			if node.Receiver == nil {
				if _, seen := directives[node.Name]; !seen {
					directives[node.Name] = node
				}
			}
		case *groovyName:
			if len(node.Path) == 1 {
				directives[node.Path[0]] = &groovyCall{groovySpan: node.groovySpan, Name: node.Path[0]}
			}
		}
	}
	return directives
}

// pipeline reads a declarative pipeline, and returns the scope its last
// statement leaves.
func (r *jenkinsReader) pipeline(body *groovyBlock, scope jenkinsScope) jenkinsScope {
	directives := jenkinsDirectives(body)
	if libraries, ok := directives["libraries"]; ok && libraries.Closure != nil {
		for _, statement := range libraries.Closure.Body.Statements {
			if call, ok := statement.(*groovyCall); ok && call.Name == "lib" {
				positional, _ := jenkinsArgs(call)
				for _, arg := range positional {
					r.libraries = append(r.libraries, r.literals(arg)...)
				}
			}
		}
	}
	environment := &jenkinsSlot{name: "environment"}
	scope = r.directives(directives, scope, true, environment)
	r.finish(environment)
	if stages, ok := directives["stages"]; ok && stages.Closure != nil {
		scope = r.stages(stages.Closure.Body, scope, false)
	}
	if post, ok := directives["post"]; ok {
		slot := &jenkinsSlot{name: "post", scope: scope}
		scope = r.post(post, scope, slot)
		r.finish(slot)
	}
	return scope
}

// directives applies a pipeline's or stage's agent, environment, options,
// tools, parameters and when to scope. The steps its environment values run
// go to slot, whose scope becomes the one returned: Jenkins runs them on the
// scope's agent when it enters the scope, before the scope's steps.
func (r *jenkinsReader) directives(directives map[string]*groovyCall, scope jenkinsScope, top bool, slot *jenkinsSlot) jenkinsScope {
	result := scope.with(func(next *jenkinsScope) {
		if parameters, ok := directives["parameters"]; ok && parameters.Closure != nil {
			for _, statement := range parameters.Closure.Body.Statements {
				call, ok := statement.(*groovyCall)
				if !ok {
					continue
				}
				_, named := jenkinsArgs(call)
				name, _ := r.value(named["name"], *next)
				if name == "" {
					continue
				}
				if value, problem := r.value(named["defaultValue"], *next); problem == "" {
					next.params[name] = value
				} else if choices, ok := named["choices"]; ok {
					if items := r.literals(choices); len(items) > 0 {
						next.params[name] = items[0]
					}
				}
			}
		}
		if agent, ok := directives["agent"]; ok {
			next.agent = r.agent(agent, *next)
		}
		if environment, ok := directives["environment"]; ok && environment.Closure != nil {
			if options, ok := directives["options"]; ok && top {
				// The plugin sets its variables for the environment's steps
				// too; the option is read again with the environment below.
				if setup, found := r.pipelineSetup(options, *next); found {
					next.setup = setup
				}
			}
			r.environment(environment.Closure.Body, next, top, slot)
		}
		if tools, ok := directives["tools"]; ok && tools.Closure != nil {
			for _, statement := range tools.Closure.Body.Statements {
				if call, ok := statement.(*groovyCall); ok {
					next.pathChange = "the Jenkins tool " + r.text(call) + " (tools directive)"
				}
			}
		}
		if options, ok := directives["options"]; ok && top {
			if setup, found := r.pipelineSetup(options, *next); found {
				next.setup = setup
			}
		} else if ok && options.Closure != nil {
			for _, statement := range options.Closure.Body.Statements {
				if call, ok := statement.(*groovyCall); ok && call.Name == "datadog" {
					if _, _, has := jenkinsTestOptimization(call); has {
						next.setupProblem = "The datadog(…) option at " + r.where(call) + " is in a stage's options; ddtest certifies the Datadog Jenkins plugin setup only in the pipeline's top-level options, which the plugin documents. Move it to the top-level options directive."
					}
				}
			}
		}
		if when, ok := directives["when"]; ok && when.Closure != nil && jenkinsNeverWhen(when.Closure.Body) {
			next.disabled = true
		}
	})
	// They run with the scope's agent and setup, which the directives after
	// the environment complete.
	slot.scope = result
	if slot.job != nil {
		slot.job.scope = result
	}
	return result
}

// pipelineSetup reads the datadog(…) option of a pipeline's top-level
// options with scope; found reports one.
func (r *jenkinsReader) pipelineSetup(options *groovyCall, scope jenkinsScope) (setup *jenkinsSetup, found bool) {
	if options.Closure == nil {
		return nil, false
	}
	for _, statement := range options.Closure.Body.Statements {
		if call, ok := statement.(*groovyCall); ok && call.Name == "datadog" {
			setup, found = r.datadog(call, scope), true
		}
	}
	return setup, found
}

// jenkinsNeverWhen reports a when directive that can never pass: a literal
// expression { false } or expression { return false }.
func jenkinsNeverWhen(body *groovyBlock) bool {
	for _, statement := range body.Statements {
		call, ok := statement.(*groovyCall)
		if !ok || call.Name != "expression" || call.Closure == nil || len(call.Closure.Body.Statements) != 1 {
			continue
		}
		value := call.Closure.Body.Statements[0]
		if ret, isReturn := value.(*groovyReturn); isReturn {
			value = ret.Value
		}
		if literal, isLiteral := value.(*groovyLiteral); isLiteral && literal.Text == "false" {
			return true
		}
	}
	return false
}

// environment reads an environment directive. Its values run what they
// call, such as X = sh(script: 'git rev-parse HEAD', returnStdout: true),
// to slot, in order; the value read from a command's output is unknown.
func (r *jenkinsReader) environment(body *groovyBlock, scope *jenkinsScope, top bool, slot *jenkinsSlot) {
	for _, statement := range body.Statements {
		assign, ok := statement.(*groovyAssign)
		if !ok {
			continue
		}
		name, isName := assign.Target.(*groovyName)
		if !isName || len(name.Path) != 1 {
			continue
		}
		if call, isCall := assign.Value.(*groovyCall); !isCall || call.Name != "credentials" {
			r.expression(assign.Value, scope, slot)
		}
		r.assignEnvironment(name.Path[0], assign.Value, scope, "the environment directive at "+r.where(assign), !top)
		scope.shadow[name.Path[0]] = true
	}
}

// assignEnvironment records one environment variable.
func (r *jenkinsReader) assignEnvironment(key string, node groovyNode, scope *jenkinsScope, where string, narrow bool) {
	if call, ok := node.(*groovyCall); ok && call.Name == "credentials" {
		scope.env[key] = jenkinsSecret(key)
		if key == "DD_API_KEY" {
			r.review = append(r.review, r.file+": "+where+" binds DD_API_KEY from a Jenkins credential; the Datadog Jenkins plugin overwrites DD_API_KEY with its own configured key, so this binding is not the key the tests report with.")
		}
		return
	}
	r.workspace = ciProjectDirectory
	value, problem := r.value(node, *scope)
	r.workspace = "$WORKSPACE"
	if key == "PATH" {
		// Prepending the checkout's node_modules/.bin selects the tools local
		// validation resolves from it too.
		if !jenkinsRepositoryBinPath(node) {
			scope.pathChange = "PATH set in " + where
		}
		return
	}
	if problem == "" && strings.HasPrefix(value, ciProjectDirectory+"/") && jenkinsInstallDirectory(key) {
		// Where the agent installs dependencies in the workspace: local
		// validation installs its own.
		r.review = append(r.review, r.file+": "+key+" ("+where+") points to an install directory in the Jenkins workspace; local validation installs dependencies itself, so it does not replay it.")
		return
	}
	if problem != "" {
		value = unresolvedCIValue(r.text(node))
	}
	if key == "DD_API_KEY" && problem == "" && value != "" {
		scope.setupProblem = jenkinsLiteralKeyProblem(where)
	}
	scope.env[key] = value
	if narrow {
		scope.overrides[key] = where
	} else {
		scope.pipelineEnv[key] = value
	}
}

// jenkinsLiteralKeyProblem refuses a DD_API_KEY written into the
// Jenkinsfile where it says.
func jenkinsLiteralKeyProblem(where string) string {
	return "DD_API_KEY is written into the Jenkinsfile (" + where + "); never commit the key: configure it as a Jenkins credential in the Datadog plugin's settings, and remove it from the Jenkinsfile."
}

// jenkinsInstallDirectory reports a variable that locates a package cache
// or install directory, such as a language's.
func jenkinsInstallDirectory(key string) bool {
	return strings.Contains(strings.ToLower(key), "cache") || slices.ContainsFunc(discoveryLanguages, func(language discoveryLanguage) bool { return language.cacheVariable(key) })
}

// jenkinsRepositoryBinPath reports a PATH that only prepends the checkout's
// node_modules/.bin to the agent's PATH.
func jenkinsRepositoryBinPath(node groovyNode) bool {
	value, ok := node.(*groovyString)
	if !ok {
		return false
	}
	var text strings.Builder
	for _, part := range value.Value.parts {
		if part.interpolated {
			text.WriteString("$" + strings.TrimPrefix(strings.TrimSpace(part.expr), "env."))
		} else {
			text.WriteString(part.text)
		}
	}
	parts := strings.Split(text.String(), ":")
	return len(parts) == 2 && slices.Contains([]string{"$WORKSPACE/node_modules/.bin", "./node_modules/.bin", "node_modules/.bin"}, strings.TrimSuffix(parts[0], "/")) && parts[1] == "$PATH"
}

// agent reads an agent directive.
func (r *jenkinsReader) agent(call *groovyCall, scope jenkinsScope) jenkinsAgent {
	positional, _ := jenkinsArgs(call)
	if call.Closure == nil {
		if len(positional) == 1 {
			if name, ok := positional[0].(*groovyName); ok && len(name.Path) == 1 {
				switch name.Path[0] {
				case "any":
					return jenkinsAgent{kind: "host", describe: "agent any"}
				case "none":
					return jenkinsAgent{kind: "none"}
				}
			}
		}
		return jenkinsAgent{kind: "host", problem: "its agent " + r.text(call) + " is not one ddtest can read"}
	}
	for _, statement := range call.Closure.Body.Statements {
		inner, ok := statement.(*groovyCall)
		if !ok {
			continue
		}
		positional, named := jenkinsArgs(inner)
		switch inner.Name {
		case "label":
			label := ""
			if len(positional) == 1 {
				label, _ = r.value(positional[0], scope)
			}
			return jenkinsAgent{kind: "host", label: label, describe: "agent { label '" + label + "' }"}
		case "node":
			label := ""
			if inner.Closure != nil {
				for _, item := range inner.Closure.Body.Statements {
					if labelCall, ok := item.(*groovyCall); ok && labelCall.Name == "label" {
						args, _ := jenkinsArgs(labelCall)
						if len(args) == 1 {
							label, _ = r.value(args[0], scope)
						}
					}
				}
			}
			if value, ok := named["label"]; ok {
				label, _ = r.value(value, scope)
			}
			return jenkinsAgent{kind: "host", label: label, describe: "agent { node { label '" + label + "' } }"}
		case "docker":
			image := ""
			if len(positional) == 1 {
				image, _ = r.value(positional[0], scope)
			}
			if value, ok := named["image"]; ok {
				image, _ = r.value(value, scope)
			}
			if inner.Closure != nil {
				for _, item := range inner.Closure.Body.Statements {
					if imageCall, ok := item.(*groovyCall); ok && imageCall.Name == "image" {
						args, _ := jenkinsArgs(imageCall)
						if len(args) == 1 {
							image, _ = r.value(args[0], scope)
						}
					}
				}
			}
			return jenkinsAgent{kind: "container", image: image, describe: "agent { docker { image '" + image + "' } }"}
		case "dockerfile":
			return jenkinsAgent{kind: "container", describe: "agent { dockerfile … }"}
		case "kubernetes":
			return jenkinsAgent{kind: "container", describe: "agent { kubernetes … }"}
		}
	}
	return jenkinsAgent{kind: "host", problem: "its agent " + r.text(call) + " is not one ddtest can read"}
}

// stages reads a stages section, in order, or a parallel section, whose
// stages run at the same time; it returns the scope after them.
func (r *jenkinsReader) stages(body *groovyBlock, scope jenkinsScope, concurrent bool) jenkinsScope {
	current := scope
	if concurrent {
		current = r.invalidate(scope, body)
	}
	var paths []jenkinsScope
	for _, statement := range body.Statements {
		if call, ok := statement.(*groovyCall); ok && call.Name == "stage" && call.Receiver == nil {
			if concurrent {
				paths = append(paths, r.stage(call, current))
				continue
			}
			current = r.stage(call, current)
		}
	}
	if concurrent && len(paths) > 0 {
		return r.merge(paths...)
	}
	return current
}

// stageName is a stage's literal name, or its position.
func (r *jenkinsReader) stageName(call *groovyCall, scope jenkinsScope) string {
	positional, named := jenkinsArgs(call)
	if len(positional) > 0 {
		if name, problem := r.value(positional[0], scope); problem == "" && len(name) <= jenkinsNameLimit {
			return name
		}
	}
	if value, ok := named["name"]; ok {
		if name, problem := r.value(value, scope); problem == "" && len(name) <= jenkinsNameLimit {
			return name
		}
	}
	return "stage at line " + strconv.Itoa(r.line(call))
}

// jobName joins a scope's stage path with name, adding a matrix cell.
func jobName(stages []string, name, suffix string) string {
	return strings.Join(append(slices.Clone(stages), name), " / ") + suffix
}

// stage reads a declarative stage: steps, nested stages, parallel stages or
// a matrix. It returns the scope after the stage: what its steps assign to
// the build's environment reaches the stages after it.
func (r *jenkinsReader) stage(call *groovyCall, scope jenkinsScope) jenkinsScope {
	name := r.stageName(call, scope)
	if call.Closure == nil {
		return scope
	}
	directives := jenkinsDirectives(call.Closure.Body)
	// A stage's environment runs before its steps, as part of its job; a
	// stage of stages runs it once, before them.
	slot := &jenkinsSlot{name: jobName(scope.stages, name, scope.suffix)}
	if directives["steps"] == nil {
		slot.name += " (environment)"
	}
	inner := r.directives(directives, scope, false, slot)
	if directives["steps"] == nil {
		r.finish(slot)
	}
	final := inner
	switch {
	case directives["steps"] != nil:
		if steps := directives["steps"]; steps.Closure != nil {
			final = r.block(steps.Closure.Body, inner, slot)
		}
		if post, ok := directives["post"]; ok {
			final = r.post(post, final, slot)
		}
		r.finish(slot)
	case directives["matrix"] != nil:
		final = r.matrix(directives["matrix"], inner.with(func(next *jenkinsScope) { next.stages = append(next.stages, name) }))
	case directives["stages"] != nil && directives["stages"].Closure != nil:
		final = r.stages(directives["stages"].Closure.Body, inner.with(func(next *jenkinsScope) { next.stages = append(next.stages, name) }), false)
	case directives["parallel"] != nil && directives["parallel"].Closure != nil:
		final = r.stages(directives["parallel"].Closure.Body, inner.with(func(next *jenkinsScope) { next.stages = append(next.stages, name) }), true)
	}
	if post, ok := directives["post"]; ok && directives["steps"] == nil {
		postSlot := &jenkinsSlot{name: jobName(scope.stages, name, scope.suffix) + " (post)", scope: inner}
		final = r.post(post, final, postSlot)
		r.finish(postSlot)
	}
	switch {
	case inner.disabled && !scope.disabled:
		return scope
	case directives["when"] != nil:
		// A when ddtest cannot decide may skip the stage.
		return r.merge(scope, r.persist(scope, final, call.Closure.Body))
	}
	return r.persist(scope, final, call.Closure.Body)
}

// jenkinsPassingPost are the post conditions that run when the build passes.
var jenkinsPassingPost = []string{"always", "success", "cleanup", "changed", "fixed"}

// post reads a post section: conditions that run on a passing build add
// their steps to slot; the others are reported. It returns the scope after
// the section.
func (r *jenkinsReader) post(call *groovyCall, scope jenkinsScope, slot *jenkinsSlot) jenkinsScope {
	if call.Closure == nil {
		return scope
	}
	for _, statement := range call.Closure.Body.Statements {
		condition, ok := statement.(*groovyCall)
		if !ok || condition.Closure == nil {
			continue
		}
		if slices.Contains(jenkinsPassingPost, condition.Name) {
			final := r.persist(scope, r.block(condition.Closure.Body, scope, slot), condition.Closure.Body)
			if condition.Name == "changed" || condition.Name == "fixed" {
				// They run on some passing builds only.
				final = r.merge(scope, final)
			}
			scope = final
			continue
		}
		if jenkinsRunsCommands(condition.Closure.Body) {
			r.review = append(r.review, r.file+": the post { "+condition.Name+" } block at "+r.where(condition)+" runs only when the build does not pass, so its commands are outside this validation.")
		}
	}
	return scope
}

// jenkinsRunsCommands reports whether a block calls sh, bat or powershell.
func jenkinsRunsCommands(body *groovyBlock) bool {
	found := false
	var visit func(node groovyNode)
	visit = func(node groovyNode) {
		switch node := node.(type) {
		case *groovyCall:
			if slices.Contains([]string{"sh", "bat", "powershell", "pwsh"}, node.Name) {
				found = true
			}
			if node.Closure != nil {
				for _, inner := range node.Closure.Body.Statements {
					visit(inner)
				}
			}
		case *groovyControl:
			for _, block := range append(append(slices.Clone(node.Blocks), node.Catches...), node.Finally) {
				if block != nil {
					for _, inner := range block.Statements {
						visit(inner)
					}
				}
			}
		}
	}
	for _, statement := range body.Statements {
		visit(statement)
	}
	return found
}

// matrix expands a matrix section into one stage set per cell; the cells
// run at the same time. It returns the scope after them.
func (r *jenkinsReader) matrix(call *groovyCall, scope jenkinsScope) jenkinsScope {
	where := jobName(scope.stages[:len(scope.stages)-1], scope.stages[len(scope.stages)-1], scope.suffix)
	if call.Closure == nil {
		return scope
	}
	directives := jenkinsDirectives(call.Closure.Body)
	type axis struct {
		name   string
		values []string
	}
	var axes []axis
	problem := ""
	if section, ok := directives["axes"]; ok && section.Closure != nil {
		for _, statement := range section.Closure.Body.Statements {
			definition, ok := statement.(*groovyCall)
			if !ok || definition.Name != "axis" || definition.Closure == nil {
				continue
			}
			current := axis{}
			for _, item := range definition.Closure.Body.Statements {
				field, ok := item.(*groovyCall)
				if !ok {
					continue
				}
				positional, _ := jenkinsArgs(field)
				switch field.Name {
				case "name":
					if len(positional) == 1 {
						current.name, _ = r.value(positional[0], scope)
					}
				case "values":
					for _, value := range positional {
						text, valueProblem := r.value(value, scope)
						switch {
						case valueProblem != "":
							problem = "the axis values " + r.text(field) + " are not literal"
						case len(text) > jenkinsNameLimit:
							problem = fmt.Sprintf("an axis value is longer than %d bytes", jenkinsNameLimit)
						}
						current.values = append(current.values, text)
					}
				}
			}
			if current.name == "" || len(current.values) == 0 {
				problem = "the axis " + r.text(definition) + " has no literal name and values"
			}
			axes = append(axes, current)
		}
	}
	if len(axes) > jenkinsAxisLimit {
		problem = fmt.Sprintf("it has more than %d axes", jenkinsAxisLimit)
	}
	if len(axes) == 0 {
		problem = "it has no axes ddtest can read"
		if _, ok := directives["axes"]; ok {
			problem = "its axes are not literal"
		}
	}
	// The product is bounded before it is built, and with the cells the
	// Jenkinsfile's other matrices expand, since each cell reads its stages
	// again.
	count := 1
	for _, current := range axes {
		if len(current.values) == 0 || problem != "" {
			break // Reported as an axis without values.
		}
		if len(current.values) > jenkinsMatrixLimit/count {
			count = jenkinsMatrixLimit + 1
			break
		}
		count *= len(current.values)
	}
	switch {
	case problem != "":
	case count > jenkinsMatrixLimit:
		problem = fmt.Sprintf("it has more than %d cells", jenkinsMatrixLimit)
	case r.cells+count > jenkinsMatrixBudget:
		problem = fmt.Sprintf("with the Jenkinsfile's other matrices, it has more than %d cells", jenkinsMatrixBudget)
	}
	cells := []map[string]string{{}}
	for _, current := range axes {
		if problem != "" {
			break
		}
		next := make([]map[string]string, 0, len(cells)*len(current.values))
		for _, cell := range cells {
			for _, value := range current.values {
				combined := maps.Clone(cell)
				combined[current.name] = value
				next = append(next, combined)
			}
		}
		cells = next
	}
	if problem != "" {
		slot := &jenkinsSlot{name: where, scope: scope}
		r.unresolved(slot, call, "ddtest cannot expand its matrix ("+problem+"), so it cannot tell which stages it runs")
		r.finish(slot)
		return r.invalidate(scope, call.Closure.Body)
	}
	excluded := func(cell map[string]string) bool {
		section, ok := directives["excludes"]
		if !ok || section.Closure == nil {
			return false
		}
		for _, statement := range section.Closure.Body.Statements {
			exclude, ok := statement.(*groovyCall)
			if !ok || exclude.Name != "exclude" || exclude.Closure == nil {
				continue
			}
			matches := true
			for _, item := range exclude.Closure.Body.Statements {
				definition, ok := item.(*groovyCall)
				if !ok || definition.Name != "axis" || definition.Closure == nil {
					continue
				}
				name, inValues, negated := "", false, false
				for _, field := range definition.Closure.Body.Statements {
					fieldCall, ok := field.(*groovyCall)
					if !ok {
						continue
					}
					positional, _ := jenkinsArgs(fieldCall)
					switch fieldCall.Name {
					case "name":
						if len(positional) == 1 {
							name, _ = r.value(positional[0], scope)
						}
					case "values", "notValues":
						negated = fieldCall.Name == "notValues"
						for _, value := range positional {
							text, _ := r.value(value, scope)
							inValues = inValues || text == cell[name]
						}
					}
				}
				matches = matches && inValues != negated
			}
			if matches {
				return true
			}
		}
		return false
	}
	r.cells += len(cells)
	start := r.invalidate(scope, call.Closure.Body)
	var paths []jenkinsScope
	for _, cell := range cells {
		if excluded(cell) {
			continue
		}
		var values []string
		for _, current := range axes {
			values = append(values, cell[current.name])
		}
		cellScope := start.with(func(next *jenkinsScope) {
			maps.Copy(next.env, cell)
			for key := range cell {
				next.shadow[key] = true
			}
			next.suffix = scope.suffix + " (" + strings.Join(values, ", ") + ")"
		})
		cellDirectives := map[string]*groovyCall{}
		for _, key := range []string{"agent", "environment", "tools", "when"} {
			if directive, ok := directives[key]; ok {
				cellDirectives[key] = directive
			}
		}
		environment := &jenkinsSlot{name: strings.Join(scope.stages, " / ") + cellScope.suffix + " (environment)"}
		cellScope = r.directives(cellDirectives, cellScope, false, environment)
		r.finish(environment)
		if stages, ok := directives["stages"]; ok && stages.Closure != nil && !cellScope.disabled {
			paths = append(paths, r.persist(start, r.stages(stages.Closure.Body, cellScope, false), call.Closure.Body))
		} else if stages, ok := directives["stages"]; ok && stages.Closure != nil {
			r.stages(stages.Closure.Body, cellScope, false)
		}
	}
	if len(paths) == 0 {
		return scope
	}
	return r.merge(append(paths, start)...)
}

// jenkinsNonCommandSteps run nothing ddtest replays: they check out,
// archive, publish, notify or read files.
var jenkinsNonCommandSteps = []string{
	"echo", "error", "unstable", "print", "println", "checkout", "git", "junit", "archiveArtifacts", "archive", "stash", "unstash",
	"cleanWs", "deleteDir", "publishHTML", "recordIssues", "recordCoverage", "publishCoverage", "cobertura", "jacoco",
	"slackSend", "emailext", "mail", "milestone", "input", "githubNotify", "setGitHubPullRequestStatus", "step",
	"writeFile", "readFile", "readJSON", "readYaml", "readProperties", "writeJSON", "writeYaml", "fileExists",
	"findFiles", "sleep", "isUnix", "pwd", "properties", "office365ConnectorSend", "notifyBitbucket",
	"bitbucketStatusNotify", "updateGitlabCommitStatus", "addBadge", "addShortText", "publishChecks",
	"discoverGitReferenceBuild", "scanForIssues", "copyArtifacts", "setBuildStatus", "httpRequest", "buildName",
	"buildDescription", "zip", "unzip", "touch", "library", "tool", "waitForQualityGate", "fingerprint",
	"dockerFingerprintFrom", "s3Upload", "s3Download", "rtUpload", "currentBuild", "scm", "lastChanges",
}

// jenkinsConfigurationSteps take configuration: the calls in their
// arguments, such as recordIssues(tools: [esLint()]) or
// checkout(scmGit(…)), name plugin components (symbols) and run no step.
var jenkinsConfigurationSteps = []string{
	"checkout", "properties", "step", "junit", "recordIssues", "recordCoverage", "publishCoverage", "publishHTML",
	"scanForIssues", "publishChecks", "cobertura", "jacoco", "archiveArtifacts", "discoverGitReferenceBuild",
	"copyArtifacts", "library",
}

// configuration runs what a configuration step's argument runs: a call to a
// plugin symbol, a bare name without a closure that is no step ddtest
// reads, runs nothing but its arguments.
func (r *jenkinsReader) configuration(node groovyNode, scope *jenkinsScope, slot *jenkinsSlot) {
	switch node := node.(type) {
	case *groovyCall:
		step := slices.Contains(jenkinsExecutableSteps, node.Name) || r.functions[node.Name] != nil || strings.Contains(node.Name, ".")
		if node.Receiver != nil || node.Closure != nil || step {
			r.expression(node, scope, slot)
			return
		}
		for _, arg := range node.Args {
			r.configuration(arg.Value, scope, slot)
		}
	case *groovyList:
		for _, item := range node.Items {
			r.configuration(item, scope, slot)
		}
	case *groovyMap:
		for _, entry := range node.Entries {
			r.configuration(entry.Value, scope, slot)
		}
	default:
		r.expression(node, scope, slot)
	}
}

// jenkinsWrappers run their closure's steps in the same place, with the same
// environment and toolchain.
var jenkinsWrappers = []string{
	"timeout", "retry", "timestamps", "ansiColor", "lock", "catchError", "warnError", "sshagent", "wrap", "script",
	"withSonarQubeEnv", "configFileProvider", "withFolderProperties", "throttle", "waitUntil", "withAWS", "withVault",
	"withDockerRegistry", "withDockerServer", "withKubeConfig", "xvfb", "gitlabCommitStatus", "gitlabBuilds",
	"withChecks", "docker.withRegistry", "docker.withServer", "withNPM",
}

// jenkinsRepeatingWrappers may run their closure more than once, or stop it
// part way and let the build go on.
var jenkinsRepeatingWrappers = []string{"retry", "waitUntil", "catchError", "warnError"}

// jenkinsToolWrappers put a Jenkins tool installation on PATH for their
// closure.
var jenkinsToolWrappers = []string{"nodejs", "nvm", "rbenv", "rvm", "withAnt", "withGoEnv"}

// jenkinsValueFunctions compute a value and run no step: Groovy functions,
// and the credential, parameter and property definitions steps take.
var jenkinsValueFunctions = []string{
	"sprintf", "libraryResource", "docker.image", "string", "usernamePassword", "usernameColonPassword", "file",
	"sshUserPrivateKey", "certificate", "dockerCert", "booleanParam", "choice", "text", "password", "credentials",
	"parameters", "pipelineTriggers", "cron", "pollSCM", "buildDiscarder", "logRotator", "disableConcurrentBuilds",
}

// jenkinsValueMethods are Groovy and Java methods of strings, numbers and
// collections: called on a value, they run no step, though a closure they
// take may run any number of times.
var jenkinsValueMethods = []string{
	"equals", "equalsIgnoreCase", "trim", "toString", "startsWith", "endsWith", "contains", "matches", "split",
	"tokenize", "replace", "replaceAll", "replaceFirst", "toLowerCase", "toUpperCase", "toInteger", "toBoolean",
	"toLong", "toDouble", "size", "isEmpty", "length", "substring", "take", "drop", "join", "indexOf",
	"lastIndexOf", "find", "findAll", "findResult", "any", "every", "collect", "collectEntries", "each",
	"eachWithIndex", "inject", "sort", "unique", "reverse", "first", "last", "get", "getAt", "put", "add",
	"addAll", "remove", "containsKey", "keySet", "values", "entrySet", "plus", "minus", "format", "padLeft",
	"padRight", "readLines", "asBoolean", "asType", "isNumber", "isInteger", "stripIndent", "stripMargin",
	"capitalize", "count", "max", "min", "sum", "flatten", "grep", "toList", "toSet", "subList", "times",
}

// block reads statements in order: assignments to env and variables reach
// later statements of the block. It returns the scope after the block.
func (r *jenkinsReader) block(body *groovyBlock, scope jenkinsScope, slot *jenkinsSlot) jenkinsScope {
	current := scope
	for _, statement := range body.Statements {
		r.statement(statement, &current, slot)
	}
	return current
}

func (r *jenkinsReader) statement(statement groovyNode, scope *jenkinsScope, slot *jenkinsSlot) {
	switch node := statement.(type) {
	case *groovyCall:
		r.call(node, scope, slot)
	case *groovyName:
		r.call(&groovyCall{groovySpan: node.groovySpan, Name: strings.Join(node.Path, "."), NameStart: node.Start}, scope, slot)
	case *groovyAssign:
		r.assign(node, scope, slot)
	case *groovyControl:
		r.control(node, scope, slot)
	case *groovyFunction:
		r.functions[node.Name] = cmp.Or(node.Body, &groovyBlock{})
	case *groovyAnnotation:
		r.annotation(node)
	case *groovyReturn:
		// A returned expression still runs: return sh('npm test').
		r.expression(node.Value, scope, slot)
	case *groovyAssert:
		// assert sh(script: 'npm test', returnStatus: true) == 0 runs the
		// sh; a passing build passes the assertion.
		r.expression(node.Value, scope, slot)
		r.expression(node.Message, scope, slot)
	case *groovyTernary, *groovyString:
		r.expression(node, scope, slot)
	case *groovyLiteral, *groovyJump:
	case *groovyBinary:
		if node.Op == "++" || node.Op == "--" {
			return // A counter changes no step.
		}
		r.expression(node, scope, slot)
		r.unresolved(slot, statement, "the Groovy statement "+r.text(statement)+" is not one ddtest can read")
	default:
		r.unresolved(slot, statement, "the Groovy statement "+r.text(statement)+" is not one ddtest can read")
	}
}

// assign records env.X = value, env['X'] = value and def x = value.
func (r *jenkinsReader) assign(node *groovyAssign, scope *jenkinsScope, slot *jenkinsSlot) {
	// The value still runs: def out = sh(…).trim(), def result = runTests().
	r.expression(node.Value, scope, slot)
	r.setVariable(node, scope)
}

// setVariable records what an assignment, or env.setProperty(…), sets. An
// environment variable whose name ddtest cannot tell makes the whole
// environment unknown after it.
func (r *jenkinsReader) setVariable(node groovyNode, scope *jenkinsScope) {
	assign, key, isEnv := r.envMutation(node, *scope)
	if assign == nil {
		return
	}
	*scope = scope.with(func(next *jenkinsScope) {
		if isEnv {
			if key == "" {
				next.envUnknown = cmp.Or(next.envUnknown, r.text(node)+" at "+r.where(node))
				return
			}
			r.assignGlobal(key, assign, next)
			return
		}
		target, ok := assign.Target.(*groovyName)
		if !ok || len(target.Path) != 1 {
			return
		}
		value, problem := r.value(assign.Value, *next)
		if assign.Op == "=" && problem == "" {
			next.vars[target.Path[0]] = value
		} else {
			next.vars[target.Path[0]] = jenkinsUnknownVariable(target.Path[0])
		}
	})
}

// assignGlobal records env.X = value: the build's value of X from then on,
// which the scope reads unless an environment directive or withEnv around
// it sets X.
func (r *jenkinsReader) assignGlobal(key string, node *groovyAssign, next *jenkinsScope) {
	where := "env." + key + " at " + r.where(node)
	previous, had := next.env[key]
	previousOverride, overridden := next.overrides[key]
	pathChange := next.pathChange
	if node.Op == "=" {
		r.assignEnvironment(key, node.Value, next, where, true)
	} else {
		next.env[key] = unresolvedCIValue(r.text(node))
		next.overrides[key] = where
	}
	if key == "PATH" {
		if next.pathChange != pathChange {
			next.globals[key] = next.pathChange
		}
		return
	}
	if value, set := next.env[key]; set {
		next.globals[key] = value
	}
	if !next.shadow[key] {
		return
	}
	if had {
		next.env[key] = previous
	} else {
		delete(next.env, key)
	}
	if overridden {
		next.overrides[key] = previousOverride
	} else {
		delete(next.overrides, key)
	}
}

// persist carries what a closure's statements assign past it: the build's
// environment (env.X), PATH, and the script variables the closure does not
// declare.
func (r *jenkinsReader) persist(outer, inner jenkinsScope, body *groovyBlock) jenkinsScope {
	declared := map[string]bool{}
	jenkinsVisit(body, func(node groovyNode) {
		if assign, ok := node.(*groovyAssign); ok && assign.Declared {
			if target, ok := assign.Target.(*groovyName); ok && len(target.Path) == 1 {
				declared[target.Path[0]] = true
			}
		}
	})
	return outer.with(func(next *jenkinsScope) {
		next.envUnknown = cmp.Or(next.envUnknown, inner.envUnknown)
		for key, value := range inner.globals {
			if old, known := outer.globals[key]; known && old == value {
				continue
			}
			next.globals[key] = value
			if key == "PATH" {
				if value != "" {
					next.pathChange = value
				}
				continue
			}
			if key == "DD_API_KEY" && value != "" && !strings.Contains(value, "${{") {
				next.setupProblem = joinDiagnostic(next.setupProblem, jenkinsLiteralKeyProblem(inner.overrides[key]))
			}
			if !outer.shadow[key] {
				next.env[key] = value
				next.overrides[key] = cmp.Or(inner.overrides[key], "env."+key)
			}
		}
		for _, name := range slices.Collect(maps.Keys(mergeEnvironment(outer.vars, inner.vars))) {
			if declared[name] {
				continue
			}
			if value, known := inner.vars[name]; known {
				next.vars[name] = value
			} else {
				delete(next.vars, name)
			}
		}
	})
}

// merge joins the scopes after paths of which any one may run: a value the
// paths disagree on is unknown after them.
func (r *jenkinsReader) merge(paths ...jenkinsScope) jenkinsScope {
	return paths[0].with(func(next *jenkinsScope) {
		agree := func(read func(jenkinsScope) (string, bool)) bool {
			first, firstSet := read(paths[0])
			for _, path := range paths[1:] {
				if value, set := read(path); set != firstSet || value != first {
					return false
				}
			}
			return true
		}
		for _, path := range paths {
			for key := range path.env {
				if !agree(func(s jenkinsScope) (string, bool) { v, ok := s.env[key]; return v, ok }) {
					next.env[key] = unresolvedCIValue("env." + key + ", which the Jenkinsfile sets on some paths only")
					next.overrides[key] = cmp.Or(next.overrides[key], path.overrides[key], "env."+key)
				}
			}
			for key, value := range path.globals {
				switch {
				case agree(func(s jenkinsScope) (string, bool) { v, ok := s.globals[key]; return v, ok }):
				case key == "PATH":
					next.globals[key] = cmp.Or(next.globals[key], value)
				default:
					next.globals[key] = unresolvedCIValue("env." + key + ", which the Jenkinsfile sets on some paths only")
				}
			}
			for name := range path.vars {
				if !agree(func(s jenkinsScope) (string, bool) { v, ok := s.vars[name]; return v, ok }) {
					next.vars[name] = jenkinsUnknownVariable(name)
				}
			}
			next.pathChange = cmp.Or(next.pathChange, path.pathChange)
			next.envUnknown = cmp.Or(next.envUnknown, path.envUnknown)
			next.setupProblem = cmp.Or(next.setupProblem, path.setupProblem)
		}
	})
}

// invalidate forgets what blocks may assign: the scope a block starts from
// when it may run after part of itself or of another block.
func (r *jenkinsReader) invalidate(scope jenkinsScope, blocks ...*groovyBlock) jenkinsScope {
	return scope.with(func(next *jenkinsScope) {
		for _, block := range blocks {
			jenkinsVisit(block, func(node groovyNode) {
				if control, ok := node.(*groovyControl); ok {
					// A loop's header binds its variables.
					for _, name := range control.Variables {
						next.vars[name] = jenkinsUnknownVariable(name)
					}
					return
				}
				assign, key, isEnv := r.envMutation(node, scope)
				switch {
				case assign == nil:
				case isEnv && key == "":
					next.envUnknown = cmp.Or(next.envUnknown, r.text(node)+" at "+r.where(node))
				case isEnv:
					unknown := unresolvedCIValue(r.text(node))
					next.globals[key] = unknown
					if key == "PATH" {
						next.globals[key] = "PATH set in " + r.where(node)
						next.pathChange = cmp.Or(next.pathChange, next.globals[key])
					} else if !next.shadow[key] {
						next.env[key] = unknown
						next.overrides[key] = "env." + key + " at " + r.where(node)
					}
				default:
					// Only a known variable: an environment directive's
					// NAME = value is no Groovy variable.
					if target, ok := assign.Target.(*groovyName); ok && len(target.Path) == 1 {
						if _, known := next.vars[target.Path[0]]; known {
							next.vars[target.Path[0]] = jenkinsUnknownVariable(target.Path[0])
						}
					}
				}
			})
		}
	})
}

// jenkinsVisit calls visit on every statement a block runs, in closures and
// control blocks too, but not in the functions it defines.
func jenkinsVisit(block *groovyBlock, visit func(groovyNode)) {
	if block == nil {
		return
	}
	for _, statement := range block.Statements {
		visit(statement)
		switch node := statement.(type) {
		case *groovyCall:
			if node.Closure != nil {
				jenkinsVisit(node.Closure.Body, visit)
			}
			for _, arg := range node.Args {
				if closure, ok := arg.Value.(*groovyClosure); ok {
					jenkinsVisit(closure.Body, visit)
				}
			}
		case *groovyControl:
			if node.Init != nil {
				visit(node.Init)
			}
			for _, inner := range append(append(slices.Clone(node.Blocks), node.Catches...), node.Finally) {
				jenkinsVisit(inner, visit)
			}
		}
	}
}

// expression runs the steps evaluating an expression runs: an sh whose
// output is used, or a step or Groovy function whose result is assigned or
// returned, which may run tests. A method on a value, such as .trim() or
// env.BRANCH_NAME.startsWith(…), runs none.
func (r *jenkinsReader) expression(node groovyNode, scope *jenkinsScope, slot *jenkinsSlot) {
	switch node := node.(type) {
	case *groovyCall:
		if node.Receiver != nil {
			if node.Closure != nil {
				r.method(node, scope, slot)
				return
			}
			r.expression(node.Receiver, scope, slot)
			for _, arg := range node.Args {
				r.expression(arg.Value, scope, slot)
			}
			return
		}
		r.call(node, scope, slot)
	case *groovyBinary:
		r.expression(node.Left, scope, slot)
		r.expression(node.Right, scope, slot)
	case *groovyIndex:
		r.expression(node.Receiver, scope, slot)
		r.expression(node.Index, scope, slot)
	case *groovyList:
		for _, item := range node.Items {
			r.expression(item, scope, slot)
		}
	case *groovyMap:
		for _, entry := range node.Entries {
			r.expression(entry.Value, scope, slot)
		}
	case *groovyTernary:
		// Either branch may run, as an if/else's.
		r.expression(node.Condition, scope, slot)
		then, otherwise := *scope, *scope
		r.expression(node.Then, &then, slot)
		r.expression(node.Else, &otherwise, slot)
		*scope = r.merge(then, otherwise)
	case *groovyString:
		// "${sh(script: 'npm test', returnStdout: true)}" runs the sh.
		for _, interpolation := range node.Interpolations {
			r.expression(interpolation, scope, slot)
		}
		if quote := node.Value.quote; (quote == "/" || quote == "$/") && len(node.Value.parts) == 1 && node.Value.parts[0].interpolated {
			// A slashy string's interpolations are not read.
			r.opaque(node, node.Value.parts[0].text, slot)
		}
	case *groovyUnknown:
		r.opaque(node, groovyText(r.source, node), slot)
	}
}

// jenkinsExecutableSteps run commands or code ddtest may not read.
var jenkinsExecutableSteps = []string{"sh", "bat", "powershell", "pwsh", "load", "evaluate", "build"}

// jenkinsShellStepName finds a shell step's name in text.
var jenkinsShellStepName = regexp.MustCompile(`\b(?:sh|bat|powershell|pwsh)\b`)

// opaque reports syntax ddtest cannot read, node with source text, that
// calls a step which may run tests, such as sh in new File(sh(…)), as
// unresolved: it is never dropped.
func (r *jenkinsReader) opaque(node groovyNode, text string, slot *jenkinsSlot) {
	if called := r.opaqueCall(text); called != "" {
		r.unresolved(slot, node, "the Groovy expression "+r.text(node)+" is not one ddtest can read, and it calls "+called+", which may run tests")
	}
}

// opaqueCall is the first step text calls that may run tests: sh, bat,
// powershell, load, a Groovy function of the Jenkinsfile, or a step ddtest
// does not know; a method on a value or a constructor (new File(…)) is
// none. Empty when it calls none.
func (r *jenkinsReader) opaqueCall(text string) string {
	tokens, err := lexGroovy(text)
	if err != nil {
		// Text ddtest cannot even split: any shell step name in it may run.
		return jenkinsShellStepName.FindString(text)
	}
	for i, token := range tokens {
		if token.kind != groovyIdent || i+1 >= len(tokens) {
			continue
		}
		next := tokens[i+1]
		calls := next.kind == groovyPunct && (next.text == "(" || next.text == "{") || next.kind == groovyStringToken && next.spaced
		if !calls || i > 0 && (tokens[i-1].kind == groovyPunct && slices.Contains([]string{".", "?.", "*.", ".&", "@"}, tokens[i-1].text) || tokens[i-1].kind == groovyIdent && slices.Contains([]string{"new", "def", "void"}, tokens[i-1].text)) {
			continue
		}
		name := token.text
		switch {
		case slices.Contains(jenkinsExecutableSteps, name) || r.functions[name] != nil:
			return name
		case name == "$", name[0] >= 'A' && name[0] <= 'Z', slices.Contains([]string{"if", "for", "while", "switch", "catch", "return", "assert", "throw"}, name):
		case slices.Contains(jenkinsNonCommandSteps, name), slices.Contains(jenkinsValueFunctions, name), slices.Contains(jenkinsValueMethods, name), slices.Contains(jenkinsWrappers, name), slices.Contains(jenkinsToolWrappers, name):
		default:
			return name
		}
	}
	return ""
}

// argumentSteps runs what a step's arguments run before the step, for the
// steps that read their arguments as values: the interpolations of their
// strings, and the calls among them that run steps, such as
// dir(sh(script: …, returnStdout: true).trim()). A call among them that
// runs none, such as configFile(…), names a plugin component.
func (r *jenkinsReader) argumentSteps(node groovyNode, scope *jenkinsScope, slot *jenkinsSlot) {
	switch node := node.(type) {
	case *groovyString, *groovyTernary, *groovyUnknown:
		r.expression(node, scope, slot)
	case *groovyList:
		for _, item := range node.Items {
			r.argumentSteps(item, scope, slot)
		}
	case *groovyMap:
		for _, entry := range node.Entries {
			r.argumentSteps(entry.Value, scope, slot)
		}
	case *groovyBinary:
		r.argumentSteps(node.Left, scope, slot)
		r.argumentSteps(node.Right, scope, slot)
	case *groovyIndex:
		r.argumentSteps(node.Receiver, scope, slot)
		r.argumentSteps(node.Index, scope, slot)
	case *groovyCall:
		if node.Receiver == nil && (slices.Contains(jenkinsExecutableSteps, node.Name) || r.functions[node.Name] != nil) {
			r.expression(node, scope, slot)
			return
		}
		if node.Receiver != nil {
			r.argumentSteps(node.Receiver, scope, slot)
		}
		for _, arg := range node.Args {
			r.argumentSteps(arg.Value, scope, slot)
		}
	}
}

// jenkinsValueMethod reports a call that computes a value: a value function,
// a method on env, params, currentBuild, scm or a Java class, or a Groovy
// value method such as tag.trim().
func jenkinsValueMethod(name string) bool {
	if slices.Contains(jenkinsValueFunctions, name) {
		return true
	}
	head, _, dotted := strings.Cut(name, ".")
	if !dotted || head == "" {
		return false
	}
	method := name[strings.LastIndex(name, ".")+1:]
	return slices.Contains([]string{"env", "params", "currentBuild", "scm", "String", "Integer", "Long", "Double", "Boolean", "Math", "System", "Arrays", "Collections", "UUID", "URLEncoder"}, head) || slices.Contains(jenkinsValueMethods, method)
}

// control reads if/else, loops, switch and try: every branch that can run on
// a passing build is a possible path, a catch block included, since it can
// recover.
func (r *jenkinsReader) control(node *groovyControl, scope *jenkinsScope, slot *jenkinsSlot) {
	if node.Init != nil {
		// for (init; …): an unknown header is unresolved.
		r.statement(node.Init, scope, slot)
	}
	r.expression(node.Condition, scope, slot)
	r.expression(node.Update, scope, slot)
	for _, label := range node.Labels {
		r.expression(label, scope, slot)
	}
	before := *scope
	switch node.Kind {
	case "if":
		blocks := slices.Clone(node.Blocks)
		if literal, ok := node.Condition.(*groovyLiteral); ok && (literal.Text == "true" || literal.Text == "false") {
			if literal.Text == "false" {
				blocks = blocks[1:]
			} else {
				blocks = blocks[:1]
			}
			for _, block := range blocks {
				*scope = r.block(block, *scope, slot)
			}
			return
		}
		var paths []jenkinsScope
		for _, block := range blocks {
			paths = append(paths, r.block(block, before, slot))
		}
		if len(blocks) < 2 {
			paths = append(paths, before)
		}
		*scope = r.merge(paths...)
	case "try":
		paths := []jenkinsScope{r.block(node.Blocks[0], before, slot)}
		// A catch runs after any statement of the try failed.
		recovering := r.invalidate(before, node.Blocks[0])
		for _, block := range node.Catches {
			paths = append(paths, r.block(block, recovering, slot))
		}
		after := r.merge(paths...)
		if node.Finally != nil {
			after = r.block(node.Finally, after, slot)
		}
		*scope = after
	case "switch":
		*scope = r.cases(node, before, slot)
	default:
		// A loop's body runs any number of times, and may stop part way
		// (break): what it assigns, and the variables its header binds,
		// are unknown in it and after it.
		for _, block := range node.Blocks {
			start := r.invalidate(before, block).with(func(next *jenkinsScope) {
				for _, name := range node.Variables {
					next.vars[name] = jenkinsUnknownVariable(name)
				}
			})
			*scope = r.merge(before, start, r.block(block, start, slot))
		}
	}
}

// cases reads a switch: any one case may be where it starts, and a case
// without a break at its end falls through to the next. It returns the
// scope after the switch, which may also be before when no case matches.
func (r *jenkinsReader) cases(node *groovyControl, before jenkinsScope, slot *jenkinsSlot) jenkinsScope {
	var paths []jenkinsScope
	exit := before
	for i, block := range node.Blocks {
		start := before
		if i > 0 && !jenkinsEndsCase(node.Blocks[i-1]) {
			start = r.merge(before, exit)
		}
		exit = r.block(block, start, slot)
		if jenkinsJumpsInside(block) {
			// A break inside an if or try may leave the case part way.
			paths = append(paths, r.invalidate(start, block))
		}
		if jenkinsEndsCase(block) || i == len(node.Blocks)-1 {
			paths = append(paths, exit)
		}
	}
	if !slices.Contains(node.Labels, nil) {
		paths = append(paths, before)
	}
	return r.merge(paths...)
}

// jenkinsEndsCase reports a switch case that ends with break, continue or
// return, so it does not fall through to the next.
func jenkinsEndsCase(block *groovyBlock) bool {
	if len(block.Statements) == 0 {
		return false
	}
	switch block.Statements[len(block.Statements)-1].(type) {
	case *groovyJump, *groovyReturn:
		return true
	}
	return false
}

// jenkinsJumpsInside reports a break or continue in a switch case other than
// its last statement, such as one inside an if: one that leaves the case.
// A loop's or a closure's own jumps do not.
func jenkinsJumpsInside(block *groovyBlock) bool {
	var jumps func(statements []groovyNode) bool
	jumps = func(statements []groovyNode) bool {
		for _, statement := range statements {
			switch node := statement.(type) {
			case *groovyJump:
				return true
			case *groovyControl:
				if node.Kind != "if" && node.Kind != "try" {
					continue
				}
				for _, inner := range append(append(slices.Clone(node.Blocks), node.Catches...), node.Finally) {
					if inner != nil && jumps(inner.Statements) {
						return true
					}
				}
			}
		}
		return false
	}
	statements := block.Statements
	if jenkinsEndsCase(block) {
		statements = statements[:len(statements)-1]
	}
	return jumps(statements)
}

// call reads one step call.
func (r *jenkinsReader) call(call *groovyCall, scope *jenkinsScope, slot *jenkinsSlot) {
	if call.Receiver == nil && call.Name != "pipeline" && !slices.Contains(jenkinsEnvMethods, call.Name) && !slices.Contains(jenkinsNonCommandSteps, call.Name) && (r.functions[call.Name] != nil || !jenkinsValueMethod(call.Name)) {
		// The steps that read their arguments as values still run what
		// the arguments run first: sh "${sh(script: …, returnStdout: true)}".
		for _, arg := range call.Args {
			r.argumentSteps(arg.Value, scope, slot)
		}
	}
	positional, named := jenkinsArgs(call)
	outer := *scope
	// body runs the closure in inner; what it assigns reaches the statements
	// after the call.
	body := func(inner jenkinsScope) {
		if call.Closure != nil {
			*scope = r.persist(outer, r.block(call.Closure.Body, inner, slot), call.Closure.Body)
		}
	}
	// repeat runs a closure that may run more than once, or not to its end.
	repeat := func(inner jenkinsScope) {
		if call.Closure != nil {
			final := r.block(call.Closure.Body, r.invalidate(inner, call.Closure.Body), slot)
			*scope = r.merge(outer, r.persist(outer, final, call.Closure.Body))
		}
	}
	if call.Receiver != nil {
		r.method(call, scope, slot)
		return
	}
	switch name := call.Name; {
	case name == "sh":
		r.shell(call, outer, slot)
	case name == "bat" || name == "powershell" || name == "pwsh":
		script := ""
		if len(positional) > 0 {
			script, _ = r.value(positional[0], outer)
		} else if value, ok := named["script"]; ok {
			script, _ = r.value(value, outer)
		}
		r.add(slot, call, runtimeStep{Run: script, ResolutionError: "runs " + name + ", a Windows command; ddtest does not set up or validate Windows agents"})
	case name == "dir":
		target := ""
		problem := "its directory is not literal"
		if len(positional) == 1 {
			target, problem = r.value(positional[0], outer)
		}
		body(outer.with(func(next *jenkinsScope) {
			if rest, ok := strings.CutPrefix(target, "$WORKSPACE"); ok && (rest == "" || strings.HasPrefix(rest, "/")) {
				target = "." + rest
			}
			joined := path.Clean(path.Join(outer.dir, target))
			switch {
			case problem != "":
				next.problem = joinDiagnostic(next.problem, "it runs inside dir("+r.text(call.Args[0].Value)+"), whose directory ddtest cannot resolve")
			case path.IsAbs(target) || strings.HasPrefix(joined, ".."):
				next.problem = joinDiagnostic(next.problem, "it runs inside dir('"+target+"'), outside the checkout")
			default:
				if joined == "." {
					joined = ""
				}
				next.dir = joined
			}
		}))
	case name == "withEnv":
		body(outer.with(func(next *jenkinsScope) {
			items := []string(nil)
			literal := len(positional) == 1
			if list, ok := firstNode(positional).(*groovyList); ok {
				for _, item := range list.Items {
					text, problem := r.value(item, outer)
					literal = literal && problem == ""
					items = append(items, text)
				}
			} else {
				literal = false
			}
			if !literal {
				next.problem = joinDiagnostic(next.problem, "it runs inside withEnv("+r.text(firstNode(positional))+"), whose variables ddtest cannot resolve")
				return
			}
			for _, item := range items {
				key, value, _ := strings.Cut(item, "=")
				if key == "PATH" || strings.HasPrefix(key, "PATH+") {
					next.pathChange = "withEnv(['" + item + "'])"
					continue
				}
				next.env[key] = value
				next.shadow[key] = true
				next.overrides[key] = "withEnv at " + r.where(call)
			}
		}))
	case name == "withCredentials":
		body(outer.with(func(next *jenkinsScope) {
			text := groovyText(r.source, firstNode(positional))
			for _, match := range jenkinsCredentialVariable.FindAllStringSubmatch(text, -1) {
				next.env[match[1]] = jenkinsSecret(match[1])
				next.shadow[match[1]] = true
				if match[1] == "DD_API_KEY" {
					r.review = append(r.review, r.file+": withCredentials at "+r.where(call)+" binds DD_API_KEY; the Datadog Jenkins plugin overwrites DD_API_KEY with its own configured key, so this binding is not the key the tests report with.")
				}
			}
		}))
	case slices.Contains(jenkinsToolWrappers, name):
		body(outer.with(func(next *jenkinsScope) { next.pathChange = "the Jenkins tool " + r.text(call) }))
	case name == "node":
		label := ""
		if len(positional) > 0 {
			label, _ = r.value(positional[0], outer)
		}
		if value, ok := named["label"]; ok {
			label, _ = r.value(value, outer)
		}
		nodeName := "node"
		if label != "" {
			nodeName = "node (" + label + ")"
		}
		if len(outer.stages) > 0 {
			nodeName = strings.Join(outer.stages, " / ")
		}
		inner := outer.with(func(next *jenkinsScope) {
			next.agent = jenkinsAgent{kind: "host", label: label, describe: "node('" + label + "')"}
			if label == "" {
				next.agent.describe = "node"
			}
			if outer.pod != "" && (label == "" || label == outer.podLabel) {
				// node(POD_LABEL): the agent is the template's pod.
				head := "node"
				if len(call.Args) > 0 {
					head = "node(" + r.text(call.Args[0].Value) + ")"
				}
				next.agent = jenkinsAgent{kind: "container", describe: head + " of " + outer.pod}
			}
		})
		nodeSlot := &jenkinsSlot{name: nodeName, scope: inner}
		if call.Closure != nil {
			*scope = r.persist(outer, r.block(call.Closure.Body, inner, nodeSlot), call.Closure.Body)
		}
		r.finish(nodeSlot)
	case name == "podTemplate" || name == "containerTemplate":
		// A Kubernetes pod template: a node(POD_LABEL) inside runs in a
		// container of the pod.
		body(outer.with(func(next *jenkinsScope) {
			next.pod = "the Kubernetes " + name + " at " + r.where(call)
			next.podLabel = ""
			if value, ok := named["label"]; ok {
				next.podLabel, _ = r.value(value, outer)
			}
		}))
	case name == "stage":
		if call.Closure == nil {
			return
		}
		stageName := r.stageName(call, outer)
		inner := outer.with(func(next *jenkinsScope) { next.stages = append(next.stages, stageName) })
		stageSlot := &jenkinsSlot{name: jobName(outer.stages, stageName, outer.suffix), scope: inner}
		*scope = r.persist(outer, r.block(call.Closure.Body, inner, stageSlot), call.Closure.Body)
		r.finish(stageSlot)
	case name == "parallel":
		// The branches run at the same time.
		var closures []*groovyBlock
		for _, arg := range call.Args {
			if closure, ok := arg.Value.(*groovyClosure); ok && arg.Name != "" {
				closures = append(closures, closure.Body)
			}
		}
		if call.Closure != nil {
			closures = append(closures, call.Closure.Body)
		}
		start := r.invalidate(outer, closures...)
		var paths []jenkinsScope
		for _, arg := range call.Args {
			closure, ok := arg.Value.(*groovyClosure)
			if arg.Name == "" || arg.Name == "failFast" {
				if arg.Name == "" {
					r.unresolved(slot, call, "parallel runs branches ddtest cannot read: "+r.text(arg.Value))
				}
				continue
			}
			if !ok {
				r.unresolved(slot, call, "the parallel branch "+arg.Name+" is not a closure ddtest can read")
				continue
			}
			inner := start.with(func(next *jenkinsScope) { next.stages = append(next.stages, arg.Name) })
			branchSlot := &jenkinsSlot{name: jobName(outer.stages, arg.Name, outer.suffix), scope: inner}
			paths = append(paths, r.persist(start, r.block(closure.Body, inner, branchSlot), closure.Body))
			r.finish(branchSlot)
		}
		if call.Closure != nil {
			// A declarative parallel { stage … } inside a script block.
			paths = append(paths, r.stages(call.Closure.Body, start, true))
		}
		if len(paths) > 0 {
			*scope = r.merge(append(paths, start)...)
		}
	case name == "container":
		image := ""
		if len(positional) > 0 {
			image, _ = r.value(positional[0], outer)
		}
		r.inContainer(call, jenkinsAgent{kind: "container", describe: "container('" + image + "') of a Kubernetes pod"}, scope, slot)
	case strings.HasSuffix(name, ".inside"):
		// image.inside { … } on a variable holding docker.image(…) or
		// docker.build(…).
		r.inContainer(call, jenkinsAgent{kind: "container", describe: name + " (a Docker container)"}, scope, slot)
	case name == "datadog":
		body(outer.with(func(next *jenkinsScope) { next.setup = r.datadog(call, outer) }))
	case slices.Contains(jenkinsRepeatingWrappers, name):
		repeat(outer)
	case slices.Contains(jenkinsWrappers, name):
		body(outer)
	case slices.Contains(jenkinsEnvMethods, name):
		for _, arg := range call.Args {
			r.expression(arg.Value, scope, slot)
		}
		r.setVariable(call, scope)
	case name == "pipeline" || slices.Contains(jenkinsNonCommandSteps, name):
		if name != "pipeline" && name != "echo" && name != "print" && name != "println" && name != "error" {
			r.nonTest = append(r.nonTest, name)
		}
		// Their arguments still run: echo sh(script: …, returnStdout: true).
		for _, arg := range call.Args {
			if slices.Contains(jenkinsConfigurationSteps, name) {
				r.configuration(arg.Value, scope, slot)
			} else {
				r.expression(arg.Value, scope, slot)
			}
		}
		if name == "library" {
			for _, arg := range positional {
				r.libraries = append(r.libraries, r.literals(arg)...)
			}
		}
	case name == "load" || name == "evaluate":
		r.unresolved(slot, call, name+" runs Groovy code from "+r.text(firstNode(positional))+", which ddtest does not read; its steps may run tests")
	case name == "docker.build":
		r.unresolved(slot, call, "docker.build builds a Docker image ("+r.text(call)+"), whose Dockerfile steps ddtest does not read; they may run tests, in a container the Datadog Jenkins plugin does not instrument")
	case name == "build":
		r.unresolved(slot, call, "build triggers another Jenkins job ("+r.text(call)+"), whose steps ddtest cannot read; they may run tests. Validate that job's Jenkinsfile separately")
	case r.functions[name] != nil:
		r.unresolved(slot, call, "it calls "+name+"(), a Groovy function of the Jenkinsfile, which ddtest does not follow; it may run tests")
		// What the function assigns is unknown after it.
		*scope = r.invalidate(outer, r.functions[name])
	case jenkinsValueMethod(name):
		for _, arg := range call.Args {
			r.expression(arg.Value, scope, slot)
		}
		outer = *scope
		// Such as versions.each { … }: a closure that runs any number of times.
		repeat(outer.with(func(next *jenkinsScope) {
			next.problem = joinDiagnostic(next.problem, "it runs inside "+name+" { … }, a Groovy loop ddtest does not expand")
		}))
	default:
		r.unresolved(slot, call, "it calls "+name+", which is not a Pipeline step ddtest knows; "+r.libraryNote()+", and may run tests")
		repeat(outer.with(func(next *jenkinsScope) {
			next.problem = joinDiagnostic(next.problem, "it runs inside "+name+"(…), which is not a Pipeline step ddtest knows; "+r.libraryNote())
		}))
	}
}

// jenkinsSecret is the shared model's reference to a Jenkins credential bound
// to a variable: a secret that exists only in CI.
func jenkinsSecret(variable string) string {
	return "${{ secrets." + variable + " }}"
}

// jenkinsCredentialVariable finds the variables withCredentials binds.
var jenkinsCredentialVariable = regexp.MustCompile(`(?:variable|usernameVariable|passwordVariable|keyFileVariable|passphraseVariable|tokenVariable)\s*:\s*['"]([A-Za-z_][A-Za-z0-9_]*)['"]`)

func firstNode(nodes []groovyNode) groovyNode {
	if len(nodes) == 0 {
		return nil
	}
	return nodes[0]
}

// method reads a call on another call's result, such as
// docker.image('node:22').inside { … }.
func (r *jenkinsReader) method(call *groovyCall, scope *jenkinsScope, slot *jenkinsSlot) {
	receiver, _ := call.Receiver.(*groovyCall)
	r.expression(call.Receiver, scope, slot)
	outer := *scope
	if receiver != nil && (receiver.Name == "docker.image" || receiver.Name == "docker.build") {
		image := ""
		if args, _ := jenkinsArgs(receiver); receiver.Name == "docker.image" && len(args) > 0 {
			image, _ = r.value(args[0], outer)
		}
		switch call.Name {
		case "inside":
			describe := receiver.Name + "(…).inside"
			if image != "" {
				describe = "docker.image('" + image + "').inside"
			}
			r.inContainer(call, jenkinsAgent{kind: "container", image: image, describe: describe}, scope, slot)
			return
		case "withRun":
			// A service container; the closure runs on the agent.
			if call.Closure != nil {
				*scope = r.persist(outer, r.block(call.Closure.Body, outer, slot), call.Closure.Body)
			}
			return
		case "pull", "push", "tag":
			return
		}
	}
	if call.Closure != nil {
		// Such as items.each { … }: the closure may run any number of times.
		inner := r.invalidate(outer, call.Closure.Body).with(func(next *jenkinsScope) {
			next.problem = joinDiagnostic(next.problem, "it runs inside "+r.text(call.Receiver)+"."+call.Name+"(…), which ddtest cannot read")
		})
		*scope = r.merge(outer, r.persist(outer, r.block(call.Closure.Body, inner, slot), call.Closure.Body))
		return
	}
	if slices.Contains(jenkinsValueMethods, call.Name) {
		for _, arg := range call.Args {
			r.expression(arg.Value, scope, slot)
		}
		return
	}
	r.unresolved(slot, call, "it calls "+r.text(call)+", which ddtest cannot read; it may run tests")
}

// inContainer runs a container block's closure, such as container('node')
// or docker.image(…).inside: its steps run in the container, the job's
// other steps do not.
func (r *jenkinsReader) inContainer(call *groovyCall, agent jenkinsAgent, scope *jenkinsScope, slot *jenkinsSlot) {
	if call.Closure == nil {
		return
	}
	outer := *scope
	inner := outer.with(func(next *jenkinsScope) { next.agent = agent })
	target := r.containerSlot(slot, outer, inner)
	*scope = r.persist(outer, r.block(call.Closure.Body, inner, target), call.Closure.Body)
	if target != slot {
		r.finish(target)
	}
}

// containerSlot is where a container block's steps go: the slot itself when
// its job already runs in a container, or else a job of their own, so the
// job's steps on the agent stay instrumentable.
func (r *jenkinsReader) containerSlot(slot *jenkinsSlot, outer, inner jenkinsScope) *jenkinsSlot {
	if outer.agent.kind == "container" {
		return slot
	}
	return &jenkinsSlot{name: slot.name + " (" + inner.agent.describe + ")", scope: inner, within: slot.name}
}

// unresolved adds a step that explains what ddtest cannot read.
func (r *jenkinsReader) unresolved(slot *jenkinsSlot, node groovyNode, reason string) {
	r.add(slot, node, runtimeStep{ResolutionError: reason})
}

// add appends a step to the slot's job, creating it.
func (r *jenkinsReader) add(slot *jenkinsSlot, node groovyNode, step runtimeStep) *runtimeStep {
	if slot.job == nil {
		slot.job = &jenkinsJob{name: slot.name, scope: slot.scope}
	}
	step.Source = r.where(node)
	if step.ResolutionError != "" {
		step.ResolutionError = "Jenkinsfile " + slot.name + ": " + step.ResolutionError
	}
	slot.job.steps = append(slot.job.steps, step)
	return &slot.job.steps[len(slot.job.steps)-1]
}

// jenkinsShellDialect is how sh scripts read: WORKSPACE is the checkout.
var jenkinsShellDialect = ciShellDialect{label: "Jenkins sh script", projectVariable: "WORKSPACE"}

// shell reads an sh step: each top-level statement of a literal script is
// one step, sharing that script's shell.
func (r *jenkinsReader) shell(call *groovyCall, scope jenkinsScope, slot *jenkinsSlot) {
	positional, named := jenkinsArgs(call)
	node := firstNode(positional)
	if value, ok := named["script"]; ok {
		node = value
	}
	if node == nil {
		r.unresolved(slot, call, "sh has no script ddtest can read")
		return
	}
	script, problem := r.value(node, scope)
	if problem != "" {
		run := ""
		if text, ok := node.(*groovyString); ok {
			run = strings.Join(slices.Collect(func(yield func(string) bool) {
				for _, part := range text.Value.parts {
					if !yield(part.text) {
						return
					}
				}
			}), " ")
		}
		r.add(slot, call, runtimeStep{Run: run, ResolutionError: "its sh script reads " + problem + ", so ddtest cannot resolve it: " + r.text(node)})
		return
	}
	reason := scope.problem
	switch {
	case scope.agent.kind == "":
		reason = joinDiagnostic(reason, "it runs outside a node block, where Jenkins has no agent for shell steps")
	case scope.agent.kind == "none":
		reason = joinDiagnostic(reason, "it runs under agent none, without an agent for shell steps")
	case scope.agent.problem != "":
		reason = joinDiagnostic(reason, scope.agent.problem)
	case strings.Contains(strings.ToLower(scope.agent.label), "windows"):
		reason = joinDiagnostic(reason, "it runs on a Windows agent (label "+scope.agent.label+"); ddtest does not set up or validate Windows agents")
	}
	if scope.envUnknown != "" {
		reason = joinDiagnostic(reason, "it runs after "+scope.envUnknown+", which sets an environment variable ddtest cannot name, so its environment is unknown")
	}
	if first, _, _ := strings.Cut(strings.TrimLeft(script, " \t\n"), "\n"); strings.HasPrefix(first, "#!") {
		interpreter := strings.Fields(strings.TrimPrefix(first, "#!"))
		if len(interpreter) == 0 || !slices.Contains([]string{"sh", "bash"}, path.Base(interpreter[len(interpreter)-1])) && !slices.Contains([]string{"sh", "bash"}, path.Base(interpreter[0])) {
			reason = joinDiagnostic(reason, "its sh script runs with "+strings.TrimPrefix(first, "#!")+", not a shell ddtest reads")
		}
	}
	env := mergeEnvironment(scope.env, nil)
	exports, references := r.pluginExports(scope)
	state := ciShellState{dialect: jenkinsShellDialect, env: map[string]string{}, references: maps.Clone(references), job: env, directory: scope.dir}
	if state.references == nil {
		state.references = map[string]string{}
	}
	for key, where := range scope.overrides {
		if _, exported := exports[key]; exported {
			r.review = append(r.review, r.file+": "+jenkinsOverwrittenExport(key, where))
		}
		if _, overridden := env[key]; overridden {
			state.env[key] = env[key]
		}
	}
	// The plugin sets its additionalVariables after the pipeline's
	// environment, withEnv and withCredentials, and its tracer variables
	// after them.
	for key, value := range r.pluginVariables(scope) {
		if current, set := env[key]; set && current != value {
			r.review = append(r.review, r.file+": "+key+" set in the Jenkinsfile is overwritten by the additionalVariables of datadog(…) at "+scope.setup.source+", which the Datadog Jenkins plugin sets for every step.")
		}
		state.env[key] = value
	}
	maps.Copy(state.env, exports)
	file, parsed := parseCIShell(script)
	if !parsed || len(file.Stmts) == 0 {
		if strings.TrimSpace(script) != "" {
			r.add(slot, call, runtimeStep{Run: script, ResolutionError: joinDiagnostic(reason, "its sh script is not shell ddtest can parse")})
		}
		return
	}
	pathChange := scope.pathChange
	selected := map[string]ciShellRuntime{}
	for i, stmt := range file.Stmts {
		known := mergeEnvironment(mergeEnvironment(env, state.env), state.references)
		statement := strings.TrimSpace(jenkinsStatementText(script, file.Stmts, i))
		step := runtimeStep{WorkingDirectory: state.directory}
		if len(state.env) > 0 {
			step.Env = maps.Clone(state.env)
		}
		stepReason := joinDiagnostic(reason, state.problem)
		if container := jenkinsContainerCommand(stmt); container != "" {
			step.Run = statement
			step.ResolutionError = joinDiagnostic(stepReason, "it runs "+container+": tests inside run in a container ddtest cannot validate, and the Datadog Jenkins plugin does not instrument them")
			r.add(slot, call, step)
			continue
		}
		command := gitLabResolveBranches(statement, known)
		if jenkinsChangesPath(stmt) {
			pathChange = "the PATH the sh script at " + r.where(call) + " sets"
		}
		if manager := jenkinsRuntimeManager(stmt); manager != "" && pathChange == scope.pathChange {
			// Such as nvm use 20: the tests run on another runtime than
			// the agent's default.
			pathChange = "the runtime " + manager + " (sh script at " + r.where(call) + ") selects"
		}
		if token, selection, ok := r.shellSelection(stmt, state.directory, call); ok && stepReason == "" {
			// Such as nvm use 22: the job's host description names it, so
			// the runtime the user states for the host (--ci-runtime) is the
			// one it selects, and the later statements of this shell check
			// that statement against it.
			selected[token] = selection
			r.add(slot, call, step)
			continue
		}
		if len(selected) > 0 {
			step.ShellRuntime = maps.Clone(selected)
		}
		step.Run = command
		step.ResolutionError = stepReason
		added := r.add(slot, call, step)
		if stepReason == "" && state.apply(command, mergeEnvironment(env, state.env)) {
			added.Run = ""
		}
	}
	if job := slot.job; job != nil && pathChange != "" && job.pathChange == "" {
		job.pathChange = pathChange
	}
}

// jenkinsOverwrittenExport explains a variable the Datadog Jenkins plugin
// sets that a scope narrower than the pipeline sets too.
func jenkinsOverwrittenExport(key, where string) string {
	detail := "it sets " + key + " for every step"
	for _, language := range discoveryLanguages {
		if composes := language.ciPluginComposes(key); composes != "" {
			detail = composes
		}
	}
	return key + " set in " + where + " is overwritten by the Datadog Jenkins plugin: " + detail + "; set those options inside the sh script that runs the tests instead."
}

// jenkinsStatementText is statement i's text, up to the next statement.
func jenkinsStatementText(text string, stmts []*syntax.Stmt, i int) string {
	start := int(stmts[i].Pos().Offset())
	end := len(text)
	if i+1 < len(stmts) {
		end = int(stmts[i+1].Pos().Offset())
	}
	return text[start:end]
}

// jenkinsContainerCommand names a command that starts a container which
// may run tests: docker run or exec, or a Compose run, exec or up.
func jenkinsContainerCommand(stmt *syntax.Stmt) string {
	found := ""
	syntax.Walk(stmt, func(node syntax.Node) bool {
		call, ok := node.(*syntax.CallExpr)
		if !ok || found != "" {
			return found == ""
		}
		var words []string
		for _, word := range call.Args {
			words = append(words, word.Lit())
		}
		for i := 0; i < len(words); i++ {
			switch path.Base(words[i]) {
			case "docker", "podman":
				for _, next := range words[i+1:] {
					if strings.HasPrefix(next, "-") {
						continue
					}
					if next == "run" || next == "exec" {
						found = path.Base(words[i]) + " " + next
					}
					if next == "compose" {
						for _, sub := range words[i+2:] {
							if sub == "run" || sub == "exec" || sub == "up" {
								found = "docker compose " + sub
								break
							}
						}
					}
					break
				}
			case "docker-compose":
				for _, next := range words[i+1:] {
					if next == "run" || next == "exec" || next == "up" {
						found = "docker-compose " + next
						break
					}
				}
			}
			if found != "" {
				break
			}
		}
		return found == ""
	})
	return found
}

// jenkinsRuntimeManagers select a runtime of any language from a shell,
// as the scripts that load them do.
var jenkinsRuntimeManagers = []string{"asdf", "mise", "rtx"}

// jenkinsRuntimeManager is a statement's command that selects another
// runtime than the agent's default, such as a version manager's (see
// discoveryLanguage.selectsShellRuntime); empty when none does.
func jenkinsRuntimeManager(stmt *syntax.Stmt) string {
	found := ""
	syntax.Walk(stmt, func(node syntax.Node) bool {
		call, ok := node.(*syntax.CallExpr)
		if !ok || found != "" || len(call.Args) == 0 {
			return found == ""
		}
		var words []string
		for _, word := range call.Args {
			words = append(words, cmp.Or(word.Lit(), strings.Trim(shellText(word), `"'`)))
		}
		selects := slices.Contains(jenkinsRuntimeManagers, path.Base(words[0])) || (words[0] == "source" || words[0] == ".") && len(words) > 1 && path.Base(words[1]) == "asdf.sh"
		for _, language := range discoveryLanguages {
			selects = selects || language.selectsShellRuntime(words)
		}
		if selects {
			found = shellText(call)
		}
		return found == ""
	})
	return found
}

// shellSelection reads a statement that only selects a runtime for the rest
// of its shell (see statedRuntime.shellSelection), run in directory: the
// language's installer token and its selection. A version the manager reads
// from a version file in the checkout is that file's.
func (r *jenkinsReader) shellSelection(stmt *syntax.Stmt, directory string, call *groovyCall) (string, ciShellRuntime, bool) {
	command, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok || stmt.Negated || stmt.Background || stmt.Coprocess || len(command.Assigns) > 0 {
		return "", ciShellRuntime{}, false
	}
	for _, redirect := range stmt.Redirs {
		// Only output it discards: nvm use 22 > /dev/null.
		if redirect.Word == nil || redirect.Hdoc != nil {
			return "", ciShellRuntime{}, false
		}
		if target := redirect.Word.Lit(); target != "/dev/null" && (redirect.Op != syntax.DplOut || target != "1" && target != "2") {
			return "", ciShellRuntime{}, false
		}
	}
	words, ok := jenkinsCommandWords(command)
	if len(command.Args) == 2 && command.Args[0].Lit() == "eval" {
		// eval "$(rbenv init -)"
		words, ok = jenkinsEvaluatedWords(command.Args[1])
	}
	if !ok {
		return "", ciShellRuntime{}, false
	}
	for _, language := range statedRuntimeLanguages() {
		runtime := language.statedRuntime()
		if runtime.shellSelection == nil {
			continue
		}
		version, versionFile, selects := runtime.shellSelection(words)
		if !selects {
			continue
		}
		source := shellText(command) + " in the sh script at " + r.where(call)
		if version == "" && versionFile != "" && runtime.fileVersion != nil {
			if value := jenkinsVersionFile(r.root, path.Join(directory, versionFile), runtime); value != "" {
				version, source = value, source+" (from "+path.Join(directory, versionFile)+")"
			}
		}
		return language.actionLanguage(), ciShellRuntime{Version: version, Source: source}, true
	}
	return "", ciShellRuntime{}, false
}

// jenkinsCommandWords are a command's words, quotes removed, when none
// substitutes a command's output.
func jenkinsCommandWords(command *syntax.CallExpr) ([]string, bool) {
	var words []string
	for _, word := range command.Args {
		substitutes := false
		syntax.Walk(word, func(node syntax.Node) bool {
			switch node.(type) {
			case *syntax.CmdSubst, *syntax.ProcSubst:
				substitutes = true
			}
			return !substitutes
		})
		if substitutes {
			return nil, false
		}
		words = append(words, cmp.Or(word.Lit(), strings.Trim(shellText(word), `"'`)))
	}
	return words, len(words) > 0
}

// jenkinsEvaluatedWords are the literal words of the single command a word
// substitutes, such as "$(rbenv init -)".
func jenkinsEvaluatedWords(word *syntax.Word) ([]string, bool) {
	parts := word.Parts
	if len(parts) == 1 {
		if quoted, ok := parts[0].(*syntax.DblQuoted); ok {
			parts = quoted.Parts
		}
	}
	if len(parts) != 1 {
		return nil, false
	}
	substitution, ok := parts[0].(*syntax.CmdSubst)
	if !ok || len(substitution.Stmts) != 1 || len(substitution.Stmts[0].Redirs) > 0 {
		return nil, false
	}
	command, ok := substitution.Stmts[0].Cmd.(*syntax.CallExpr)
	if !ok || len(command.Assigns) > 0 {
		return nil, false
	}
	return jenkinsCommandWords(command)
}

// jenkinsVersionFile is the version a version file in the checkout names,
// such as .nvmrc's 22, or "" when it names none ddtest can compare.
func jenkinsVersionFile(root, name string, runtime *statedRuntime) string {
	if !filepath.IsLocal(name) {
		return ""
	}
	file, err := os.Open(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		return ""
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(io.LimitReader(file, 256))
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(content)), "\n")
	return runtime.fileVersion(strings.TrimSpace(line))
}

// jenkinsChangesPath reports a statement that assigns PATH.
func jenkinsChangesPath(stmt *syntax.Stmt) bool {
	changes := false
	syntax.Walk(stmt, func(node syntax.Node) bool {
		if assign, ok := node.(*syntax.Assign); ok && assign.Name != nil && assign.Name.Value == "PATH" {
			changes = true
		}
		return !changes
	})
	return changes
}

// finish converts a slot's job into the workflow, with the plugin setup
// step first when datadog(…) applies to it.
func (r *jenkinsReader) finish(slot *jenkinsSlot) {
	job := slot.job
	if job == nil {
		return
	}
	slot.job = nil
	scope := job.scope
	result := runtimeJob{Env: maps.Clone(scope.env)}
	if len(result.Env) == 0 {
		result.Env = nil
	}
	if scope.disabled {
		result.If = "false"
	}
	switch scope.agent.kind {
	case "container":
		result.Image = scope.agent.image
		result.ImageTemplate = scope.agent.image
		if scope.agent.image == "" {
			result.ImageProblem = "the job runs in " + scope.agent.describe + ", whose image ddtest does not read"
		}
		result.SetupUnsupported = "it runs in " + scope.agent.describe + ", a container: the Datadog Jenkins plugin installs the tracer on the build agent and does not instrument tests inside containers"
		if slot.within != "" {
			result.SetupUnsupported = "it runs in " + scope.agent.describe + ", a container inside " + slot.within + ", whose steps outside the container run on the build agent and are validated as that job: the Datadog Jenkins plugin installs the tracer on the build agent and does not instrument tests inside containers"
		}
	case "host":
		result.Host = jenkinsHost(scope.agent, job.pathChange)
	}
	steps := job.steps
	if setup := scope.setup; setup != nil {
		current := setup.ciSetup()
		problem := scope.setupProblem
		if result.SetupUnsupported != "" {
			problem = joinDiagnostic(problem, "This stage cannot use the Datadog Jenkins plugin: "+result.SetupUnsupported+". The user's options are to run its tests on a host agent (agent { label … }), or to set up Test Optimization in the container by hand, outside ddtest, which ddtest cannot validate.")
		}
		if problem != "" && current.Problem == "" {
			current.Problem = problem
		}
		if job.pathChange != "" {
			current.Confirmations = append(current.Confirmations, jenkinsRuntimeMatches)
		}
		setup.steps = append(setup.steps, &current)
		steps = append([]runtimeStep{{Source: setup.source, Setup: &current}}, steps...)
	} else if scope.setupProblem != "" {
		steps = append([]runtimeStep{{Source: r.file, Setup: &ciSetup{Reference: jenkinsPluginReference, Pinned: true, Problem: scope.setupProblem, Inputs: map[string]string{}}}}, steps...)
	}
	for i := range steps {
		steps[i].Number = i + 1
	}
	result.Steps = steps
	name := job.name
	for i := 2; ; i++ {
		if _, taken := r.workflow.Jobs[name]; !taken {
			break
		}
		name = job.name + " [" + strconv.Itoa(i) + "]"
	}
	r.workflow.Jobs[name] = result
	if setup := scope.setup; setup != nil && result.SetupUnsupported != "" && !scope.disabled && len(job.steps) > 0 {
		setup.containers = append(setup.containers, r.file+" / "+name)
		if !slices.Contains(r.setups, setup) {
			r.setups = append(r.setups, setup)
		}
	}
}

// jenkinsHost describes a host agent for the user who knows its runtime.
func jenkinsHost(agent jenkinsAgent, pathChange string) string {
	host := "any Jenkins agent (" + agent.describe + ", with no label)"
	if agent.label != "" {
		host = "the Jenkins agents labeled " + strconv.Quote(agent.label)
	}
	if pathChange != "" {
		host += ", with " + pathChange + " on PATH"
	}
	return host
}

// pluginVariables are the additionalVariables the Datadog Jenkins plugin
// sets in every step of a job in its scope.
func (r *jenkinsReader) pluginVariables(scope jenkinsScope) map[string]string {
	if scope.setup == nil || scope.setup.problem() != "" {
		return nil
	}
	return scope.setup.variables
}

// pluginExports are the variables the Datadog Jenkins plugin sets in every
// step of a job in its scope, for each language it selects.
func (r *jenkinsReader) pluginExports(scope jenkinsScope) (map[string]string, map[string]string) {
	if scope.setup == nil || scope.setup.problem() != "" {
		return nil, nil
	}
	// The plugin computes its variables once per agent, from the
	// environment of the first step it configures there, and sets them on
	// every step after it: ddtest composes them with the pipeline's
	// environment, and reports a narrower scope's value (see shell).
	base := scope.pipelineEnv
	env, references := map[string]string{}, map[string]string{}
	for _, language := range discoveryLanguages {
		if !slices.Contains(scope.setup.languages, language.ciPluginLanguage()) {
			continue
		}
		exported, referenced := language.ciPluginExports(base)
		maps.Copy(env, exported)
		maps.Copy(references, referenced)
	}
	return env, references
}
