// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
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
	// keyProblem a DD_API_KEY written into the configuration; keyEmpty one
	// set to an empty value, which replaces the agent's key.
	keyPassed, keyProblem, keyEmpty bool
	windows                         bool
	// plugin is the docker or docker-compose plugin's configuration, for
	// edits that pass DD_API_KEY into the container; pluginAliased reports
	// one some step reaches through a YAML alias.
	plugin        *yaml.Node
	pluginAliased bool
	// env is the environment the container receives: the variables its
	// plugin and Compose service list. propagate reports a plugin that also
	// passes the step's pipeline environment (propagate-environment).
	env       map[string]string
	propagate bool
	// directory is where the commands start, relative to the checkout.
	directory string
	// dockerfile names the Dockerfile the image is built from; bash and
	// downloader report its RUN instructions installing Bash, and curl or
	// wget, on top of image.
	dockerfile       string
	bash, downloader bool
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
func (c *buildkiteConfig) executor(fields map[string]*yaml.Node, scope buildkiteScope, matrix func(string) (string, string), env map[string]string) *buildkiteExecutor {
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
			c.docker(executor, plugin.config, value, env)
		case "buildkite-plugins/docker-compose":
			c.compose(executor, plugin.config, value, env)
		default:
			executor.problem = joinDiagnostic(executor.problem, "the plugin "+reference+" may change how the step runs its commands, or run tests itself, and ddtest cannot read it; validate this step separately")
		}
	}
	return executor
}

// containerOptions reads what docker and docker-compose plugins share: the
// shell running the commands and the environment passed into the
// container.
func (executor *buildkiteExecutor) containerOptions(fields map[string]*yaml.Node, plugin string, value func(*yaml.Node) (string, bool), host map[string]string) {
	executor.container = true
	executor.shell = "sh"
	if executor.env == nil {
		executor.env = map[string]string{}
	}
	// It passes only what $BUILDKITE_ENV_FILE holds: the pipeline's env and
	// the one set in the Buildkite UI, never what agent hooks export.
	executor.propagate = executor.propagate || gitLabScalar(fields["propagate-environment"]) == "true"
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
			name, written, assigned := strings.Cut(item, "=")
			name = strings.TrimSpace(name)
			if name != "DD_API_KEY" {
				executor.containerVariable(name, written, assigned, value, host)
				continue
			}
			// Only a bare name passes the agent's value through; KEY= passes
			// an empty value and KEY=value writes the key.
			switch {
			case !assigned:
				executor.keyPassed = true
			case written == "":
				executor.keyEmpty = true
			default:
				executor.keyProblem = true
			}
		}
	}
}

// containerVariable records one variable a plugin lists for its container:
// NAME=value as written (pipeline upload interpolates it), a bare NAME with
// the step's value when the pipeline sets it. A value ddtest cannot know
// stays unresolved.
func (executor *buildkiteExecutor) containerVariable(name, written string, assigned bool, value func(*yaml.Node) (string, bool), host map[string]string) {
	if name == "" {
		return
	}
	if !assigned {
		if known, ok := host[name]; ok {
			executor.env[name] = known
		}
		return
	}
	resolved, ok := value(&yaml.Node{Kind: yaml.ScalarNode, Value: written})
	if !ok {
		resolved = unresolvedCIValue(written)
	}
	executor.env[name] = resolved
}

// buildkitePassthroughEntrypoints run the command they receive unchanged:
// init processes and the official images' entrypoints.
var buildkitePassthroughEntrypoints = [][]string{{"tini", "--"}, {"/sbin/tini", "--"}, {"/usr/bin/tini", "--"}, {"dumb-init", "--"}, {"/usr/bin/dumb-init", "--"}, {"docker-entrypoint.sh"}, {"/usr/local/bin/docker-entrypoint.sh"}, {"env"}, {"/usr/bin/env"}}

