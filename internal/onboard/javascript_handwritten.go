// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"encoding/json"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/environment"
	"go.yaml.in/yaml/v3"
)

// A hand-written dd-trace setup loads the tracer the project depends on
// with NODE_OPTIONS, as dd-trace's documentation shows:
// NODE_OPTIONS="-r dd-trace/ci/init" (with --import dd-trace/register.js for
// ESM tests), with the DD_* configuration in the job's environment.

// handWrittenNodePreload is what a test command's NODE_OPTIONS loads of
// dd-trace.
type handWrittenNodePreload struct {
	// found reports any dd-trace module or variable in NODE_OPTIONS.
	found bool
	// ci and esm report dd-trace/ci/init and dd-trace/register.js.
	ci, esm bool
	// problems are preloads that cannot set up Test Optimization.
	problems []string
	// normalized is NODE_OPTIONS with dd-trace's modules written as the
	// setup action's preload and loader, for the shared bootstrap checks.
	normalized string
	// unresolved is why ddtest cannot read NODE_OPTIONS.
	unresolved string
}

// javascriptHandWrittenModule classifies a module NODE_OPTIONS preloads:
// dd-trace/ci/init, dd-trace/register.js, by package name or through a
// node_modules path.
func javascriptHandWrittenModule(module string) string {
	module = strings.Trim(module, `"'`)
	for _, name := range []string{"ci/init", "ci/init.js", "register.js", "init", "init.js"} {
		if module == "dd-trace/"+name || strings.HasSuffix(module, "node_modules/dd-trace/"+name) {
			return strings.TrimSuffix(name, ".js")
		}
	}
	return ""
}

// javascriptHandWrittenPreload reads the dd-trace preloads of NODE_OPTIONS.
func javascriptHandWrittenPreload(options string) handWrittenNodePreload {
	var preload handWrittenNodePreload
	if strings.Contains(options, "$DD_TRACE_PACKAGE") || strings.Contains(options, "${DD_TRACE_PACKAGE") {
		preload.found = true
		preload.problems = append(preload.problems, "NODE_OPTIONS preloads $DD_TRACE_PACKAGE, a variable only the Datadog GitHub Actions setup exports, so here Node gets -r without a module and fails to start: preload the project's dd-trace instead (-r dd-trace/ci/init).")
		return preload
	}
	if err := unresolvedCIError(options); err != nil || strings.Contains(options, "${{") && !strings.Contains(options, "${{ github.workspace }}") {
		if strings.Contains(options, "dd-trace") || strings.Contains(options, "DD_TRACE_") {
			preload.found = true
			preload.unresolved = "NODE_OPTIONS (" + options + ") holds values only CI knows, so ddtest cannot tell what it preloads."
		}
		return preload
	}
	options = strings.ReplaceAll(options, ciProjectDirectory, ".")
	words, err := environment.SplitNodeOptions(options)
	if err != nil {
		if strings.Contains(options, "dd-trace") {
			preload.found, preload.unresolved = true, "NODE_OPTIONS ("+options+") cannot be parsed: "+err.Error()+"."
		}
		return preload
	}
	var normalized []string
	for i := 0; i < len(words); i++ {
		word := words[i]
		flag, module, inline := strings.Cut(word, "=")
		if strings.HasPrefix(word, "-r") && len(word) > 2 && !inline {
			flag, module, inline = "-r", word[2:], true
		}
		if !slices.Contains([]string{"-r", "--require", "--import"}, flag) {
			normalized = append(normalized, word)
			continue
		}
		if !inline {
			if i+1 >= len(words) {
				normalized = append(normalized, word)
				continue
			}
			i++
			module = words[i]
		}
		if strings.Contains(module, "DD_TRACE_PACKAGE") || strings.Contains(module, "DD_TRACE_ESM_IMPORT") {
			preload.found = true
			preload.problems = append(preload.problems, "NODE_OPTIONS preloads "+module+", a variable only the Datadog GitHub Actions setup exports, so here Node gets "+flag+" without a module and fails to start: preload the project's dd-trace instead ("+flag+" "+map[bool]string{true: "dd-trace/register.js", false: "dd-trace/ci/init"}[flag == "--import"]+").")
			normalized = append(normalized, flag, module)
			continue
		}
		kind := javascriptHandWrittenModule(module)
		switch {
		case kind == "ci/init" && flag != "--import":
			preload.found, preload.ci = true, true
			normalized = append(normalized, flag, "${{ env.DD_TRACE_PACKAGE }}")
		case kind == "register" && flag == "--import":
			preload.found, preload.esm = true, true
			normalized = append(normalized, flag, "${{ env.DD_TRACE_ESM_IMPORT }}")
		case kind != "":
			preload.found = true
			preload.problems = append(preload.problems, "NODE_OPTIONS loads "+flag+" "+module+", which does not set up Test Optimization: preload -r dd-trace/ci/init (and --import dd-trace/register.js for ESM tests).")
			normalized = append(normalized, flag, module)
		default:
			normalized = append(normalized, flag, module)
		}
	}
	if preload.found && !preload.ci && len(preload.problems) == 0 {
		preload.problems = append(preload.problems, "NODE_OPTIONS loads only dd-trace's ESM loader: add -r dd-trace/ci/init, which starts Test Optimization.")
	}
	preload.normalized = environment.JoinNodeOptions(normalized)
	return preload
}

