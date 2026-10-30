// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"fmt"
	"maps"
	"path"
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

// jenkinsMatrixLimit bounds the cells of one matrix.
const jenkinsMatrixLimit = 200

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
}

func (s jenkinsScope) with(change func(*jenkinsScope)) jenkinsScope {
	next := s
	next.stages = slices.Clone(s.stages)
	next.env = maps.Clone(s.env)
	next.vars = maps.Clone(s.vars)
	next.params = maps.Clone(s.params)
	next.overrides = maps.Clone(s.overrides)
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
	// container describes a container a step ran in; pathChange a PATH change
	// a step ran under.
	container, image, pathChange string
}

// jenkinsSlot is the job steps go to, created when the first one is read.
type jenkinsSlot struct {
	name  string
	scope jenkinsScope
	job   *jenkinsJob
}

type jenkinsReader struct {
	root, file, source string
	workflow           *ciWorkflow
	functions          map[string]bool
	libraries          []string
	review, nonTest    []string
	// workspace is what ${WORKSPACE} reads as: $WORKSPACE in a script, which
	// the shell reads as the checkout (see jenkinsShellDialect), and the
	// shared model's checkout expression in an environment value.
	workspace string
}

// readJenkinsfile reads one Jenkinsfile into a workflow.
func readJenkinsfile(root, file, source string) (ciWorkflow, error) {
	tree, err := parseGroovy(source)
	if err != nil {
		return ciWorkflow{}, err
	}
	workflow := ciWorkflow{Path: file, Jobs: map[string]runtimeJob{}}
	r := &jenkinsReader{root: root, file: file, source: source, workflow: &workflow, functions: map[string]bool{}, workspace: "$WORKSPACE"}
	for _, statement := range tree.Statements {
		switch node := statement.(type) {
		case *groovyFunction:
			r.functions[node.Name] = true
		case *groovyAnnotation:
			r.annotation(node)
		}
	}
	scope := jenkinsScope{}.with(func(*jenkinsScope) {})
	top := &jenkinsSlot{name: "top level", scope: scope}
	declarative := false
	for _, statement := range tree.Statements {
		if call, ok := statement.(*groovyCall); ok && call.Name == "pipeline" && call.Closure != nil && call.Receiver == nil {
			declarative = true
			r.pipeline(call.Closure.Body, scope)
		}
	}
	if !declarative {
		r.block(tree, scope, top)
	} else {
		// Scripted statements around a declarative pipeline run first.
		for _, statement := range tree.Statements {
			if call, ok := statement.(*groovyCall); ok && call.Name == "pipeline" {
				continue
			}
			r.statement(statement, &scope, top)
		}
	}
	r.finish(top)
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
	case nil:
		return "", "a missing value"
	}
	return "", "the computed value " + r.text(node)
}

// reference reads env.X, params.X, a variable, or an environment variable
// by its bare name.
func (r *jenkinsReader) reference(name string, scope jenkinsScope) (string, string) {
	name = strings.TrimSpace(name)
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
		return value, ""
	}
	if value, known := scope.env[key]; known && unresolvedCIError(value) == nil {
		return value, ""
	}
	return "", "${" + name + "}, which only the Jenkins build knows"
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

// pipeline reads a declarative pipeline.
func (r *jenkinsReader) pipeline(body *groovyBlock, scope jenkinsScope) {
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
	scope = r.directives(directives, scope, true)
	if stages, ok := directives["stages"]; ok && stages.Closure != nil {
		r.stages(stages.Closure.Body, scope)
	}
	if post, ok := directives["post"]; ok {
		slot := &jenkinsSlot{name: "post", scope: scope}
		r.post(post, scope, slot)
		r.finish(slot)
	}
}

