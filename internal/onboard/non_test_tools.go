// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
	"mvdan.cc/sh/v3/syntax"
)

// Tools that read, check or scan a project and never start a test runner:
// code-quality scanners, linters for CI and shell files, and Git hook
// installers. Tools that run other commands (pre-commit, make) are not here.
// Each language adds its own (see discoveryLanguage.checkTool).
var checkAndPackageTools = []string{
	"sonar-scanner", "sonarqube-scanner", "sonar-scanner-cli", "husky",
	"shellcheck", "hadolint", "actionlint", "yamllint", "editorconfig-checker", "typos", "codespell",
}

// Publishing CLIs that only upload what the project built, running at most
// the package's own publish lifecycle scripts. Tools documented to run the
// project's tests, a build command or other hooks, such as np, release-it,
// netlify deploy --build, firebase emulators:exec or changeset publish across
// workspaces, are not among them.
var publishTools = []string{"semantic-release", "gh-pages", "vsce", "ovsx", "surge", "jsr", "pkg-pr-new"}

// Package-manager lifecycle scripts a publication runs in the package, the
// one VS Code's vsce and ovsx run, and the version scripts a release that
// bumps the version (semantic-release, yarn publish) runs.
var publishLifecycle = []string{"prepublishOnly", "prepublish", "prepack", "prepare", "postpack", "publish", "postpublish", "vscode:prepublish", "preversion", "version", "postversion"}

// recognizedTool classifies one command, as words with the executable first,
// that is a tool ddtest knows: handled is false for any other command. For a
// handled command, mayStart reports whether it may start framework's tests;
// it is false only when the tool, as invoked, cannot. directory is the
// command's working directory ("" when unreadable), used to read the files
// the tool would run, such as a Dockerfile or angular.json.
func recognizedTool(root, directory string, words []string, framework string, depth int) (mayStart, handled bool) {
	if len(words) == 0 {
		return false, false
	}
	// A repository path is a script whatever its name, as ./ci/ruff may wrap
	// npm test: callers inspect it as one. A scoped package keeps its name.
	if strings.ContainsAny(strings.Trim(words[0], `"'`), "/\\") && !strings.HasPrefix(words[0], "@") {
		return false, false
	}
	words = slices.Clone(words)
	for i := range words {
		words[i] = strings.Trim(words[i], `"'`)
	}
	name := strings.ToLower(filepath.Base(words[0]))
	args := words[1:]
	switch {
	case slices.Contains(checkAndPackageTools, name):
		return false, true
	case name == "docker" || name == "podman":
		return dockerMayStart(root, directory, args, depth, func(body string) bool {
			return mayStartTestsIn(root, "", runtimeStep{Run: body}, framework, depth+1)
		}), true
	case name == "ng":
		return angularMayStart(root, directory, args, framework), true
	case name == "vue-cli-service":
		return vueCLICommand(args) != "", true
	case publishToolName(words[0]) == "semantic-release":
		return semanticReleaseMayStart(root, directory, args, framework, depth), true
	case publishTool(words[0]):
		return publishMayStart(root, directory, framework, depth), true
	}
	for _, language := range discoveryLanguages {
		if mayStart, handled := language.checkTool(root, directory, words); handled {
			return mayStart, true
		}
	}
	return false, false
}

// publishTool reports a known publish-only CLI by its executable or package
// name. A package merely named publish, release or deploy, such as
// @acme/deploy, has no documented behavior and may start the tests itself,
// so it stays a command ddtest can't classify.
func publishTool(executable string) bool {
	return slices.Contains(publishTools, publishToolName(executable))
}

// publishToolName returns the package name of an executable without its
// scope and pinned version, as semantic-release for semantic-release@24;
// "" for a repository script, whatever its name.
func publishToolName(executable string) string {
	name := strings.ToLower(executable)
	if at := strings.LastIndex(name, "@"); at > 0 {
		name = name[:at] // A pinned version: tool@1.2.3 or @org/tool@1.2.3.
	}
	if strings.HasPrefix(name, "@") {
		_, name, _ = strings.Cut(name, "/")
	} else if strings.ContainsAny(name, "/\\") {
		return ""
	}
	return name
}