func (javascriptDiscovery) handWrittenSetup(root string, provider ciProvider, workflow ciWorkflow, job runtimeJob, index int, row map[string]any, framework, site string) *handWrittenSetup {
	setup, _ := javascriptHandWrittenSetup(root, provider, workflow, job, index, framework, site)
	return setup
}

// javascriptHandWrittenSetup recognizes a hand-written dd-trace setup of
// the test command at index of job (see handWrittenSetup), and returns the
// command's step with NODE_OPTIONS normalized for the shared bootstrap
// checks, which then apply to it as to the setup action's preload. Cypress
// loads dd-trace through its configuration, which this does not read.
func javascriptHandWrittenSetup(root string, provider ciProvider, workflow ciWorkflow, job runtimeJob, index int, framework, site string) (*handWrittenSetup, runtimeStep) {
	step := job.Steps[index]
	if framework == "cypress" {
		return nil, step
	}
	env := handWrittenEnvironment(workflow, job, step)
	options, set := env["NODE_OPTIONS"]
	if !set {
		return nil, step
	}
	preload := javascriptHandWrittenPreload(options)
	if !preload.found {
		return nil, step
	}
	setup := &handWrittenSetup{Reference: handWrittenReference("dd-trace", "NODE_OPTIONS")}
	for _, problem := range preload.problems {
		setup.missing(problem)
	}
	if preload.unresolved != "" {
		setup.unresolved(preload.unresolved)
	}
	if preload.ci && !preload.esm && (framework == "vitest" || framework == "cucumber") {
		setup.missing("NODE_OPTIONS preloads dd-trace/ci/init without dd-trace's ESM loader, which " + framework + " needs: add --import dd-trace/register.js (Node.js 20.6 or newer, or 18.19 or a later 18 release).")
	}
	normalized := step
	normalized.Env = maps.Clone(step.Env)
	if normalized.Env == nil {
		normalized.Env = map[string]string{}
	}
	normalized.Env["NODE_OPTIONS"] = preload.normalized
	directory := filepath.Clean(filepath.FromSlash(stepDirectory(workflow, job, step)))
	if strings.HasPrefix(directory, "..") || filepath.IsAbs(directory) || strings.Contains(directory, "$") {
		setup.unresolved("the test command runs in " + stepDirectory(workflow, job, step) + ", outside the repository ddtest reads.")
		return setup, normalized
	}
	setup.Tracer, setup.Pin = javascriptHandWrittenTracer(setup, root, directory, workflow, job, index, env)
	if setup.Tracer != "" {
		setup.Reference = handWrittenReference("dd-trace@"+setup.Tracer, "NODE_OPTIONS")
	}
	handWrittenConfiguration(setup, provider, job, env, site, true, false)
	return setup, normalized
}

