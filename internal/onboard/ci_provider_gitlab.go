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
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// gitLabCIProvider is the --ci-provider value of GitLab CI/CD.
const gitLabCIProvider = "gitlab"

// gitLabCIFile is the pipeline configuration GitLab reads by default.
const gitLabCIFile = ".gitlab-ci.yml"

// gitLabCI reads .gitlab-ci.yml and the local files it includes. Each job
// becomes one job of the shared model per parallel:matrix entry, and each
// before_script/script entry one step; the shell state entries share (exports
// and cd) is carried into later steps. Its setup mechanism is Datadog's
// GitLab setup script (test-optimization-gitlab-script). Anything ddtest
// cannot read locally, such as remote includes or child pipelines, becomes an
// unresolved step: it is reported, never dropped or certified.
type gitLabCI struct{}

func (gitLabCI) name() string { return gitLabCIProvider }

func (gitLabCI) displayName() string { return "GitLab CI" }

func (gitLabCI) environment() map[string]string {
	return map[string]string{"CI": "true", "GITLAB_CI": "true"}
}

func (gitLabCI) sharedSyntax() bool { return false }

func (gitLabCI) configurationFiles(root string) ([]string, error) {
	info, err := os.Stat(filepath.Join(root, gitLabCIFile))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find GitLab CI configuration: %w", err)
	}
	if info.IsDir() {
		return nil, nil
	}
	return []string{gitLabCIFile}, nil
}

func (g gitLabCI) readWorkflows(root string) ([]ciWorkflow, error) {
	files, err := g.configurationFiles(root)
	if err != nil || len(files) == 0 {
		return nil, err
	}
	config := &gitLabConfig{root: root, definitions: map[string]*yaml.Node{}, owners: map[string]string{}, files: map[*yaml.Node]string{}}
	if err := config.load(gitLabCIFile, nil); err != nil {
		return nil, err
	}
	return config.workflows(), nil
}

func (g gitLabCI) findTestWorkflows(root, language, framework string) ([]string, []string, error) {
	workflows, err := g.readWorkflows(root)
	if err != nil {
		return nil, nil, err
	}
	found, configured := testWorkflowsWithSetup(root, workflows, language, framework)
	return found, configured, nil
}

func (gitLabCI) setupStep(step runtimeStep) (ciSetup, bool) {
	if step.Setup == nil {
		return ciSetup{}, false
	}
	return *step.Setup, true
}

// gitLabConfig is the merged configuration: top-level keys of every loaded
// file, later files overriding earlier ones as GitLab merges includes.
type gitLabConfig struct {
	root        string
	definitions map[string]*yaml.Node
	// owners names the file that last defined each top-level key.
	owners map[string]string
	// files names the file of every loaded node.
	files map[*yaml.Node]string
	// order lists the loaded files once each, parsed their documents, and
	// loads counts every inclusion.
	order  []string
	parsed map[string]*yaml.Node
	loads  int
	// gaps are configuration ddtest cannot read, per file; review is
	// configuration known to run no tests.
	gaps    []gitLabGap
	review  []string
	nonTest []string
}

type gitLabGap struct{ file, reason string }

// gitLabReserved are top-level keys that are not jobs.
var gitLabReserved = []string{"include", "default", "variables", "stages", "workflow", "image", "services", "cache", "before_script", "after_script", "spec", "types", "pages:deploy"}

// gitLabIncludeLimit bounds includes, as GitLab does.
const gitLabIncludeLimit = 150

// load reads file and, first, the local files it includes, then merges its
// keys. A file included again merges again at its new position, as GitLab
// does; order lists each file once.
func (c *gitLabConfig) load(file string, chain []string) error {
	if slices.Contains(chain, file) {
		c.gaps = append(c.gaps, gitLabGap{chain[len(chain)-1], "include cycle through " + file})
		return nil
	}
	if c.loads >= gitLabIncludeLimit || len(chain) > 100 {
		c.gaps = append(c.gaps, gitLabGap{file, "include limit exceeded"})
		return nil
	}
	c.loads++
	document, cached := c.parsed[file]
	if !cached {
		var err error
		document, err = c.readFile(file)
		if err != nil {
			if len(chain) == 0 {
				return err
			}
			c.gaps = append(c.gaps, gitLabGap{chain[len(chain)-1], err.Error()})
			return nil
		}
		if c.parsed == nil {
			c.parsed = map[string]*yaml.Node{}
		}
		c.parsed[file] = document
		c.order = append(c.order, file)
		if document != nil {
			c.record(document, file)
		}
	}
	if document == nil {
		return nil
	}
	members := gitLabMembers(document)
	if include := members["include"]; include != nil {
		// Include rules see no YAML variables, and !reference variables
		// resolve only after every file merges.
		for _, local := range c.includes(file, include) {
			if err := c.load(local, append(slices.Clone(chain), file)); err != nil {
				return err
			}
		}
	}
	for i := 0; i+1 < len(document.Content); i += 2 {
		key, value := document.Content[i].Value, document.Content[i+1]
		if key == "include" || key == "<<" {
			continue
		}
		if previous := c.definitions[key]; previous != nil {
			value = gitLabMerge(previous, value)
		}
		c.definitions[key] = value
		c.owners[key] = file
	}
	return nil
}

// readFile parses one repository file. A header document (spec: inputs)
// makes the configuration depend on inputs ddtest cannot supply.
func (c *gitLabConfig) readFile(file string) (*yaml.Node, error) {
	info, ok := repositoryFile(c.root, c.root, file)
	if !ok || info.IsDir() {
		return nil, fmt.Errorf("included file %s is not a file in the repository", file)
	}
	if info.Size() > 2<<20 {
		return nil, fmt.Errorf("%s exceeds 2 MiB", file)
	}
	data, err := os.ReadFile(filepath.Join(c.root, file))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", file, err)
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	var documents []*yaml.Node
	for {
		var document yaml.Node
		if err := decoder.Decode(&document); err != nil {
			if err.Error() == "EOF" {
				break
			}
			return nil, fmt.Errorf("parse %s: %w", file, err)
		}
		documents = append(documents, &document)
	}
	if len(documents) == 0 || len(documents[len(documents)-1].Content) == 0 {
		return nil, nil
	}
	if len(documents) > 2 {
		return nil, fmt.Errorf("parse %s: more than two YAML documents", file)
	}
	if len(documents) == 2 {
		c.gaps = append(c.gaps, gitLabGap{file, "the spec: inputs header makes this configuration depend on inputs ddtest cannot supply"})
	}
	body := documents[len(documents)-1].Content[0]
	if body.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("parse %s: the configuration must be a YAML mapping", file)
	}
	if strings.Contains(string(data), "$[[") {
		c.gaps = append(c.gaps, gitLabGap{file, "$[[ inputs ]] interpolation depends on inputs ddtest cannot supply"})
	}
	return body, nil
}