// buildkiteEntrypointProblem explains an entrypoint that receives the
// step's commands without running them as given; empty for none, an
// emptied one or a known passthrough.
func buildkiteEntrypointProblem(node *yaml.Node, owner string) string {
	words := gitLabStrings(node)
	if len(words) == 0 || len(words) == 1 && strings.TrimSpace(words[0]) == "" {
		return ""
	}
	if gitLabAlias(node).Kind == yaml.ScalarNode {
		words = strings.Fields(words[0])
	}
	for _, passthrough := range buildkitePassthroughEntrypoints {
		if slices.Equal(words, passthrough) {
			return ""
		}
	}
	return owner + " entrypoint " + strings.Join(words, " ") + " receives the commands, and ddtest cannot tell how it runs them"
}

// docker reads the docker plugin: commands run in image, with the checkout
// mounted at its working directory.
func (c *buildkiteConfig) docker(executor *buildkiteExecutor, config *yaml.Node, value func(*yaml.Node) (string, bool), host map[string]string) {
	fields := gitLabMembers(config)
	executor.plugin = config
	executor.containerOptions(fields, "docker", value, host)
	executor.template = gitLabScalar(fields["image"])
	image, resolved := value(fields["image"])
	executor.image, executor.imageProblem = image, ""
	switch {
	case executor.template == "":
		executor.imageProblem = "its docker plugin names no image"
	case !resolved:
		executor.imageProblem = "its image " + executor.template + " depends on a variable or matrix value ddtest cannot resolve"
	}
	executor.problem = joinDiagnostic(executor.problem, buildkiteEntrypointProblem(fields["entrypoint"], "the docker plugin's"))
	if mount := gitLabScalar(fields["mount-checkout"]); mount == "false" {
		executor.problem = joinDiagnostic(executor.problem, "the docker plugin does not mount the checkout, so the commands run on files ddtest cannot map")
	}
}

