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

// buildkiteProvider is the --ci-provider value of Buildkite.
const buildkiteProvider = "buildkite"

// buildkite reads every pipeline file in the repository: buildkite.yml and
// the YAML files under .buildkite/ that define steps. Each command step
// becomes one job of the shared model per matrix combination, and each
// top-level shell statement of its commands one step: a step's commands
// share one shell, so exports and cd carry into later statements, but every
// Buildkite step starts a fresh shell, possibly on another agent. Its setup
// mechanism is Datadog's installer, downloaded, checksum-verified and run in
// the test step's own commands (see ci_provider_buildkite_setup.go).
// Anything ddtest cannot read locally, such as triggered pipelines, plugins
// it does not know or pipelines generated with buildkite-agent pipeline
// upload, becomes an unresolved step: it is reported, never dropped or
// certified.
type buildkite struct{}

func (buildkite) name() string { return buildkiteProvider }

func (buildkite) displayName() string { return "Buildkite" }

func (buildkite) environment() map[string]string {
	return map[string]string{"CI": "true", "BUILDKITE": "true"}
}

func (buildkite) sharedSyntax() bool { return false }

// buildkiteRootFiles are the pipeline files Buildkite looks for at the
// repository root.
var buildkiteRootFiles = []string{"buildkite.yml", "buildkite.yaml", "buildkite.json", "buildkite/pipeline.yml", "buildkite/pipeline.yaml", "buildkite/pipeline.json"}

// buildkiteSizeLimit bounds every file ddtest reads.
const buildkiteSizeLimit = 2 << 20

// configurationFiles lists buildkite.yml and the YAML files under
// .buildkite/ (and .buildkite/pipelines/), without Docker Compose files.
// Which of them a Buildkite pipeline uploads is configured in the Buildkite
// UI, so every one that defines steps is read.
func (buildkite) configurationFiles(root string) ([]string, error) {
	var files []string
	for _, name := range buildkiteRootFiles {
		if info, ok := repositoryFile(root, root, name); ok && !info.IsDir() {
			files = append(files, name)
		}
	}
	for _, directory := range []string{".buildkite", ".buildkite/pipelines"} {
		entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(directory)))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("find Buildkite configuration: %w", err)
		}
		for _, entry := range entries {
			name := entry.Name()
			extension := path.Ext(name)
			pipeline := slices.Contains([]string{".yml", ".yaml"}, extension) || extension == ".json" && strings.HasPrefix(name, "pipeline")
			file := directory + "/" + name
			if entry.IsDir() || !pipeline || buildkiteComposeFile(root, file) {
				continue
			}
			if info, ok := repositoryFile(root, root, file); ok && !info.IsDir() {
				files = append(files, file)
			}
		}
	}
	return files, nil
}

// buildkiteComposeTop matches the top-level keys of a Compose file and of
// a pipeline.
var (
	buildkiteComposeTop  = regexp.MustCompile(`(?m)^(?:services|version)\s*:`)
	buildkitePipelineTop = regexp.MustCompile(`(?m)^(?:steps\s*:|- )`)
)

// buildkiteComposeFile reports a Docker Compose file: top-level services or
// version and no steps. Listing reads only its top-level keys.
func buildkiteComposeFile(root, file string) bool {
	info, ok := repositoryFile(root, root, file)
	if !ok || info.IsDir() || info.Size() > buildkiteSizeLimit {
		return false
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
	return err == nil && buildkiteComposeTop.Match(data) && !buildkitePipelineTop.Match(data)
}

func (b buildkite) readWorkflows(root string) ([]ciWorkflow, error) {
	files, err := b.configurationFiles(root)
	if err != nil || len(files) == 0 {
		return nil, err
	}
	config := &buildkiteConfig{root: root, aliased: map[*yaml.Node]bool{}, documents: map[string]*yaml.Node{}}
	// A file that cannot be parsed fails the read, as for every provider:
	// selection then leaves Buildkite out, and a selected Buildkite reports it.
	for _, file := range files {
		document, err := config.read(file)
		if err != nil {
			return nil, err
		}
		if document != nil {
			config.documents[file] = document
			config.order = append(config.order, file)
		}
	}
	var workflows []ciWorkflow
	for _, file := range config.order {
		workflows = append(workflows, config.workflow(file, config.documents[file]))
	}
	return workflows, nil
}

func (b buildkite) findTestWorkflows(root, language, framework string) ([]string, []string, error) {
	workflows, err := b.readWorkflows(root)
	if err != nil {
		return nil, nil, err
	}
	found, configured := testWorkflowsWithSetup(root, workflows, language, framework)
	return found, configured, nil
}

func (buildkite) setupStep(step runtimeStep) (ciSetup, bool) {
	if step.Setup == nil {
		return ciSetup{}, false
	}
	return *step.Setup, true
}

// buildkiteConfig is every pipeline file read, and what reading one found.
type buildkiteConfig struct {
	root string
	// documents are the pipeline files that define steps, in order.
	documents map[string]*yaml.Node
	order     []string
	// aliased marks nodes reached through a YAML alias (*name).
	aliased map[*yaml.Node]bool
	// review, nonTest and files belong to the workflow being converted.
	review, nonTest, files []string
}

// read parses one file: nil when it defines no steps, such as a file of
// YAML anchors or another tool's configuration.
func (c *buildkiteConfig) read(file string) (*yaml.Node, error) {
	info, ok := repositoryFile(c.root, c.root, file)
	if !ok || info.IsDir() {
		return nil, fmt.Errorf("%s is not a file in the repository", file)
	}
	if info.Size() > buildkiteSizeLimit {
		return nil, fmt.Errorf("%s exceeds 2 MiB", file)
	}
	data, err := os.ReadFile(filepath.Join(c.root, filepath.FromSlash(file)))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", file, err)
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("parse %s: %w", file, err)
	}
	if len(document.Content) == 0 {
		return nil, nil
	}
	body := document.Content[0]
	switch {
	case body.Kind == yaml.SequenceNode:
		// The legacy form: the document is the step list.
	case body.Kind == yaml.MappingNode && gitLabMembers(body)["steps"] != nil:
	default:
		return nil, nil
	}
	buildkiteMarkAliases(c.aliased, body, false, 0)
	return body, nil
}