// javascriptHandWrittenTracer finds the exact dd-trace release a
// hand-written setup loads in directory: one an install command earlier in
// the job adds (npm install dd-trace@5.40.0), otherwise the one the
// package.json that resolves dd-trace there pins, or its lockfile
// resolves. It records on setup why none is pinned.
func javascriptHandWrittenTracer(setup *handWrittenSetup, root, directory string, workflow ciWorkflow, job runtimeJob, index int, env map[string]string) (version, source string) {
	for _, step := range job.Steps[:index] {
		commands, err := staticCommands(step.Run)
		if err != nil {
			continue
		}
		for _, words := range commands {
			release, global, ok := javascriptTracerInstall(words)
			if !ok {
				continue
			}
			where := step.Source
			if where == "" {
				where = "an earlier step"
			}
			switch {
			case global && env["NODE_PATH"] == "":
				setup.missing("The job installs dd-trace globally (" + strings.Join(words, " ") + "), where Node does not resolve -r dd-trace/ci/init: install an exact release in the project instead (npm install --no-save dd-trace@<release>), or add it to package.json as a devDependency.")
				return "", ""
			case global:
				setup.unresolved("the job installs dd-trace globally (" + strings.Join(words, " ") + ") and reads it through NODE_PATH, which ddtest does not follow.")
				return "", ""
			case !exactRelease(release):
				setup.missing("The job installs dd-trace without an exact release (" + strings.Join(words, " ") + "), so each build can load another one: install an exact release, such as dd-trace@5.40.0.")
				return "", ""
			}
			version, source = release, strings.Join(words, " ")+" at "+where
		}
	}
	if version != "" {
		return version, source
	}
	declared, manifest, development := javascriptDeclaredTracer(root, directory)
	switch {
	case manifest == "":
		setup.missing("No package.json in " + displayDirectory(directory) + " or above it depends on dd-trace, and no command of the job installs it, so -r dd-trace/ci/init cannot load: add dd-trace as a devDependency with an exact release (npm install --save-dev --save-exact dd-trace@<release>) and commit the lockfile.")
		return "", ""
	case development && javascriptOmitsDevelopment(job, index, env):
		setup.missing("dd-trace is a devDependency of " + manifest + ", but the job installs production dependencies only, so -r dd-trace/ci/init cannot load: install the devDependencies too, or make dd-trace a dependency.")
		return "", ""
	case exactRelease(strings.TrimPrefix(declared, "=")):
		return strings.TrimPrefix(declared, "="), manifest
	}
	locked, lockfile := javascriptLockedTracer(root, filepath.Dir(filepath.FromSlash(manifest)))
	if locked != "" {
		return locked, lockfile
	}
	setup.missing(manifest + " requires dd-trace " + declared + ", which is not an exact release, and no lockfile pins it, so each build can load another one: pin an exact release (npm install --save-dev --save-exact dd-trace@<release>) or commit the lockfile.")
	return "", ""
}

// displayDirectory names a repository directory in findings.
func displayDirectory(directory string) string {
	if directory == "." || directory == "" {
		return "the checkout root"
	}
	return filepath.ToSlash(directory)
}

// javascriptTracerInstall reads a package manager command that installs
// dd-trace: its requested release (empty without one), and whether it
// installs globally.
func javascriptTracerInstall(words []string) (release string, global, ok bool) {
	if len(words) < 3 || !slices.Contains([]string{"npm", "yarn", "pnpm"}, words[0]) {
		return "", false, false
	}
	arguments := words[1:]
	if words[0] == "yarn" && arguments[0] == "global" {
		global, arguments = true, arguments[1:]
	}
	if len(arguments) == 0 || !slices.Contains([]string{"install", "i", "add"}, arguments[0]) {
		return "", false, false
	}
	for _, word := range arguments[1:] {
		switch {
		case word == "-g" || word == "--global":
			global = true
		case word == "dd-trace":
			ok = true
		case strings.HasPrefix(word, "dd-trace@"):
			ok, release = true, strings.TrimPrefix(word, "dd-trace@")
		}
	}
	return release, global, ok
}

// javascriptDeclaredTracer finds the package.json that resolves dd-trace in
// directory, there or in a parent within the repository: its requested
// release, its repository path and whether only devDependencies has it.
func javascriptDeclaredTracer(root, directory string) (requested, manifest string, development bool) {
	for current := directory; ; current = filepath.Dir(current) {
		data, err := os.ReadFile(filepath.Join(root, current, "package.json"))
		if err == nil {
			var pkg struct {
				Dependencies, DevDependencies, OptionalDependencies map[string]string
			}
			if json.Unmarshal(data, &pkg) == nil {
				for _, dependencies := range []map[string]string{pkg.Dependencies, pkg.OptionalDependencies, pkg.DevDependencies} {
					if version, ok := dependencies["dd-trace"]; ok {
						_, inProduction := pkg.Dependencies["dd-trace"]
						return strings.TrimSpace(version), path.Join(filepath.ToSlash(current), "package.json"), !inProduction
					}
				}
			}
		}
		if current == "." || current == "" {
			return "", "", false
		}
	}
}