// publishMayStart reports whether publishing the package in directory may
// start framework's tests: a publication runs the package's publish lifecycle
// scripts, which are followed like any other step. Unreadable scripts may.
func publishMayStart(root, directory, framework string, depth int) bool {
	if directory == "" || depth >= 8 {
		return true
	}
	data, err := os.ReadFile(filepath.Join(directory, "package.json"))
	if os.IsNotExist(err) {
		return false // Nothing to publish from here runs a lifecycle script.
	}
	var manifest struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err != nil || json.Unmarshal(data, &manifest) != nil {
		return true
	}
	for _, hook := range publishLifecycle {
		if body := manifest.Scripts[hook]; body != "" && mayStartTestsIn(root, directory, runtimeStep{Run: body}, framework, depth+1) {
			return true
		}
	}
	return false
}

// Options of npm, pnpm, Yarn and Bun publish and pack that take a value, and
// those that take none; neither selects another package.
var (
	publishValueOptions = []string{"--tag", "--access", "--otp", "--registry", "--publish-branch", "--auth-type", "--pack-destination", "--loglevel", "--userconfig", "--new-version", "--message"}
	publishFlags        = []string{"--dry-run", "--provenance", "--no-provenance", "--ignore-scripts", "--no-git-checks", "--force", "--json", "--silent", "--quiet", "--verbose", "--foreground-scripts", "--non-interactive", "--report-summary", "--no-git-tag-version"}
)

// publicationMayStart reports whether a package manager's publish or pack,
// with args after the subcommand, may start framework's tests: through the
// lifecycle scripts of the package it publishes (see publishMayStart). A
// package ddtest can't locate or read may, and so may workspace selections.
func publicationMayStart(root, directory string, args []string, framework string, depth int) bool {
	target, ok := publishTarget(root, directory, args)
	if !ok {
		return true
	}
	return publishMayStart(root, target, framework, depth)
}

// publishTarget returns the package directory a publish or pack invocation,
// with args after the subcommand, publishes: directory, or the literal
// repository folder it names, when that folder has a package.json. ok is
// false for a tarball, a workspace selection, an unknown option or a folder
// ddtest can't read, such as one the build generates.
func publishTarget(root, directory string, args []string) (string, bool) {
	if directory == "" {
		return "", false
	}
	target := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		option, _, assigned := strings.Cut(arg, "=")
		switch {
		case arg == "--":
			return "", false
		case !strings.HasPrefix(arg, "-"):
			if target != "" {
				return "", false
			}
			target = arg
		case slices.Contains(publishValueOptions, option):
			if !assigned {
				i++
			}
		case slices.Contains(publishFlags, option):
		default:
			return "", false // A workspace selection or an option ddtest doesn't know.
		}
	}
	if target == "" || target == "." {
		return directory, true
	}
	if strings.HasSuffix(target, ".tgz") || strings.HasSuffix(target, ".tar.gz") {
		return "", false
	}
	resolved, err := repositoryDirectory(root, directory, target)
	if err != nil {
		return "", false
	}
	if _, err := os.Stat(filepath.Join(resolved, "package.json")); err != nil {
		return "", false // A generated package, or none, is read separately.
	}
	return resolved, true
}

// semantic-release's options that select no plugin or configuration, and
// those of them that take a value.
var (
	semanticReleaseFlags        = []string{"--dry-run", "-d", "--ci", "--no-ci", "--debug"}
	semanticReleaseValueOptions = []string{"-b", "--branches", "-r", "--repository-url", "-t", "--tag-format"}
)

// The plugins semantic-release runs when its configuration names none.
var semanticReleaseDefaultPlugins = []any{"@semantic-release/commit-analyzer", "@semantic-release/release-notes-generator", "@semantic-release/npm", "@semantic-release/github"}

// Official plugins that analyze commits, write notes or a changelog, or call
// a forge's API, running no project command. Git's commits are classified
// like a step's git commands.
var semanticReleaseInertPlugins = []string{"@semantic-release/commit-analyzer", "@semantic-release/release-notes-generator", "@semantic-release/changelog", "@semantic-release/github", "@semantic-release/gitlab", "@semantic-release/git"}

// Where semantic-release looks for its configuration, in order, in a
// directory and then its ancestors.
var semanticReleaseConfigurations = []string{"package.json", ".releaserc", ".releaserc.json", ".releaserc.yaml", ".releaserc.yml", ".releaserc.js", ".releaserc.cjs", ".releaserc.mjs", ".releaserc.ts", ".releaserc.cts", ".releaserc.mts", "release.config.js", "release.config.cjs", "release.config.mjs", "release.config.ts", "release.config.cts", "release.config.mts"}