// directives applies a pipeline's or stage's agent, environment, options,
// tools, parameters and when to scope.
func (r *jenkinsReader) directives(directives map[string]*groovyCall, scope jenkinsScope, top bool) jenkinsScope {
	return scope.with(func(next *jenkinsScope) {
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
			r.environment(environment.Closure.Body, next, top)
		}
		if tools, ok := directives["tools"]; ok && tools.Closure != nil {
			for _, statement := range tools.Closure.Body.Statements {
				if call, ok := statement.(*groovyCall); ok {
					next.pathChange = "the Jenkins tool " + r.text(call) + " (tools directive)"
				}
			}
		}
		if options, ok := directives["options"]; ok && options.Closure != nil {
			for _, statement := range options.Closure.Body.Statements {
				call, ok := statement.(*groovyCall)
				if !ok || call.Name != "datadog" {
					continue
				}
				if top {
					next.setup = r.datadog(call, *next)
				} else if _, _, has := jenkinsTestOptimization(call); has {
					next.setupProblem = "The datadog(…) option at " + r.where(call) + " is in a stage's options; ddtest certifies the Datadog Jenkins plugin setup only in the pipeline's top-level options, which the plugin documents. Move it to the top-level options directive."
				}
			}
		}
		if when, ok := directives["when"]; ok && when.Closure != nil && jenkinsNeverWhen(when.Closure.Body) {
			next.disabled = true
		}
	})
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