// buildkiteMarkAliases records every node reached through an alias, and
// their children.
func buildkiteMarkAliases(aliased map[*yaml.Node]bool, node *yaml.Node, through bool, depth int) {
	if node == nil || depth > 64 {
		return
	}
	if node.Kind == yaml.AliasNode {
		buildkiteMarkAliases(aliased, node.Alias, true, depth+1)
		return
	}
	if through {
		aliased[node] = true
	}
	for _, child := range node.Content {
		buildkiteMarkAliases(aliased, child, through, depth+1)
	}
}

// buildkiteScope is what a step inherits from its pipeline and groups.
type buildkiteScope struct {
	file string
	// group prefixes job names with the enclosing groups' labels.
	group string
	// disabled reports an enclosing group that never runs.
	disabled bool
	// env is the pipeline's environment; known the values pipeline upload
	// interpolates into the rest of the file (see buildkiteInterpolate).
	env, known map[string]string
	// image is the pipeline's container image, agents its agent targeting.
	image  string
	agents *yaml.Node
}

// workflow converts one pipeline file.
func (c *buildkiteConfig) workflow(file string, body *yaml.Node) ciWorkflow {
	c.review, c.nonTest, c.files = nil, nil, nil
	workflow := ciWorkflow{Path: file, Jobs: map[string]runtimeJob{}}
	scope := buildkiteScope{file: file, env: map[string]string{}, known: map[string]string{}}
	steps := body
	if body.Kind == yaml.MappingNode {
		fields := gitLabMembers(body)
		steps = fields["steps"]
		// Pipeline upload interpolates the top-level env first, in order,
		// and then the rest of the file with it.
		envNode := gitLabAlias(fields["env"])
		for _, key := range gitLabKeys(envNode) {
			value, unknown := buildkiteInterpolate(gitLabScalar(gitLabMembers(envNode)[key]), scope.known)
			if len(unknown) > 0 && !strings.HasPrefix(key, "DD_") {
				value = unresolvedCIValue(value)
			} else {
				scope.known[key] = value
			}
			scope.env[key] = value
		}
		scope.image, _ = buildkiteInterpolate(gitLabScalar(fields["image"]), scope.known)
		scope.agents = fields["agents"]
	}
	c.walk(&workflow, steps, scope, 0)
	slices.Sort(c.review)
	workflow.Review = slices.Compact(c.review)
	if len(c.nonTest) > 0 {
		slices.Sort(c.nonTest)
		workflow.Caveats = []string{file + ": Buildkite plugins and steps that only fetch secrets, artifacts or caches, log in to registries, build images or annotate results run no tests and are outside this validation: " + strings.Join(slices.Compact(c.nonTest), ", ") + "."}
	}
	slices.Sort(c.files)
	workflow.Files = slices.Compact(c.files)
	return workflow
}

// buildkiteGates are steps that only pause the pipeline.
var buildkiteGates = []string{"wait", "waiter", "block", "input", "manual"}

// walk converts a step list, recursing into group steps.
func (c *buildkiteConfig) walk(workflow *ciWorkflow, list *yaml.Node, scope buildkiteScope, depth int) {
	list = gitLabAlias(list)
	if list == nil {
		return
	}
	if depth > 8 || list.Kind != yaml.SequenceNode {
		c.unresolved(workflow, scope.group+"(steps)", scope.file+":"+strconv.Itoa(list.Line), "the step list is not a list ddtest can read")
		return
	}
	for index, entry := range list.Content {
		item := gitLabAlias(entry)
		source := scope.file + ":" + strconv.Itoa(item.Line)
		if item.Kind == yaml.ScalarNode {
			if !slices.Contains(buildkiteGates, strings.TrimSpace(item.Value)) {
				c.unresolved(workflow, scope.group+"step "+strconv.Itoa(index+1), source, "step "+item.Value+" is not a step ddtest can read")
			}
			continue
		}
		if item.Kind != yaml.MappingNode {
			continue
		}
		fields := gitLabMembers(item)
		if slices.ContainsFunc(buildkiteGates, func(key string) bool { _, ok := fields[key]; return ok }) || slices.Contains(buildkiteGates, gitLabScalar(fields["type"])) {
			continue
		}
		label := buildkiteLabel(fields, index)
		disabled := scope.disabled || buildkiteSkipped(fields)
		if condition := strings.TrimSpace(gitLabScalar(fields["if"])); condition != "" && buildkiteCondition(condition) == gitLabFalse {
			disabled = true
		}
		if nested, ok := fields["steps"]; ok {
			inner := scope
			inner.group = scope.group + label + " / "
			inner.disabled = disabled
			c.walk(workflow, nested, inner, depth+1)
			continue
		}
		if fields["trigger"] != nil || gitLabScalar(fields["type"]) == "trigger" {
			if !disabled {
				target, _ := buildkiteInterpolate(gitLabScalar(fields["trigger"]), scope.known)
				c.unresolved(workflow, scope.group+label, source, "triggers the pipeline "+target+", whose steps ddtest cannot read; they may run tests. Validate that pipeline's test steps separately")
			}
			continue
		}
		c.command(workflow, item, fields, scope, label, disabled, source)
	}
}