// semanticReleaseMayStart reports whether semantic-release, run in directory
// with args, may start framework's tests. Its plugins run at each release
// step: the exec plugin runs any command, @semantic-release/npm runs npm
// version and npm publish with the package's lifecycle scripts, and any other
// plugin, a shareable configuration or a configuration file in JavaScript is
// code ddtest doesn't read, so it may.
func semanticReleaseMayStart(root, directory string, args []string, framework string, depth int) bool {
	if directory == "" || depth >= 8 {
		return true
	}
	for i := 0; i < len(args); i++ {
		option, _, assigned := strings.Cut(args[i], "=")
		switch {
		case slices.Contains(semanticReleaseFlags, option):
		case slices.Contains(semanticReleaseValueOptions, option):
			if !assigned {
				i++
			}
		default:
			return true // --extends, --plugins or a step's plugins select code ddtest doesn't read.
		}
	}
	configuration, ok := semanticReleaseConfiguration(root, directory)
	if !ok {
		return true
	}
	plugins := semanticReleaseDefaultPlugins
	for key, value := range configuration {
		switch key {
		case "plugins":
			list, isList := value.([]any)
			if !isList {
				return true
			}
			plugins = list
		case "branches", "repositoryUrl", "tagFormat", "dryRun", "ci", "debug":
		default:
			return true // extends, a step's plugins or options ddtest doesn't read.
		}
	}
	for _, plugin := range plugins {
		name, options := plugin, map[string]any{}
		if pair, isPair := plugin.([]any); isPair {
			if len(pair) == 0 || len(pair) > 2 {
				return true
			}
			name = pair[0]
			if len(pair) == 2 {
				if options, ok = pair[1].(map[string]any); !ok {
					return true
				}
			}
		}
		switch name {
		case "@semantic-release/npm":
			if semanticReleaseNPMMayStart(root, directory, options, framework, depth) {
				return true
			}
		default:
			if text, isText := name.(string); !isText || !slices.Contains(semanticReleaseInertPlugins, text) {
				return true
			}
		}
	}
	return false
}

// semanticReleaseNPMMayStart reports whether @semantic-release/npm may start
// framework's tests: it runs npm version and npm publish in its pkgRoot,
// which run that package's lifecycle scripts.
func semanticReleaseNPMMayStart(root, directory string, options map[string]any, framework string, depth int) bool {
	target := directory
	if value, set := options["pkgRoot"]; set {
		pkgRoot, isText := value.(string)
		if !isText {
			return true
		}
		resolved, ok := publishTarget(root, directory, []string{pkgRoot})
		if !ok {
			return true
		}
		target = resolved
	}
	return publishMayStart(root, target, framework, depth)
}

// semanticReleaseConfiguration reads the semantic-release configuration that
// applies in directory: the first one found there or in an ancestor within
// the repository, as JSON or YAML, or the release key of a package.json. An
// empty configuration means none was found. ok is false for one ddtest can't
// read, such as a JavaScript module.
func semanticReleaseConfiguration(root, directory string) (map[string]any, bool) {
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, false
	}
	for current := directory; ; current = filepath.Dir(current) {
		for _, name := range semanticReleaseConfigurations {
			data, err := os.ReadFile(filepath.Join(current, name))
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return nil, false
			}
			var configuration map[string]any
			switch {
			case name == "package.json":
				var manifest map[string]any
				if json.Unmarshal(data, &manifest) != nil {
					return nil, false
				}
				release, set := manifest["release"]
				if !set {
					continue
				}
				object, isObject := release.(map[string]any)
				if !isObject {
					return nil, false
				}
				configuration = object
			case filepath.Ext(name) == "" || slices.Contains([]string{".json", ".yaml", ".yml"}, filepath.Ext(name)):
				if yaml.Unmarshal(data, &configuration) != nil || configuration == nil {
					return nil, false
				}
			default:
				return nil, false // A JavaScript or TypeScript module.
			}
			return configuration, true
		}
		canonical, err := filepath.EvalSymlinks(current)
		if err != nil {
			return nil, false
		}
		if canonical == canonicalRoot || filepath.Dir(current) == current {
			return map[string]any{}, true
		}
		if relative, err := filepath.Rel(canonicalRoot, canonical); err != nil || !filepath.IsLocal(relative) {
			return map[string]any{}, true
		}
	}
}