// environment reads an environment directive.
func (r *jenkinsReader) environment(body *groovyBlock, scope *jenkinsScope, top bool) {
	for _, statement := range body.Statements {
		assign, ok := statement.(*groovyAssign)
		if !ok {
			continue
		}
		name, isName := assign.Target.(*groovyName)
		if !isName || len(name.Path) != 1 {
			continue
		}
		r.assignEnvironment(name.Path[0], assign.Value, scope, "the environment directive at "+r.where(assign), !top)
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
		scope.setupProblem = "DD_API_KEY is written into the Jenkinsfile (" + where + "); never commit the key: configure it as a Jenkins credential in the Datadog plugin's settings, and remove it from the Jenkinsfile."
	}
	scope.env[key] = value
	if narrow {
		scope.overrides[key] = where
	}
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

// stages reads a stages section.
func (r *jenkinsReader) stages(body *groovyBlock, scope jenkinsScope) {
	for _, statement := range body.Statements {
		if call, ok := statement.(*groovyCall); ok && call.Name == "stage" && call.Receiver == nil {
			r.stage(call, scope)
		}
	}
}

// stageName is a stage's literal name, or its position.
func (r *jenkinsReader) stageName(call *groovyCall, scope jenkinsScope) string {
	positional, named := jenkinsArgs(call)
	if len(positional) > 0 {
		if name, problem := r.value(positional[0], scope); problem == "" {
			return name
		}
	}
	if value, ok := named["name"]; ok {
		if name, problem := r.value(value, scope); problem == "" {
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
// a matrix.
func (r *jenkinsReader) stage(call *groovyCall, scope jenkinsScope) {
	name := r.stageName(call, scope)
	if call.Closure == nil {
		return
	}
	directives := jenkinsDirectives(call.Closure.Body)
	inner := r.directives(directives, scope, false)
	switch {
	case directives["steps"] != nil:
		slot := &jenkinsSlot{name: jobName(scope.stages, name, scope.suffix), scope: inner}
		if steps := directives["steps"]; steps.Closure != nil {
			r.block(steps.Closure.Body, inner, slot)
		}
		if post, ok := directives["post"]; ok {
			r.post(post, inner, slot)
		}
		r.finish(slot)
		return
	case directives["matrix"] != nil:
		r.matrix(directives["matrix"], inner.with(func(next *jenkinsScope) { next.stages = append(next.stages, name) }))
	case directives["stages"] != nil && directives["stages"].Closure != nil:
		r.stages(directives["stages"].Closure.Body, inner.with(func(next *jenkinsScope) { next.stages = append(next.stages, name) }))
	case directives["parallel"] != nil && directives["parallel"].Closure != nil:
		r.stages(directives["parallel"].Closure.Body, inner.with(func(next *jenkinsScope) { next.stages = append(next.stages, name) }))
	}
	if post, ok := directives["post"]; ok {
		slot := &jenkinsSlot{name: jobName(scope.stages, name, scope.suffix) + " (post)", scope: inner}
		r.post(post, inner, slot)
		r.finish(slot)
	}
}

// jenkinsPassingPost are the post conditions that run when the build passes.
var jenkinsPassingPost = []string{"always", "success", "cleanup", "changed", "fixed"}

// post reads a post section: conditions that run on a passing build add
// their steps to slot; the others are reported.
func (r *jenkinsReader) post(call *groovyCall, scope jenkinsScope, slot *jenkinsSlot) {
	if call.Closure == nil {
		return
	}
	for _, statement := range call.Closure.Body.Statements {
		condition, ok := statement.(*groovyCall)
		if !ok || condition.Closure == nil {
			continue
		}
		if slices.Contains(jenkinsPassingPost, condition.Name) {
			r.block(condition.Closure.Body, scope, slot)
			continue
		}
		if jenkinsRunsCommands(condition.Closure.Body) {
			r.review = append(r.review, r.file+": the post { "+condition.Name+" } block at "+r.where(condition)+" runs only when the build does not pass, so its commands are outside this validation.")
		}
	}
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

// matrix expands a matrix section into one stage set per cell.
func (r *jenkinsReader) matrix(call *groovyCall, scope jenkinsScope) {
	where := jobName(scope.stages[:len(scope.stages)-1], scope.stages[len(scope.stages)-1], scope.suffix)
	if call.Closure == nil {
		return
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
						if valueProblem != "" {
							problem = "the axis values " + r.text(field) + " are not literal"
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
	if len(axes) == 0 {
		problem = "it has no axes ddtest can read"
		if _, ok := directives["axes"]; ok {
			problem = "its axes are not literal"
		}
	}
	cells := []map[string]string{{}}
	for _, current := range axes {
		var next []map[string]string
		for _, cell := range cells {
			for _, value := range current.values {
				combined := maps.Clone(cell)
				combined[current.name] = value
				next = append(next, combined)
			}
		}
		cells = next
		if len(cells) > jenkinsMatrixLimit {
			problem = fmt.Sprintf("it has more than %d cells", jenkinsMatrixLimit)
			break
		}
	}
	if problem != "" {
		slot := &jenkinsSlot{name: where, scope: scope}
		r.unresolved(slot, call, "ddtest cannot expand its matrix ("+problem+"), so it cannot tell which stages it runs")
		r.finish(slot)
		return
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
	for _, cell := range cells {
		if excluded(cell) {
			continue
		}
		var values []string
		for _, current := range axes {
			values = append(values, cell[current.name])
		}
		cellScope := scope.with(func(next *jenkinsScope) {
			maps.Copy(next.env, cell)
			next.suffix = scope.suffix + " (" + strings.Join(values, ", ") + ")"
		})
		cellDirectives := map[string]*groovyCall{}
		for _, key := range []string{"agent", "environment", "tools", "when"} {
			if directive, ok := directives[key]; ok {
				cellDirectives[key] = directive
			}
		}
		cellScope = r.directives(cellDirectives, cellScope, false)
		if stages, ok := directives["stages"]; ok && stages.Closure != nil {
			r.stages(stages.Closure.Body, cellScope)
		}
	}
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

// jenkinsWrappers run their closure's steps in the same place, with the same
// environment and toolchain.
var jenkinsWrappers = []string{
	"timeout", "retry", "timestamps", "ansiColor", "lock", "catchError", "warnError", "sshagent", "wrap", "script",
	"withSonarQubeEnv", "configFileProvider", "withFolderProperties", "throttle", "waitUntil", "withAWS", "withVault",
	"withDockerRegistry", "withDockerServer", "withKubeConfig", "xvfb", "gitlabCommitStatus", "gitlabBuilds",
	"withChecks", "podTemplate", "docker.withRegistry", "docker.withServer", "withNPM",
}

// jenkinsToolWrappers put a Jenkins tool installation on PATH for their
// closure.
var jenkinsToolWrappers = []string{"nodejs", "nvm", "rbenv", "rvm", "withAnt", "withGoEnv"}

// block reads statements in order: assignments to env and variables reach
// later statements of the block.
func (r *jenkinsReader) block(body *groovyBlock, scope jenkinsScope, slot *jenkinsSlot) {
	current := scope
	for _, statement := range body.Statements {
		r.statement(statement, &current, slot)
	}
}

func (r *jenkinsReader) statement(statement groovyNode, scope *jenkinsScope, slot *jenkinsSlot) {
	switch node := statement.(type) {
	case *groovyCall:
		r.call(node, *scope, slot)
	case *groovyName:
		r.call(&groovyCall{groovySpan: node.groovySpan, Name: strings.Join(node.Path, "."), NameStart: node.Start}, *scope, slot)
	case *groovyAssign:
		r.assign(node, scope, slot)
	case *groovyControl:
		r.control(node, scope, slot)
	case *groovyFunction:
		r.functions[node.Name] = true
	case *groovyAnnotation:
		r.annotation(node)
	case *groovyReturn, *groovyString, *groovyLiteral:
	case *groovyBinary:
		if node.Op == "++" || node.Op == "--" {
			return // A counter changes no step.
		}
		r.embeddedCommands(node, *scope, slot)
		r.unresolved(slot, statement, "the Groovy statement "+r.text(statement)+" is not one ddtest can read")
	default:
		r.unresolved(slot, statement, "the Groovy statement "+r.text(statement)+" is not one ddtest can read")
	}
}

// assign records env.X = value and def x = value.
func (r *jenkinsReader) assign(node *groovyAssign, scope *jenkinsScope, slot *jenkinsSlot) {
	// A script in the value still runs: def out = sh(…).trim().
	r.embeddedCommands(node.Value, *scope, slot)
	target, ok := node.Target.(*groovyName)
	if !ok {
		return
	}
	next := scope.with(func(next *jenkinsScope) {
		switch {
		case len(target.Path) == 2 && target.Path[0] == "env":
			if node.Op == "=" {
				r.assignEnvironment(target.Path[1], node.Value, next, "env."+target.Path[1]+" at "+r.where(node), true)
			} else {
				next.env[target.Path[1]] = unresolvedCIValue(r.text(node))
			}
		case len(target.Path) == 1:
			value, problem := r.value(node.Value, *next)
			if node.Op == "=" && problem == "" {
				next.vars[target.Path[0]] = value
			} else {
				delete(next.vars, target.Path[0])
			}
		}
	})
	*scope = next
}

// embeddedCommands adds the sh calls inside an expression, which run when it
// is evaluated.
func (r *jenkinsReader) embeddedCommands(node groovyNode, scope jenkinsScope, slot *jenkinsSlot) {
	switch node := node.(type) {
	case *groovyCall:
		if node.Receiver != nil {
			r.embeddedCommands(node.Receiver, scope, slot)
		}
		if slices.Contains([]string{"sh", "bat", "powershell", "pwsh"}, node.Name) && node.Receiver == nil {
			r.call(node, scope, slot)
		}
		for _, arg := range node.Args {
			r.embeddedCommands(arg.Value, scope, slot)
		}
	case *groovyBinary:
		r.embeddedCommands(node.Left, scope, slot)
		r.embeddedCommands(node.Right, scope, slot)
	}
}

// control reads if/else, loops, switch and try: every branch that can run on
// a passing build is a possible path; a catch block is not.
func (r *jenkinsReader) control(node *groovyControl, scope *jenkinsScope, slot *jenkinsSlot) {
	r.embeddedCommands(node.Condition, *scope, slot)
	blocks := slices.Clone(node.Blocks)
	if node.Kind == "if" {
		if literal, ok := node.Condition.(*groovyLiteral); ok {
			switch literal.Text {
			case "false":
				blocks = blocks[1:]
			case "true":
				blocks = blocks[:1]
			}
		}
	}
	if node.Finally != nil {
		blocks = append(blocks, node.Finally)
	}
	for _, block := range blocks {
		r.block(block, *scope, slot)
	}
	// What a branch assigns is unknown after it.
	for _, block := range append(append(slices.Clone(node.Blocks), node.Catches...), node.Finally) {
		if block == nil {
			continue
		}
		for _, statement := range block.Statements {
			if assign, ok := statement.(*groovyAssign); ok {
				if target, ok := assign.Target.(*groovyName); ok {
					*scope = scope.with(func(next *jenkinsScope) {
						if len(target.Path) == 2 && target.Path[0] == "env" {
							next.env[target.Path[1]] = unresolvedCIValue(r.text(assign))
						} else if len(target.Path) == 1 {
							delete(next.vars, target.Path[0])
						}
					})
				}
			}
		}
	}
}

// call reads one step call.
func (r *jenkinsReader) call(call *groovyCall, scope jenkinsScope, slot *jenkinsSlot) {
	positional, named := jenkinsArgs(call)
	body := func(next jenkinsScope) {
		if call.Closure != nil {
			r.block(call.Closure.Body, next, slot)
		}
	}
	if call.Receiver != nil {
		r.method(call, scope, slot)
		return
	}
	switch name := call.Name; {
	case name == "sh":
		r.shell(call, scope, slot)
	case name == "bat" || name == "powershell" || name == "pwsh":
		script := ""
		if len(positional) > 0 {
			script, _ = r.value(positional[0], scope)
		} else if value, ok := named["script"]; ok {
			script, _ = r.value(value, scope)
		}
		r.add(slot, call, runtimeStep{Run: script, ResolutionError: "runs " + name + ", a Windows command; ddtest does not set up or validate Windows agents"})
	case name == "dir":
		target := ""
		problem := "its directory is not literal"
		if len(positional) == 1 {
			target, problem = r.value(positional[0], scope)
		}
		body(scope.with(func(next *jenkinsScope) {
			if rest, ok := strings.CutPrefix(target, "$WORKSPACE"); ok && (rest == "" || strings.HasPrefix(rest, "/")) {
				target = "." + rest
			}
			joined := path.Clean(path.Join(scope.dir, target))
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
		body(scope.with(func(next *jenkinsScope) {
			items := []string(nil)
			literal := len(positional) == 1
			if list, ok := firstNode(positional).(*groovyList); ok {
				for _, item := range list.Items {
					text, problem := r.value(item, scope)
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
				next.overrides[key] = "withEnv at " + r.where(call)
			}
		}))
	case name == "withCredentials":
		body(scope.with(func(next *jenkinsScope) {
			text := groovyText(r.source, firstNode(positional))
			for _, match := range jenkinsCredentialVariable.FindAllStringSubmatch(text, -1) {
				next.env[match[1]] = jenkinsSecret(match[1])
				if match[1] == "DD_API_KEY" {
					r.review = append(r.review, r.file+": withCredentials at "+r.where(call)+" binds DD_API_KEY; the Datadog Jenkins plugin overwrites DD_API_KEY with its own configured key, so this binding is not the key the tests report with.")
				}
			}
		}))
	case slices.Contains(jenkinsToolWrappers, name):
		body(scope.with(func(next *jenkinsScope) { next.pathChange = "the Jenkins tool " + r.text(call) }))
	case name == "node":
		label := ""
		if len(positional) > 0 {
			label, _ = r.value(positional[0], scope)
		}
		if value, ok := named["label"]; ok {
			label, _ = r.value(value, scope)
		}
		nodeName := "node"
		if label != "" {
			nodeName = "node (" + label + ")"
		}
		if len(scope.stages) > 0 {
			nodeName = strings.Join(scope.stages, " / ")
		}
		inner := scope.with(func(next *jenkinsScope) {
			next.agent = jenkinsAgent{kind: "host", label: label, describe: "node('" + label + "')"}
			if label == "" {
				next.agent.describe = "node"
			}
		})
		nodeSlot := &jenkinsSlot{name: nodeName, scope: inner}
		if call.Closure != nil {
			r.block(call.Closure.Body, inner, nodeSlot)
		}
		r.finish(nodeSlot)
	case name == "stage":
		if call.Closure == nil {
			return
		}
		stageName := r.stageName(call, scope)
		inner := scope.with(func(next *jenkinsScope) { next.stages = append(next.stages, stageName) })
		stageSlot := &jenkinsSlot{name: jobName(scope.stages, stageName, scope.suffix), scope: inner}
		r.block(call.Closure.Body, inner, stageSlot)
		r.finish(stageSlot)
	case name == "parallel":
		branches := 0
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
			branches++
			inner := scope.with(func(next *jenkinsScope) { next.stages = append(next.stages, arg.Name) })
			branchSlot := &jenkinsSlot{name: jobName(scope.stages, arg.Name, scope.suffix), scope: inner}
			r.block(closure.Body, inner, branchSlot)
			r.finish(branchSlot)
		}
		if call.Closure != nil {
			// A declarative parallel { stage … } inside a script block.
			r.stages(call.Closure.Body, scope)
		}
	case name == "container":
		image := ""
		if len(positional) > 0 {
			image, _ = r.value(positional[0], scope)
		}
		body(scope.with(func(next *jenkinsScope) {
			next.agent = jenkinsAgent{kind: "container", describe: "container('" + image + "') of a Kubernetes pod"}
		}))
	case strings.HasSuffix(name, ".inside"):
		// image.inside { … } on a variable holding docker.image(…) or
		// docker.build(…).
		body(scope.with(func(next *jenkinsScope) {
			next.agent = jenkinsAgent{kind: "container", describe: name + " (a Docker container)"}
		}))
	case name == "datadog":
		body(scope.with(func(next *jenkinsScope) { next.setup = r.datadog(call, scope) }))
	case slices.Contains(jenkinsWrappers, name):
		body(scope)
	case name == "pipeline" || slices.Contains(jenkinsNonCommandSteps, name):
		if name != "pipeline" && name != "echo" && name != "print" && name != "println" && name != "error" {
			r.nonTest = append(r.nonTest, name)
		}
		if name == "library" {
			for _, arg := range positional {
				r.libraries = append(r.libraries, r.literals(arg)...)
			}
		}
	case name == "load" || name == "evaluate":
		r.unresolved(slot, call, name+" runs Groovy code from "+r.text(firstNode(positional))+", which ddtest does not read; its steps may run tests")
	case name == "build":
		r.unresolved(slot, call, "build triggers another Jenkins job ("+r.text(call)+"), whose steps ddtest cannot read; they may run tests. Validate that job's Jenkinsfile separately")
	case r.functions[name]:
		r.unresolved(slot, call, "it calls "+name+"(), a Groovy function of the Jenkinsfile, which ddtest does not follow; it may run tests")
	default:
		reason := "it calls " + name + ", which is not a Pipeline step ddtest knows; " + r.libraryNote() + ", and may run tests"
		if call.Closure == nil {
			r.unresolved(slot, call, reason)
			return
		}
		body(scope.with(func(next *jenkinsScope) {
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
func (r *jenkinsReader) method(call *groovyCall, scope jenkinsScope, slot *jenkinsSlot) {
	receiver, _ := call.Receiver.(*groovyCall)
	r.embeddedCommands(call.Receiver, scope, slot)
	if receiver != nil && (receiver.Name == "docker.image" || receiver.Name == "docker.build") {
		image := ""
		if args, _ := jenkinsArgs(receiver); receiver.Name == "docker.image" && len(args) > 0 {
			image, _ = r.value(args[0], scope)
		}
		switch call.Name {
		case "inside":
			describe := receiver.Name + "(…).inside"
			if image != "" {
				describe = "docker.image('" + image + "').inside"
			}
			if call.Closure != nil {
				r.block(call.Closure.Body, scope.with(func(next *jenkinsScope) {
					next.agent = jenkinsAgent{kind: "container", image: image, describe: describe}
				}), slot)
			}
			return
		case "withRun":
			// A service container; the closure runs on the agent.
			if call.Closure != nil {
				r.block(call.Closure.Body, scope, slot)
			}
			return
		case "pull", "push", "tag":
			return
		}
	}
	if call.Closure != nil {
		r.block(call.Closure.Body, scope.with(func(next *jenkinsScope) {
			next.problem = joinDiagnostic(next.problem, "it runs inside "+r.text(call.Receiver)+"."+call.Name+"(…), which ddtest cannot read")
		}), slot)
		return
	}
	r.unresolved(slot, call, "it calls "+r.text(call)+", which ddtest cannot read; it may run tests")
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
			r.review = append(r.review, r.file+": "+key+" set in "+where+" is overwritten by the Datadog Jenkins plugin, which sets it once per build for every step; set those options inside the sh script that runs the tests instead.")
		}
		if _, overridden := env[key]; overridden {
			state.env[key] = env[key]
		}
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
		step.Run = command
		step.ResolutionError = stepReason
		added := r.add(slot, call, step)
		if stepReason == "" && state.apply(command, mergeEnvironment(env, state.env)) {
			added.Run = ""
		}
	}
	if job := slot.job; job != nil {
		if scope.agent.kind == "container" && job.container == "" {
			job.container, job.image = scope.agent.describe, scope.agent.image
		}
		if pathChange != "" && job.pathChange == "" {
			job.pathChange = pathChange
		}
	}
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
	switch {
	case job.container != "":
		result.Image = job.image
		result.ImageTemplate = job.image
		if job.image == "" {
			result.ImageProblem = "the job runs in " + job.container + ", whose image ddtest does not read"
		}
		result.SetupUnsupported = "it runs in " + job.container + ": the Datadog Jenkins plugin installs the tracer on the build agent and does not instrument tests inside containers. Run these tests on a host agent (agent { label … }), or set up Test Optimization in the container yourself (ddtest cannot validate that)"
	case scope.agent.kind == "host":
		result.Host = jenkinsHost(scope.agent, job.pathChange)
	}
	steps := job.steps
	if setup := scope.setup; setup != nil {
		current := setup.ciSetup()
		problem := scope.setupProblem
		if result.SetupUnsupported != "" {
			problem = joinDiagnostic(problem, "This stage cannot use the Datadog Jenkins plugin: "+result.SetupUnsupported+".")
		}
		if problem != "" && current.Problem == "" {
			current.Problem = problem
		}
		if job.pathChange != "" {
			current.Confirmations = append(current.Confirmations, jenkinsRuntimeMatches)
		}
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

// pluginExports are the variables the Datadog Jenkins plugin sets in every
// step of a job in its scope, for each language it selects.
func (r *jenkinsReader) pluginExports(scope jenkinsScope) (map[string]string, map[string]string) {
	if scope.setup == nil || scope.setup.problem() != "" {
		return nil, nil
	}
	// The plugin computes its variables once per build: a narrower scope's
	// value never reaches them (see shell).
	base := maps.Clone(scope.env)
	for key := range scope.overrides {
		delete(base, key)
	}
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