// unresolved adds a job whose only step explains what ddtest cannot read.
func (c *buildkiteConfig) unresolved(workflow *ciWorkflow, name, source, reason string) {
	workflow.Jobs[buildkiteUniqueName(workflow, name)] = runtimeJob{Steps: []runtimeStep{{Number: 1, Source: source, ResolutionError: "Buildkite step " + name + ": " + reason}}}
}

// buildkiteUniqueName keeps job names distinct, as labels may repeat.
func buildkiteUniqueName(workflow *ciWorkflow, name string) string {
	unique := name
	for i := 2; ; i++ {
		if _, taken := workflow.Jobs[unique]; !taken {
			return unique
		}
		unique = name + " [" + strconv.Itoa(i) + "]"
	}
}

// buildkiteLabel names a step: its label (or name, or group), else its key,
// else its position.
func buildkiteLabel(fields map[string]*yaml.Node, index int) string {
	for _, key := range []string{"label", "name", "group", "key", "id", "identifier"} {
		if value := strings.TrimSpace(gitLabScalar(fields[key])); value != "" {
			return value
		}
	}
	return "step " + strconv.Itoa(index+1)
}

// buildkiteSkipped reports skip: true or a skip reason.
func buildkiteSkipped(fields map[string]*yaml.Node) bool {
	skip := gitLabAlias(fields["skip"])
	if skip == nil || skip.Kind != yaml.ScalarNode {
		return false
	}
	return skip.Tag == "!!bool" && skip.Value == "true" || skip.Tag == "!!str" && strings.TrimSpace(skip.Value) != ""
}

// buildkiteEntry is one command entry of a step, and the statement of it a
// step of the shared model runs: the origin of prescribed edits.
type buildkiteEntry struct {
	node *yaml.Node
	file string
	// listed reports an item of a command list; aliased an entry, or list,
	// some step reaches through a YAML alias.
	listed, aliased bool
	// line is the statement's first line within the entry's text (1-based).
	line int
	// first reports the entry's first statement.
	first bool
	// executor is how the step runs its commands, for edits that pass the
	// API key into a container.
	executor *buildkiteExecutor
}

// fileLine is the statement's line in the file: a block scalar's text starts
// on the line after its indicator.
func (entry buildkiteEntry) fileLine() int {
	if entry.node.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
		return entry.node.Line + max(entry.line, 1)
	}
	return entry.node.Line + max(entry.line-1, 0)
}

// command converts one command step into a job per matrix combination.
func (c *buildkiteConfig) command(workflow *ciWorkflow, item *yaml.Node, fields map[string]*yaml.Node, scope buildkiteScope, label string, disabled bool, source string) {
	commands := fields["command"]
	if commands == nil {
		commands = fields["commands"]
	}
	rows, err := buildkiteMatrix(fields["matrix"])
	if err != nil {
		if !disabled {
			c.unresolved(workflow, scope.group+label, source, err.Error())
		}
		return
	}
	for _, row := range rows {
		matrix := func(text string) (string, string) { return buildkiteMatrixText(text, row) }
		name, _ := matrix(label)
		if len(row) > 0 && name == label {
			var values []string
			for _, key := range slices.Sorted(maps.Keys(row)) {
				values = append(values, row[key])
			}
			name += " (" + strings.Join(values, ", ") + ")"
		}
		name = buildkiteUniqueName(workflow, scope.group+name)
		env := maps.Clone(scope.env)
		keyProblem := scope.env["DD_API_KEY"] != ""
		stepEnv := gitLabAlias(fields["env"])
		for _, key := range gitLabKeys(stepEnv) {
			text, problem := matrix(gitLabScalar(gitLabMembers(stepEnv)[key]))
			value, unknown := buildkiteInterpolate(text, scope.known)
			if key == "DD_API_KEY" {
				keyProblem = true
			}
			if (len(unknown) > 0 || problem != "") && !strings.HasPrefix(key, "DD_") {
				value = unresolvedCIValue(value)
			}
			env[key] = value
		}
		if _, set := scope.env["DD_API_KEY"]; set {
			keyProblem = true
		}
		executor := c.executor(fields, scope, matrix, env)
		if commands == nil {
			if !disabled {
				if reason := c.commandless(fields, executor); reason != "" {
					c.unresolved(workflow, name, source, reason)
				}
			}
			continue
		}
		executor.keyProblem = executor.keyProblem || keyProblem
		if executor.windows = buildkiteWindows(matrix, buildkiteAgents(scope.agents, fields["agents"])); executor.windows {
			executor.problem = joinDiagnostic(executor.problem, "it runs on Windows agents, whose default shell is cmd.exe, which ddtest cannot replay")
		}
		if executor.container && executor.composeRun || executor.container && executor.compose == "" {
			// A container receives only what its plugin passes: the variables
			// it lists and, with propagate-environment, the pipeline's env.
			passed := map[string]string{}
			if executor.propagate {
				passed = maps.Clone(env)
			}
			env = mergeEnvironment(passed, executor.env)
			if executor.propagate && !executor.keyPassed {
				c.review = append(c.review, scope.file+": "+name+" passes DD_API_KEY into its container with propagate-environment, which carries only the pipeline's env, such as one set in the Buildkite UI, and never a key an agent hook or secrets plugin exports: list DD_API_KEY in the plugin's environment for those.")
			}
		}
		job := runtimeJob{Env: env, Image: executor.image, ImageTemplate: executor.template}
		if disabled {
			job.If = "false"
		}
		job.Steps = c.steps(name, scope, commands, matrix, env, executor)
		workflow.Jobs[name] = job
	}
}