// compose reads the docker-compose plugin: commands run in the run
// service's container, whose image comes from the Compose file.
func (c *buildkiteConfig) compose(executor *buildkiteExecutor, config *yaml.Node, value func(*yaml.Node) (string, bool), env map[string]string) {
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
	executor.containerOptions(fields, "docker-compose", value, env)
	executor.problem = joinDiagnostic(executor.problem, buildkiteEntrypointProblem(fields["entrypoint"], "the docker-compose plugin's"))
	var files []string
	for _, written := range gitLabStrings(fields["config"]) {
		file, resolved := value(&yaml.Node{Kind: yaml.ScalarNode, Value: written})
		if !resolved {
			executor.imageProblem = "its Compose file " + written + " depends on a variable or matrix value ddtest cannot resolve"
			return
		}
		files = append(files, file)
	}
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
	var built buildkiteDockerfile
	var context string
	if build := gitLabAlias(definition["build"]); build != nil {
		context, built = c.composeBuild(executor, file, service, build, fields, env)
	} else {
		executor.template = gitLabScalar(definition["image"])
		image, resolved := buildkiteComposeImage(executor.template, env)
		executor.image = image
		if !resolved {
			executor.image = ""
			executor.imageProblem = "its Docker Compose service " + service + " image " + executor.template + " depends on a variable the step's env does not set, which the agent or a .env file may supply"
		}
	}
	executor.problem = joinDiagnostic(executor.problem, buildkiteEntrypointProblem(definition["entrypoint"], "the Docker Compose service "+service+"'s"))
	// The service's environment, under what the plugin passes with -e.
	listed := executor.env
	executor.env = map[string]string{}
	environment := gitLabAlias(definition["environment"])
	if environment != nil && environment.Kind == yaml.MappingNode {
		for _, name := range gitLabKeys(environment) {
			node := gitLabAlias(gitLabMembers(environment)[name])
			text := strings.TrimSpace(gitLabScalar(node))
			if name == "DD_API_KEY" {
				// A null value passes the variable through; "" sets it empty.
				switch {
				case node == nil || node.Tag == "!!null", text == "$DD_API_KEY", text == "${DD_API_KEY}":
					executor.keyPassed = true
				case text == "":
					executor.keyEmpty = true
				default:
					executor.keyProblem = true
				}
				continue
			}
			c.composeVariable(executor, name, gitLabScalar(node), node != nil && node.Tag != "!!null", env)
		}
	} else {
		for _, item := range gitLabStrings(environment) {
			name, written, assigned := strings.Cut(item, "=")
			name = strings.TrimSpace(name)
			if name != "DD_API_KEY" {
				c.composeVariable(executor, name, written, assigned, env)
				continue
			}
			switch {
			case !assigned || written == "$DD_API_KEY" || written == "${DD_API_KEY}":
				executor.keyPassed = true
			case written == "":
				executor.keyEmpty = true
			default:
				executor.keyProblem = true
			}
		}
	}
	maps.Copy(executor.env, listed)
	// The commands start in working_dir, or else the Dockerfile's last
	// WORKDIR, mapped back into the checkout through the volume that mounts
	// a repository directory (relative to the Compose file, as Compose
	// reads it) or, without one, the copy of the build context the
	// Dockerfile makes.
	mounts := map[string]string{}
	for _, volume := range gitLabStrings(definition["volumes"]) {
		source, destination, _ := strings.Cut(volume, ":")
		destination, _, _ = strings.Cut(destination, ":")
		if !strings.HasPrefix(source, ".") || destination == "" {
			continue
		}
		if directory, ok := buildkiteRepositoryDirectory(c.root, path.Join(path.Dir(file), source)); ok {
			mounts[path.Clean(destination)] = directory
		}
	}
	mounted := slices.Contains(slices.Collect(maps.Values(mounts)), "")
	if _, taken := mounts[built.contextCopy]; !mounted && !taken && built.contextCopy != "" {
		mounts[built.contextCopy] = context
	}
	directory := gitLabScalar(definition["working_dir"])
	source := "runs the commands in " + directory
	if directory == "" && built.workdirProblem != "" {
		executor.problem = joinDiagnostic(executor.problem, "the Docker Compose service "+service+" sets no working_dir, and "+executor.dockerfile+" "+built.workdirProblem)
	}
	if directory == "" && built.workdir != "" {
		directory, source = built.workdir, "starts in its Dockerfile's WORKDIR "+built.workdir
	}
	if !mounted {
		c.review = append(c.review, file+": the Docker Compose service "+service+" does not mount the checkout, so the step runs on the copy of the repository in its image; ddtest validates the checkout.")
	}
	if directory == "" || len(mounts) == 0 {
		return
	}
	mapped, ok := buildkiteMountedDirectory(mounts, directory)
	switch {
	case ok:
		executor.directory = mapped
	case mounted:
		var destinations []string
		for destination, directory := range mounts {
			if directory == "" {
				destinations = append(destinations, destination)
			}
		}
		slices.Sort(destinations)
		executor.problem = joinDiagnostic(executor.problem, "the Docker Compose service "+service+" "+source+", outside the checkout it mounts at "+strings.Join(destinations, ", "))
	}
}

// buildkiteRepositoryDirectory is a directory of the repository, relative
// to its root ("" for the root itself).
func buildkiteRepositoryDirectory(root, directory string) (string, bool) {
	directory = path.Clean(directory)
	if !filepath.IsLocal(filepath.FromSlash(directory)) && directory != "." {
		return "", false
	}
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(directory)))
	if err != nil || !info.IsDir() {
		return "", false
	}
	if directory == "." {
		return "", true
	}
	return directory, true
}

// buildkiteMountedDirectory maps a container directory back into the
// checkout through the deepest mount containing it.
func buildkiteMountedDirectory(mounts map[string]string, directory string) (string, bool) {
	if strings.Contains(directory, "$") {
		return "", false
	}
	directory = path.Clean(directory)
	best, found := "", false
	for destination := range mounts {
		if (directory == destination || strings.HasPrefix(directory, strings.TrimSuffix(destination, "/")+"/")) && (!found || len(destination) > len(best)) {
			best, found = destination, true
		}
	}
	if !found {
		return "", false
	}
	rest := strings.TrimPrefix(strings.TrimPrefix(directory, best), "/")
	mapped := path.Join(mounts[best], rest)
	if mapped == "." {
		mapped = ""
	}
	return mapped, true
}

