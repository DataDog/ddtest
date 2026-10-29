// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"os"
	"path"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// buildkiteExecutor is where a step's commands run: on the agent host, or in
// a container a docker or docker-compose plugin (or the image attribute)
// starts, with the shell it runs them with.
type buildkiteExecutor struct {
	// image is the container image, template as written; empty on the agent
	// host, whose runtime ddtest cannot know.
	image, template string
	// imageProblem is why ddtest cannot tell the image's tools.
	imageProblem string
	// container reports commands run by a docker or docker-compose plugin,
	// which pass only the environment they list into the container.
	container bool
	// compose names the Docker Compose service the commands run in;
	// composeRun reports a plugin that runs it rather than only building.
	compose    string
	composeRun bool
	// shell is the program running the commands: bash on the agent, sh in a
	// plugin's container unless its shell option says otherwise.
	shell string
	// problem makes every command of the step unresolved.
	problem string
	// keyPassed reports DD_API_KEY listed in the container's environment;
	// keyProblem a DD_API_KEY written into the configuration.
	keyPassed, keyProblem bool
	windows               bool
	// plugin is the docker or docker-compose plugin's configuration, for
	// edits that pass DD_API_KEY into the container; pluginAliased reports
	// one some step reaches through a YAML alias.
	plugin        *yaml.Node
	pluginAliased bool
}

// buildkiteNonTestPlugins are plugins that only fetch secrets, artifacts or
// caches, log in to registries or annotate results: none of them runs a
// project's tests or changes how a step runs its commands. A use of one is
// reported, not a blocker; every other plugin leaves its step unresolved.
// Plugins match by organization/name, at any version.
var buildkiteNonTestPlugins = map[string]string{
	"buildkite-plugins/artifacts":                         "artifacts",
	"buildkite-plugins/cache":                             "cache",
	"buildkite-plugins/docker-login":                      "docker-login",
	"buildkite-plugins/ecr":                               "ecr",
	"buildkite-plugins/junit-annotate":                    "junit-annotate",
	"buildkite-plugins/test-collector":                    "test-collector",
	"buildkite-plugins/secrets":                           "secrets",
	"buildkite-plugins/vault-secrets":                     "vault-secrets",
	"buildkite-plugins/aws-assume-role-with-web-identity": "aws-assume-role-with-web-identity",
	"seek-oss/aws-sm":                                     "aws-sm",
	"bugcrowd/test-summary":                               "test-summary",
}

// buildkitePluginName normalizes a plugin reference to organization/name:
// docker#v5.12.0, buildkite-plugins/docker-buildkite-plugin#v5 and
// https://github.com/buildkite-plugins/docker-buildkite-plugin.git#v5 are
// all buildkite-plugins/docker. Local plugins (./path) keep their path.
func buildkitePluginName(reference string) string {
	name, _, _ := strings.Cut(strings.TrimSpace(reference), "#")
	if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "/") {
		return name
	}
	if _, rest, ok := strings.Cut(name, "://"); ok {
		name = rest
		if _, after, found := strings.Cut(name, "/"); found {
			name = after
		}
	} else if _, rest, ok := strings.Cut(name, ":"); ok && strings.Contains(name, "@") {
		name = rest // git@github.com:org/repo
	}
	name = strings.TrimSuffix(strings.TrimSuffix(name, "/"), ".git")
	segments := strings.Split(name, "/")
	if len(segments) > 2 {
		segments = segments[len(segments)-2:]
	}
	if len(segments) == 1 {
		segments = []string{"buildkite-plugins", segments[0]}
	}
	segments[1] = strings.TrimSuffix(segments[1], "-buildkite-plugin")
	return segments[0] + "/" + segments[1]
}

// buildkitePlugin is one entry of a step's plugins.
type buildkitePlugin struct {
	reference string
	config    *yaml.Node
}