// commandless explains why a step without commands may run tests, or
// returns empty for steps that only run plugins known to run none.
func (c *buildkiteConfig) commandless(fields map[string]*yaml.Node, executor *buildkiteExecutor) string {
	switch {
	case executor.compose != "" && executor.composeRun:
		return "runs the Docker Compose service " + executor.compose + " with its own command from the Compose configuration, which may run tests; write the test command in the step's commands, or validate that service separately"
	case executor.container && executor.compose == "":
		return "runs the docker plugin's image with its own command, which may run tests; write the test command in the step's commands, or validate that image separately"
	case executor.problem != "":
		return executor.problem
	case executor.compose != "":
		c.nonTest = append(c.nonTest, "docker-compose (image build)")
	}
	return ""
}

// buildkiteAgents is a step's effective agent targeting: the pipeline's
// agents, each key overridden by the step's (key: value or "key=value").
func buildkiteAgents(nodes ...*yaml.Node) map[string]string {
	agents := map[string]string{}
	for _, node := range nodes {
		node = gitLabAlias(node)
		if node == nil {
			continue
		}
		if node.Kind == yaml.MappingNode {
			for key, value := range gitLabMembers(node) {
				agents[key] = gitLabScalar(value)
			}
			continue
		}
		for _, item := range gitLabStrings(node) {
			key, value, _ := strings.Cut(item, "=")
			agents[key] = value
		}
	}
	return agents
}

// buildkiteWindows reports agent targeting that selects Windows agents,
// with the step's matrix values substituted.
func buildkiteWindows(matrix func(string) (string, string), agents map[string]string) bool {
	for key, value := range agents {
		value, _ = matrix(key + "=" + value)
		if strings.Contains(strings.ToLower(value), "windows") {
			return true
		}
	}
	return false
}

// steps splits a step's commands into top-level statements and follows the
// shell state they share: literal exports, assignments and cd reach later
// statements, and the Datadog installer snippet is a setup step.
func (c *buildkiteConfig) steps(name string, scope buildkiteScope, commands *yaml.Node, matrix func(string) (string, string), env map[string]string, executor *buildkiteExecutor) []runtimeStep {
	list := gitLabAlias(commands)
	var entries []buildkiteEntry
	listAliased := commands.Kind == yaml.AliasNode || c.aliased[list]
	if list.Kind == yaml.SequenceNode {
		for _, item := range list.Content {
			entries = append(entries, buildkiteEntry{node: gitLabAlias(item), file: scope.file, listed: true, aliased: listAliased || item.Kind == yaml.AliasNode || c.aliased[gitLabAlias(item)], executor: executor})
		}
	} else {
		entries = append(entries, buildkiteEntry{node: list, file: scope.file, aliased: listAliased, executor: executor})
	}
	state := ciShellState{dialect: buildkiteShellDialect, env: map[string]string{}, references: map[string]string{}, job: env, directory: executor.directory}
	var steps []runtimeStep
	add := func(entry buildkiteEntry, run, problem string) *runtimeStep {
		step := runtimeStep{Number: len(steps) + 1, Source: entry.file + ":" + strconv.Itoa(entry.fileLine()), origin: entry, Shell: executor.shell}
		if len(state.env) > 0 {
			step.Env = maps.Clone(state.env)
		}
		step.WorkingDirectory = state.directory
		step.Run = run
		step.ResolutionError = joinDiagnostic(joinDiagnostic(state.problem, executor.problem), problem)
		if step.ResolutionError != "" {
			step.ResolutionError = "Buildkite step " + name + ": " + step.ResolutionError
		}
		steps = append(steps, step)
		return &steps[len(steps)-1]
	}
	for _, entry := range entries {
		if entry.node == nil || entry.node.Kind != yaml.ScalarNode {
			add(entry, "", "command "+gitLabDescribe(entry.node)+" is not a command")
			continue
		}
		text, matrixProblem := matrix(entry.node.Value)
		text, unknown := buildkiteInterpolate(text, scope.known)
		uploadProblem := buildkiteUploadProblem(unknown)
		if matrixProblem != "" || strings.TrimSpace(text) == "" {
			if matrixProblem != "" {
				entry.line, entry.first = 1, true
				add(entry, text, matrixProblem)
			}
			continue
		}
		file, parsed := parseCIShell(text)
		if !parsed || len(file.Stmts) == 0 {
			entry.line, entry.first = 1, true
			add(entry, text, uploadProblem)
			continue
		}
		for i := 0; i < len(file.Stmts); i++ {
			current := entry
			current.line, current.first = int(file.Stmts[i].Pos().Line()), i == 0
			known := mergeEnvironment(mergeEnvironment(env, state.env), state.references)
			if setup, consumed, ok := buildkiteSetupAt(file.Stmts, i, known); ok {
				statement := buildkiteStatementText(text, file.Stmts, i, i+consumed)
				if !setup.shaped {
					// It may run anything, tests included: an unresolved
					// command, never a setup step that hides it.
					add(current, statement, setup.Problem)
					i += consumed - 1
					continue
				}
				if reads := buildkiteReads(statement, unknown); len(reads) > 0 && setup.Problem == "" {
					setup.Problem = "The Datadog setup snippet reads " + strings.Join(reads, ", ") + " without escaping the $: buildkite-agent pipeline upload replaces it when the pipeline is uploaded (with the upload step's value, or empty), so write every $ in the snippet as $$, as ddtest's instructions show."
				}
				if setup.Problem == "" {
					setup.Problem = executor.setupProblem(state.curl)
				}
				if key, changed := state.env["DD_API_KEY"]; changed && setup.Problem == "" {
					switch {
					case key == "":
						setup.Problem = "An earlier command of the step clears DD_API_KEY (export DD_API_KEY= or unset), so the Datadog setup snippet always skips; remove it."
					case unresolvedCIError(key) == nil:
						setup.Problem = "An earlier command of the step sets DD_API_KEY to a value written into the pipeline; never commit the key: provide it from " + buildkiteKeySources + "."
					}
				}
				step := add(current, "", "")
				step.ResolutionError = ""
				if state.problem != "" {
					step.ResolutionError = "Buildkite step " + name + ": " + state.problem
				}
				if executor.problem != "" && setup.Problem == "" {
					setup.Problem = "The step cannot run the Datadog setup snippet: " + executor.problem + "."
				}
				step.Setup = &setup
				if setup.Supported && setup.Problem == "" {
					state.applySetup(setup, mergeEnvironment(env, state.env))
					for _, stmt := range setup.after {
						state.statement(stmt, mergeEnvironment(mergeEnvironment(env, state.env), state.references))
					}
				}
				i += consumed - 1
				continue
			}
			statement := buildkiteStatementText(text, file.Stmts, i, i+1)
			command := gitLabResolveBranches(statement, known)
			state.curl = state.curl || installsCurl(command)
			problem := ""
			if reads := buildkiteReads(statement, unknown); len(reads) > 0 {
				problem = uploadProblem
			}
			dynamic, uploads := buildkiteUpload(c, statement, state.directory)
			problem = joinDiagnostic(problem, dynamic)
			step := add(current, command, problem)
			if problem == "" && (uploads || buildkiteAgentRecord(statement)) {
				step.Run = "" // It runs nothing ddtest replays.
				continue
			}
			if problem != "" {
				// What it changes depends on values ddtest cannot know.
				if strings.Contains(statement, "=") || strings.HasPrefix(strings.TrimSpace(statement), "cd") {
					state.problem = joinDiagnostic(state.problem, problem)
				}
				continue
			}
			if consumed := strings.TrimSpace(command) == "" || state.apply(command, mergeEnvironment(env, state.env)); consumed {
				step.Run = ""
			}
		}
	}
	return steps
}

