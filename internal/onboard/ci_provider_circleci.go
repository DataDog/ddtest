// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
	"mvdan.cc/sh/v3/syntax"
)

// circleCIProvider is the --ci-provider value of CircleCI.
const circleCIProvider = "circleci"

// circleCIFile is the configuration CircleCI reads.
const circleCIFile = ".circleci/config.yml"

// circleCI reads .circleci/config.yml. Every job a workflow runs becomes one
// job of the shared model per matrix combination, named
// <workflow>/<job>; reusable commands, executors, parameters and inline orbs
// are expanded, and each run step becomes one step. Run steps don't share a
// shell: only what a step appends to $BASH_ENV, such as the Datadog orb's
// exports, reaches later steps. Its setup mechanism is Datadog's Test
// Optimization orb (datadog/test-optimization-circleci-orb). Anything ddtest
// cannot read locally, such as orbs from the registry or dynamic
// configuration, becomes an unresolved step: it is reported, never dropped
// or certified.
type circleCI struct{}

func (circleCI) name() string { return circleCIProvider }

func (circleCI) displayName() string { return "CircleCI" }

func (circleCI) environment() map[string]string {
	return map[string]string{"CI": "true", "CIRCLECI": "true"}
}

func (circleCI) sharedSyntax() bool { return false }

func (circleCI) configurationFiles(root string) ([]string, error) {
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(circleCIFile)))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find CircleCI configuration: %w", err)
	}
	if info.IsDir() {
		return nil, nil
	}
	return []string{circleCIFile}, nil
}

func (c circleCI) readWorkflows(root string) ([]ciWorkflow, error) {
	files, err := c.configurationFiles(root)
	if err != nil || len(files) == 0 {
		return nil, err
	}
	config, err := readCircleConfig(root)
	if err != nil {
		return nil, err
	}
	return []ciWorkflow{config.workflow()}, nil
}

func (c circleCI) findTestWorkflows(root, language, framework string) ([]string, []string, error) {
	workflows, err := c.readWorkflows(root)
	if err != nil {
		return nil, nil, err
	}
	found, configured := testWorkflowsWithSetup(root, workflows, language, framework)
	return found, configured, nil
}

func (circleCI) setupStep(step runtimeStep) (ciSetup, bool) {
	if step.Setup == nil {
		return ciSetup{}, false
	}
	return *step.Setup, true
}

// circleConfig is the parsed configuration.
type circleConfig struct {
	root string
	top  map[string]*yaml.Node
	// orbs are the declared orbs by alias.
	orbs map[string]circleOrb
	// pipeline holds the pipeline parameters' defaults.
	pipeline map[string]string
	// aliased marks nodes some part of the configuration reaches through a
	// YAML alias (*name), which one edit cannot change for one user only.
	aliased map[*yaml.Node]bool
	// gaps are configuration ddtest cannot read; review and nonTest are
	// configuration known to run no tests.
	gaps    []string
	review  []string
	nonTest []string
}

// circleOrb is one entry of orbs: a registry reference or an inline orb.
type circleOrb struct {
	alias string
	// reference is the registry reference as declared, such as
	// datadog/test-optimization-circleci-orb@2; path and version are its
	// parts.
	reference, path, version string
	// inline holds an inline orb's definitions.
	inline map[string]*yaml.Node
}

// circleValue is a parameter's value: text, or a list of steps (parameters
// of type steps) expanded in the scope that passed it.
type circleValue struct {
	text  string
	steps *yaml.Node
	scope *circleScope
	// unresolved reports a value ddtest cannot know, such as a required
	// parameter without a value.
	unresolved bool
}

// circleScope is where << parameters.x >> and << matrix.x >> resolve.
type circleScope struct {
	parameters map[string]circleValue
	matrix     map[string]string
	// orb is the inline orb whose definitions a name without an alias
	// refers to inside it.
	orb *circleOrb
}

// circleReserved are workflow job keys that are not job parameters.
var circleReserved = []string{"requires", "context", "filters", "matrix", "name", "type", "pre-steps", "post-steps", "serial-group", "override-with"}

// circleNoOpSteps are built-in steps that only move files between jobs or
// containers; they run nothing ddtest replays.
var circleNoOpSteps = []string{"save_cache", "restore_cache", "store_artifacts", "store_test_results", "persist_to_workspace", "attach_workspace", "add_ssh_keys", "setup_remote_docker"}

// circleSizeLimit bounds the configuration ddtest reads.
const circleSizeLimit = 2 << 20

func readCircleConfig(root string) (*circleConfig, error) {
	info, ok := repositoryFile(root, root, circleCIFile)
	if !ok || info.IsDir() {
		return nil, fmt.Errorf("%s is not a file in the repository", circleCIFile)
	}
	if info.Size() > circleSizeLimit {
		return nil, fmt.Errorf("%s exceeds 2 MiB", circleCIFile)
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(circleCIFile)))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", circleCIFile, err)
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("parse %s: %w", circleCIFile, err)
	}
	config := &circleConfig{root: root, orbs: map[string]circleOrb{}, pipeline: map[string]string{}, aliased: map[*yaml.Node]bool{}}
	if len(document.Content) == 0 {
		config.top = map[string]*yaml.Node{}
		return config, nil
	}
	body := document.Content[0]
	if body.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("parse %s: the configuration must be a YAML mapping", circleCIFile)
	}
	config.markAliases(body, false, 0)
	config.top = gitLabMembers(body)
	if gitLabScalar(config.top["setup"]) == "true" {
		config.gaps = append(config.gaps, "setup: true makes this a dynamic configuration that continues with configuration generated when the pipeline runs")
	}
	for name, definition := range gitLabMembers(config.top["parameters"]) {
		if value := gitLabMembers(definition)["default"]; value != nil {
			config.pipeline[name] = gitLabScalar(value)
		}
	}
	for alias, value := range gitLabMembers(config.top["orbs"]) {
		value = gitLabAlias(value)
		orb := circleOrb{alias: alias}
		if value != nil && value.Kind == yaml.MappingNode {
			orb.inline = gitLabMembers(value)
		} else {
			orb.reference = gitLabScalar(value)
			orb.path, orb.version, _ = strings.Cut(orb.reference, "@")
		}
		config.orbs[alias] = orb
	}
	return config, nil
}