// buildkitePlugins lists a step's plugins: a list of names or
// name-to-configuration mappings, or one such mapping.
func buildkitePlugins(node *yaml.Node) ([]buildkitePlugin, bool) {
	node = gitLabAlias(node)
	if node == nil {
		return nil, true
	}
	entries := []*yaml.Node{node}
	if node.Kind == yaml.SequenceNode {
		entries = node.Content
	}
	var plugins []buildkitePlugin
	for _, entry := range entries {
		entry = gitLabAlias(entry)
		switch {
		case entry == nil:
		case entry.Kind == yaml.ScalarNode:
			plugins = append(plugins, buildkitePlugin{reference: entry.Value})
		case entry.Kind == yaml.MappingNode:
			for i := 0; i+1 < len(entry.Content); i += 2 {
				plugins = append(plugins, buildkitePlugin{reference: entry.Content[i].Value, config: gitLabAlias(entry.Content[i+1])})
			}
		default:
			return nil, false
		}
	}
	return plugins, true
}

// executor reads where a step runs its commands from its plugins and the
// image attribute; matrix substitutes the step's combination.
func (c *buildkiteConfig) executor(fields map[string]*yaml.Node, scope buildkiteScope, matrix func(string) (string, string)) *buildkiteExecutor {
	executor := &buildkiteExecutor{shell: "bash"}
	// A value pipeline upload leaves reading a variable ($$NAME) is never
	// expanded by a plugin either.
	value := func(node *yaml.Node) (string, bool) {
		text, problem := matrix(gitLabScalar(node))
		text, unknown := buildkiteInterpolate(text, scope.known)
		return text, problem == "" && len(unknown) == 0 && !strings.Contains(text, "$")
	}
	if image := gitLabScalar(fields["image"]); image != "" || scope.image != "" {
		executor.template = image
		if image == "" {
			executor.template = scope.image
		}
		var resolved bool
		executor.image, resolved = value(&yaml.Node{Kind: yaml.ScalarNode, Value: executor.template})
		if !resolved {
			executor.imageProblem = "its image " + executor.template + " depends on a variable or matrix value ddtest cannot resolve"
		}
	}
	plugins, ok := buildkitePlugins(fields["plugins"])
	if !ok {
		executor.problem = "its plugins are not a list ddtest can read"
		return executor
	}
	for _, plugin := range plugins {
		reference, resolved := value(&yaml.Node{Kind: yaml.ScalarNode, Value: plugin.reference})
		if !resolved {
			executor.problem = joinDiagnostic(executor.problem, "the plugin "+plugin.reference+" is read from a variable ddtest cannot resolve; it may change how the step runs its commands, or run tests itself")
			continue
		}
		name := buildkitePluginName(reference)
		if purpose := buildkiteNonTestPlugins[name]; purpose != "" {
			c.nonTest = append(c.nonTest, purpose)
			continue
		}
		executor.pluginAliased = executor.pluginAliased || c.aliased[plugin.config]
		switch name {
		case "buildkite-plugins/docker":
			c.docker(executor, plugin.config, value)
		case "buildkite-plugins/docker-compose":
			c.compose(executor, plugin.config, value)
		default:
			executor.problem = joinDiagnostic(executor.problem, "the plugin "+reference+" may change how the step runs its commands, or run tests itself, and ddtest cannot read it; validate this step separately")
		}
	}
	return executor
}

// containerOptions reads what docker and docker-compose plugins share: the
// shell running the commands and the environment passed into the
// container.
func (executor *buildkiteExecutor) containerOptions(fields map[string]*yaml.Node, plugin string) {
	executor.container = true
	executor.shell = "sh"
	if shell := gitLabAlias(fields["shell"]); shell != nil {
		words := gitLabStrings(shell)
		switch {
		case shell.Kind == yaml.ScalarNode && shell.Value == "false":
			executor.problem = joinDiagnostic(executor.problem, "the "+plugin+" plugin's shell: false passes the commands to the container without a shell, which ddtest cannot replay")
		case len(words) > 0 && shellProgram(words[0]) == "bash":
			executor.shell = "bash"
		case len(words) > 0 && shellProgram(words[0]) == "sh":
		default:
			executor.problem = joinDiagnostic(executor.problem, "the "+plugin+" plugin runs the commands with "+strings.Join(words, " ")+", which ddtest cannot replay")
		}
	}
	for _, key := range []string{"environment", "env"} {
		for _, item := range gitLabStrings(fields[key]) {
			name, value, assigned := strings.Cut(item, "=")
			if strings.TrimSpace(name) != "DD_API_KEY" {
				continue
			}
			if assigned && value != "" {
				executor.keyProblem = true
			} else {
				executor.keyPassed = true
			}
		}
	}
}