// buildkiteStatementText is the text of statements [from, to): from the
// first's start to the next statement's start, so heredoc bodies and
// trailing comments stay with their statement.
func buildkiteStatementText(text string, stmts []*syntax.Stmt, from, to int) string {
	end := len(text)
	if to < len(stmts) {
		end = int(stmts[to].Pos().Offset())
	}
	return strings.TrimSpace(text[stmts[from].Pos().Offset():end])
}

// buildkiteReads lists the variables of unknown a statement reads.
func buildkiteReads(statement string, unknown []string) []string {
	var reads []string
	for _, name := range unknown {
		if regexp.MustCompile(`\$(?:` + regexp.QuoteMeta(name) + `\b|\{` + regexp.QuoteMeta(name) + `\b)`).MatchString(statement) {
			reads = append(reads, "$"+name)
		}
	}
	return reads
}

// buildkiteUploadProblem explains reads pipeline upload interpolates.
func buildkiteUploadProblem(unknown []string) string {
	if len(unknown) == 0 {
		return ""
	}
	return "the command reads $" + strings.Join(unknown, ", $") + ", which buildkite-agent pipeline upload replaces when the pipeline is uploaded, not when the step runs, with a value ddtest cannot know (the step's own env is not available then); write $$ to read it when the step runs"
}

// buildkitePipelineUpload matches a buildkite-agent pipeline upload command.
var buildkitePipelineUpload = regexp.MustCompile(`buildkite-agent\s+pipeline\s+upload`)

// buildkiteAgentRecord reports a buildkite-agent command that only moves
// artifacts or records annotations and metadata for the build, which runs
// nothing ddtest replays, like CircleCI's artifact steps.
func buildkiteAgentRecord(statement string) bool {
	file, ok := parseCIShell(statement)
	if !ok || len(file.Stmts) != 1 || file.Stmts[0].Background || file.Stmts[0].Negated || buildkiteSubstitutes(file.Stmts[0]) {
		return false
	}
	call, isCall := file.Stmts[0].Cmd.(*syntax.CallExpr)
	return isCall && len(call.Assigns) == 0 && len(call.Args) >= 2 && call.Args[0].Lit() == "buildkite-agent" && slices.Contains([]string{"artifact", "annotate", "annotation", "meta-data"}, call.Args[1].Lit())
}

// buildkiteSubstitutes reports a command or process substitution anywhere in
// a statement, such as an argument "$(npm test)": it runs a command of its own.
func buildkiteSubstitutes(stmt *syntax.Stmt) bool {
	found := false
	syntax.Walk(stmt, func(node syntax.Node) bool {
		switch node.(type) {
		case *syntax.CmdSubst, *syntax.ProcSubst:
			found = true
		}
		return !found
	})
	return found
}

// buildkiteDefaultPipelines are the files buildkite-agent pipeline upload
// looks for without arguments, relative to its working directory; exactly
// one must exist (buildkite/agent, clicommand/pipeline_upload.go).
var buildkiteDefaultPipelines = []string{"buildkite.yml", "buildkite.yaml", "buildkite.json", ".buildkite/pipeline.yml", ".buildkite/pipeline.yaml", ".buildkite/pipeline.json", "buildkite/pipeline.yml", "buildkite/pipeline.yaml", "buildkite/pipeline.json"}