// Docker subcommands that move, tag, inspect or authenticate images and
// containers; none runs a command in a container.
var dockerInertSubcommands = []string{"push", "tag", "login", "logout", "pull", "images", "rmi", "save", "load", "inspect", "ps", "version", "info", "manifest", "network", "volume", "context", "search", "history", "import", "logs", "stop", "kill", "rm", "port", "top", "stats", "events", "diff"}

// dockerMayStart reports whether a docker (or podman) command may start
// tests. Running a container (run, compose, exec, start) may; moving or
// inspecting images can't; a build may only through the Dockerfile RUN
// instructions it executes, which runMayStart classifies by their command.
func dockerMayStart(root, directory string, args []string, depth int, runMayStart func(string) bool) bool {
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		option := args[0]
		args = args[1:]
		if slices.Contains([]string{"--context", "-c", "-H", "--host", "--config", "-l", "--log-level"}, option) && len(args) > 0 {
			args = args[1:]
		}
	}
	if len(args) == 0 {
		return false // Usage only.
	}
	switch subcommand, rest := args[0], args[1:]; {
	case subcommand == "build":
		return dockerBuildMayStart(root, directory, rest, depth, runMayStart)
	case (subcommand == "buildx" || subcommand == "image" || subcommand == "builder") && len(rest) > 0 && rest[0] == "build":
		return dockerBuildMayStart(root, directory, rest[1:], depth, runMayStart)
	case subcommand == "image" && len(rest) > 0 && slices.Contains([]string{"ls", "list", "rm", "remove", "prune", "tag", "push", "pull", "inspect", "history", "save", "load", "import"}, rest[0]),
		subcommand == "buildx" && len(rest) > 0 && slices.Contains([]string{"create", "use", "inspect", "ls", "rm", "stop", "version", "imagetools", "prune", "du"}, rest[0]):
		return false
	case slices.Contains(dockerInertSubcommands, subcommand):
		return false
	}
	return true
}

// Options of docker build and docker buildx build that take a separate value.
var dockerBuildValueOptions = []string{"-f", "--file", "-t", "--tag", "--build-arg", "--target", "--platform", "--label", "--cache-from", "--cache-to", "--secret", "--ssh", "-o", "--output", "--network", "--progress", "--iidfile", "--add-host", "--build-context", "--cgroup-parent", "--cpu-period", "--cpu-quota", "-c", "--cpu-shares", "--cpuset-cpus", "--cpuset-mems", "-m", "--memory", "--memory-swap", "--shm-size", "--ulimit", "--annotation", "--attest", "--metadata-file", "--builder", "--allow", "--isolation", "--security-opt", "--call"}

// Options of docker build that take no value.
var dockerBuildFlags = []string{"--load", "--push", "--pull", "--no-cache", "-q", "--quiet", "--rm", "--force-rm", "--squash", "--compress", "--check", "--disable-content-trust", "--debug"}

// dockerBuildMayStart reports whether a docker build may start framework's
// tests: whether any RUN instruction of the Dockerfile it builds may. A
// Dockerfile ddtest cannot locate or read, a remote context, a custom
// frontend or a heredoc may.
func dockerBuildMayStart(root, directory string, args []string, depth int, runMayStart func(string) bool) bool {
	if directory == "" || depth >= 8 {
		return true
	}
	dockerfile, context := "", ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		option, value, assigned := strings.Cut(arg, "=")
		switch {
		case !strings.HasPrefix(arg, "-") || arg == "-":
			if context != "" {
				return true
			}
			context = arg
		case slices.Contains(dockerBuildValueOptions, option):
			if !assigned {
				if i+1 >= len(args) {
					return true
				}
				i++
				value = args[i]
			}
			if option == "-f" || option == "--file" {
				dockerfile = value
			}
		case strings.HasPrefix(arg, "-f") && len(arg) > 2:
			dockerfile = arg[2:]
		case slices.Contains(dockerBuildFlags, option):
		default:
			return true // An option ddtest doesn't know may take a value.
		}
	}
	if context == "" || context == "-" || strings.ContainsAny(context+dockerfile, "$`~") || strings.Contains(context, "://") || strings.HasPrefix(context, "git@") || dockerfile == "-" {
		return true
	}
	if dockerfile == "" {
		dockerfile = filepath.Join(context, "Dockerfile")
	}
	if info, ok := repositoryFile(root, directory, dockerfile); !ok || info.Size() > 1<<20 {
		return true
	}
	data, err := os.ReadFile(filepath.Join(directory, dockerfile))
	if err != nil {
		return true
	}
	return dockerfileMayStart(string(data), runMayStart)
}