// docker reads the docker plugin: commands run in image, with the checkout
// mounted at its working directory.
func (c *buildkiteConfig) docker(executor *buildkiteExecutor, config *yaml.Node, value func(*yaml.Node) (string, bool)) {
	fields := gitLabMembers(config)
	executor.plugin = config
	executor.containerOptions(fields, "docker")
	executor.template = gitLabScalar(fields["image"])
	image, resolved := value(fields["image"])
	executor.image, executor.imageProblem = image, ""
	switch {
	case executor.template == "":
		executor.imageProblem = "its docker plugin names no image"
	case !resolved:
		executor.imageProblem = "its image " + executor.template + " depends on a variable or matrix value ddtest cannot resolve"
	}
	if entrypoint := gitLabScalar(fields["entrypoint"]); entrypoint != "" {
		executor.problem = joinDiagnostic(executor.problem, "the docker plugin's entrypoint "+entrypoint+" receives the commands, and ddtest cannot tell how it runs them")
	}
	if mount := gitLabScalar(fields["mount-checkout"]); mount == "false" {
		executor.problem = joinDiagnostic(executor.problem, "the docker plugin does not mount the checkout, so the commands run on files ddtest cannot map")
	}
}

// compose reads the docker-compose plugin: commands run in the run
// service's container, whose image comes from the Compose file.
func (c *buildkiteConfig) compose(executor *buildkiteExecutor, config *yaml.Node, value func(*yaml.Node) (string, bool)) {
	fields := gitLabMembers(config)
	executor.plugin = config
	service, _ := value(fields["run"])
	executor.compose, executor.composeRun = service, service != ""
	if service == "" {
		for _, key := range []string{"build", "push"} {
			if names := gitLabStrings(fields[key]); len(names) > 0 {
				executor.compose = strings.Join(names, ", ")
			}
		}
		executor.container = executor.compose != ""
		return
	}
	executor.containerOptions(fields, "docker-compose")
	files := gitLabStrings(fields["config"])
	if len(files) == 0 {
		files = []string{"docker-compose.yml"}
	}
	executor.image, executor.template = "", ""
	if len(files) > 1 {
		executor.imageProblem = "its Docker Compose service " + service + " comes from several Compose files, which ddtest does not merge"
		return
	}
	file := path.Clean(files[0])
	info, ok := repositoryFile(c.root, c.root, file)
	if !ok || info.IsDir() || info.Size() > buildkiteSizeLimit {
		executor.imageProblem = "its Compose file " + file + " is not a file in the repository"
		return
	}
	data, err := os.ReadFile(filepath.Join(c.root, filepath.FromSlash(file)))
	var document yaml.Node
	if err == nil {
		err = yaml.Unmarshal(data, &document)
	}
	if err != nil || len(document.Content) == 0 {
		executor.imageProblem = "its Compose file " + file + " cannot be read"
		return
	}
	c.files = append(c.files, file)
	definition := gitLabMembers(gitLabMembers(gitLabMembers(document.Content[0])["services"])[service])
	if len(definition) == 0 {
		executor.imageProblem = "its Compose file " + file + " defines no service " + service
		return
	}
	if build := gitLabAlias(definition["build"]); build != nil {
		context := gitLabScalar(build)
		if context == "" {
			context = gitLabScalar(gitLabMembers(build)["context"])
		}
		executor.imageProblem = "its Docker Compose service " + service + " is built from " + strings.TrimSpace(context+" ") + "with a Dockerfile ddtest does not read"
	} else {
		executor.template = gitLabScalar(definition["image"])
		image, resolved := value(&yaml.Node{Kind: yaml.ScalarNode, Value: buildkiteComposeDefaults(executor.template)})
		executor.image = image
		if !resolved || strings.Contains(image, "$") {
			executor.imageProblem = "its Docker Compose service " + service + " image " + executor.template + " depends on a variable ddtest cannot resolve"
		}
	}
	environment := gitLabAlias(definition["environment"])
	if environment != nil && environment.Kind == yaml.MappingNode {
		if key, set := gitLabMembers(environment)["DD_API_KEY"]; set {
			switch text := strings.TrimSpace(gitLabScalar(key)); text {
			case "", "$DD_API_KEY", "${DD_API_KEY}":
				executor.keyPassed = true
			default:
				executor.keyProblem = true
			}
		}
	} else {
		for _, item := range gitLabStrings(environment) {
			name, value, assigned := strings.Cut(item, "=")
			switch {
			case strings.TrimSpace(name) != "DD_API_KEY":
			case assigned && value != "" && value != "$DD_API_KEY" && value != "${DD_API_KEY}":
				executor.keyProblem = true
			default:
				executor.keyPassed = true
			}
		}
	}
	mounted := false
	for _, volume := range gitLabStrings(definition["volumes"]) {
		source, _, _ := strings.Cut(volume, ":")
		mounted = mounted || source == "." || source == "./"
	}
	if !mounted {
		c.review = append(c.review, file+": the Docker Compose service "+service+" does not mount the checkout, so the step runs on the copy of the repository in its image; ddtest validates the checkout.")
	}
}

