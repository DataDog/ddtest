// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/settings"
	"go.yaml.in/yaml/v3"
)

// Adapters supply the same jobs/steps consumed by command discovery, runtime
// checking and the execution plan. Native syntax never becomes GitHub actions.
type ciAdapter struct {
	name  string
	paths []string
	read  func(string, string) (ciWorkflow, error)
}

var ciAdapters = []ciAdapter{
	{"github", []string{".github/workflows/*.yml", ".github/workflows/*.yaml"}, nil},
	{"gitlab", []string{".gitlab-ci.yml", ".gitlab-ci.yaml"}, readGitLab},
	{"jenkins", []string{"Jenkinsfile", "Jenkinsfile.*", "jenkins/Jenkinsfile", ".jenkins/Jenkinsfile"}, readJenkins},
	{"circleci", []string{".circleci/config.yml", ".circleci/config.yaml"}, readCircleCI},
	{"buildkite", []string{".buildkite/pipeline.yml", ".buildkite/pipeline.yaml", "buildkite.yml", "buildkite.yaml"}, readBuildkite},
}

// CIProviderChoice is a user decision. No command is suggested until it is made.
type CIProviderChoice struct{ Providers []string }

func (e *CIProviderChoice) Error() string {
	return "Several CI providers contain test commands: " + strings.Join(e.Providers, ", ") + ". Ask the user which provider to install Test Optimization on, then rerun with --ci-provider <provider>."
}

type nativeCommand struct {
	Path                    string
	Node                    *yaml.Node
	Start, End              int // Groovy string byte offsets, including quotes.
	Quote                   string
	Original                string
	Tracer, Framework, Site string
	NodeVersion, NodeSource string
	Manual, ESM             bool
	Blocked                 string
}

func readWorkflows(root string) ([]ciWorkflow, error) {
	return readSelectedWorkflows(root, settings.GetCIProvider())
}

// SelectCIProvider looks for test consumers, not merely a provider's filename.
// A selected provider remains explicit even if some of its commands are opaque.
func SelectCIProvider(root, requested string) (string, error) {
	workflows, err := readSelectedWorkflows(root, requested)
	if err != nil {
		return "", err
	}
	if len(workflows) == 0 {
		return requested, nil
	}
	return workflows[0].Provider, nil
}

func readSelectedWorkflows(root, requested string) ([]ciWorkflow, error) {
	if requested != "" && !slices.ContainsFunc(ciAdapters, func(a ciAdapter) bool { return a.name == requested }) {
		return nil, fmt.Errorf("unknown --ci-provider %q; choose github, gitlab, jenkins, circleci or buildkite", requested)
	}
	var all []ciWorkflow
	var candidates []string
	for _, adapter := range ciAdapters {
		if requested != "" && adapter.name != requested {
			continue
		}
		var workflows []ciWorkflow
		if adapter.name == "github" {
			var err error
			workflows, err = readGitHubWorkflows(root)
			if err != nil {
				return nil, err
			}
		} else {
			for _, pattern := range adapter.paths {
				paths, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(pattern)))
				if err != nil {
					return nil, err
				}
				for _, path := range paths {
					relative := repositoryPath(root, path)
					if _, ok := repositoryFile(root, root, relative); !ok {
						return nil, fmt.Errorf("CI configuration must be a regular file inside the repository: %s", relative)
					}
					workflow, err := adapter.read(root, relative)
					if err != nil {
						return nil, err
					}
					workflows = append(workflows, workflow)
				}
			}
		}
		candidate := false
		for i := range workflows {
			workflows[i].Provider = adapter.name
			if adapter.name != "github" {
				for name, job := range workflows[i].Jobs {
					for n := range job.Steps {
						job.Steps[n].Native = nativeExistingSetup(root, adapter.name, job, n)
					}
					workflows[i].Jobs[name] = job
				}
			}
			candidate = candidate || workflowHasTests(root, workflows[i])
		}
		if candidate {
			candidates = append(candidates, adapter.name)
		}
		all = append(all, workflows...)
	}
	if requested != "" {
		if len(all) == 0 {
			return nil, fmt.Errorf("no %s CI configuration found in this repository", requested)
		}
		return all, nil
	}
	if len(candidates) > 1 {
		return nil, &CIProviderChoice{candidates}
	}
	if len(candidates) == 1 {
		return slices.DeleteFunc(all, func(w ciWorkflow) bool { return w.Provider != candidates[0] }), nil
	}
	// Retain opaque configurations as evidence rather than silently claiming
	// the repository has no CI. Explicit selection can disambiguate these too.
	providers := []string{}
	for _, w := range all {
		if !slices.Contains(providers, w.Provider) {
			providers = append(providers, w.Provider)
		}
	}
	if len(providers) > 1 {
		return nil, &CIProviderChoice{providers}
	}
	return all, nil
}