var dockerSyntaxDirective = regexp.MustCompile(`(?i)^#\s*syntax\s*=\s*(\S+)`)
var dockerEscapeDirective = regexp.MustCompile(`(?i)^#\s*escape\s*=`)

// dockerfileMayStart reports whether any RUN instruction of a Dockerfile may
// start tests, as runMayStart classifies its command. RUN commands execute
// inside the image, whose package scripts ddtest doesn't read, so each is
// classified by its words alone: any package script may be a wrapper.
func dockerfileMayStart(text string, runMayStart func(string) bool) bool {
	// Spaces or tabs separate an instruction from its arguments.
	split := func(line string) (string, string) {
		if index := strings.IndexAny(line, " \t"); index >= 0 {
			return line[:index], strings.TrimSpace(line[index+1:])
		}
		return line, ""
	}
	var instruction strings.Builder
	flush := func() bool {
		keyword, body := split(strings.TrimSpace(instruction.String()))
		instruction.Reset()
		keyword = strings.ToUpper(keyword)
		if keyword == "ONBUILD" {
			keyword, body = split(body)
			keyword = strings.ToUpper(keyword)
		}
		if keyword != "RUN" {
			return false
		}
		for strings.HasPrefix(body, "--") {
			_, body = split(body)
		}
		if strings.Contains(body, "<<") {
			return true // A heredoc body ddtest doesn't read.
		}
		if strings.HasPrefix(body, "[") {
			var argv []string
			if json.Unmarshal([]byte(body), &argv) != nil {
				return true
			}
			body = strings.Join(argv, " ")
		}
		return runMayStart(body)
	}
	for line := range strings.Lines(text) {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			if instruction.Len() == 0 {
				if match := dockerSyntaxDirective.FindStringSubmatch(trimmed); match != nil && !strings.HasPrefix(strings.TrimPrefix(match[1], "docker.io/"), "docker/dockerfile") {
					return true // A custom frontend builds however it likes.
				}
				if dockerEscapeDirective.MatchString(trimmed) {
					return true
				}
			}
			continue
		}
		if continued, found := strings.CutSuffix(trimmed, "\\"); found {
			instruction.WriteString(continued + " ")
			continue
		}
		instruction.WriteString(trimmed)
		if flush() {
			return true
		}
	}
	return instruction.Len() > 0 && flush()
}

// Vue CLI service commands that build, serve or lint and run no test: its
// own, and @vue/cli-plugin-eslint's lint.
var vueCLICommands = []string{"serve", "build", "inspect", "help", "lint"}

// vueCLICommand returns the command a vue-cli-service invocation, with args
// after the executable, runs when it may start tests: test:unit, test:e2e or
// another plugin's command, which runs code ddtest doesn't read (a third-party
// plugin such as vue-cli-plugin-styleguidist's styleguidist:build). It is ""
// for the commands that can't (see vueCLICommands) and for usage only.
func vueCLICommand(args []string) string {
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if slices.Contains(vueCLICommands, arg) {
			return ""
		}
		return arg
	}
	return ""
}

// Angular CLI command aliases.
var angularAliases = map[string]string{"t": "test", "l": "lint", "b": "build", "s": "serve", "e": "e2e", "g": "generate", "n": "new", "v": "version", "d": "deploy", "dev": "serve"}

// Angular CLI commands that run no architect target and no test.
var angularCommands = []string{"version", "help", "generate", "new", "add", "update", "config", "analytics", "cache", "completion", "doc"}

// Builder packages the Angular team ships, whose builders run only what
// their name says; any other builder is opaque code.
var angularBuilderPackages = []string{"@angular-devkit/build-angular", "@angular/build", "@angular-eslint/builder", "@angular-devkit/architect"}

