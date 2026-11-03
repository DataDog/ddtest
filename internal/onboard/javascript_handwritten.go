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
// dd-trace/ci/init, dd-trace/register.js or dd-trace/init, by package name
// or through a node_modules path. canonical reports the spelling dd-trace
// documents (dd-trace/ci/init, dd-trace/register.js), the only one the
// command resolution and validation scope accept.
func javascriptHandWrittenModule(module string) (kind string, canonical bool) {
	module = strings.Trim(module, `"'`)
	for _, name := range []string{"ci/init", "ci/init.js", "register.js", "register", "init", "init.js"} {
		if module == "dd-trace/"+name || strings.HasSuffix(module, "node_modules/dd-trace/"+name) {
			kind = strings.TrimSuffix(name, ".js")
			return kind, module == "dd-trace/ci/init" || module == "dd-trace/register.js"
		}
	}
	return "", false
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
		kind, canonical := javascriptHandWrittenModule(module)
		switch {
		case (kind == "ci/init" && flag != "--import" || kind == "register" && flag == "--import") && !canonical:
			// Such as -r ./node_modules/dd-trace/ci/init: the test command's
			// resolution and the validation scope read only the documented
			// spelling, so ddtest cannot validate this one.
			preload.found = true
			if preload.unresolved == "" {
				preload.unresolved = "NODE_OPTIONS loads " + flag + " " + module + ", a spelling ddtest does not validate: preload the package as dd-trace documents it (-r dd-trace/ci/init, and --import dd-trace/register.js for ESM tests)."
			}
			normalized = append(normalized, flag, module)
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
	if preload.found && !preload.ci && len(preload.problems) == 0 && preload.unresolved == "" {
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
	handWrittenPreloadBeforeInstall(setup, workflow, job, index, "NODE_OPTIONS", "dd-trace", javascriptInstallsDependencies, func(value string) bool {
		return javascriptHandWrittenPreload(value).found
	})
	handWrittenFrameworkDisabled(setup, env, framework)
	handWrittenConfiguration(setup, provider, job, env, site, true, reportingFromEnvironment)
	return setup, normalized
}

// javascriptInstallsDependencies reports a package manager command that
// installs the project's dependencies: npm ci, an install or add, or Yarn
// alone.
func javascriptInstallsDependencies(words []string) bool {
	if len(words) == 0 || !slices.Contains([]string{"npm", "yarn", "pnpm"}, words[0]) {
		return false
	}
	if len(words) == 1 || strings.HasPrefix(words[1], "-") {
		return words[0] == "yarn"
	}
	return slices.Contains([]string{"ci", "install", "i", "add"}, words[1])
}

// javascriptHandWrittenTracer finds the exact dd-trace release a
// hand-written setup loads in directory: one an install command earlier in
// the job adds there (npm install dd-trace@5.40.0) and no later install
// removes, otherwise the one the package.json that resolves dd-trace there
// pins, or its lockfile resolves. It records on setup why none is pinned.
func javascriptHandWrittenTracer(setup *handWrittenSetup, root, directory string, workflow ciWorkflow, job runtimeJob, index int, env map[string]string) (version, source string) {
	// uninstalled is a command that removes dd-trace, whichever install it
	// came from (package.json's too), when no later one installs it again.
	// saved reports an install the job saves to package.json, and group
	// the dependency group it saves it in: a later command that omits that
	// group (npm prune --omit=dev for a devDependency) removes it.
	removed, uninstalled, saved, group := "", "", false, ""
	for _, step := range job.Steps[:index] {
		where := step.Source
		if where == "" {
			where = "an earlier step"
		}
		commands, err := staticCommands(step.Run)
		if err != nil {
			if version != "" && javascriptMentionsPackageManager(step.Run) {
				// An install ddtest cannot read may remove the tracer.
				setup.unresolved("the job installs dd-trace (" + source + "), and a later command ddtest cannot read (" + step.Run + " at " + where + ") may reinstall the dependencies without it.")
				return "", ""
			}
			continue
		}
		installDirectory := filepath.Clean(filepath.FromSlash(stepDirectory(workflow, job, step)))
		moved := false
		for _, words := range commands {
			if len(words) > 0 && slices.Contains([]string{"cd", "pushd", "popd"}, words[0]) {
				moved = true
				continue
			}
			elsewhere := moved || installDirectory != directory || javascriptInstallsElsewhere(words)
			release, global, ok := javascriptTracerInstall(words)
			if !ok {
				uninstall := javascriptRemovesTracer(words) || version != "" && saved && javascriptOmitsGroupCommand(mergeEnvironment(handWrittenEnvironment(workflow, job, step), javascriptAssigned(words)), words, group)
				if !uninstall && (version == "" && uninstalled == "" || version != "" && saved || !javascriptReinstalls(words)) {
					// A saved install is in the manifest and lockfile the job
					// reinstalls from.
					continue
				}
				if !moved && !javascriptInstallsElsewhere(words) && !javascriptRelatedDirectories(installDirectory, directory) {
					continue // Another package's dependencies.
				}
				command := strings.Join(words, " ") + " at " + where
				switch {
				case elsewhere:
					installed := "the job installs dd-trace (" + source + "), and "
					if source == "" {
						installed = ""
					}
					setup.unresolved(installed + "ddtest cannot tell whether " + command + " removes dd-trace from " + displayDirectory(directory) + ".")
					return "", ""
				case uninstall:
					// Whichever install added it, the tests no longer find it.
					if version != "" {
						removed = source + ", which " + command + " removes"
					}
					uninstalled, version, source = command, "", ""
				case version == "":
					// After an uninstall, whether a reinstall restores dd-trace
					// depends on whether the uninstall saved its removal.
					setup.unresolved("the job removes dd-trace (" + uninstalled + "), and ddtest cannot tell whether the later " + command + " installs it again.")
					return "", ""
				default:
					// npm ci, a later install or a prune reinstalls the
					// dependencies as declared, removing a tracer installed
					// without saving it.
					removed = source + ", which " + command + " removes"
					version, source = "", ""
				}
				continue
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
			case elsewhere:
				setup.unresolved("the job installs dd-trace (" + strings.Join(words, " ") + " at " + where + ") outside " + displayDirectory(directory) + ", where the tests run, so ddtest cannot tell that their -r dd-trace/ci/init resolves that install.")
				return "", ""
			}
			version, source, removed, uninstalled = release, strings.Join(words, " ")+" at "+where, "", ""
			saved = words[0] != "npm" || !slices.Contains(words, "--no-save")
			group = javascriptSavedGroup(root, directory, words)
		}
	}
	if version != "" {
		return version, source
	}
	if uninstalled != "" {
		// package.json may still declare it; the installed tree does not.
		setup.missing("The job removes dd-trace (" + uninstalled + ") before the tests, and no later command installs it, so -r dd-trace/ci/init cannot load: remove that command, or install an exact release after it.")
		return "", ""
	}
	declared, manifest, group := javascriptDeclaredTracer(root, directory)
	switch {
	case manifest == "" && removed != "":
		setup.missing("The job installs dd-trace (" + removed + ": package.json does not declare dd-trace), so -r dd-trace/ci/init cannot load: install the exact release after the last dependency install, or add dd-trace to package.json as a devDependency with an exact release.")
		return "", ""
	case manifest == "":
		setup.missing("No package.json in " + displayDirectory(directory) + " or above it depends on dd-trace, and no command of the job installs it, so -r dd-trace/ci/init cannot load: add dd-trace as a devDependency with an exact release (npm install --save-dev --save-exact dd-trace@<release>) and commit the lockfile.")
		return "", ""
	case group == javascriptDevelopment && javascriptOmitsGroup(workflow, job, index, directory, filepath.Dir(filepath.FromSlash(manifest)), group):
		setup.missing("dd-trace is a devDependency of " + manifest + ", but the job installs production dependencies only, so -r dd-trace/ci/init cannot load: install the devDependencies too, or make dd-trace a dependency.")
		return "", ""
	case group == javascriptOptional && javascriptOmitsGroup(workflow, job, index, directory, filepath.Dir(filepath.FromSlash(manifest)), group):
		setup.missing("dd-trace is an optionalDependency of " + manifest + ", but the job installs the dependencies without the optional ones, so -r dd-trace/ci/init cannot load: install the optional dependencies too, or make dd-trace a devDependency.")
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

// javascriptMentionsPackageManager reports a command that may run npm, Yarn
// or pnpm.
func javascriptMentionsPackageManager(command string) bool {
	return slices.ContainsFunc([]string{"npm", "yarn", "pnpm"}, func(name string) bool { return strings.Contains(command, name) })
}

// javascriptReinstalls reports a package manager command that reinstalls
// the dependencies as package.json and the lockfile declare them, removing
// a package installed without saving it: npm ci, any install or add, prune,
// or removing a package.
func javascriptReinstalls(words []string) bool {
	if len(words) == 0 || !slices.Contains([]string{"npm", "yarn", "pnpm"}, words[0]) {
		return false
	}
	if len(words) == 1 || strings.HasPrefix(words[1], "-") {
		return words[0] == "yarn" // Yarn alone installs.
	}
	return slices.Contains([]string{"ci", "install", "i", "add", "prune", "uninstall", "remove", "rm", "un", "r", "unlink"}, words[1])
}

// javascriptRemovesTracer reports a package manager command that removes
// dd-trace, such as npm uninstall dd-trace.
func javascriptRemovesTracer(words []string) bool {
	return len(words) > 2 && slices.Contains([]string{"npm", "yarn", "pnpm"}, words[0]) &&
		slices.Contains([]string{"uninstall", "remove", "rm", "un", "r"}, words[1]) && slices.Contains(words[2:], "dd-trace")
}

// javascriptRelatedDirectories reports two repository directories one of
// which contains the other, so an install in one can change the packages
// the other resolves.
func javascriptRelatedDirectories(a, b string) bool {
	within := func(inner, outer string) bool {
		relative, err := filepath.Rel(outer, inner)
		return err == nil && filepath.IsLocal(relative) || relative == "."
	}
	return within(a, b) || within(b, a)
}

// javascriptInstallsElsewhere reports a package manager option that runs it
// in another directory or package than the one it starts in.
func javascriptInstallsElsewhere(words []string) bool {
	return slices.ContainsFunc(words, func(word string) bool {
		option, _, _ := strings.Cut(word, "=")
		return slices.Contains([]string{"--prefix", "-C", "--cwd", "--dir", "-w", "--workspace", "--workspaces", "-ws", "--filter", "-F", "--location"}, option)
	})
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

// The dependency groups of a package.json that an install can leave out.
const (
	javascriptDevelopment = "dev"
	javascriptOptional    = "optional"
)

// javascriptDeclaredTracer finds the package.json that resolves dd-trace in
// directory, there or in a parent within the repository: its requested
// release, its repository path and its group: javascriptDevelopment when
// only devDependencies has it, javascriptOptional when optionalDependencies
// does, or "" for dependencies.
func javascriptDeclaredTracer(root, directory string) (requested, manifest, group string) {
	for current := directory; ; current = filepath.Dir(current) {
		data, err := os.ReadFile(filepath.Join(root, current, "package.json"))
		if err == nil {
			var pkg struct {
				Dependencies, DevDependencies, OptionalDependencies map[string]string
			}
			if json.Unmarshal(data, &pkg) == nil {
				for _, dependencies := range []map[string]string{pkg.Dependencies, pkg.OptionalDependencies, pkg.DevDependencies} {
					if version, ok := dependencies["dd-trace"]; ok {
						// Optional dependencies install without devDependencies
						// too, and npm reads optionalDependencies over
						// dependencies.
						group := ""
						if _, optional := pkg.OptionalDependencies["dd-trace"]; optional {
							group = javascriptOptional
						} else if _, inProduction := pkg.Dependencies["dd-trace"]; !inProduction {
							group = javascriptDevelopment
						}
						return strings.TrimSpace(version), path.Join(filepath.ToSlash(current), "package.json"), group
					}
				}
			}
		}
		if current == "." || current == "" {
			return "", "", ""
		}
	}
}

// javascriptSavedGroup is the dependency group (see
// javascriptDeclaredTracer) a dd-trace install (see javascriptTracerInstall)
// saves it in: javascriptDevelopment with -D, --save-dev or --dev,
// javascriptOptional with -O, --save-optional or --optional, "" with -P,
// --save-prod or --prod, and otherwise the group of the package.json that
// resolves dd-trace in directory, where npm and Yarn keep it.
func javascriptSavedGroup(root, directory string, words []string) string {
	has := func(options ...string) bool {
		return slices.ContainsFunc(words, func(word string) bool { return slices.Contains(options, word) })
	}
	switch {
	case has("-D", "--save-dev", "--dev"):
		return javascriptDevelopment
	case has("-O", "--save-optional", "--optional"):
		return javascriptOptional
	case has("-P", "--save-prod", "--prod"):
		return ""
	}
	_, _, group := javascriptDeclaredTracer(root, directory)
	return group
}

// javascriptAssigned is the variables a command assigns for itself (see
// handWrittenCommand).
func javascriptAssigned(words []string) map[string]string {
	assigned, _ := handWrittenCommand(words)
	return assigned
}

// javascriptOmitsGroup reports whether the last command before index in job
// that decides which dependencies are installed leaves group out (see
// javascriptOmitsGroupCommand), wherever it runs. Only an npm ci or install
// without such an option in directory, where the tests run, or in
// manifest's directory (the package.json that declares dd-trace) installs
// them again; a prune without one keeps them.
func javascriptOmitsGroup(workflow ciWorkflow, job runtimeJob, index int, directory, manifest, group string) bool {
	omitted := false
	for _, step := range job.Steps[:index] {
		commands, err := staticCommands(step.Run)
		if err != nil {
			continue
		}
		installDirectory := filepath.Clean(filepath.FromSlash(stepDirectory(workflow, job, step)))
		moved := false
		for _, words := range commands {
			env := mergeEnvironment(handWrittenEnvironment(workflow, job, step), javascriptAssigned(words))
			_, command := handWrittenCommand(words)
			if len(command) > 0 && slices.Contains([]string{"cd", "pushd", "popd"}, command[0]) {
				moved = true
				continue
			}
			if len(command) < 2 || !slices.Contains([]string{"npm", "yarn", "pnpm"}, command[0]) {
				continue
			}
			switch {
			case javascriptOmitsGroupCommand(env, words, group):
				omitted = true
			case slices.Contains([]string{"ci", "install", "i"}, command[1]) && !moved && !javascriptInstallsElsewhere(command) && (installDirectory == directory || installDirectory == manifest):
				omitted = false
			}
		}
	}
	return omitted
}

// javascriptOmitsGroupCommand reports a package manager command (words,
// with the variables it assigns for itself) that installs or prunes the
// dependencies without group (see javascriptDeclaredTracer). The
// devDependencies are left out with --production, --omit=dev (or --omit
// dev), --only=production and the like, or NODE_ENV=production in its
// environment (env); the optional ones with --omit=optional, --no-optional
// or Yarn's --ignore-optional. npm's --include (or npm_config_include)
// installs a group whatever omits it, and npm_config_omit omits one as
// --omit does; Yarn and npm's --production=false install the
// devDependencies whatever NODE_ENV says.
func javascriptOmitsGroupCommand(env map[string]string, words []string, group string) bool {
	_, words = handWrittenCommand(words)
	if group == "" || len(words) < 2 || !slices.Contains([]string{"npm", "yarn", "pnpm"}, words[0]) || !slices.Contains([]string{"ci", "install", "i", "prune"}, words[1]) {
		return false
	}
	npm := words[0] == "npm"
	var included, omitted []string
	if npm {
		// npm reads its configuration variables whatever their case.
		for key, value := range env {
			switch strings.ToLower(key) {
			case "npm_config_include":
				included = append(included, strings.FieldsFunc(value, javascriptListSeparator)...)
			case "npm_config_omit":
				omitted = append(omitted, strings.FieldsFunc(value, javascriptListSeparator)...)
			}
		}
	}
	production, development := strings.TrimSpace(env["NODE_ENV"]) == "production", false
	for i, word := range words[2:] {
		next := ""
		if i+3 < len(words) {
			next = words[i+3]
		}
		option, value, inline := strings.Cut(word, "=")
		if !inline {
			value = next
		}
		switch {
		case option == "--include" && npm:
			included = append(included, value)
		case option == "--omit":
			omitted = append(omitted, value)
		case slices.Contains([]string{"--production", "--production=true", "--prod"}, word) || word == "-P" && words[0] == "pnpm",
			option == "--only" && slices.Contains([]string{"production", "prod"}, value):
			production = true
		case word == "--production=false" && words[0] != "pnpm":
			development = true
		case slices.Contains([]string{"--no-optional", "--ignore-optional"}, word):
			omitted = append(omitted, javascriptOptional)
		}
	}
	switch {
	case slices.Contains(included, group):
		return false
	case slices.Contains(omitted, group):
		return true
	}
	return group == javascriptDevelopment && production && !development
}

// javascriptListSeparator separates the entries of an npm list
// configuration variable, such as npm_config_omit="dev optional".
func javascriptListSeparator(r rune) bool { return r == ' ' || r == ',' }

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