// buildkiteUpload explains why a statement that uploads steps leaves them
// unresolved: anything but an upload of pipeline files ddtest reads, which
// is reported and runs nothing ddtest replays (uploads). Paths resolve from
// directory, the statement's working directory. Empty for other statements.
func buildkiteUpload(c *buildkiteConfig, statement, directory string) (problem string, uploads bool) {
	if !buildkitePipelineUpload.MatchString(statement) {
		return "", false
	}
	unresolved := "the command generates or uploads steps with buildkite-agent pipeline upload (" + statement + "), which ddtest cannot read; they may run tests. Commit the uploaded pipeline to the repository, or validate those steps separately"
	file, ok := parseCIShell(statement)
	if !ok || len(file.Stmts) != 1 || file.Stmts[0].Background || file.Stmts[0].Negated || len(file.Stmts[0].Redirs) > 0 {
		return unresolved, true
	}
	call, isCall := file.Stmts[0].Cmd.(*syntax.CallExpr)
	if !isCall || len(call.Assigns) > 0 || len(call.Args) < 3 {
		return unresolved, true
	}
	var words []string
	for _, word := range call.Args {
		value, literal := ciShellWord(word, nil, "", "")
		if !literal || value == "" {
			return unresolved, true
		}
		words = append(words, value)
	}
	if !slices.Equal(words[:3], []string{"buildkite-agent", "pipeline", "upload"}) {
		return unresolved, true
	}
	var paths []string
	for _, value := range words[3:] {
		if strings.HasPrefix(value, "-") || path.IsAbs(value) {
			return unresolved, true
		}
		paths = append(paths, path.Join(directory, value))
	}
	if len(paths) == 0 {
		for _, candidate := range buildkiteDefaultPipelines {
			if info, found := repositoryFile(c.root, c.root, path.Join(directory, candidate)); found && !info.IsDir() {
				paths = append(paths, path.Join(directory, candidate))
			}
		}
		if len(paths) != 1 {
			return unresolved, true // Upload fails, or ddtest cannot tell which file.
		}
	}
	if slices.ContainsFunc(paths, func(file string) bool { return c.documents[file] == nil }) {
		return unresolved, true
	}
	c.review = append(c.review, "Buildkite uploads "+strings.Join(paths, ", ")+" with buildkite-agent pipeline upload; ddtest reads "+map[bool]string{true: "it", false: "them"}[len(paths) == 1]+" as "+map[bool]string{true: "a pipeline", false: "pipelines"}[len(paths) == 1]+" of its own.")
	return "", true
}

// buildkiteShellDialect is how a Buildkite step's commands read: the
// checkout is BUILDKITE_BUILD_CHECKOUT_PATH and the installer's inputs are
// its variables.
var buildkiteShellDialect = ciShellDialect{
	label:           "Buildkite command",
	projectVariable: "BUILDKITE_BUILD_CHECKOUT_PATH",
	runnerPrefixes:  []string{"BUILDKITE_", "CI"},
	setupVariable:   buildkiteSetupVariable,
}

// buildkiteVariable matches $NAME, ${NAME}, ${NAME:-default} and the other
// forms pipeline upload interpolates, and the escapes $$ and \$.
var buildkiteVariable = regexp.MustCompile(`\\\$|\$\$|\$\{([A-Za-z_][A-Za-z0-9_]*)([^}]*)\}|\$([A-Za-z_][A-Za-z0-9_]*)`)

// buildkiteBuildVariables are the variables pipeline upload interpolates
// with a value Buildkite also gives the step, or one that only names the
// build: reading them at upload time is reading them at runtime.
func buildkiteBuildVariable(name string) bool {
	return slices.Contains(buildkiteBuildVariables, name)
}

// buildkiteBuildVariables describe the build, so every job of it, the
// upload step's included, sees the same value. Agent and job variables
// (BUILDKITE_AGENT_*, BUILDKITE_BUILD_CHECKOUT_PATH, BUILDKITE_JOB_ID,
// BUILDKITE_PARALLEL_JOB...) differ between the upload step and the test.
var buildkiteBuildVariables = []string{
	"CI", "BUILDKITE", "BUILDKITE_BRANCH", "BUILDKITE_COMMIT", "BUILDKITE_TAG", "BUILDKITE_MESSAGE", "BUILDKITE_REPO",
	"BUILDKITE_BUILD_ID", "BUILDKITE_BUILD_NUMBER", "BUILDKITE_BUILD_URL", "BUILDKITE_BUILD_AUTHOR", "BUILDKITE_BUILD_AUTHOR_EMAIL", "BUILDKITE_BUILD_CREATOR", "BUILDKITE_BUILD_CREATOR_EMAIL", "BUILDKITE_SOURCE",
	"BUILDKITE_PIPELINE_ID", "BUILDKITE_PIPELINE_SLUG", "BUILDKITE_PIPELINE_NAME", "BUILDKITE_PIPELINE_DEFAULT_BRANCH", "BUILDKITE_ORGANIZATION_ID", "BUILDKITE_ORGANIZATION_SLUG",
	"BUILDKITE_PULL_REQUEST", "BUILDKITE_PULL_REQUEST_BASE_BRANCH", "BUILDKITE_PULL_REQUEST_REPO", "BUILDKITE_PULL_REQUEST_DRAFT",
	"BUILDKITE_REBUILT_FROM_BUILD_ID", "BUILDKITE_REBUILT_FROM_BUILD_NUMBER", "BUILDKITE_TRIGGERED_FROM_BUILD_ID", "BUILDKITE_TRIGGERED_FROM_BUILD_NUMBER", "BUILDKITE_TRIGGERED_FROM_BUILD_PIPELINE_SLUG",
}