// angularMayStart reports whether an Angular CLI command may start
// framework's tests. ng <target> runs the target's builder from angular.json:
// it may when the target's configuration names the framework, when its
// builder is opaque, or when ddtest can't find it.
func angularMayStart(root, directory string, args []string, framework string) bool {
	var positional []string
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			positional = append(positional, arg)
		}
	}
	if len(positional) == 0 {
		return false // Usage only.
	}
	command := positional[0]
	if alias, ok := angularAliases[command]; ok {
		command = alias
	}
	if slices.Contains(angularCommands, command) {
		return false
	}
	project, target := "", command
	if command == "run" {
		if len(positional) < 2 {
			return true
		}
		parts := strings.Split(positional[1], ":")
		if len(parts) < 2 {
			return true
		}
		project, target = parts[0], parts[1]
	} else if len(positional) > 1 {
		project = positional[1]
	}
	if directory == "" {
		return true
	}
	workspace, ok := angularWorkspace(root, directory)
	if !ok {
		return true
	}
	found := false
	for name, definition := range workspace {
		if project != "" && name != project {
			continue
		}
		configuration, ok := definition[target]
		if !ok {
			continue
		}
		found = true
		if angularTargetMayStart(configuration, framework) {
			return true
		}
	}
	return !found
}

// angularTargetMayStart reports whether one angular.json target may start
// framework's tests.
func angularTargetMayStart(configuration json.RawMessage, framework string) bool {
	var target struct {
		Builder string `json:"builder"`
		Options struct {
			Runner string `json:"runner"`
		} `json:"options"`
	}
	if json.Unmarshal(configuration, &target) != nil {
		return true
	}
	text := strings.ToLower(string(configuration))
	if strings.Contains(text, framework) || slices.ContainsFunc(frameworkExecutables[framework], func(executable string) bool { return strings.Contains(text, executable) }) {
		return true
	}
	builderPackage, builder, _ := strings.Cut(target.Builder, ":")
	if !slices.Contains(angularBuilderPackages, builderPackage) {
		return true
	}
	// The unit-test builder runs Vitest unless the target selects Karma.
	return builder == "unit-test" && framework == "vitest" && target.Options.Runner != "karma"
}

// angularWorkspace reads the targets of every project of the angular.json in
// directory or its closest ancestor in the repository, by project and target.
func angularWorkspace(root, directory string) (map[string]map[string]json.RawMessage, bool) {
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, false
	}
	for current := directory; ; current = filepath.Dir(current) {
		for _, name := range []string{"angular.json", ".angular.json"} {
			data, err := os.ReadFile(filepath.Join(current, name))
			if os.IsNotExist(err) {
				continue
			}
			var workspace struct {
				Projects map[string]struct {
					Architect map[string]json.RawMessage `json:"architect"`
					Targets   map[string]json.RawMessage `json:"targets"`
				} `json:"projects"`
			}
			if err != nil || json.Unmarshal(data, &workspace) != nil {
				return nil, false
			}
			projects := map[string]map[string]json.RawMessage{}
			for name, project := range workspace.Projects {
				targets := project.Architect
				if targets == nil {
					targets = project.Targets
				}
				projects[name] = targets
			}
			return projects, true
		}
		canonical, err := filepath.EvalSymlinks(current)
		if err != nil || canonical == canonicalRoot || filepath.Dir(current) == current {
			return nil, false
		}
		if relative, err := filepath.Rel(canonicalRoot, canonical); err != nil || !filepath.IsLocal(relative) {
			return nil, false
		}
	}
}

var shellShebang = regexp.MustCompile(`\b(?:ba|da|z)?sh\b`)

// repositoryScriptMayStart reports whether a repository script that a step
// runs, directly or through an interpreter, may start framework's tests:
// Node and TypeScript scripts are inspected with their local imports (see
// nodeScriptMayStart), shell scripts are followed like a step. A script
// ddtest cannot read, outside the repository or of another kind may.
func repositoryScriptMayStart(root, directory, path, framework string, depth int) bool {
	if directory == "" || depth >= 8 || strings.ContainsAny(path, "$`*?~") || filepath.IsAbs(path) {
		return true
	}
	info, ok := repositoryFile(root, directory, path)
	if !ok || info.IsDir() || info.Size() > 1<<20 {
		return true
	}
	switch filepath.Ext(path) {
	case ".js", ".cjs", ".mjs", ".ts", ".cts", ".mts":
		return nodeScriptMayStart(root, directory, filepath.Join(directory, path), framework)
	}
	text, ok := repositoryShellScript(root, directory, path)
	return !ok || mayStartTestsIn(root, directory, runtimeStep{Run: text}, framework, depth+1)
}