// markAliases records every node reached through an alias, and their
// children.
func (c *circleConfig) markAliases(node *yaml.Node, through bool, depth int) {
	if node == nil || depth > 64 {
		return
	}
	if node.Kind == yaml.AliasNode {
		c.markAliases(node.Alias, true, depth+1)
		return
	}
	if through {
		c.aliased[node] = true
	}
	for _, child := range node.Content {
		c.markAliases(child, through, depth+1)
	}
}

// circleParameter matches a CircleCI parameter reference: << parameters.x >>,
// << pipeline.parameters.x >>, << matrix.x >> and other pipeline values.
var circleParameter = regexp.MustCompile(`<<\s*((?:parameters|pipeline|matrix)\.[A-Za-z0-9_.-]+)\s*>>`)

// circleUnresolvedMarker is left in text for a reference ddtest cannot
// resolve, as written.
func circleReads(text string) bool {
	return circleParameter.MatchString(text) || strings.Contains(text, "<<include(")
}

// substitute replaces the parameter references of text that the scope and
// the pipeline parameters resolve; others stay as written.
func (c *circleConfig) substitute(text string, scope *circleScope) string {
	for depth := 0; depth < 8 && circleParameter.MatchString(text); depth++ {
		changed := false
		text = circleParameter.ReplaceAllStringFunc(text, func(reference string) string {
			name := circleParameter.FindStringSubmatch(reference)[1]
			if value, ok := c.reference(name, scope); ok {
				changed = true
				return value
			}
			return reference
		})
		if !changed {
			break
		}
	}
	return text
}

// reference resolves one parameter reference to text.
func (c *circleConfig) reference(name string, scope *circleScope) (string, bool) {
	switch {
	case strings.HasPrefix(name, "pipeline.parameters."):
		value, ok := c.pipeline[strings.TrimPrefix(name, "pipeline.parameters.")]
		return value, ok
	case strings.HasPrefix(name, "parameters.") && scope != nil:
		value, ok := scope.parameters[strings.TrimPrefix(name, "parameters.")]
		if !ok || value.unresolved || value.steps != nil {
			return "", false
		}
		return value.text, true
	case strings.HasPrefix(name, "matrix.") && scope != nil:
		value, ok := scope.matrix[strings.TrimPrefix(name, "matrix.")]
		return value, ok
	}
	return "", false
}

// parameters builds the scope of a job, command or executor: its declared
// parameters' defaults, overridden by the arguments passed, which the
// caller's scope already resolved.
func (c *circleConfig) parameters(definition *yaml.Node, arguments map[string]*yaml.Node, caller *circleScope, orb *circleOrb) *circleScope {
	scope := &circleScope{parameters: map[string]circleValue{}, orb: orb}
	if caller != nil {
		scope.matrix = caller.matrix
	}
	for name, declaration := range gitLabMembers(gitLabMembers(definition)["parameters"]) {
		members := gitLabMembers(declaration)
		value := circleValue{unresolved: true}
		if fallback := gitLabAlias(members["default"]); fallback != nil {
			value = c.value(fallback, &circleScope{orb: orb})
		}
		scope.parameters[name] = value
	}
	for name, argument := range arguments {
		scope.parameters[name] = c.value(gitLabAlias(argument), caller)
	}
	return scope
}

// value reads one parameter value in scope.
func (c *circleConfig) value(node *yaml.Node, scope *circleScope) circleValue {
	if node == nil {
		return circleValue{}
	}
	if node.Kind == yaml.SequenceNode {
		return circleValue{steps: node, scope: scope}
	}
	if node.Kind != yaml.ScalarNode {
		return circleValue{unresolved: true}
	}
	text := c.substitute(node.Value, scope)
	if node.Tag == "!!null" {
		text = ""
	}
	return circleValue{text: text, unresolved: circleReads(text)}
}

// circleTruth is what a logic statement evaluates to.
type circleTruth int

const (
	circleUnknown circleTruth = iota
	circleTrue
	circleFalse
)