func workflowHasTests(root string, workflow ciWorkflow) bool {
	for _, job := range workflow.Jobs {
		if job.If == "false" {
			continue
		}
		for _, step := range job.Steps {
			if step.If == "false" {
				continue
			}
			for _, framework := range []string{"jest", "vitest", "mocha", "playwright", "cypress", "cucumber", "webdriverio", "pytest", "rspec", "minitest"} {
				language := "javascript"
				if framework == "pytest" {
					language = "python"
				}
				if framework == "rspec" || framework == "minitest" {
					language = "ruby"
				}
				// Native blockers must not conceal a recognizable test consumer.
				probe := step
				probe.ResolutionError = ""
				if workflow.Provider != "" && workflow.Provider != "github" {
					if resolved, _, err := resolveVariantStep(workflow, job, probe, nil, framework); err == nil {
						probe = resolved
					}
				}
				r := resolveTestStep(root, workflow, job, probe, language, framework)
				if r.Matched || namesFramework(step.Run, framework, true) {
					return true
				}
			}
		}
	}
	return false
}

func providerEnvironment(provider string, env map[string]string) map[string]string {
	defaults := map[string]string{"CI": "true"}
	key := map[string]string{"github": "GITHUB_ACTIONS", "gitlab": "GITLAB_CI", "circleci": "CIRCLECI", "buildkite": "BUILDKITE"}[provider]
	if key != "" {
		defaults[key] = "true"
	}
	return mergeEnvironment(defaults, env)
}

func readCIYAML(root, path string) (*yaml.Node, error) {
	info, ok := repositoryFile(root, root, path)
	if !ok || info.Size() > 2<<20 {
		return nil, fmt.Errorf("CI file is missing, outside the repository, or exceeds 2 MiB: %s", path)
	}
	data, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		return nil, err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	budget := 20000
	if err := validateCINode(&document, map[*yaml.Node]bool{}, 0, &budget); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s must contain one YAML mapping", path)
	}
	return document.Content[0], nil
}

func ciScalar(node *yaml.Node) string {
	if node == nil {
		return ""
	}
	if node.Kind == yaml.AliasNode {
		return ciScalar(node.Alias)
	}
	if node.Kind == yaml.ScalarNode {
		return node.Value
	}
	return ""
}

func ciMembers(node *yaml.Node) map[string]*yaml.Node {
	result := map[string]*yaml.Node{}
	if node == nil {
		return result
	}
	if node.Kind == yaml.AliasNode {
		return ciMembers(node.Alias)
	}
	if node.Kind != yaml.MappingNode {
		return result
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value != "<<" {
			continue
		}
		merge := node.Content[i+1]
		if merge.Kind == yaml.SequenceNode {
			for j := len(merge.Content) - 1; j >= 0; j-- {
				maps.Copy(result, ciMembers(merge.Content[j]))
			}
		} else {
			maps.Copy(result, ciMembers(merge))
		}
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value != "<<" {
			result[node.Content[i].Value] = node.Content[i+1]
		}
	}
	return result
}

func ciEnvironment(node *yaml.Node) map[string]string {
	env := map[string]string{}
	if node != nil && node.Kind == yaml.SequenceNode {
		for _, entry := range node.Content {
			key, value, ok := strings.Cut(ciScalar(entry), "=")
			if ok {
				env[key] = value
			} else {
				env[key] = "${" + key + "}"
			}
		}
		return env
	}
	for k, v := range ciMembers(node) {
		if v.Kind == yaml.ScalarNode {
			env[k] = v.Value
		} else if value := ciMembers(v)["value"]; value != nil {
			env[k] = ciScalar(value)
		} else {
			env[k] = "${" + k + "}"
		}
	}
	return env
}