// repositoryShellScript reads a repository shell script a command in
// directory names by path: ok is false for a path ddtest can't resolve
// statically, outside the repository or too large, and for a script of
// another kind or interpreter.
func repositoryShellScript(root, directory, path string) (string, bool) {
	if directory == "" || strings.ContainsAny(path, "$`*?~") || filepath.IsAbs(path) || !slices.Contains([]string{".sh", ".bash", ""}, filepath.Ext(path)) {
		return "", false
	}
	info, ok := repositoryFile(root, directory, path)
	if !ok || info.IsDir() || info.Size() > 1<<20 {
		return "", false
	}
	data, err := os.ReadFile(filepath.Join(directory, path))
	if err != nil {
		return "", false
	}
	text := string(data)
	if first, _, _ := strings.Cut(text, "\n"); strings.HasPrefix(first, "#!") && !shellShebang.MatchString(first) {
		return "", false // Another interpreter's script.
	}
	return text, true
}

// repositoryRelativeCommands writes the paths of a job's command text that
// start at the checkout, such as $CI_PROJECT_DIR/scripts/check.sh or
// CircleCI's /root/project/scripts/check.sh (see runtimeJob.Checkout), as
// paths relative to directory, the repository directory the commands run
// in, so the repository scripts they run are read as ./scripts/check.sh is.
// A checkout path must be a whole word or the start of one, followed by /.
func repositoryRelativeCommands(text string, checkout []string, directory string) string {
	if len(checkout) == 0 {
		return text
	}
	up, err := filepath.Rel(filepath.Clean(directory), ".")
	if err != nil || !filepath.IsLocal(filepath.Clean(directory)) {
		return text
	}
	quoted := make([]string, 0, len(checkout))
	for _, prefix := range slices.SortedFunc(slices.Values(checkout), func(a, b string) int { return len(b) - len(a) }) {
		quoted = append(quoted, regexp.QuoteMeta(prefix))
	}
	const delimiters = "\\s;&|()<>\"'`="
	pattern := regexp.MustCompile(`(^|[` + delimiters + `])(` + strings.Join(quoted, "|") + `)(/|$|[` + delimiters + `])`)
	// A delimiter ends one match and starts the next: replace until none is
	// left.
	for range 64 {
		replaced := pattern.ReplaceAllString(text, "${1}"+filepath.ToSlash(up)+"${3}")
		if replaced == text {
			break
		}
		text = replaced
	}
	return text
}

// shellCommandClassifier reports whether one simple command of a shell
// script may start tests: call is the command, and start the index of its
// executable after the wrappers shellScriptMayStart reads through. handled
// is false for a repository path it leaves to shellScriptMayStart.
type shellCommandClassifier func(call *syntax.CallExpr, start int) (mayStart, handled bool)

// shellScriptMayStart reports whether shell text, run in directory ("" when
// unreadable) with args, may start tests. It reads what the text runs, in
// source order: the bodies of the functions it defines, which a call by name
// runs, and the repository scripts it sources or runs, followed with the
// literal arguments they get ("$@" in a script dispatching to one of its
// functions, as ./scripts/build.sh push_image, runs that function). Any
// other command is classify's to decide. An expansion that chooses a
// command, eval, a shell reading stdin, a directory change before a
// repository script, or a script ddtest can't read may.
func shellScriptMayStart(root, directory, text string, args []string, depth int, classify shellCommandClassifier) bool {
	return readShellScript(root, directory, text, args, depth, classify, map[string]bool{})
}