// condition evaluates a when/unless logic statement: literals, and, or,
// not, equal and matches over resolved parameters. Pipeline values ddtest
// does not know make it unknown.
func (c *circleConfig) condition(node *yaml.Node, scope *circleScope, depth int) circleTruth {
	node = gitLabAlias(node)
	if node == nil || depth > 16 {
		return circleUnknown
	}
	switch node.Kind {
	case yaml.ScalarNode:
		if node.Tag == "!!null" {
			return circleFalse
		}
		text := c.substitute(node.Value, scope)
		if circleReads(text) {
			return circleUnknown
		}
		switch strings.TrimSpace(text) {
		case "", "false", "0", "null", "NaN":
			return circleFalse
		}
		return circleTrue
	case yaml.SequenceNode:
		return circleUnknown
	}
	members := gitLabMembers(node)
	if len(members) != 1 {
		return circleUnknown
	}
	for key, value := range members {
		switch key {
		case "not":
			switch c.condition(value, scope, depth+1) {
			case circleTrue:
				return circleFalse
			case circleFalse:
				return circleTrue
			}
			return circleUnknown
		case "and", "or":
			value = gitLabAlias(value)
			if value == nil || value.Kind != yaml.SequenceNode {
				return circleUnknown
			}
			decided, result := circleTrue, circleTrue
			if key == "or" {
				decided, result = circleFalse, circleFalse
			}
			for _, item := range value.Content {
				truth := c.condition(item, scope, depth+1)
				switch {
				case truth == circleUnknown:
					result = circleUnknown
				case truth != decided:
					return truth
				}
			}
			return result
		case "equal":
			value = gitLabAlias(value)
			if value == nil || value.Kind != yaml.SequenceNode || len(value.Content) == 0 {
				return circleUnknown
			}
			var first string
			for i, item := range value.Content {
				text := c.substitute(gitLabScalar(item), scope)
				if gitLabAlias(item).Kind != yaml.ScalarNode || circleReads(text) {
					return circleUnknown
				}
				if i == 0 {
					first = text
				} else if text != first {
					return circleFalse
				}
			}
			return circleTrue
		case "matches":
			fields := gitLabMembers(value)
			pattern, text := c.substitute(gitLabScalar(fields["pattern"]), scope), c.substitute(gitLabScalar(fields["value"]), scope)
			expression, err := regexp.Compile(pattern)
			if err != nil || circleReads(pattern+text) {
				return circleUnknown
			}
			if expression.MatchString(text) {
				return circleTrue
			}
			return circleFalse
		}
	}
	return circleUnknown
}

// circleWhen decides a when or unless clause: when runs on true, unless on
// false.
func (c *circleConfig) circleWhen(fields map[string]*yaml.Node, scope *circleScope) circleTruth {
	truth := circleTrue
	if condition, ok := fields["when"]; ok {
		truth = c.condition(condition, scope, 0)
	}
	if condition, ok := fields["unless"]; ok {
		switch c.condition(condition, scope, 0) {
		case circleTrue:
			return circleFalse
		case circleUnknown:
			if truth == circleTrue {
				truth = circleUnknown
			}
		}
	}
	return truth
}

// circleInvocation is one job a workflow runs, after matrix expansion.
type circleInvocation struct {
	workflow, job, name string
	arguments           map[string]*yaml.Node
	matrix              map[string]string
	pre, post           *yaml.Node
	disabled            bool
	source              string
}

// invocations lists the jobs every workflow runs. A configuration without
// workflows runs its build job.
func (c *circleConfig) invocations() []circleInvocation {
	workflows := gitLabAlias(c.top["workflows"])
	if workflows == nil {
		if c.top["jobs"] != nil && gitLabMembers(c.top["jobs"])["build"] != nil {
			return []circleInvocation{{workflow: "build", job: "build", name: "build", source: circleCIFile}}
		}
		return nil
	}
	var invocations []circleInvocation
	for _, workflowName := range gitLabKeys(workflows) {
		definition := gitLabMembers(workflows)[workflowName]
		if workflowName == "version" || gitLabAlias(definition).Kind != yaml.MappingNode {
			continue
		}
		fields := gitLabMembers(definition)
		disabled := c.circleWhen(fields, nil) == circleFalse
		list := gitLabAlias(fields["jobs"])
		if list == nil || list.Kind != yaml.SequenceNode {
			continue
		}
		for _, entry := range list.Content {
			entry = gitLabAlias(entry)
			source := circleCIFile + ":" + strconv.Itoa(entry.Line)
			if entry.Kind == yaml.ScalarNode {
				invocations = append(invocations, circleInvocation{workflow: workflowName, job: entry.Value, name: entry.Value, disabled: disabled, source: source})
				continue
			}
			for _, job := range gitLabKeys(entry) {
				arguments := gitLabMembers(gitLabMembers(entry)[job])
				if kind := gitLabScalar(arguments["type"]); kind == "approval" || kind == "no-op" {
					continue
				}
				rows, err := circleMatrix(arguments["matrix"])
				if err != nil {
					c.gaps = append(c.gaps, "workflow "+workflowName+" job "+job+": "+err.Error())
					continue
				}
				for _, row := range rows {
					invocation := circleInvocation{workflow: workflowName, job: job, arguments: map[string]*yaml.Node{}, matrix: row, pre: arguments["pre-steps"], post: arguments["post-steps"], disabled: disabled, source: source}
					scope := &circleScope{matrix: row}
					for key, value := range arguments {
						if slices.Contains(circleReserved, key) {
							continue
						}
						invocation.arguments[key] = circleMatrixNode(value, c, scope)
					}
					for _, key := range slices.Sorted(maps.Keys(row)) {
						invocation.arguments[key] = &yaml.Node{Kind: yaml.ScalarNode, Value: row[key]}
					}
					invocation.name = job
					if name := gitLabScalar(arguments["name"]); name != "" {
						invocation.name = c.substitute(name, scope)
					} else if len(row) > 0 {
						for _, key := range slices.Sorted(maps.Keys(row)) {
							invocation.name += "-" + row[key]
						}
					}
					invocations = append(invocations, invocation)
				}
			}
		}
	}
	return invocations
}

// circleMatrixNode substitutes matrix values in a scalar argument.
func circleMatrixNode(node *yaml.Node, c *circleConfig, scope *circleScope) *yaml.Node {
	node = gitLabAlias(node)
	if node == nil || node.Kind != yaml.ScalarNode {
		return node
	}
	copied := *node
	copied.Value = c.substitute(node.Value, scope)
	return &copied
}