// composeBuild reads the Dockerfile a Compose service is built from: its
// image becomes the step's, for the image and static runtime rules. A
// Dockerfile ddtest cannot read, or whose base image it cannot resolve,
// leaves the image unknown with that reason. context is the build context,
// relative to the repository root.
func (c *buildkiteConfig) composeBuild(executor *buildkiteExecutor, file, service string, build *yaml.Node, plugin map[string]*yaml.Node, env map[string]string) (string, buildkiteDockerfile) {
	settings := gitLabMembers(build)
	written := gitLabScalar(build)
	if build.Kind == yaml.MappingNode {
		written = gitLabScalar(settings["context"])
		if written == "" {
			written = "."
		}
	}
	owner := "its Docker Compose service " + service + " is built from "
	context, resolved := buildkiteComposeImage(written, env)
	if !resolved || strings.Contains(context, "://") || strings.HasPrefix(context, "git@") {
		executor.imageProblem = owner + "the build context " + written + ", which ddtest cannot read"
		return "", buildkiteDockerfile{}
	}
	context, ok := buildkiteRepositoryDirectory(c.root, path.Join(path.Dir(file), context))
	if !ok {
		executor.imageProblem = owner + "the build context " + written + ", which is not a directory of the repository"
		return "", buildkiteDockerfile{}
	}
	overridden := map[string]bool{}
	for _, args := range []*yaml.Node{settings["args"], plugin["args"]} {
		args = gitLabAlias(args)
		names := gitLabKeys(args)
		if args != nil && args.Kind != yaml.MappingNode {
			names = nil
			for _, item := range gitLabStrings(args) {
				name, _, _ := strings.Cut(item, "=")
				names = append(names, strings.TrimSpace(name))
			}
		}
		for _, name := range names {
			overridden[name] = true
		}
	}
	target := gitLabScalar(settings["target"])
	var text string
	if inline := gitLabScalar(settings["dockerfile_inline"]); inline != "" {
		executor.dockerfile = "its inline Dockerfile"
		text = inline
	} else {
		dockerfile := gitLabScalar(settings["dockerfile"])
		if dockerfile == "" {
			dockerfile = "Dockerfile"
		}
		name := path.Join(context, dockerfile)
		if path.IsAbs(dockerfile) || strings.Contains(dockerfile, "$") {
			name = dockerfile
		}
		info, ok := repositoryFile(c.root, c.root, name)
		var data []byte
		var err error
		if ok && !info.IsDir() && info.Size() <= buildkiteSizeLimit {
			data, err = os.ReadFile(filepath.Join(c.root, filepath.FromSlash(name)))
		}
		if !ok || err != nil || info.Size() > buildkiteSizeLimit {
			executor.imageProblem = owner + "the Dockerfile " + name + ", which is not a file ddtest can read in the repository"
			return context, buildkiteDockerfile{}
		}
		c.files = append(c.files, name)
		executor.dockerfile = name
		text = string(data)
	}
	built := readBuildkiteDockerfile(text, target, overridden)
	if built.problem != "" {
		executor.imageProblem = owner + executor.dockerfile + ", " + built.problem
		return context, built
	}
	executor.image, executor.template = built.image, built.image
	executor.bash, executor.downloader = built.bash, built.downloader
	return context, built
}

// composeVariable records one variable a Compose service sets: a literal or
// ${NAME} reads of the step's env, a bare name the step's value.
func (c *buildkiteConfig) composeVariable(executor *buildkiteExecutor, name, written string, assigned bool, host map[string]string) {
	if name == "" {
		return
	}
	if !assigned {
		if known, ok := host[name]; ok {
			executor.env[name] = known
		}
		return
	}
	value, resolved := buildkiteComposeImage(written, host)
	if !resolved {
		value = unresolvedCIValue(written)
	}
	executor.env[name] = value
}