func (c *gitLabConfig) record(node *yaml.Node, file string) {
	if node == nil {
		return
	}
	if _, seen := c.files[node]; seen {
		return
	}
	c.files[node] = file
	for _, child := range node.Content {
		c.record(child, file)
	}
}

// includes returns the repository files file includes locally; every other
// include is a gap.
func (c *gitLabConfig) includes(file string, include *yaml.Node) []string {
	include = gitLabAlias(include)
	entries := []*yaml.Node{include}
	if include.Kind == yaml.SequenceNode {
		entries = include.Content
	}
	var locals []string
	for _, entry := range entries {
		entry = gitLabAlias(entry)
		local := ""
		switch entry.Kind {
		case yaml.ScalarNode:
			local = entry.Value
			if strings.Contains(local, "://") {
				c.gaps = append(c.gaps, gitLabGap{file, "remote include " + local + " cannot be read locally"})
				continue
			}
		case yaml.MappingNode:
			members := gitLabMembers(entry)
			for _, kind := range []string{"remote", "project", "template", "component"} {
				if members[kind] == nil {
					continue
				}
				if purpose := gitLabNonTestInclude(kind, gitLabScalar(members[kind])); purpose != "" {
					c.nonTest = append(c.nonTest, gitLabScalar(members[kind]))
					c.review = append(c.review, file+": "+kind+" include "+gitLabDescribe(members[kind])+" is GitLab's "+purpose+"; it runs no tests and is outside this validation.")
					continue
				}
				c.gaps = append(c.gaps, gitLabGap{file, kind + " include " + gitLabDescribe(members[kind]) + " cannot be read locally"})
			}
			if members["inputs"] != nil {
				c.gaps = append(c.gaps, gitLabGap{file, "include inputs make " + gitLabDescribe(members["local"]) + " depend on values ddtest cannot supply"})
			}
			local = gitLabScalar(members["local"])
			if rules := members["rules"]; rules != nil && local != "" {
				// GitLab evaluates includes before YAML variables exist: only
				// predefined variables, which ddtest does not know, apply.
				switch gitLabEvaluateRules(rules, nil).state {
				case gitLabNever:
					continue
				case gitLabPossible:
					c.gaps = append(c.gaps, gitLabGap{file, "include " + local + " has rules ddtest cannot decide locally"})
					continue
				}
			}
			if local == "" {
				if members["remote"] == nil && members["project"] == nil && members["template"] == nil && members["component"] == nil {
					c.gaps = append(c.gaps, gitLabGap{file, "unrecognized include " + gitLabDescribe(entry)})
				}
				continue
			}
		default:
			c.gaps = append(c.gaps, gitLabGap{file, "unrecognized include " + gitLabDescribe(entry)})
			continue
		}
		if strings.ContainsAny(local, "$") {
			c.gaps = append(c.gaps, gitLabGap{file, "include " + local + " depends on a variable ddtest cannot resolve"})
			continue
		}
		relative := strings.TrimPrefix(local, "/")
		if !filepath.IsLocal(relative) {
			c.gaps = append(c.gaps, gitLabGap{file, "include " + local + " is outside the repository"})
			continue
		}
		if !strings.ContainsAny(relative, "*?[") {
			locals = append(locals, filepath.ToSlash(filepath.Clean(relative)))
			continue
		}
		if strings.Contains(relative, "**") {
			c.gaps = append(c.gaps, gitLabGap{file, "recursive include pattern " + local + " is not resolved locally"})
			continue
		}
		matches, err := filepath.Glob(filepath.Join(c.root, relative))
		if err != nil || len(matches) == 0 {
			c.gaps = append(c.gaps, gitLabGap{file, "include pattern " + local + " matches no repository file"})
			continue
		}
		slices.Sort(matches)
		for _, match := range matches {
			if path, err := filepath.Rel(c.root, match); err == nil {
				locals = append(locals, filepath.ToSlash(path))
			}
		}
	}
	return locals
}

// gitLabNonTestComponents and gitLabNonTestTemplates are GitLab-provided
// CI/CD components and templates that only scan, review or release: none of
// them runs a project's tests. An include of one is reported, not a blocker;
// every other remote, project, template or component include stays
// unresolved. Components match by path, at any version.
var gitLabNonTestComponents = map[string]string{
	"gitlab.com/components/sast/sast":                                          "SAST scanning component",
	"gitlab.com/components/secret-detection/secret-detection":                  "secret detection component",
	"gitlab.com/components/dependency-scanning/main":                           "dependency scanning component",
	"gitlab.com/components/dependency-scanning/dependency-scanning":            "dependency scanning component",
	"gitlab.com/components/container-scanning/container-scanning":              "container scanning component",
	"gitlab.com/components/code-quality/code-quality":                          "code quality component",
	"gitlab.com/gitlab-org/components/danger-review/danger-review":             "Danger merge request review component",
	"gitlab.com/gitlab-org/components/gem-release/gem-release":                 "gem release component",
	"gitlab.com/gitlab-org/components/dependency-scanning/dependency-scanning": "dependency scanning component",
}

var gitLabNonTestTemplates = map[string]string{
	"Jobs/SAST.gitlab-ci.yml":                        "SAST scanning template",
	"Security/SAST.gitlab-ci.yml":                    "SAST scanning template",
	"Jobs/Secret-Detection.gitlab-ci.yml":            "secret detection template",
	"Security/Secret-Detection.gitlab-ci.yml":        "secret detection template",
	"Jobs/Dependency-Scanning.gitlab-ci.yml":         "dependency scanning template",
	"Security/Dependency-Scanning.gitlab-ci.yml":     "dependency scanning template",
	"Jobs/Container-Scanning.gitlab-ci.yml":          "container scanning template",
	"Security/Container-Scanning.gitlab-ci.yml":      "container scanning template",
	"Jobs/License-Scanning.gitlab-ci.yml":            "license scanning template",
	"Security/License-Scanning.gitlab-ci.yml":        "license scanning template",
	"Jobs/Code-Quality.gitlab-ci.yml":                "code quality template",
	"Code-Quality.gitlab-ci.yml":                     "code quality template",
	"Workflows/MergeRequest-Pipelines.gitlab-ci.yml": "merge request pipeline rules template",
	"Workflows/Branch-Pipelines.gitlab-ci.yml":       "branch pipeline rules template",
}

// gitLabNonTestInclude names the purpose of a well-known GitLab component or
// template that runs no tests, or "".
func gitLabNonTestInclude(kind, value string) string {
	switch kind {
	case "component":
		path, _, _ := strings.Cut(value, "@")
		return gitLabNonTestComponents[path]
	case "template":
		return gitLabNonTestTemplates[value]
	}
	return ""
}