// circleMatrix expands matrix parameters into rows, without excluded
// combinations; a job without a matrix has one empty row.
func circleMatrix(matrix *yaml.Node) ([]map[string]string, error) {
	matrix = gitLabAlias(matrix)
	if matrix == nil {
		return []map[string]string{nil}, nil
	}
	parameters := gitLabMembers(matrix)["parameters"]
	if gitLabAlias(parameters) == nil || gitLabAlias(parameters).Kind != yaml.MappingNode {
		return nil, fmt.Errorf("matrix without a parameters mapping")
	}
	rows := []map[string]string{{}}
	for _, name := range gitLabKeys(parameters) {
		values := gitLabAlias(gitLabMembers(parameters)[name])
		if values == nil || values.Kind != yaml.SequenceNode {
			return nil, fmt.Errorf("matrix parameter %s is not a list", name)
		}
		var next []map[string]string
		for _, row := range rows {
			for _, value := range values.Content {
				if gitLabAlias(value).Kind != yaml.ScalarNode {
					return nil, fmt.Errorf("matrix parameter %s has a value that is not a scalar", name)
				}
				expanded := maps.Clone(row)
				expanded[name] = gitLabScalar(value)
				next = append(next, expanded)
			}
		}
		rows = next
		if len(rows) > 200 {
			return nil, fmt.Errorf("matrix expands to more than 200 jobs")
		}
	}
	exclude := gitLabAlias(gitLabMembers(matrix)["exclude"])
	if exclude != nil {
		rows = slices.DeleteFunc(rows, func(row map[string]string) bool {
			for _, excluded := range exclude.Content {
				members := gitLabMembers(excluded)
				matches := len(members) > 0
				for key, value := range members {
					matches = matches && row[key] == gitLabScalar(value)
				}
				if matches {
					return true
				}
			}
			return false
		})
	}
	return rows, nil
}

// circleDefinition finds a job, command or executor by name: local, or
// from an inline orb as alias/name. orb is the inline orb it belongs to.
func (c *circleConfig) circleDefinition(kind, name string, scope *circleScope) (*yaml.Node, *circleOrb, bool) {
	if alias, local, ok := strings.Cut(name, "/"); ok {
		orb, declared := c.orbs[alias]
		if !declared || orb.inline == nil {
			return nil, nil, false
		}
		definition := gitLabMembers(orb.inline[kind])[local]
		return definition, &orb, definition != nil
	}
	if scope != nil && scope.orb != nil {
		if definition := gitLabMembers(scope.orb.inline[kind])[name]; definition != nil {
			return definition, scope.orb, true
		}
	}
	definition := gitLabMembers(c.top[kind])[name]
	return definition, nil, definition != nil
}

// circleJob is the context steps run in: one job invocation after its
// executor resolves.
type circleJob struct {
	name string
	env  map[string]string
	// image is the primary container's image, template as written.
	image, template string
	// imageProblem is why ddtest cannot tell the image's shell.
	imageProblem string
	// directory is the job's working directory, the checkout.
	directory string
	shell     string
	// keyProblem reports a DD_API_KEY written into the configuration.
	keyProblem bool
}

// circleExecutor reads a job's executor: docker, machine or macos keys, or
// a named executor with its parameters.
func (c *circleConfig) circleExecutor(job *circleJob, fields map[string]*yaml.Node, scope *circleScope, depth int) {
	if depth > 8 {
		job.imageProblem = "its executor nests more than 8 levels"
		return
	}
	if executor := gitLabAlias(fields["executor"]); executor != nil {
		name := c.substitute(gitLabScalar(executor), scope)
		arguments := map[string]*yaml.Node{}
		if executor.Kind == yaml.MappingNode {
			members := gitLabMembers(executor)
			name = c.substitute(gitLabScalar(members["name"]), scope)
			for key, value := range members {
				if key != "name" {
					arguments[key] = value
				}
			}
		}
		definition, orb, ok := c.circleDefinition("executors", name, scope)
		if !ok {
			job.imageProblem = "its executor " + name + " comes from an orb or parameter ddtest cannot read"
			return
		}
		inner := c.parameters(definition, arguments, scope, orb)
		c.circleExecutor(job, gitLabMembers(definition), inner, depth+1)
		job.env = mergeEnvironment(job.env, c.environment(gitLabMembers(definition)["environment"], inner, job))
		if directory := gitLabScalar(gitLabMembers(definition)["working_directory"]); directory != "" {
			job.directory = c.substitute(directory, inner)
		}
		if shell := gitLabScalar(gitLabMembers(definition)["shell"]); shell != "" {
			job.shell = c.substitute(shell, inner)
		}
	}
	if docker := gitLabAlias(fields["docker"]); docker != nil {
		if docker.Kind != yaml.SequenceNode || len(docker.Content) == 0 {
			job.imageProblem = "its docker executor lists no image"
			return
		}
		primary := gitLabMembers(docker.Content[0])
		job.template = gitLabScalar(primary["image"])
		job.image = c.substitute(job.template, scope)
		job.imageProblem = ""
		if circleReads(job.image) {
			job.imageProblem = "its image " + job.template + " depends on a parameter ddtest cannot resolve"
		}
		job.env = mergeEnvironment(job.env, c.environment(primary["environment"], scope, job))
	}
	for _, key := range []string{"machine", "macos"} {
		if machine := gitLabAlias(fields[key]); machine != nil {
			job.image, job.template, job.imageProblem = "", "", ""
			image := strings.ToLower(c.substitute(gitLabScalar(gitLabMembers(machine)["image"]), scope))
			if strings.Contains(image, "windows") {
				job.imageProblem = "it runs on a Windows machine, where steps run in PowerShell"
			}
		}
	}
}