func ciStrings(node *yaml.Node) []string {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.SequenceNode {
		var out []string
		for _, v := range node.Content {
			out = append(out, ciScalar(v))
		}
		return out
	}
	return []string{ciScalar(node)}
}

func nativeSteps(path string, node *yaml.Node, provider string) []runtimeStep {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.SequenceNode && node.Tag != "!reference" {
		var out []runtimeStep
		for _, child := range node.Content {
			out = append(out, nativeSteps(path, child, provider)...)
		}
		return out
	}
	if node.Kind != yaml.ScalarNode || node.Tag == "!reference" {
		return []runtimeStep{{ResolutionError: path + ": dynamic script/reference needs resolved configuration"}}
	}
	command := node.Value
	native := &nativeCommand{Path: path, Node: node, Original: command}
	if setup, original, ok := unwrapNativeInstrumentation(command, provider); ok {
		native.Framework, native.Tracer, native.Site, native.ESM = setup.Framework, setup.Version, setup.Site, setup.ESM
		command = original
	}
	return []runtimeStep{{Run: command, Source: fmt.Sprintf("%s:%d", path, node.Line), Number: node.Line, Native: native}}
}

func ciImageNode(image string, env map[string]string) string {
	resolved, err := nativeEnvironmentValue(image, env)
	if err != nil {
		return ""
	}
	image = resolved
	image = strings.Split(image, "@")[0]
	name, tag, ok := strings.Cut(image, ":")
	if !ok {
		return ""
	}
	if !slices.Contains([]string{"node", "cimg/node", "circleci/node"}, name) {
		return cypressContainerNode(image)
	}
	version := strings.Split(tag, "-")[0]
	if version == "" || strings.ContainsAny(version, "$<>{}") || latestNodeAlias(version) {
		return ""
	}
	if version[0] < '0' || version[0] > '9' {
		return ""
	}
	return version
}

func literalCIPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return ".", nil
	}
	if !filepath.IsLocal(path) || strings.ContainsAny(path, "$<>{}") {
		return "", fmt.Errorf("working directory %q needs an explicit repository-relative mapping", path)
	}
	return filepath.ToSlash(filepath.Clean(path)), nil
}

func validateCINode(node *yaml.Node, active map[*yaml.Node]bool, depth int, budget *int) error {
	*budget--
	if depth > 64 || *budget < 0 {
		return fmt.Errorf("CI YAML exceeds the bounded node/depth budget")
	}
	if active[node] {
		return fmt.Errorf("cyclic YAML alias requires review")
	}
	active[node] = true
	defer delete(active, node)
	if node.Kind == yaml.MappingNode {
		keys := map[string]bool{}
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Kind != yaml.ScalarNode || keys[key.Value] {
				return fmt.Errorf("duplicate or non-scalar CI YAML key requires review: %s", key.Value)
			}
			keys[key.Value] = true
		}
	}
	if node.Kind == yaml.AliasNode {
		return validateCINode(node.Alias, active, depth+1, budget)
	}
	for _, child := range node.Content {
		if err := validateCINode(child, active, depth+1, budget); err != nil {
			return err
		}
	}
	return nil
}

var nativeVariable = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z_0-9]*)\}|\$([A-Za-z_][A-Za-z_0-9]*)`)

func nativeEnvironmentValue(value string, environment map[string]string) (string, error) {
	for depth := 0; depth < 16; depth++ {
		previous := value
		value = nativeVariable.ReplaceAllStringFunc(value, func(match string) string {
			parts := nativeVariable.FindStringSubmatch(match)
			key := parts[1]
			if key == "" {
				key = parts[2]
			}
			if known, ok := environment[key]; ok {
				return known
			}
			return match
		})
		if value == previous {
			break
		}
	}
	if strings.ContainsAny(value, "$`\x00") {
		return "", fmt.Errorf("unresolved native CI environment expression %q; supply its literal value or local setup facts", value)
	}
	return value, nil
}