// gitLabMerge deep-merges an override into an inherited mapping as GitLab
// merges includes and extends: mappings merge, everything else is replaced.
// The result shares the original nodes, so their files and lines are kept.
func gitLabMerge(base, override *yaml.Node) *yaml.Node {
	base, override = gitLabAlias(base), gitLabAlias(override)
	if base == nil || override == nil || base.Kind != yaml.MappingNode || override.Kind != yaml.MappingNode || override.Tag == "!reference" {
		return override
	}
	members := gitLabMembers(base)
	keys := gitLabKeys(base)
	for _, key := range gitLabKeys(override) {
		value := gitLabMembers(override)[key]
		if previous, ok := members[key]; ok {
			value = gitLabMerge(previous, value)
		} else {
			keys = append(keys, key)
		}
		members[key] = value
	}
	merged := &yaml.Node{Kind: yaml.MappingNode, Line: override.Line, Column: override.Column}
	for _, key := range keys {
		merged.Content = append(merged.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, members[key])
	}
	return merged
}

func gitLabAlias(node *yaml.Node) *yaml.Node {
	for depth := 0; node != nil && node.Kind == yaml.AliasNode && depth < 32; depth++ {
		node = node.Alias
	}
	return node
}

func gitLabScalar(node *yaml.Node) string {
	node = gitLabAlias(node)
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag == "!!null" {
		return ""
	}
	return node.Value
}

// gitLabKeys lists a mapping's keys in order, merge keys (<<) first.
func gitLabKeys(node *yaml.Node) []string {
	node = gitLabAlias(node)
	var keys []string
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == "<<" {
			sources := []*yaml.Node{gitLabAlias(node.Content[i+1])}
			if sources[0] != nil && sources[0].Kind == yaml.SequenceNode {
				sources = sources[0].Content
			}
			for _, source := range sources {
				for _, key := range gitLabKeys(source) {
					if !slices.Contains(keys, key) {
						keys = append(keys, key)
					}
				}
			}
		}
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if key := node.Content[i].Value; key != "<<" && !slices.Contains(keys, key) {
			keys = append(keys, key)
		}
	}
	return keys
}

// gitLabMembers maps a mapping's keys to values, resolving YAML merge keys.
func gitLabMembers(node *yaml.Node) map[string]*yaml.Node {
	node = gitLabAlias(node)
	members := map[string]*yaml.Node{}
	if node == nil || node.Kind != yaml.MappingNode {
		return members
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value != "<<" {
			continue
		}
		sources := []*yaml.Node{gitLabAlias(node.Content[i+1])}
		if sources[0] != nil && sources[0].Kind == yaml.SequenceNode {
			sources = sources[0].Content
		}
		for j := len(sources) - 1; j >= 0; j-- {
			maps.Copy(members, gitLabMembers(sources[j]))
		}
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if key := node.Content[i].Value; key != "<<" {
			members[key] = node.Content[i+1]
		}
	}
	return members
}

func gitLabDescribe(node *yaml.Node) string {
	node = gitLabAlias(node)
	if node == nil {
		return "(empty)"
	}
	if node.Kind == yaml.ScalarNode {
		return node.Value
	}
	data, err := yaml.Marshal(node)
	if err != nil {
		return "(unreadable)"
	}
	return strings.Join(strings.Fields(string(data)), " ")
}

// gitLabStrings reads a scalar or a list of scalars.
func gitLabStrings(node *yaml.Node) []string {
	node = gitLabAlias(node)
	if node == nil {
		return nil
	}
	if node.Kind == yaml.SequenceNode {
		var values []string
		for _, item := range node.Content {
			values = append(values, gitLabScalar(item))
		}
		return values
	}
	return []string{gitLabScalar(node)}
}

// gitLabJob is one job after extends, defaults and !reference resolution.
type gitLabJob struct {
	name   string
	fields map[string]*yaml.Node
	// problem makes every step of the job unresolved.
	problem string
}

// resolveJob merges a job with the templates it extends, in order.
func (c *gitLabConfig) resolveJob(name string, visiting []string) (map[string]*yaml.Node, error) {
	if slices.Contains(visiting, name) || len(visiting) > 11 {
		return nil, fmt.Errorf("extends cycle or more than 11 levels at %s", name)
	}
	definition, ok := c.definitions[name]
	if !ok {
		return nil, fmt.Errorf("extends %s, which is not defined in the repository's configuration", name)
	}
	if gitLabAlias(definition).Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s is not a job mapping", name)
	}
	own := gitLabAlias(definition)
	var merged *yaml.Node
	for _, parent := range gitLabStrings(gitLabMembers(own)["extends"]) {
		inherited, err := c.resolveJob(parent, append(slices.Clone(visiting), name))
		if err != nil {
			return nil, err
		}
		merged = gitLabMerge(merged, gitLabMapping(inherited))
	}
	merged = gitLabMerge(merged, own)
	members := gitLabMembers(merged)
	delete(members, "extends")
	return members, nil
}

// gitLabMapping builds a mapping node from members, in key order.
func gitLabMapping(members map[string]*yaml.Node) *yaml.Node {
	node := &yaml.Node{Kind: yaml.MappingNode}
	for _, key := range slices.Sorted(maps.Keys(members)) {
		node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, members[key])
	}
	return node
}

// reference resolves a !reference [name, key, ...] tag within the
// configuration.
func (c *gitLabConfig) reference(node *yaml.Node, depth int) (*yaml.Node, error) {
	path := gitLabStrings(node)
	describe := "[" + strings.Join(path, ", ") + "]"
	if node.Kind != yaml.SequenceNode || len(path) == 0 || slices.Contains(path, "") {
		return nil, fmt.Errorf("!reference %s is not a list of names", describe)
	}
	if depth > 10 {
		return nil, fmt.Errorf("!reference %s nests more than 10 levels", describe)
	}
	// A reference reads a job after extends; top-level keys such as
	// variables or default are mappings without a job's shape.
	members, err := c.resolveJob(path[0], nil)
	if err != nil {
		definition, ok := c.definitions[path[0]]
		if !ok {
			return nil, fmt.Errorf("!reference %s: %w", describe, err)
		}
		members = gitLabMembers(definition)
	}
	var target *yaml.Node
	for i, key := range path[1:] {
		target = gitLabAlias(members[key])
		if target == nil {
			return nil, fmt.Errorf("!reference %s names a key that is not defined", describe)
		}
		if i < len(path)-2 {
			members = gitLabMembers(target)
		}
	}
	if target == nil {
		return nil, fmt.Errorf("!reference %s must name a key", describe)
	}
	return c.expand(target, depth+1)
}