// buildkiteComposeVariable matches Compose's $NAME, ${NAME}, ${NAME:-x}
// and ${NAME-x}.
var buildkiteComposeVariable = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?:(:?-)([^}]*))?\}|\$([A-Za-z_][A-Za-z0-9_]*)`)

// buildkiteComposeImage resolves a Compose image with the variables the
// step's env sets, as Compose reads them from the environment it runs in.
// A variable the env does not set comes from the agent or a .env file:
// unresolved, even with a default, since either may supply it.
func buildkiteComposeImage(image string, env map[string]string) (string, bool) {
	resolved := true
	result := buildkiteComposeVariable.ReplaceAllStringFunc(image, func(match string) string {
		parts := buildkiteComposeVariable.FindStringSubmatch(match)
		name := parts[1] + parts[4]
		value, set := env[name]
		if !set || unresolvedCIError(value) != nil {
			resolved = false
			return match
		}
		if value == "" && parts[2] == ":-" {
			return parts[3]
		}
		return value
	})
	return result, resolved && !strings.Contains(result, "$")
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
	case executor.imageSetupProblem(curl) != "":
		return executor.imageSetupProblem(curl)
	case executor.keyEmpty:
		return "The step's container receives DD_API_KEY set to an empty value, which replaces the agent's key, so the Datadog setup snippet always skips: list DD_API_KEY without a value so the agent's key passes through."
	case executor.container && !executor.keyPassed && !executor.propagate:
		return "The step runs its commands in a container, which receives only the environment its plugin lists, so DD_API_KEY never reaches the Datadog setup snippet: add DD_API_KEY to the plugin's environment list."
	}
	return ""
}

// imageSetupProblem explains why the step's container cannot run the
// Datadog installer, which needs Bash and curl or wget: an image ddtest
// cannot resolve, or one known to lack them. curl reports an earlier
// command that installed curl.
func (executor *buildkiteExecutor) imageSetupProblem(curl bool) string {
	switch {
	case executor.imageProblem != "" && executor.dockerfile != "":
		return "The step's container cannot be checked: " + executor.imageProblem + ", so ddtest cannot tell whether the image it builds has the Bash and the curl or wget the Datadog installer needs. Make the Dockerfile's final FROM a literal image that has them, or validate this step's image separately."
	case executor.imageProblem != "":
		return "The step's container cannot be checked: " + executor.imageProblem + ", so ddtest cannot tell whether it has the Bash and the curl or wget the Datadog installer needs. Give the step a literal image that has them."
	case !executor.bash && imageWithoutBash(executor.image) && executor.dockerfile != "":
		return "The step's image is built from " + executor.dockerfile + ", whose final stage is FROM " + executor.image + ", which has no Bash, which the Datadog installer needs. Install bash in the Dockerfile (for Alpine: RUN apk add --no-cache bash), or build FROM the non-Alpine variant."
	case !executor.bash && imageWithoutBash(executor.image):
		return "The step's image " + executor.image + " has no Bash, which the Datadog installer needs. Use a Bash-capable image for this step, such as the non-Alpine variant, or a custom image with Bash preinstalled."
	case !curl && !executor.downloader && imageWithoutDownloader(executor.image) && executor.dockerfile != "":
		return "The step's image is built from " + executor.dockerfile + ", whose final stage is FROM " + executor.image + ", which has neither curl nor wget, and no RUN instruction of the Dockerfile installs them, so the Datadog installer cannot be downloaded and its tests would run untraced. Install curl in the Dockerfile (add curl to its apt-get install packages, or RUN " + buildkiteCurlInstall + "), or build FROM the full image (without -slim)."
	case !curl && !executor.downloader && imageWithoutDownloader(executor.image):
		return "The step's image " + executor.image + " has neither curl nor wget, so the Datadog installer cannot be downloaded and its tests would run untraced. Use the full image (without -slim), or install curl in a command before the setup: " + buildkiteCurlInstall + "."
	}
	return ""
}