// buildkiteInterpolate applies the interpolation buildkite-agent pipeline
// upload performs on the pipeline file: $$ and \$ become a literal $ (a read
// when the step runs), known values replace their variables, and Buildkite's
// own variables stay reads. unknown lists every other variable read: upload
// replaces it with the upload step's environment, which ddtest cannot know
// and which never holds the step's own env.
func buildkiteInterpolate(text string, known map[string]string) (string, []string) {
	var unknown []string
	result := buildkiteVariable.ReplaceAllStringFunc(text, func(match string) string {
		if match == `\$` || match == "$$" {
			return "$"
		}
		parts := buildkiteVariable.FindStringSubmatch(match)
		name := parts[1] + parts[3]
		if value, ok := known[name]; ok && parts[2] == "" {
			return value
		}
		if !buildkiteBuildVariable(name) && !slices.Contains(unknown, name) {
			unknown = append(unknown, name)
		}
		return match
	})
	return result, unknown
}

// buildkiteMatrixLimit bounds the jobs one matrix expands to.
const buildkiteMatrixLimit = 200

// buildkiteMatrixReference matches {{matrix}} and {{matrix.name}}.
var buildkiteMatrixReference = regexp.MustCompile(`\{\{\s*matrix(?:\.([A-Za-z0-9_-]+))?\s*\}\}`)

// buildkiteMatrixText substitutes a matrix combination's values; problem
// names a reference the combination does not define.
func buildkiteMatrixText(text string, row map[string]string) (string, string) {
	problem := ""
	result := buildkiteMatrixReference.ReplaceAllStringFunc(text, func(match string) string {
		name := buildkiteMatrixReference.FindStringSubmatch(match)[1]
		if value, ok := row[name]; ok {
			return value
		}
		problem = "the command reads " + match + ", which no matrix dimension of the step defines"
		return match
	})
	return result, problem
}

// buildkiteMatrix expands a matrix into combinations: a list of values (one
// anonymous dimension, {{matrix}}), or setup (a list, or a mapping of
// dimensions) with adjustments that add combinations or skip them. A step
// without one has a single empty combination.
func buildkiteMatrix(node *yaml.Node) ([]map[string]string, error) {
	node = gitLabAlias(node)
	if node == nil {
		return []map[string]string{nil}, nil
	}
	setup, adjustments := node, (*yaml.Node)(nil)
	if node.Kind == yaml.MappingNode {
		setup, adjustments = gitLabAlias(gitLabMembers(node)["setup"]), gitLabAlias(gitLabMembers(node)["adjustments"])
	}
	scalars := func(values *yaml.Node, dimension string) ([]string, error) {
		values = gitLabAlias(values)
		if values == nil || values.Kind != yaml.SequenceNode {
			return nil, fmt.Errorf("matrix dimension %s is not a list", strings.TrimSpace(dimension+" "))
		}
		var result []string
		for _, value := range values.Content {
			if gitLabAlias(value).Kind != yaml.ScalarNode {
				return nil, fmt.Errorf("matrix dimension %s has a value that is not a scalar", strings.TrimSpace(dimension+" "))
			}
			result = append(result, gitLabScalar(value))
		}
		return result, nil
	}
	var rows []map[string]string
	switch {
	case setup == nil:
		return nil, fmt.Errorf("matrix has no setup ddtest can read")
	case setup.Kind == yaml.SequenceNode:
		values, err := scalars(setup, "")
		if err != nil {
			return nil, err
		}
		for _, value := range values {
			rows = append(rows, map[string]string{"": value})
		}
	case setup.Kind == yaml.MappingNode:
		// Bound the combinations before building them: adjustments that skip
		// can only remove one each.
		skips := 0
		if adjustments != nil && adjustments.Kind == yaml.SequenceNode {
			for _, adjustment := range adjustments.Content {
				if buildkiteSkipped(gitLabMembers(adjustment)) {
					skips++
				}
			}
		}
		dimensions := map[string][]string{}
		combinations := 1
		for _, dimension := range gitLabKeys(setup) {
			values, err := scalars(gitLabMembers(setup)[dimension], dimension)
			if err != nil {
				return nil, err
			}
			dimensions[dimension] = values
			if combinations *= len(values); combinations-skips > buildkiteMatrixLimit {
				return nil, fmt.Errorf("matrix expands to more than %d jobs", buildkiteMatrixLimit)
			}
		}
		rows = []map[string]string{{}}
		for _, dimension := range gitLabKeys(setup) {
			values := dimensions[dimension]
			var next []map[string]string
			for _, row := range rows {
				for _, value := range values {
					expanded := maps.Clone(row)
					expanded[dimension] = value
					next = append(next, expanded)
				}
			}
			rows = next
		}
	default:
		return nil, fmt.Errorf("matrix has no setup ddtest can read")
	}
	if adjustments != nil {
		if adjustments.Kind != yaml.SequenceNode {
			return nil, fmt.Errorf("matrix adjustments are not a list")
		}
		for _, adjustment := range adjustments.Content {
			fields := gitLabMembers(adjustment)
			with := gitLabAlias(fields["with"])
			combination := map[string]string{}
			switch {
			case with == nil:
				return nil, fmt.Errorf("a matrix adjustment has no with")
			case with.Kind == yaml.ScalarNode:
				combination[""] = with.Value
			case with.Kind == yaml.MappingNode:
				for key, value := range gitLabMembers(with) {
					combination[key] = gitLabScalar(value)
				}
			default:
				return nil, fmt.Errorf("a matrix adjustment's with is not a combination ddtest can read")
			}
			index := slices.IndexFunc(rows, func(row map[string]string) bool { return maps.Equal(row, combination) })
			switch {
			case buildkiteSkipped(fields) && index >= 0:
				rows = slices.Delete(rows, index, index+1)
			case !buildkiteSkipped(fields) && index < 0:
				rows = append(rows, combination)
			}
		}
	}
	if len(rows) > buildkiteMatrixLimit {
		return nil, fmt.Errorf("matrix expands to more than %d jobs", buildkiteMatrixLimit)
	}
	return rows, nil
}