// expand resolves !reference tags inside node; a sequence item that is a
// reference to a sequence is inlined, as GitLab does in scripts.
func (c *gitLabConfig) expand(node *yaml.Node, depth int) (*yaml.Node, error) {
	node = gitLabAlias(node)
	if node == nil {
		return nil, nil
	}
	if node.Tag == "!reference" {
		return c.reference(node, depth)
	}
	switch node.Kind {
	case yaml.SequenceNode:
		expanded := *node
		expanded.Content = nil
		changed := false
		for _, item := range node.Content {
			if item.Kind == yaml.AliasNode && gitLabAlias(item).Kind == yaml.ScalarNode {
				// Keep the alias: edits must know the entry is shared.
				expanded.Content = append(expanded.Content, item)
				continue
			}
			resolved, err := c.expand(item, depth)
			if err != nil {
				return nil, err
			}
			if gitLabAlias(item).Tag == "!reference" && resolved.Kind == yaml.SequenceNode {
				expanded.Content = append(expanded.Content, resolved.Content...)
				changed = true
				continue
			}
			changed = changed || resolved != item
			expanded.Content = append(expanded.Content, resolved)
		}
		if !changed {
			return node, nil
		}
		return &expanded, nil
	case yaml.MappingNode:
		expanded := *node
		expanded.Content = slices.Clone(node.Content)
		changed := false
		for i := 1; i < len(expanded.Content); i += 2 {
			resolved, err := c.expand(expanded.Content[i], depth)
			if err != nil {
				return nil, err
			}
			changed = changed || resolved != expanded.Content[i]
			expanded.Content[i] = resolved
		}
		if !changed {
			return node, nil
		}
		return &expanded, nil
	}
	return node, nil
}

// jobs resolves every job: hidden jobs (.name) are templates only.
func (c *gitLabConfig) jobs() []gitLabJob {
	defaults := gitLabMembers(c.definitions["default"])
	for _, key := range []string{"image", "before_script", "after_script", "services", "cache"} {
		if defaults[key] == nil && c.definitions[key] != nil {
			defaults[key] = c.definitions[key]
		}
	}
	var jobs []gitLabJob
	for _, name := range slices.Sorted(maps.Keys(c.definitions)) {
		if strings.HasPrefix(name, ".") || slices.Contains(gitLabReserved, name) {
			continue
		}
		if gitLabAlias(c.definitions[name]).Kind != yaml.MappingNode {
			continue
		}
		job := gitLabJob{name: name}
		fields, err := c.resolveJob(name, nil)
		if err != nil {
			job.fields, job.problem = gitLabMembers(c.definitions[name]), err.Error()
			jobs = append(jobs, job)
			continue
		}
		policy := gitLabAlias(gitLabMembers(fields["inherit"])["default"])
		for key, value := range defaults {
			inherited := gitLabScalar(policy) != "false"
			if policy != nil && policy.Kind == yaml.SequenceNode {
				inherited = slices.Contains(gitLabStrings(policy), key)
			}
			if inherited && fields[key] == nil {
				fields[key] = value
			}
		}
		for _, key := range []string{"script", "before_script", "after_script", "variables", "rules", "image", "parallel", "only", "except", "when", "trigger"} {
			if fields[key] == nil {
				continue
			}
			expanded, err := c.expand(fields[key], 0)
			if err != nil {
				job.problem = err.Error()
				break
			}
			fields[key] = expanded
		}
		job.fields = fields
		jobs = append(jobs, job)
	}
	return jobs
}

// workflows converts the merged configuration into one workflow per file
// that defines jobs; unreadable configuration becomes unresolved steps of
// the root file.
func (c *gitLabConfig) workflows() []ciWorkflow {
	byFile := map[string]*ciWorkflow{}
	workflow := func(file string) *ciWorkflow {
		if byFile[file] == nil {
			byFile[file] = &ciWorkflow{Path: file, Jobs: map[string]runtimeJob{}}
		}
		return byFile[file]
	}
	globals := c.variables(c.definitions["variables"], gitLabCIFile)
	pipeline := gitLabEvaluateRules(gitLabMembers(c.definitions["workflow"])["rules"], gitLabExpandVariables(globals))
	pipelineActive := pipeline.state != gitLabNever
	if pipeline.unknownVariables {
		c.gaps = append(c.gaps, gitLabGap{c.owners["workflow"], "workflow " + gitLabRuleVariablesGap})
	}
	globals = mergeEnvironment(globals, pipeline.variables)
	for _, job := range c.jobs() {
		file := c.owners[job.name]
		for name, converted := range c.convert(job, globals) {
			if !pipelineActive {
				converted.If = "false"
			}
			workflow(file).Jobs[name] = converted
		}
	}
	c.gaps = slices.CompactFunc(c.gaps, func(a, b gitLabGap) bool { return a == b })
	for i, gap := range c.gaps {
		name := fmt.Sprintf("(unresolved configuration %d)", i+1)
		workflow(gitLabCIFile).Jobs[name] = runtimeJob{Steps: []runtimeStep{{Number: 1, Source: gap.file, ResolutionError: "GitLab CI: " + gap.file + ": " + gap.reason + "; its jobs may run tests. Supply the resolved configuration in the repository, or validate those jobs separately."}}}
	}
	root := workflow(gitLabCIFile)
	root.Review = slices.Compact(c.review)
	if len(c.nonTest) > 0 {
		root.Caveats = []string{gitLabCIFile + ": GitLab components and templates that only scan, review or release run no tests and are outside this validation: " + strings.Join(slices.Compact(c.nonTest), ", ") + "."}
	}
	root.Files = slices.DeleteFunc(slices.Clone(c.order), func(file string) bool { return file == gitLabCIFile })
	var workflows []ciWorkflow
	for _, file := range slices.Sorted(maps.Keys(byFile)) {
		workflows = append(workflows, *byFile[file])
	}
	return workflows
}

// variables reads a variables mapping after resolving its !reference tags;
// a reference that cannot be resolved is a gap of file.
func (c *gitLabConfig) variables(node *yaml.Node, file string) map[string]string {
	if node == nil {
		return map[string]string{}
	}
	expanded, err := c.expand(node, 0)
	if err != nil {
		c.gaps = append(c.gaps, gitLabGap{file, "variables: " + err.Error()})
		return map[string]string{}
	}
	return gitLabVariables(expanded)
}

// gitLabVariables reads a variables mapping: plain values or
// {value, description, expand, options}.
func gitLabVariables(node *yaml.Node) map[string]string {
	variables := map[string]string{}
	for key, value := range gitLabMembers(node) {
		value = gitLabAlias(value)
		if value == nil {
			continue
		}
		if value.Kind == yaml.MappingNode {
			members := gitLabMembers(value)
			variables[key] = gitLabScalar(members["value"])
			if gitLabScalar(members["expand"]) == "false" {
				variables[key] = strings.ReplaceAll(variables[key], "$", "$$")
			}
			continue
		}
		variables[key] = gitLabScalar(value)
	}
	return variables
}