// environment reads an environment mapping. CircleCI sets values as
// written: a value reading a variable or a parameter ddtest cannot resolve
// stays unresolved.
func (c *circleConfig) environment(node *yaml.Node, scope *circleScope, job *circleJob) map[string]string {
	env := map[string]string{}
	for key, value := range gitLabMembers(node) {
		text := c.substitute(gitLabScalar(value), scope)
		if key == "DD_API_KEY" && job != nil {
			job.keyProblem = true
		}
		if circleReads(text) || gitLabReadsUnknown(text) && !strings.HasPrefix(key, "DD_") {
			text = unresolvedCIValue(text)
		}
		env[key] = text
	}
	return env
}

// workflow converts every invocation into the shared model.
func (c *circleConfig) workflow() ciWorkflow {
	workflow := ciWorkflow{Path: circleCIFile, Jobs: map[string]runtimeJob{}}
	for _, invocation := range c.invocations() {
		key := invocation.workflow + "/" + invocation.name
		unresolved := func(reason string) {
			workflow.Jobs[key] = runtimeJob{Steps: []runtimeStep{{Number: 1, Source: invocation.source, ResolutionError: "CircleCI job " + key + ": " + reason}}}
		}
		definition, orb, ok := c.circleDefinition("jobs", invocation.job, nil)
		if !ok {
			alias, _, fromOrb := strings.Cut(invocation.job, "/")
			if declared, known := c.orbs[alias]; fromOrb && known && declared.inline == nil {
				if purpose := circleNonTestOrbs[declared.path]; purpose != "" {
					c.nonTest = append(c.nonTest, declared.reference)
					c.review = append(c.review, circleCIFile+": workflow "+invocation.workflow+" runs "+invocation.job+" from "+declared.reference+", the "+purpose+"; it runs no tests and is outside this validation.")
					continue
				}
				if !invocation.disabled {
					unresolved("runs the orb job " + invocation.job + " from " + declared.reference + ", whose steps ddtest cannot read; they may run tests. Validate that job's tests separately.")
				}
				continue
			}
			if !invocation.disabled {
				unresolved("runs job " + invocation.job + ", which the configuration does not define")
			}
			continue
		}
		converted := c.convert(invocation, definition, orb)
		if invocation.disabled {
			converted.If = "false"
		}
		workflow.Jobs[key] = converted
	}
	c.gaps = slices.Compact(c.gaps)
	for i, gap := range c.gaps {
		name := fmt.Sprintf("(unresolved configuration %d)", i+1)
		workflow.Jobs[name] = runtimeJob{Steps: []runtimeStep{{Number: 1, Source: circleCIFile, ResolutionError: "CircleCI: " + circleCIFile + ": " + gap + "; its jobs may run tests. Supply the resolved configuration in the repository, or validate those jobs separately."}}}
	}
	slices.Sort(c.review)
	workflow.Review = slices.Compact(c.review)
	if len(c.nonTest) > 0 {
		slices.Sort(c.nonTest)
		workflow.Caveats = []string{circleCIFile + ": CircleCI orbs that only notify, scan or upload coverage run no tests and are outside this validation: " + strings.Join(slices.Compact(c.nonTest), ", ") + "."}
	}
	return workflow
}

// circleNonTestOrbs are registry orbs that only notify, scan code or upload
// coverage reports: none of their commands or jobs runs a project's tests.
// A use of one is reported, not a blocker; every other registry orb stays
// unresolved. Orbs match by namespace/name, at any version.
var circleNonTestOrbs = map[string]string{
	"circleci/slack":         "Slack notification orb",
	"circleci/jira":          "Jira notification orb",
	"snyk/snyk":              "Snyk security scanning orb",
	"sonarsource/sonarcloud": "SonarCloud code analysis orb",
	"codecov/codecov":        "Codecov coverage upload orb",
	"coveralls/coveralls":    "Coveralls coverage upload orb",
}

// convert builds one job of the shared model.
func (c *circleConfig) convert(invocation circleInvocation, definition *yaml.Node, orb *circleOrb) runtimeJob {
	fields := gitLabMembers(definition)
	scope := c.parameters(definition, invocation.arguments, &circleScope{matrix: invocation.matrix}, orb)
	job := &circleJob{name: invocation.workflow + "/" + invocation.name, env: map[string]string{}, directory: "~/project"}
	c.circleExecutor(job, fields, scope, 0)
	job.env = mergeEnvironment(job.env, c.environment(fields["environment"], scope, job))
	if directory := gitLabScalar(fields["working_directory"]); directory != "" {
		job.directory = c.substitute(directory, scope)
	}
	if shell := gitLabScalar(fields["shell"]); shell != "" {
		job.shell = c.substitute(shell, scope)
	}
	var raw []circleRawStep
	expander := circleExpander{config: c, job: job, steps: &raw}
	expander.expand(invocation.pre, &circleScope{matrix: invocation.matrix}, circleOwner{kind: "pre-steps"}, "", 0)
	steps := gitLabAlias(fields["steps"])
	if steps != nil && steps.Kind != yaml.SequenceNode {
		raw = append(raw, circleRawStep{source: invocation.source, problem: "its steps are not a list"})
	}
	owner := circleOwner{kind: "job", name: invocation.job, editable: orb == nil}
	expander.expand(steps, scope, owner, "", 0)
	expander.expand(invocation.post, &circleScope{matrix: invocation.matrix}, circleOwner{kind: "post-steps"}, "", 0)
	converted := runtimeJob{Env: job.env, Image: job.image, ImageTemplate: job.template}
	converted.Steps = c.steps(job, raw)
	return converted
}