// javascriptOmitsDevelopment reports an install command before index in
// job that leaves devDependencies out: --production, --omit=dev or
// NODE_ENV=production.
func javascriptOmitsDevelopment(job runtimeJob, index int, env map[string]string) bool {
	production := strings.TrimSpace(env["NODE_ENV"]) == "production"
	for _, step := range job.Steps[:index] {
		commands, err := staticCommands(step.Run)
		if err != nil {
			continue
		}
		for _, words := range commands {
			if len(words) < 2 || !slices.Contains([]string{"npm", "yarn", "pnpm"}, words[0]) || !slices.Contains([]string{"ci", "install", "i"}, words[1]) {
				continue
			}
			if production || slices.ContainsFunc(words[2:], func(word string) bool {
				return slices.Contains([]string{"--production", "--production=true", "--prod", "--omit=dev", "--only=production", "--only=prod"}, word)
			}) {
				return true
			}
		}
	}
	return false
}

// javascriptLockedTracer is the single dd-trace release the lockfile that
// covers directory resolves, there or in a parent within the repository:
// package-lock.json, npm-shrinkwrap.json, yarn.lock or pnpm-lock.yaml.
func javascriptLockedTracer(root, directory string) (version, lockfile string) {
	for current := directory; ; current = filepath.Dir(current) {
		relative, _ := filepath.Rel(current, directory)
		relative = filepath.ToSlash(relative)
		for _, name := range []string{"npm-shrinkwrap.json", "package-lock.json", "yarn.lock", "pnpm-lock.yaml"} {
			data, err := os.ReadFile(filepath.Join(root, current, name))
			if err != nil {
				continue
			}
			var versions []string
			switch name {
			case "yarn.lock":
				versions = yarnLockedVersions(string(data), "dd-trace")
			case "pnpm-lock.yaml":
				versions = pnpmLockedVersions(data, relative, "dd-trace")
			default:
				versions = npmLockedVersions(data, relative, "dd-trace")
			}
			if len(versions) == 1 && exactRelease(versions[0]) {
				return versions[0], path.Join(filepath.ToSlash(current), name)
			}
			return "", ""
		}
		if current == "." || current == "" {
			return "", ""
		}
	}
}

// npmLockedVersions reads the release of name a package-lock.json resolves
// for the workspace at relative (. for the lockfile's own package).
func npmLockedVersions(data []byte, relative, name string) []string {
	var lock struct {
		Packages     map[string]struct{ Version string }
		Dependencies map[string]struct{ Version string }
	}
	if json.Unmarshal(data, &lock) != nil {
		return nil
	}
	keys := []string{"node_modules/" + name}
	if relative != "." {
		keys = append([]string{relative + "/node_modules/" + name}, keys...)
	}
	for _, key := range keys {
		if entry, ok := lock.Packages[key]; ok {
			return []string{entry.Version}
		}
	}
	if entry, ok := lock.Dependencies[name]; ok {
		return []string{entry.Version}
	}
	return nil
}

// yarnLockedVersions reads the releases of name a yarn.lock (classic or
// Berry) resolves.
func yarnLockedVersions(text, name string) []string {
	var versions []string
	entry := false
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, " ") {
			entry = false
			for _, selector := range strings.Split(strings.TrimSuffix(line, ":"), ",") {
				selector = strings.Trim(strings.TrimSpace(selector), `"`)
				entry = entry || strings.HasPrefix(selector, name+"@")
			}
			continue
		}
		if !entry {
			continue
		}
		field := strings.TrimSpace(line)
		if value, ok := strings.CutPrefix(field, "version"); ok {
			value = strings.Trim(strings.TrimSpace(strings.TrimPrefix(value, ":")), `"`)
			if !slices.Contains(versions, value) {
				versions = append(versions, value)
			}
		}
	}
	return versions
}

// pnpmLockedVersions reads the release of name a pnpm-lock.yaml resolves
// for the importer at relative.
func pnpmLockedVersions(data []byte, relative, name string) []string {
	var lock struct {
		Importers map[string]map[string]map[string]yaml.Node `yaml:"importers"`
		// Lockfiles without importers describe the root package.
		Dependencies    map[string]yaml.Node `yaml:"dependencies"`
		DevDependencies map[string]yaml.Node `yaml:"devDependencies"`
	}
	if yaml.Unmarshal(data, &lock) != nil {
		return nil
	}
	sections := []map[string]yaml.Node{lock.Dependencies, lock.DevDependencies}
	if importer, ok := lock.Importers[relative]; ok {
		sections = []map[string]yaml.Node{importer["dependencies"], importer["devDependencies"], importer["optionalDependencies"]}
	}
	for _, section := range sections {
		node, ok := section[name]
		if !ok {
			continue
		}
		version := node.Value
		if node.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(node.Content); i += 2 {
				if node.Content[i].Value == "version" {
					version = node.Content[i+1].Value
				}
			}
		}
		version, _, _ = strings.Cut(version, "(")
		return []string{strings.TrimSpace(version)}
	}
	return nil
}