// readShellScript is shellScriptMayStart, with functions holding the
// functions the text, or a script it sourced, defines.
func readShellScript(root, directory, text string, args []string, depth int, classify shellCommandClassifier, functions map[string]bool) bool {
	if depth >= 8 {
		return true
	}
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(text), "")
	if err != nil {
		return true
	}
	// A shell reading a downloaded installer from a pipe, as in
	// curl -s https://get.sdkman.io | bash, runs no repository code.
	installers := map[*syntax.CallExpr]bool{}
	syntax.Walk(file, func(node syntax.Node) bool {
		switch node := node.(type) {
		case *syntax.FuncDecl:
			if node.Name != nil {
				functions[node.Name.Value] = true
			}
		case *syntax.BinaryCmd:
			if from, to := pipeCall(node.X), pipeCall(node.Y); node.Op == syntax.Pipe && from != nil && to != nil && slices.Contains([]string{"curl", "wget"}, shellLiteral(from.Args[0])) {
				installers[to] = true
			}
		}
		return true
	})
	moved, may := false, false
	syntax.Walk(file, func(node syntax.Node) bool {
		call, ok := node.(*syntax.CallExpr)
		if may || !ok || len(call.Args) == 0 {
			return !may
		}
		start := 0
		for start < len(call.Args) && slices.Contains([]string{"env", "time", "nohup", "sudo", "exec", "command", "builtin"}, shellLiteral(call.Args[start])) {
			start++
			for start < len(call.Args) && (strings.HasPrefix(shellLiteral(call.Args[start]), "-") || strings.Contains(shellLiteral(call.Args[start]), "=")) {
				start++
			}
		}
		if start == len(call.Args) {
			return true
		}
		name, literal := shellLiteralWord(call.Args[start])
		var rest []string
		restLiteral := true
		for _, word := range call.Args[start+1:] {
			value, ok := shellLiteralWord(word)
			rest = append(rest, value)
			restLiteral = restLiteral && ok
		}
		switch {
		case !literal:
			// "$@" runs the script's arguments: one of its functions, or nothing.
			positional := shellPositional(call.Args[start])
			may = !positional || len(args) > 0 && !functions[args[0]]
		case functions[name]:
		case slices.Contains([]string{"cd", "pushd", "popd"}, name):
			moved = true
		case name == "eval":
			may = true
		case (name == "source" || name == ".") && len(rest) > 0 && filepath.IsAbs(rest[0]):
			// A file the runner provides, outside the checkout, as SDKMAN's
			// init script.
		case name == "source" || name == ".":
			script, ok := "", len(rest) > 0 && restLiteral && !moved
			if ok {
				script, ok = repositoryShellScript(root, directory, rest[0])
			}
			may = !ok || readShellScript(root, directory, script, rest[1:], depth+1, classify, functions)
		case slices.Contains([]string{"bash", "sh", "zsh", "dash"}, name):
			switch {
			case len(rest) > 1 && rest[0] == "-c" && restLiteral:
				may = readShellScript(root, directory, rest[1], nil, depth+1, classify, map[string]bool{})
			case len(rest) > 0 && !strings.HasPrefix(rest[0], "-") && restLiteral && !moved:
				script, ok := repositoryShellScript(root, directory, rest[0])
				may = !ok || readShellScript(root, directory, script, rest[1:], depth+1, classify, map[string]bool{})
			case installers[call] && !slices.ContainsFunc(rest, func(arg string) bool { return !strings.HasPrefix(arg, "-") }):
			default:
				may = true // A shell reading stdin or options ddtest doesn't read.
			}
		default:
			var handled bool
			if may, handled = classify(call, start); handled {
				break
			}
			script, ok := repositoryShellScript(root, directory, name)
			if !restLiteral {
				rest = nil // "$@" then holds values ddtest doesn't read.
			}
			may = moved || !ok || readShellScript(root, directory, script, rest, depth+1, classify, map[string]bool{}) || !restLiteral && strings.Contains(script, "$@")
		}
		return !may
	})
	return may
}

// shellLiteralWord is a word's text when it is wholly literal, quoted or not.
func shellLiteralWord(word *syntax.Word) (string, bool) {
	var text strings.Builder
	for _, part := range word.Parts {
		switch part := part.(type) {
		case *syntax.Lit:
			text.WriteString(part.Value)
		case *syntax.SglQuoted:
			text.WriteString(part.Value)
		case *syntax.DblQuoted:
			for _, inner := range part.Parts {
				lit, ok := inner.(*syntax.Lit)
				if !ok {
					return "", false
				}
				text.WriteString(lit.Value)
			}
		default:
			return "", false
		}
	}
	return text.String(), true
}

// shellLiteral is shellLiteralWord's text, "" for a word with expansions.
func shellLiteral(word *syntax.Word) string {
	text, _ := shellLiteralWord(word)
	return text
}

// shellPositional reports a word that is only "$@" (or $@), the arguments a
// script or function was called with.
func shellPositional(word *syntax.Word) bool {
	parts := word.Parts
	if len(parts) == 1 {
		if quoted, ok := parts[0].(*syntax.DblQuoted); ok {
			parts = quoted.Parts
		}
	}
	if len(parts) != 1 {
		return false
	}
	expansion, ok := parts[0].(*syntax.ParamExp)
	return ok && expansion.Param != nil && expansion.Param.Value == "@" && !expansion.Length && expansion.Exp == nil && expansion.Slice == nil && expansion.Repl == nil && expansion.Index == nil
}