// convert builds the shared model's jobs for one GitLab job: one per
// parallel:matrix entry, named as GitLab names them.
func (c *gitLabConfig) convert(job gitLabJob, globals map[string]string) map[string]runtimeJob {
	fields := job.fields
	jobs := map[string]runtimeJob{}
	unresolved := func(name, reason string) {
		jobs[name] = runtimeJob{Steps: []runtimeStep{{Number: 1, Source: c.owners[job.name], ResolutionError: "GitLab CI job " + job.name + ": " + reason}}}
	}
	if job.problem != "" {
		unresolved(job.name, job.problem)
		return jobs
	}
	if fields["trigger"] != nil {
		// A trigger that never runs is not a blocker.
		globalsOnly := gitLabExpandVariables(mergeEnvironment(globals, gitLabVariables(fields["variables"])))
		if gitLabScalar(fields["when"]) == "never" || fields["rules"] != nil && gitLabEvaluateRules(fields["rules"], globalsOnly).state == gitLabNever {
			return jobs
		}
		unresolved(job.name, "triggers a downstream or child pipeline whose jobs ddtest cannot read; validate that pipeline's test jobs separately")
		return jobs
	}
	if fields["script"] == nil {
		if fields["run"] != nil {
			// GitLab CI steps (run:) execute components ddtest cannot read.
			globalsOnly := gitLabExpandVariables(mergeEnvironment(globals, gitLabVariables(fields["variables"])))
			if gitLabScalar(fields["when"]) != "never" && (fields["rules"] == nil || gitLabEvaluateRules(fields["rules"], globalsOnly).state != gitLabNever) {
				unresolved(job.name, "uses GitLab CI run steps, which ddtest cannot resolve locally; validate that job's tests separately")
			}
		}
		return jobs // Not a job GitLab runs.
	}
	inherited := maps.Clone(globals)
	policy := gitLabAlias(gitLabMembers(fields["inherit"])["variables"])
	if gitLabScalar(policy) == "false" {
		inherited = map[string]string{}
	} else if policy != nil && policy.Kind == yaml.SequenceNode {
		allowed := gitLabStrings(policy)
		maps.DeleteFunc(inherited, func(key, _ string) bool { return !slices.Contains(allowed, key) })
	}
	base := mergeEnvironment(inherited, gitLabVariables(fields["variables"]))
	rows, err := gitLabMatrix(fields["parallel"])
	if err != nil {
		unresolved(job.name, err.Error())
		return jobs
	}
	for _, row := range rows {
		name := job.name
		if len(row) > 0 {
			var values []string
			for _, axis := range row {
				values = append(values, axis.value)
			}
			name += ": [" + strings.Join(values, ", ") + "]"
		}
		env := maps.Clone(base)
		if env == nil {
			env = map[string]string{}
		}
		for _, axis := range row {
			env[axis.name] = axis.value
		}
		env = gitLabExpandVariables(env)
		gitLabDropCaches(env, fields["cache"])
		converted := runtimeJob{Env: env}
		rules := gitLabEvaluateRules(fields["rules"], env)
		if fields["rules"] != nil && rules.state == gitLabNever || gitLabScalar(fields["when"]) == "never" {
			converted.If = "false"
		}
		if len(rules.variables) > 0 {
			env = gitLabExpandInto(env, rules.variables)
			converted.Env = env
		}
		if image := gitLabAlias(fields["image"]); image != nil {
			converted.Image = gitLabScalar(image)
			if image.Kind == yaml.MappingNode {
				converted.Image = gitLabScalar(gitLabMembers(image)["name"])
			}
			converted.ImageTemplate = converted.Image
			converted.Image = gitLabExpand(converted.Image, env)
		}
		converted.Steps = c.steps(fields, env, converted.Image)
		converted.Env = gitLabDropRuntimeAxes(converted, row, gitLabScalar(fields["image"])+gitLabScalar(gitLabMembers(fields["image"])["name"]))
		if rules.unknownVariables {
			for i := range converted.Steps {
				converted.Steps[i].ResolutionError = "GitLab CI job " + job.name + ": " + gitLabRuleVariablesGap
			}
		}
		jobs[name] = converted
	}
	return jobs
}

// gitLabDropCaches removes variables that only locate a directory the job
// declares as cache, such as npm_config_cache: $CI_PROJECT_DIR/.npm with
// cache:paths [.npm/]. They configure runner state, not the tests, and local
// validation keeps its own caches outside the checkout.
func gitLabDropCaches(env map[string]string, cache *yaml.Node) {
	cache = gitLabAlias(cache)
	if cache == nil {
		return
	}
	entries := []*yaml.Node{cache}
	if cache.Kind == yaml.SequenceNode {
		entries = cache.Content
	}
	var paths []string
	for _, entry := range entries {
		for _, value := range gitLabStrings(gitLabMembers(entry)["paths"]) {
			if value = strings.Trim(path.Clean(strings.TrimPrefix(value, "./")), "/"); value != "" && value != "." && !strings.ContainsAny(value, "*?[$") {
				paths = append(paths, value)
			}
		}
	}
	for key, value := range env {
		// Only tool cache settings (names with "cache", or a language's own,
		// such as its package install directory) are runner state;
		// application variables such as fixture paths stay.
		if !strings.Contains(strings.ToLower(key), "cache") && !slices.ContainsFunc(discoveryLanguages, func(language discoveryLanguage) bool { return language.cacheVariable(key) }) {
			continue
		}
		location := strings.TrimPrefix(value, gitLabProjectDirectory+"/")
		location = strings.Trim(path.Clean(strings.TrimPrefix(location, "./")), "/")
		if slices.ContainsFunc(paths, func(cached string) bool { return location == cached || strings.HasPrefix(location, cached+"/") }) {
			delete(env, key)
		}
	}
}

type gitLabAxis struct{ name, value string }

// gitLabDropRuntimeAxes removes matrix variables that only select the job's
// image, such as RUBY_IMAGE in image: $RUBY_IMAGE: like a runtime matrix
// value on GitHub Actions, they choose the runtime, which is checked per
// entry, and the entries share one local configuration. A variable a
// command, another variable or a step reads stays.
func gitLabDropRuntimeAxes(job runtimeJob, row []gitLabAxis, image string) map[string]string {
	env := job.Env
	for _, axis := range row {
		reads := func(text string) bool {
			return strings.Contains(text, "$"+axis.name) || strings.Contains(text, "${"+axis.name+"}")
		}
		if !reads(image) {
			continue
		}
		used := false
		for _, step := range job.Steps {
			used = used || reads(step.Run) || step.Run == "" && step.Setup == nil && step.ResolutionError != ""
			for key, value := range step.Env {
				used = used || key == axis.name || reads(value)
			}
		}
		if used {
			continue
		}
		env = maps.Clone(env)
		delete(env, axis.name)
	}
	return env
}