// buildkiteComposeDefaults resolves ${NAME:-default} in a Compose image to
// its default, as Compose does when NAME is unset.
func buildkiteComposeDefaults(image string) string {
	for {
		start := strings.Index(image, "${")
		if start < 0 {
			return image
		}
		end := strings.Index(image[start:], "}")
		if end < 0 {
			return image
		}
		_, fallback, ok := strings.Cut(image[start+2:start+end], ":-")
		if !ok {
			return image
		}
		image = image[:start] + fallback + image[start+end+1:]
	}
}

// buildkiteCurlInstall is the command that adds curl to a Debian slim image.
const buildkiteCurlInstall = "apt-get update && apt-get install -y --no-install-recommends curl"

// setupProblem explains why the step cannot run the Datadog setup snippet,
// which needs Bash and curl or wget, and DD_API_KEY in the environment.
// curl reports an earlier command that installed curl.
func (executor *buildkiteExecutor) setupProblem(curl bool) string {
	switch {
	case executor.keyProblem:
		return "The pipeline sets DD_API_KEY in its configuration; never commit the key, and pipeline upload bakes interpolated values into the uploaded pipeline: remove it and provide DD_API_KEY from an agent environment hook, a secrets plugin or the pipeline's environment in the Buildkite UI."
	case executor.windows:
		return "The step runs on Windows agents, whose default shell is cmd.exe; the Datadog setup snippet needs a POSIX shell and Bash. Run the tests on Linux or macOS agents."
	case executor.imageProblem != "":
		return "The step's container cannot be checked: " + executor.imageProblem + ", so ddtest cannot tell whether it has the Bash and the curl or wget the Datadog installer needs. Give the step a literal image that has them."
	case imageWithoutBash(executor.image):
		return "The step's image " + executor.image + " has no Bash, which the Datadog installer needs. Use a Bash-capable image for this step, such as the non-Alpine variant, or a custom image with Bash preinstalled."
	case !curl && imageWithoutDownloader(executor.image):
		return "The step's image " + executor.image + " has neither curl nor wget, so the Datadog installer cannot be downloaded and its tests would run untraced. Use the full image (without -slim), or install curl in a command before the setup: " + buildkiteCurlInstall + "."
	case executor.container && !executor.keyPassed:
		return "The step runs its commands in a container, which receives only the environment its plugin lists, so DD_API_KEY never reaches the Datadog setup snippet: add DD_API_KEY to the plugin's environment list."
	}
	return ""
}