// circleOwner is the part of the configuration a step is written in.
type circleOwner struct {
	// kind is job, command, pre-steps, post-steps or parameter.
	kind, name string
	// editable reports a step list in the configuration file itself, not in
	// an inline orb or a workflow's job arguments.
	editable bool
}

// circleStep is a step's origin, for prescribed edits.
type circleStep struct {
	// item is the step's entry, list its step list.
	item, list *yaml.Node
	owner      circleOwner
	// aliased reports an entry or list some job reaches through a YAML alias.
	aliased bool
	// setupProblem is why the Datadog orb step could not set up the job if
	// it ran right before this step.
	setupProblem string
}

// circleRawStep is one expanded step before $BASH_ENV is followed.
type circleRawStep struct {
	origin  circleStep
	source  string
	run     string
	env     map[string]string
	shell   string
	workdir string
	// problem makes the step unresolved; directoryProblem too, for a working
	// directory ddtest cannot map.
	problem string
	setup   *ciSetup
	// conditional is the when/unless condition ddtest cannot decide.
	conditional string
	review      string
}

// circleExpander expands one job's steps.
type circleExpander struct {
	config *circleConfig
	job    *circleJob
	steps  *[]circleRawStep
	// checkout is a checkout path that moves the repository.
	checkout string
}

func (e *circleExpander) add(step circleRawStep) { *e.steps = append(*e.steps, step) }

// expand appends the steps of list, resolved in scope. conditional is the
// undecided condition of an enclosing when/unless.
func (e *circleExpander) expand(list *yaml.Node, scope *circleScope, owner circleOwner, conditional string, depth int) {
	list = gitLabAlias(list)
	if list == nil {
		return
	}
	if depth > 16 {
		e.add(circleRawStep{source: circleCIFile + ":" + strconv.Itoa(list.Line), problem: "reusable commands nest more than 16 levels"})
		return
	}
	if list.Kind != yaml.SequenceNode {
		e.add(circleRawStep{source: circleCIFile + ":" + strconv.Itoa(list.Line), problem: "a step list is not a list"})
		return
	}
	for _, entry := range list.Content {
		item := gitLabAlias(entry)
		origin := circleStep{item: item, list: list, owner: owner, aliased: entry.Kind == yaml.AliasNode || e.config.aliased[item] || e.config.aliased[list]}
		source := circleCIFile + ":" + strconv.Itoa(item.Line)
		base := circleRawStep{origin: origin, source: source, conditional: conditional}
		if item.Kind == yaml.ScalarNode {
			name := strings.TrimSpace(item.Value)
			if e.stepsParameter(name, scope, base, conditional, depth) {
				continue
			}
			e.named(name, nil, scope, base, conditional, depth)
			continue
		}
		if item.Kind != yaml.MappingNode || len(item.Content) != 2 {
			base.problem = "step " + gitLabDescribe(item) + " is not a single step"
			e.add(base)
			continue
		}
		key, value := item.Content[0].Value, gitLabAlias(item.Content[1])
		switch key {
		case "run", "deploy":
			e.run(value, scope, base)
		case "steps":
			// - steps: << parameters.x >> or a literal step list.
			if value != nil && value.Kind == yaml.SequenceNode {
				e.expand(value, scope, owner, conditional, depth+1)
			} else if !e.stepsParameter(strings.TrimSpace(gitLabScalar(value)), scope, base, conditional, depth) {
				base.problem = "step " + gitLabDescribe(item) + " is not a step list ddtest can read"
				e.add(base)
			}
		case "checkout":
			if location := e.config.substitute(gitLabScalar(gitLabMembers(value)["path"]), scope); location != "" && location != "." && location != e.job.directory {
				e.checkout = location
			}
		case "when", "unless":
			fields := gitLabMembers(value)
			truth := e.config.condition(fields["condition"], scope, 0)
			if key == "unless" {
				truth = map[circleTruth]circleTruth{circleTrue: circleFalse, circleFalse: circleTrue}[truth]
			}
			if truth == circleFalse {
				continue
			}
			inner := conditional
			if truth == circleUnknown {
				inner = joinDiagnostic(conditional, key+": "+gitLabDescribe(fields["condition"]))
			}
			e.expand(fields["steps"], scope, owner, inner, depth+1)
		default:
			if slices.Contains(circleNoOpSteps, key) {
				continue
			}
			arguments := gitLabMembers(value)
			e.named(key, arguments, scope, base, conditional, depth)
		}
	}
}

// stepsParameter expands a reference to a parameter of type steps, and
// reports whether name is one.
func (e *circleExpander) stepsParameter(name string, scope *circleScope, base circleRawStep, conditional string, depth int) bool {
	match := circleParameter.FindStringSubmatch(name)
	if match == nil || match[0] != name || !strings.HasPrefix(match[1], "parameters.") {
		return false
	}
	value, ok := scope.parameters[strings.TrimPrefix(match[1], "parameters.")]
	if !ok || value.steps == nil {
		if ok && !value.unresolved && strings.TrimSpace(value.text) == "" {
			return true // An empty steps parameter.
		}
		base.problem = "the steps parameter " + name + " has no steps ddtest can read"
		e.add(base)
		return true
	}
	e.expand(value.steps, value.scope, circleOwner{kind: "parameter", name: name}, conditional, depth+1)
	return true
}