// gitLabMatrix expands parallel:matrix into rows of variables, in axis
// order; a job without one has a single empty row. parallel: N runs the same
// job N times and needs no row.
func gitLabMatrix(parallel *yaml.Node) ([][]gitLabAxis, error) {
	matrix := gitLabAlias(gitLabMembers(parallel)["matrix"])
	if matrix == nil {
		return [][]gitLabAxis{nil}, nil
	}
	if matrix.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("parallel:matrix must be a list of variable sets")
	}
	var rows [][]gitLabAxis
	for _, entry := range matrix.Content {
		expanded := [][]gitLabAxis{nil}
		for _, axis := range gitLabKeys(entry) {
			values := gitLabStrings(gitLabMembers(entry)[axis])
			var next [][]gitLabAxis
			for _, row := range expanded {
				for _, value := range values {
					next = append(next, append(slices.Clone(row), gitLabAxis{axis, value}))
				}
			}
			expanded = next
		}
		rows = append(rows, expanded...)
		if len(rows) > 200 {
			return nil, fmt.Errorf("parallel:matrix expands to more than 200 jobs")
		}
	}
	return rows, nil
}

// gitLabExpandVariables expands references between variables, as GitLab
// does before a job starts. $$ is a literal dollar sign.
func gitLabExpandVariables(env map[string]string) map[string]string {
	return gitLabExpandInto(nil, env)
}

// gitLabExpandInto expands added against the already expanded env and adds
// them. A value still reading a variable ddtest does not know becomes
// unresolved (unresolvedCIValue), unless it is a setup script input, which the
// setup checks read as written; $$ (and expand: false) stays a literal $.
func gitLabExpandInto(env, added map[string]string) map[string]string {
	expanded := maps.Clone(env)
	if expanded == nil {
		expanded = map[string]string{}
	}
	scope := mergeEnvironment(env, added)
	for key, value := range added {
		value = gitLabExpand(value, scope)
		if _, input := gitLabSetupInput(key); !input && gitLabReadsUnknown(value) {
			value = unresolvedCIValue(strings.ReplaceAll(value, "$$", "$"))
		}
		expanded[key] = strings.ReplaceAll(value, "$$", "$")
	}
	return expanded
}

// gitLabReadsUnknown reports a $NAME or ${NAME} read left after expansion;
// $$ and the model's ${{ }} expressions are not reads.
func gitLabReadsUnknown(value string) bool {
	for i := 0; i+1 < len(value); i++ {
		if value[i] != '$' {
			continue
		}
		next := value[i+1]
		if next == '$' {
			i++
			continue
		}
		if strings.HasPrefix(value[i:], "${{") {
			continue
		}
		if next == '{' || next == '_' || next >= 'A' && next <= 'Z' || next >= 'a' && next <= 'z' {
			return true
		}
	}
	return false
}

// gitLabProjectDirectory is the shared model's expression for the checkout
// root, which GitLab names CI_PROJECT_DIR.
const gitLabProjectDirectory = "${{ github.workspace }}"

// gitLabExpand replaces $NAME and ${NAME} with known values, a bounded
// number of times; unknown references stay as written.
func gitLabExpand(value string, env map[string]string) string {
	for depth := 0; depth < 8 && strings.Contains(value, "$"); depth++ {
		var result strings.Builder
		changed := false
		for i := 0; i < len(value); i++ {
			if value[i] != '$' || i+1 >= len(value) {
				result.WriteByte(value[i])
				continue
			}
			if value[i+1] == '$' {
				result.WriteString("$$")
				i++
				continue
			}
			name, end := "", i+1
			if value[i+1] == '{' {
				close := strings.IndexByte(value[i+2:], '}')
				if close < 0 {
					result.WriteByte(value[i])
					continue
				}
				name, end = value[i+2:i+2+close], i+3+close
			} else {
				for end < len(value) && (value[end] == '_' || value[end] >= 'A' && value[end] <= 'Z' || value[end] >= 'a' && value[end] <= 'z' || value[end] >= '0' && value[end] <= '9' && end > i+1) {
					end++
				}
				name = value[i+1 : end]
			}
			known, ok := env[name]
			if name == "CI_PROJECT_DIR" {
				known, ok = gitLabProjectDirectory, true
			}
			if name == "" || !ok {
				result.WriteString(value[i:end])
				i = end - 1
				continue
			}
			result.WriteString(known)
			changed = true
			i = end - 1
		}
		value = result.String()
		if !changed {
			break
		}
	}
	return value
}

// gitLabRuleState is whether rules let a job (or an include) run.
type gitLabRuleState int

const (
	gitLabPossible gitLabRuleState = iota
	gitLabNever
	gitLabAlways
)

// gitLabRuleResult is what rules decide for known variables.
type gitLabRuleResult struct {
	state gitLabRuleState
	// variables are those of the rule that certainly applies.
	variables map[string]string
	// unknownVariables reports that a rule that may apply sets variables,
	// but which rule applies depends on the pipeline.
	unknownVariables bool
}

// gitLabEvaluateRules evaluates rules in order, as GitLab does: the first
// matching rule decides. Conditions on unknown variables, changes and exists
// may match; a rule that may match makes later decisions uncertain.
func gitLabEvaluateRules(rules *yaml.Node, env map[string]string) gitLabRuleResult {
	rules = gitLabAlias(rules)
	if rules == nil || rules.Kind != yaml.SequenceNode {
		return gitLabRuleResult{state: gitLabPossible}
	}
	var result gitLabRuleResult
	uncertain, possible := false, false
	for _, rule := range rules.Content {
		members := gitLabMembers(rule)
		match := gitLabTrue
		if condition := members["if"]; condition != nil {
			match = gitLabCondition(gitLabScalar(condition), env)
		}
		if match != gitLabFalse && (members["changes"] != nil || members["exists"] != nil) {
			match = gitLabUnknown
		}
		if match == gitLabFalse {
			continue
		}
		runs := gitLabScalar(members["when"]) != "never"
		possible = possible || runs
		variables := gitLabVariables(members["variables"])
		if runs && len(variables) > 0 && (match == gitLabUnknown || uncertain) {
			result.unknownVariables = true
		}
		if match == gitLabUnknown {
			uncertain = true
			continue
		}
		if runs && !uncertain {
			result.state, result.variables = gitLabAlways, variables
			return result
		}
		break
	}
	result.state = gitLabPossible
	if !possible {
		result.state = gitLabNever
	}
	return result
}

// gitLabRuleVariablesGap explains why rules:variables make a job's or the
// pipeline's environment depend on the pipeline.
const gitLabRuleVariablesGap = "rules:variables set the variables per pipeline; ddtest cannot tell which values the tests see"