// buildkiteCondition decides a step's if expression when every operand is
// a literal: true, false, strings, numbers and null with !, ==, !=, && and
// ||. Build, pipeline and organization values, build.env(), regular
// expressions and includes are known only when the build runs: unknown.
func buildkiteCondition(expression string) gitLabTruth {
	parser := buildkiteConditionParser{tokens: buildkiteTokens(expression)}
	value := parser.or()
	if parser.failed || parser.position != len(parser.tokens) {
		return gitLabUnknown
	}
	return value
}

func buildkiteTokens(expression string) []string {
	var tokens []string
	for i := 0; i < len(expression); {
		switch character := expression[i]; {
		case character == ' ' || character == '\t' || character == '\n':
			i++
		case strings.HasPrefix(expression[i:], "&&") || strings.HasPrefix(expression[i:], "||") || strings.HasPrefix(expression[i:], "==") || strings.HasPrefix(expression[i:], "!=") || strings.HasPrefix(expression[i:], "=~") || strings.HasPrefix(expression[i:], "!~"):
			tokens = append(tokens, expression[i:i+2])
			i += 2
		case strings.IndexByte("()!", character) >= 0:
			tokens = append(tokens, string(character))
			i++
		case character == '"' || character == '\'' || character == '/':
			end := strings.IndexByte(expression[i+1:], character)
			if end < 0 {
				return append(tokens, "\x00")
			}
			tokens = append(tokens, expression[i:i+end+2])
			i += end + 2
		default:
			end := i
			for end < len(expression) && strings.IndexByte(" \t\n()&|=!\"'", expression[end]) < 0 {
				end++
			}
			if end == i {
				return append(tokens, "\x00")
			}
			tokens = append(tokens, expression[i:end])
			i = end
		}
	}
	return tokens
}

type buildkiteConditionParser struct {
	tokens   []string
	position int
	failed   bool
}

func (p *buildkiteConditionParser) peek() string {
	if p.position < len(p.tokens) {
		return p.tokens[p.position]
	}
	return ""
}

func (p *buildkiteConditionParser) or() gitLabTruth {
	left := p.and()
	for p.peek() == "||" {
		p.position++
		right := p.and()
		switch {
		case left == gitLabTrue || right == gitLabTrue:
			left = gitLabTrue
		case left == gitLabFalse && right == gitLabFalse:
		default:
			left = gitLabUnknown
		}
	}
	return left
}

func (p *buildkiteConditionParser) and() gitLabTruth {
	left := p.unary()
	for p.peek() == "&&" {
		p.position++
		right := p.unary()
		switch {
		case left == gitLabFalse || right == gitLabFalse:
			left = gitLabFalse
		case left == gitLabTrue && right == gitLabTrue:
		default:
			left = gitLabUnknown
		}
	}
	return left
}

func (p *buildkiteConditionParser) unary() gitLabTruth {
	if p.peek() == "!" {
		p.position++
		switch p.unary() {
		case gitLabTrue:
			return gitLabFalse
		case gitLabFalse:
			return gitLabTrue
		}
		return gitLabUnknown
	}
	return p.comparison()
}

// operand reads a value; known is false for values only the build knows.
func (p *buildkiteConditionParser) operand() (string, bool) {
	token := p.peek()
	p.position++
	switch {
	case token == "(":
		value := p.or()
		if p.peek() != ")" {
			p.failed = true
			return "", false
		}
		p.position++
		switch value {
		case gitLabTrue:
			return "true", true
		case gitLabFalse:
			return "false", true
		}
		return "", false
	case token == "" || token == "\x00" || token == ")" || strings.HasPrefix(token, "&") || strings.HasPrefix(token, "|") || strings.HasPrefix(token, "="):
		p.failed = true
		return "", false
	case token == "true" || token == "false" || token == "null":
		return token, true
	case strings.HasPrefix(token, `"`) || strings.HasPrefix(token, "'"):
		return token[1 : len(token)-1], true
	case strings.Trim(token, "0123456789.-") == "":
		return token, true
	}
	// build.branch, build.env(...) or another value of the build; calls
	// and member chains read as one operand.
	if p.peek() == "(" {
		depth := 0
		for p.position < len(p.tokens) {
			switch p.tokens[p.position] {
			case "(":
				depth++
			case ")":
				depth--
			}
			p.position++
			if depth == 0 {
				break
			}
		}
	}
	return "", false
}

func (p *buildkiteConditionParser) comparison() gitLabTruth {
	left, leftKnown := p.operand()
	switch operator := p.peek(); operator {
	case "==", "!=":
		p.position++
		right, rightKnown := p.operand()
		if !leftKnown || !rightKnown {
			return gitLabUnknown
		}
		if (left == right) == (operator == "==") {
			return gitLabTrue
		}
		return gitLabFalse
	case "=~", "!~", "includes":
		p.position++
		p.operand()
		return gitLabUnknown
	}
	if !leftKnown {
		return gitLabUnknown
	}
	switch left {
	case "true":
		return gitLabTrue
	case "false", "null", "":
		return gitLabFalse
	}
	return gitLabTrue
}