// named expands a reusable command or orb command invocation.
func (e *circleExpander) named(name string, arguments map[string]*yaml.Node, scope *circleScope, base circleRawStep, conditional string, depth int) {
	switch name {
	case "checkout":
		return
	}
	if slices.Contains(circleNoOpSteps, name) {
		return
	}
	if alias, command, fromOrb := strings.Cut(name, "/"); fromOrb {
		orb, declared := e.config.orbs[alias]
		if declared && orb.inline == nil {
			if orb.path == circleSetupOrb && command == "autoinstrument" {
				setup := e.config.circleSetup(orb, arguments, scope)
				base.setup = &setup
				e.add(base)
				return
			}
			if purpose := circleNonTestOrbs[orb.path]; purpose != "" {
				e.config.nonTest = append(e.config.nonTest, orb.reference)
				e.config.review = append(e.config.review, circleCIFile+": "+name+" from "+orb.reference+" is the "+purpose+"; it runs no tests and is outside this validation.")
				return
			}
			base.problem = "the orb command " + name + " from " + orb.reference + " runs steps ddtest cannot read; they may run tests. Validate them separately"
			e.add(base)
			return
		}
	}
	definition, orb, ok := e.config.circleDefinition("commands", name, scope)
	if !ok {
		base.problem = "step " + name + " is not a command the configuration defines"
		e.add(base)
		return
	}
	inner := e.config.parameters(definition, arguments, scope, orb)
	owner := circleOwner{kind: "command", name: name, editable: orb == nil}
	e.expand(gitLabMembers(definition)["steps"], inner, owner, conditional, depth+1)
}

// run reads a run step: a command string or a mapping with command,
// environment, working_directory, shell, background and when.
func (e *circleExpander) run(value *yaml.Node, scope *circleScope, step circleRawStep) {
	fields := map[string]*yaml.Node{"command": value}
	if value != nil && value.Kind == yaml.MappingNode {
		fields = gitLabMembers(value)
	}
	switch gitLabScalar(fields["when"]) {
	case "on_fail":
		return // It never runs when the job's earlier steps pass.
	}
	step.run = e.config.substitute(gitLabScalar(fields["command"]), scope)
	step.env = e.config.environment(fields["environment"], scope, e.job)
	if circleReads(step.run) {
		step.problem = "the command reads " + circleParameter.FindString(step.run) + ", which only CircleCI knows when the pipeline runs"
	}
	shell := e.job.shell
	if written := gitLabScalar(fields["shell"]); written != "" {
		shell = e.config.substitute(written, scope)
	}
	step.shell = "bash"
	if shell != "" {
		switch shellProgram(shell) {
		case "bash":
		case "sh":
			step.shell = "sh"
		default:
			step.problem = joinDiagnostic(step.problem, "the step runs with the shell "+shell+", which ddtest cannot replay")
		}
	}
	if background := gitLabScalar(fields["background"]); background == "true" {
		step.problem = joinDiagnostic(step.problem, "the step runs in the background alongside later steps, which ddtest cannot replay")
	}
	if e.checkout != "" {
		step.problem = joinDiagnostic(step.problem, "the job checks the repository out into "+e.checkout+", which ddtest does not map")
	}
	directory := e.config.substitute(gitLabScalar(fields["working_directory"]), scope)
	if directory != "" {
		mapped, err := circleDirectory(e.job.directory, directory)
		if err != nil {
			step.problem = joinDiagnostic(step.problem, err.Error())
		}
		step.workdir = mapped
	}
	if step.problem == "" {
		if command, review, err := circleTestSplitting(step.run); err != nil {
			step.problem = err.Error()
		} else if review != "" {
			step.run, step.review = command, review
		}
	}
	e.add(step)
}

// circleDirectory maps a step's working directory into the repository: the
// job's working directory is the checkout.
func circleDirectory(job, step string) (string, error) {
	clean := func(value string) string {
		value = strings.TrimSuffix(value, "/")
		for _, home := range []string{"/home/circleci/", "$HOME/", "${HOME}/"} {
			if rest, ok := strings.CutPrefix(value, home); ok {
				return "~/" + rest
			}
		}
		return value
	}
	job, step = clean(job), clean(step)
	if step == job {
		return "", nil
	}
	if rest, ok := strings.CutPrefix(step, job+"/"); ok {
		step = rest
	}
	if strings.HasPrefix(step, "/") || strings.HasPrefix(step, "~") || strings.Contains(step, "$") || circleReads(step) {
		return "", fmt.Errorf("the step's working directory %s is outside the checkout ddtest reads", step)
	}
	cleaned := path.Clean(step)
	if cleaned == "." {
		return "", nil
	}
	if strings.HasPrefix(cleaned, "..") {
		return "", fmt.Errorf("the step's working directory %s is outside the repository", step)
	}
	return cleaned, nil
}

// circleSplit matches CircleCI's test splitting: circleci tests glob
// patterns piped to circleci tests run --command="xargs <command>" or to
// circleci tests split and xargs <command>.
var (
	circleSplitRun   = regexp.MustCompile(`^circleci tests glob\s+(.+?)\s*\|\s*circleci tests run\s+(.*)$`)
	circleSplitXargs = regexp.MustCompile(`^circleci tests glob\s+(.+?)\s*\|\s*circleci tests split(?:\s+--[a-z-]+(?:[= ](?:"[^"]*"|'[^']*'|[^\s|]+))?)*\s*\|\s*xargs\s+(.+)$`)
	circleSplitFlag  = regexp.MustCompile(`--command[= ](?:"([^"]*)"|'([^']*)'|(\S+))`)
)