type gitLabTruth int

const (
	gitLabUnknown gitLabTruth = iota
	gitLabTrue
	gitLabFalse
)

// gitLabCondition evaluates a rules:if expression with ==, !=, &&, || and
// parentheses over variables and quoted strings. Variables the job does not
// define, regular expressions and anything else are unknown.
func gitLabCondition(expression string, env map[string]string) gitLabTruth {
	parser := gitLabConditionParser{tokens: gitLabTokens(expression), env: env}
	value := parser.or()
	if parser.failed || parser.position != len(parser.tokens) {
		return gitLabUnknown
	}
	return value
}

func gitLabTokens(expression string) []string {
	var tokens []string
	for i := 0; i < len(expression); {
		switch character := expression[i]; {
		case character == ' ' || character == '\t':
			i++
		case strings.HasPrefix(expression[i:], "&&") || strings.HasPrefix(expression[i:], "||") || strings.HasPrefix(expression[i:], "==") || strings.HasPrefix(expression[i:], "!=") || strings.HasPrefix(expression[i:], "=~") || strings.HasPrefix(expression[i:], "!~"):
			tokens = append(tokens, expression[i:i+2])
			i += 2
		case character == '(' || character == ')':
			tokens = append(tokens, string(character))
			i++
		case character == '"' || character == '\'':
			end := strings.IndexByte(expression[i+1:], character)
			if end < 0 {
				return append(tokens, "\x00")
			}
			tokens = append(tokens, expression[i:i+end+2])
			i += end + 2
		case character == '/':
			end := strings.IndexByte(expression[i+1:], '/')
			if end < 0 {
				return append(tokens, "\x00")
			}
			end = i + end + 2
			for end < len(expression) && strings.IndexByte("imx", expression[end]) >= 0 {
				end++
			}
			tokens = append(tokens, expression[i:end])
			i = end
		default:
			end := i
			for end < len(expression) && strings.IndexByte(" \t()&|=!\"'", expression[end]) < 0 {
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

// gitLabNull is null in rule conditions: a defined empty variable is not null.
const gitLabNull = "\x00null"

type gitLabConditionParser struct {
	tokens   []string
	position int
	env      map[string]string
	failed   bool
}

func (p *gitLabConditionParser) peek() string {
	if p.position < len(p.tokens) {
		return p.tokens[p.position]
	}
	return ""
}

func (p *gitLabConditionParser) or() gitLabTruth {
	left := p.and()
	for p.peek() == "||" {
		p.position++
		right := p.and()
		switch {
		case left == gitLabTrue || right == gitLabTrue:
			left = gitLabTrue
		case left == gitLabFalse && right == gitLabFalse:
			left = gitLabFalse
		default:
			left = gitLabUnknown
		}
	}
	return left
}

func (p *gitLabConditionParser) and() gitLabTruth {
	left := p.comparison()
	for p.peek() == "&&" {
		p.position++
		right := p.comparison()
		switch {
		case left == gitLabFalse || right == gitLabFalse:
			left = gitLabFalse
		case left == gitLabTrue && right == gitLabTrue:
			left = gitLabTrue
		default:
			left = gitLabUnknown
		}
	}
	return left
}

// operand returns a value: known reports whether it is known, present
// whether a variable is defined and non-empty.
func (p *gitLabConditionParser) operand() (value string, known bool) {
	token := p.peek()
	p.position++
	switch {
	case token == "" || token == "\x00":
		p.failed = true
		return "", false
	case token == "null":
		return gitLabNull, true
	case strings.HasPrefix(token, "\"") || strings.HasPrefix(token, "'"):
		return token[1 : len(token)-1], true
	case strings.HasPrefix(token, "$"):
		name := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(token, "$"), "{"), "}")
		value, ok := p.env[name]
		if !ok || strings.Contains(value, "$") {
			return "", false
		}
		return value, true
	}
	p.failed = true
	return "", false
}

func (p *gitLabConditionParser) comparison() gitLabTruth {
	if p.peek() == "(" {
		p.position++
		value := p.or()
		if p.peek() != ")" {
			p.failed = true
			return gitLabUnknown
		}
		p.position++
		return value
	}
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
	case "=~", "!~":
		p.position++
		p.position++ // A pattern is never evaluated locally.
		return gitLabUnknown
	}
	if !leftKnown {
		return gitLabUnknown
	}
	if left != "" {
		return gitLabTrue
	}
	return gitLabFalse
}

// gitLabStep is one script entry and its source, the origin of its step.
type gitLabStep struct {
	node *yaml.Node
	file string
	// listed reports an entry of a script list, rather than a script that is
	// a single string; aliased an entry written as a YAML alias (*name).
	listed, aliased bool
}

// steps converts before_script and script entries into the shared model's
// steps, carrying their shell state; after_script entries, which run in
// another shell after the job, are unresolved when they may run tests.
func (c *gitLabConfig) steps(fields map[string]*yaml.Node, env map[string]string, image string) []runtimeStep {
	var entries []gitLabStep
	for _, key := range []string{"before_script", "script"} {
		entries = append(entries, c.entries(fields[key])...)
	}
	steps := gitLabShell(entries, env, 0, image)
	after := gitLabShell(c.entries(fields["after_script"]), env, len(entries), image)
	probe := ciWorkflow{Jobs: map[string]runtimeJob{"after_script": {Env: env, Steps: after}}}
	if len(after) > 0 && workflowHasTests(c.root, probe) {
		for _, step := range after {
			step.ResolutionError = "GitLab CI after_script runs in a separate shell after the job, without the Datadog setup; run tests in script instead: " + step.Run
			steps = append(steps, step)
		}
	}
	return steps
}

// entries lists the scalar entries of a script, flattening nested lists.
func (c *gitLabConfig) entries(node *yaml.Node) []gitLabStep {
	node = gitLabAlias(node)
	if node == nil {
		return nil
	}
	if node.Kind == yaml.SequenceNode {
		var entries []gitLabStep
		for _, item := range node.Content {
			if item := gitLabAlias(item); item != nil && item.Kind == yaml.SequenceNode {
				entries = append(entries, c.entries(item)...)
				continue
			}
			entries = append(entries, gitLabStep{node: gitLabAlias(item), file: c.files[item], listed: true, aliased: item.Kind == yaml.AliasNode})
		}
		return entries
	}
	return []gitLabStep{{node: node, file: c.files[node]}}
}

// gitLabShell folds the shell state script entries share into steps: literal
// exports and assignments become the environment of later steps, a literal cd
// their working directory, and the Datadog setup script a setup step.
// Changes ddtest cannot follow make later steps unresolved.
func gitLabShell(entries []gitLabStep, env map[string]string, offset int, image string) []runtimeStep {
	state := gitLabState{env: map[string]string{}, references: map[string]string{}, job: env}
	var steps []runtimeStep
	for i, entry := range entries {
		step := runtimeStep{Number: offset + i + 1, Source: entry.file + ":" + strconv.Itoa(entry.node.Line), origin: entry}
		if len(state.env) > 0 {
			step.Env = maps.Clone(state.env)
		}
		step.WorkingDirectory = state.directory
		step.ResolutionError = state.problem
		if entry.node.Kind != yaml.ScalarNode {
			step.ResolutionError = "GitLab CI script entry " + gitLabDescribe(entry.node) + " is not a command"
			steps = append(steps, step)
			continue
		}
		command := entry.node.Value
		current := mergeEnvironment(env, state.env)
		if setup, ok := gitLabSetupInvocation(command, mergeEnvironment(current, state.references), state.literal); ok {
			if setup.Problem == "" && !setup.shaped {
				// Other commands in the entry would disappear with the setup.
				step.Run = command
				step.ResolutionError = "GitLab CI script entry combines the Datadog setup script with other commands; give the setup entry exactly the form ddtest prescribes, with the commands in their own entries: " + command
				steps = append(steps, step)
				continue
			}
			if reason := gitLabImageProblem(image, state.curl); reason != "" && setup.Problem == "" {
				setup.Problem = reason
			}
			step.Setup = &setup
			// Only a setup that can run exports anything; an incomplete one is
			// reported through the completeness check.
			if setup.Supported && setup.Problem == "" && gitLabVariableName.MatchString(strings.TrimSpace(setup.Inputs["api_key"])) {
				state.applySetup(setup, current)
				for _, stmt := range setup.after {
					state.statement(stmt, mergeEnvironment(mergeEnvironment(current, state.env), state.references))
				}
			}
			steps = append(steps, step)
			continue
		}
		command = gitLabResolveBranches(command, mergeEnvironment(current, state.references))
		state.curl = state.curl || gitLabInstallsCurl(command)
		step.Run = command
		if consumed := strings.TrimSpace(command) == "" || state.apply(command, current); consumed {
			step.Run = ""
		}
		steps = append(steps, step)
	}
	return steps
}

// gitLabShellProblem explains why a job image cannot run the setup entry,
// which needs Bash (source and process substitution): GitLab's Docker
// executor runs the job's script with sh when the image has no Bash, and it
// chooses the shell when the job starts, so installing Bash in the script
// does not help. Images not known to lack Bash pass.
func gitLabShellProblem(image string) string {
	name := strings.ToLower(image)
	if name == "" {
		return ""
	}
	if strings.Contains(name, "$") {
		return "The job image " + image + " depends on a variable ddtest cannot resolve, so it cannot tell whether the image has the Bash and curl the Datadog setup script needs. Give the job a literal image (or a variable defined in the configuration) that has both."
	}
	repository, tag, _ := strings.Cut(name[strings.LastIndex(name, "/")+1:], ":")
	if strings.Contains(tag, "alpine") || repository == "alpine" || repository == "busybox" || strings.Contains(name, "distroless") || strings.Contains(tag, "busybox") {
		return "The job image " + image + " has no Bash, so GitLab runs its script with sh and the Datadog setup script (source, process substitution) cannot run. Use a Bash-capable image for this job, such as the non-Alpine variant, or a custom image with Bash preinstalled; installing Bash in the script does not change the job's shell."
	}
	return ""
}

// gitLabCurlInstall is the entry that adds curl to a Debian slim image.
const gitLabCurlInstall = "apt-get update && apt-get install -y --no-install-recommends curl"

// gitLabImageProblem explains why the setup entry cannot run in a job
// image: no Bash, an image ddtest cannot resolve, or a slim image without
// curl (unless an earlier entry installed it).
func gitLabImageProblem(image string, curl bool) string {
	if reason := gitLabShellProblem(image); reason != "" {
		return reason
	}
	// Docker's official slim images remove curl and wget after building
	// (verified on the slim language runtime images).
	name := strings.ToLower(image)
	official := !strings.Contains(gitLabDockerHubName(name), "/")
	_, tag, _ := strings.Cut(name[strings.LastIndex(name, "/")+1:], ":")
	if !curl && official && strings.Contains(tag, "slim") {
		return "The job image " + image + " has no curl, so the Datadog setup script cannot be downloaded and its tests would run untraced. Use the full image (without -slim), or add an entry that installs curl before the setup entry: " + gitLabCurlInstall + "."
	}
	return ""
}

// gitLabDockerHubName strips Docker Hub's registry and library prefixes,
// so docker.io/library/node, index.docker.io/node and library/node all
// read as the official node image.
func gitLabDockerHubName(image string) string {
	for _, registry := range []string{"docker.io/", "index.docker.io/", "registry-1.docker.io/", "registry.hub.docker.com/"} {
		if rest, ok := strings.CutPrefix(image, registry); ok {
			image = rest
			break
		}
	}
	return strings.TrimPrefix(image, "library/")
}

// gitLabInstallsCurl reports a command installing curl with apt.
func gitLabInstallsCurl(command string) bool {
	words := strings.Fields(command)
	return (slices.Contains(words, "apt-get") || slices.Contains(words, "apt")) && slices.Contains(words, "install") && slices.Contains(words, "curl")
}

// gitLabState is the shell state script entries share.
type gitLabState struct {
	// env are the variables entries exported for later ones.
	env map[string]string
	// references are values later entries may expand without exporting.
	references map[string]string
	// job are the job's variables, which the shell exports.
	job       map[string]string
	directory string
	// problem makes later entries unresolved.
	problem string
	// curl reports an earlier entry that installed curl.
	curl bool
	// literal marks setup inputs assigned without expansion ('$NAME').
	literal map[string]bool
}

// applySetup adds what the setup script exports for each language it selects.
func (s *gitLabState) applySetup(setup ciSetup, current map[string]string) {
	languages := strings.Fields(setup.Inputs["languages"])
	for _, language := range discoveryLanguages {
		if setup.Inputs["languages"] != "all" && !slices.Contains(languages, language.actionLanguage()) {
			continue
		}
		exported, references := language.setupShellExports(mergeEnvironment(current, s.env))
		maps.Copy(s.env, exported)
		maps.Copy(s.references, references)
	}
}

// apply records the state one entry changes and reports whether the entry
// only changed state, so it runs nothing later steps must replay.
func (s *gitLabState) apply(command string, current map[string]string) bool {
	file, ok := parseCIShell(command)
	if !ok {
		return false
	}
	known := mergeEnvironment(current, s.references)
	onlyState := len(file.Stmts) > 0
	for _, stmt := range file.Stmts {
		onlyState = s.statement(stmt, known) && onlyState
		known = mergeEnvironment(mergeEnvironment(current, s.env), s.references)
	}
	return onlyState
}