// circleTestSplitting resolves a command that splits tests across parallel
// containers into the command each container runs on its share of the
// files, which local validation runs once on every test; review explains
// the difference. A splitting command in another form is unresolved.
func circleTestSplitting(command string) (resolved, review string, err error) {
	if !strings.Contains(command, "circleci tests") {
		return command, "", nil
	}
	if resolved, files, ok := circleSplitVariable(command); ok {
		return resolved, "CircleCI test splitting passes each parallel container its share of the test files in $" + files + "; local validation runs the tests once without that list, on every test.", nil
	}
	text := strings.TrimSpace(strings.Join(strings.Fields(strings.ReplaceAll(command, "\\\n", " ")), " "))
	inner := ""
	if match := circleSplitRun.FindStringSubmatch(text); match != nil {
		flag := circleSplitFlag.FindStringSubmatch(match[2])
		if flag != nil {
			inner = flag[1] + flag[2] + flag[3]
		}
	} else if match := circleSplitXargs.FindStringSubmatch(text); match != nil {
		inner = "xargs " + match[2]
	}
	rest, ok := strings.CutPrefix(strings.TrimSpace(inner), "xargs ")
	if !ok || strings.TrimSpace(rest) == "" || strings.ContainsAny(rest, "|;&`$()") {
		return "", "", fmt.Errorf("the command splits tests across parallel containers with circleci tests in a form ddtest cannot resolve: %s", command)
	}
	rest = strings.TrimSpace(rest)
	return rest, "CircleCI test splitting runs " + rest + " on each parallel container's share of the test files; local validation runs it once on every test.", nil
}

// circleSplitVariable resolves the documented splitting form that stores
// a container's share of the files in a variable, as in
// FILES="$(circleci tests glob "spec/**/*_spec.rb" | circleci tests split)"
// followed by commands passing $FILES: the assignment is removed and so is
// every unquoted or double-quoted read of the variable, which must be a
// whole word. Any other use leaves the command unresolved.
func circleSplitVariable(command string) (string, string, bool) {
	file, ok := parseCIShell(command)
	if !ok {
		return "", "", false
	}
	name := ""
	var start, end uint
	for _, stmt := range file.Stmts {
		call, ok := stmt.Cmd.(*syntax.CallExpr)
		if !ok || len(call.Args) > 0 || len(call.Assigns) != 1 || stmt.Background || len(stmt.Redirs) > 0 || call.Assigns[0].Value == nil {
			continue
		}
		value := shellText(call.Assigns[0].Value)
		pipeline := strings.Join(strings.Fields(strings.ReplaceAll(value, "\\\n", " ")), " ")
		if !strings.Contains(pipeline, "circleci tests") {
			continue
		}
		inner := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSuffix(strings.TrimPrefix(pipeline, `"`), `"`), "$("), ")")
		if name != "" || !circleSplitPipeline.MatchString(inner) {
			return "", "", false
		}
		name, start, end = call.Assigns[0].Name.Value, stmt.Pos().Offset(), stmt.End().Offset()
	}
	if name == "" {
		return "", "", false
	}
	rest := command[:start] + strings.TrimPrefix(strings.TrimLeft(command[end:], " \t"), "\n")
	if strings.Contains(rest, "circleci tests") {
		return "", "", false
	}
	// A read and the space or line continuation before it.
	reads := regexp.MustCompile(`(?m)(^|[ \t]*\\\n[ \t]*|[ \t]+)"?\$(?:` + name + `|\{` + name + `\})"?([ \t\n;]|$)`)
	rest = reads.ReplaceAllString(rest, "$2")
	if strings.Contains(rest, "$"+name) || strings.Contains(rest, "${"+name) {
		return "", "", false
	}
	// Without the list, continuation lines join the command they continue.
	rest = regexp.MustCompile(`[ \t]*\\\n[ \t]*`).ReplaceAllString(rest, " ")
	return strings.TrimSpace(rest), name, true
}

// circleSplitPipeline is circleci tests glob piped to circleci tests split.
var circleSplitPipeline = regexp.MustCompile(`^circleci tests glob\s+[^|;&$` + "`" + `]+\|\s*circleci tests split(?:\s+[^|;&$` + "`" + `]*)?$`)

// steps follows $BASH_ENV through the expanded steps: what a step appends
// to it, and the Datadog orb's exports, reach every later step and override
// their own environment. The orb step gets the problems of the job it runs
// in.
func (c *circleConfig) steps(job *circleJob, raw []circleRawStep) []runtimeStep {
	state := ciShellState{dialect: circleShellDialect, env: map[string]string{}, references: map[string]string{}, job: job.env}
	curl := false
	var steps []runtimeStep
	for i, item := range raw {
		item.origin.setupProblem = circleSetupProblem(job, item.conditional, curl)
		step := runtimeStep{Number: i + 1, Source: item.source, origin: item.origin, Shell: item.shell, WorkingDirectory: item.workdir}
		env := mergeEnvironment(item.env, state.env)
		if len(env) > 0 {
			step.Env = env
		}
		step.ResolutionError = joinDiagnostic(state.problem, item.problem)
		if step.ResolutionError != "" {
			step.ResolutionError = "CircleCI job " + job.name + ": " + step.ResolutionError
		}
		if item.review != "" {
			c.review = append(c.review, item.source+": "+item.review)
		}
		if item.setup != nil {
			setup := *item.setup
			if setup.Problem == "" {
				setup.Problem = circleSetupProblem(job, item.conditional, curl)
			}
			step.Setup = &setup
			if setup.Supported && setup.Problem == "" {
				state.applySetup(setup, mergeEnvironment(job.env, state.env))
			}
			steps = append(steps, step)
			continue
		}
		curl = curl || installsCurl(item.run)
		step.Run = item.run
		if item.problem == "" && circleFollowBashEnv(&state, item.run, mergeEnvironment(mergeEnvironment(job.env, item.env), state.env)) {
			step.Run = ""
		}
		steps = append(steps, step)
	}
	return steps
}
