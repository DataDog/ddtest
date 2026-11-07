// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// A hand-written ddtrace setup runs pytest with the ddtrace plugin enabled,
// as ddtrace's documentation shows: pytest --ddtrace, PYTEST_ADDOPTS=--ddtrace
// or --ddtrace in the pytest configuration's addopts, with ddtrace from the
// project's dependencies and the DD_* configuration in the job's
// environment. The pytest CI check validates simple forms and reports what
// remains missing or cannot be read statically.

// pythonHandWrittenRelease is one exact ddtrace release (PEP 440 final or
// pre-release), never a range.
var pythonHandWrittenRelease = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:(?:a|b|rc)[0-9]+)?$`)

// pythonHandWrittenExample is an exact ddtrace release for messages.
const pythonHandWrittenExample = "4.16.1"

// pythonHandWrittenSetup recognizes a hand-written ddtrace setup of the
// pytest command at index of job (see handWrittenSetup); nil when the
// command does not enable the ddtrace plugin.
func pythonHandWrittenSetup(root string, provider ciProvider, workflow ciWorkflow, job runtimeJob, index int, site string) *handWrittenSetup {
	step := job.Steps[index]
	env := handWrittenEnvironment(workflow, job, step)
	directory := filepath.Clean(filepath.FromSlash(stepDirectory(workflow, job, step)))
	if strings.HasPrefix(directory, "..") || filepath.IsAbs(directory) || strings.Contains(directory, "$") {
		if strings.Contains(step.Run, "--ddtrace") || strings.Contains(env["PYTEST_ADDOPTS"], "--ddtrace") {
			setup := &handWrittenSetup{Reference: handWrittenReference("ddtrace", "--ddtrace")}
			setup.unresolved("the test command runs in " + stepDirectory(workflow, job, step) + ", outside the repository ddtest reads.")
			return setup
		}
		return nil
	}
	mechanism, commandEnv, problems, unresolved := pythonHandWrittenPlugin(root, directory, step.Run, env)
	if mechanism == "" {
		return nil
	}
	setup := &handWrittenSetup{Reference: handWrittenReference("ddtrace", mechanism)}
	for _, problem := range problems {
		setup.missing(problem)
	}
	if unresolved != "" {
		setup.unresolved(unresolved)
	}
	if version, pin := pythonHandWrittenTracer(setup, root, workflow, job, index); version != "" {
		setup.Tracer, setup.Pin = version, pin
		setup.Reference = handWrittenReference("ddtrace@"+version, mechanism)
	}
	// --ddtrace turns Test Optimization on; agentless reporting is the
	// environment's.
	handWrittenConfiguration(setup, provider, job, commandEnv, site, true, reportingFromEnvironment)
	return setup
}

// pythonHandWrittenPlugin finds the first pytest invocation of run that
// enables the ddtrace plugin: how it does (--ddtrace, PYTEST_ADDOPTS or the
// pytest configuration's addopts; empty when none does), the invocation's
// environment, what keeps the plugin from starting and what ddtest cannot
// read about it.
func pythonHandWrittenPlugin(root, directory, run string, env map[string]string) (mechanism string, commandEnv map[string]string, problems []string, unresolved string) {
	textual := strings.Contains(run, "--ddtrace") || strings.Contains(env["PYTEST_ADDOPTS"], "--ddtrace")
	commands, err := staticCommands(run)
	if err != nil {
		if textual {
			return "--ddtrace", env, nil, "the test command (" + run + ") cannot be read statically, so ddtest cannot tell whether --ddtrace reaches pytest."
		}
		return "", nil, nil, ""
	}
	for _, words := range commands {
		words, assignments := handWrittenCommandPrefix(words)
		if len(words) == 2 && words[0] == "cd" && filepath.IsLocal(words[1]) {
			directory = filepath.Join(directory, words[1])
			continue
		}
		if !pythonInvocation(words) {
			continue
		}
		commandEnv = mergeEnvironment(env, assignments)
		addopts := commandEnv["PYTEST_ADDOPTS"]
		if _, dynamic := handWrittenReads(addopts); dynamic {
			if strings.Contains(addopts, "ddtrace") || slices.Contains(words, "--ddtrace") {
				return "PYTEST_ADDOPTS", commandEnv, nil, "PYTEST_ADDOPTS (" + addopts + ") holds values only CI knows, so ddtest cannot tell which pytest options it adds."
			}
			continue
		}
		defaults, err := PytestConfigOptions(root, filepath.Join(root, directory), addopts, words)
		var options []string
		if err == nil {
			options, _, err = PytestCommandOptions(addopts, words, defaults)
		}
		if err != nil {
			if textual || strings.Contains(err.Error(), "Datadog activation") {
				return "--ddtrace", commandEnv, nil, "ddtest cannot read this pytest invocation's options statically: " + err.Error() + "."
			}
			continue
		}
		if !slices.Contains(options, "--ddtrace") {
			continue
		}
		arguments, _ := pythonRunnerArguments(words, "pytest")
		switch {
		case slices.Contains(arguments, "--ddtrace"):
			mechanism = "--ddtrace"
		case slices.Contains(strings.Fields(addopts), "--ddtrace"):
			mechanism = "PYTEST_ADDOPTS"
		default:
			mechanism = "pytest addopts"
		}
		if slices.Contains(options, "--no-ddtrace") {
			problems = append(problems, "The pytest options also pass --no-ddtrace, which turns the ddtrace plugin off: remove it.")
		}
		for i, option := range options {
			plugin := strings.TrimPrefix(option, "-p")
			if option == "-p" && i+1 < len(options) {
				plugin = options[i+1]
			} else if !strings.HasPrefix(option, "-p") {
				continue
			}
			if plugin == "no:ddtrace" {
				problems = append(problems, "The pytest options block the ddtrace plugin (-p no:ddtrace): remove that option.")
			}
		}
		_, autoloadOff := commandEnv["PYTEST_DISABLE_PLUGIN_AUTOLOAD"]
		if (autoloadOff || slices.Contains(options, "--disable-plugin-autoload")) && !slices.Contains(options, "ddtrace") && !slices.Contains(options, "-pddtrace") {
			problems = append(problems, "pytest plugin autoload is disabled, so --ddtrace finds no ddtrace plugin: load it explicitly with -p ddtrace in the pytest command.")
		}
		return mechanism, commandEnv, problems, ""
	}
	if textual {
		// A wrapper, such as tox, nox or make, starts pytest.
		return "--ddtrace", env, nil, "the tests start through " + run + ", and ddtest cannot tell whether --ddtrace and the DD_* variables reach the pytest it runs."
	}
	return "", nil, nil, ""
}

// handWrittenCommandPrefix splits a command's leading variable assignments
// (DD_ENV=ci pytest) from the command they prefix.
func handWrittenCommandPrefix(words []string) ([]string, map[string]string) {
	assignments := map[string]string{}
	for len(words) > 0 && shellAssignment(words[0]) {
		key, value, _ := strings.Cut(words[0], "=")
		assignments[key] = value
		words = words[1:]
	}
	return words, assignments
}

// pythonHandWrittenTracer follows dependency operations in order and records
// the installation seen by every pytest invocation in the selected step.
func pythonHandWrittenTracer(setup *handWrittenSetup, root string, workflow ciWorkflow, job runtimeJob, index int) (version, source string) {
	requested, pin, problem := "", "", ""
	active, observed := false, false
	selected, selectedPin, selectedProblem := "", "", ""
	for stepIndex, step := range job.Steps[:index+1] {
		if !step.sameShell && active {
			// Activation ends with its shell; its installations are not
			// evidence for the next shell's Python environment.
			requested, pin, problem, active = "", "", "", false
		}
		commands, err := staticCommands(step.Run)
		if err != nil {
			if pythonActivationText.MatchString(step.Run) {
				requested, pin = "", ""
				problem = "The job changes Python environments in a command ddtest cannot resolve statically; install an exact ddtrace release in the selected environment before pytest."
				active = true
			}
			continue
		}
		cwd := stepDirectory(workflow, job, step)
		for _, words := range commands {
			words, _ = handWrittenCommandPrefix(words)
			if len(words) == 0 {
				continue
			}
			if len(words) == 2 && words[0] == "cd" && filepath.IsLocal(words[1]) {
				cwd = filepath.Join(cwd, words[1])
				continue
			}
			if pythonActivates(words) || words[0] == "deactivate" && len(words) == 1 {
				// An installation in the previous environment does not
				// establish what this environment contains.
				requested, pin, problem = "", "", ""
				active = words[0] != "deactivate"
				continue
			}
			if len(words) > 1 && path.Base(words[0]) == "uv" && (words[1] == "sync" || words[1] == "run" && !slices.Contains(words, "--no-sync")) {
				// A lockfile includes unselected extras and groups. Until
				// their selection is resolved, sync cannot certify a pin.
				requested, pin = "", ""
				problem = "The job syncs a uv environment whose selected dependencies ddtest cannot resolve statically; the lockfile alone does not establish that ddtrace is installed."
			}
			if pythonInvocation(words) && stepIndex == index {
				current, currentPin := requested, pin
				if observed && (selected != current || selectedProblem != problem) {
					setup.unresolved("The pytest invocations in this step do not share one resolved ddtrace installation; validate each environment separately.")
					return "", ""
				}
				selected, selectedPin, selectedProblem, observed = current, currentPin, problem, true
			}
			args, pip := pythonHandWrittenPip(words)
			if !pip || len(args) == 0 {
				continue
			}
			switch args[0] {
			case "install":
				next, nextPin, known, dryRun := pythonInstallTracer(root, cwd, args[1:])
				if dryRun {
					continue
				}
				if !known {
					requested, pin, problem = "", "", "The job changes Python dependencies with an installation ddtest cannot resolve statically ("+strings.Join(words, " ")+")."
				} else if nextPin != "" {
					requested, pin, problem = next, nextPin, ""
					if pin == "install command" {
						pin = strings.Join(words, " ")
					}
				}
			case "uninstall":
				removes, known := pythonUninstallTracer(root, cwd, args[1:])
				if !known || removes {
					requested, pin = "", ""
					if known {
						problem = "The job removes ddtrace (" + strings.Join(words, " ") + ") before this pytest invocation; install an exact release again."
					} else {
						problem = "The job uninstalls Python dependencies with a command ddtest cannot resolve statically (" + strings.Join(words, " ") + ")."
					}
				}
			}
		}
	}
	// Wrappers remain unresolved by the caller, but still describe the
	// dependency state reached before the wrapper starts.
	if !observed {
		selected, selectedPin, selectedProblem = requested, pin, problem
	}
	if selectedProblem != "" {
		setup.unresolved(selectedProblem)
		return "", ""
	}
	if selectedPin == "" {
		setup.missing("No observed installation command of the job pins ddtrace in this test environment, so ddtest cannot certify which ddtrace the tests load: add ddtrace==<exact release> (such as " + pythonHandWrittenExample + ") to the requirements the job installs, or lock it.")
		return "", ""
	}
	if release, exact := strings.CutPrefix(selected, "=="); exact && pythonHandWrittenRelease.MatchString(release) {
		return release, selectedPin
	}
	setup.missing(selectedPin + " requires ddtrace " + strings.TrimSpace(selected) + ", which is not an exact release, so each build can load another one: pin an exact release, such as ddtrace==" + pythonHandWrittenExample + ".")
	return "", ""
}

// pythonHandWrittenPip returns pip's subcommand and arguments, retaining
// uninstall operations as well as install operations.
func pythonHandWrittenPip(words []string) ([]string, bool) {
	if len(words) == 0 {
		return nil, false
	}
	var args []string
	switch {
	case pipExecutable.MatchString(path.Base(words[0])):
		args = words[1:]
	case len(words) > 1 && path.Base(words[0]) == "uv" && words[1] == "pip":
		args = words[2:]
	case strings.HasPrefix(path.Base(words[0]), "python"):
		if index, module := PythonModule(words[1:]); index >= 0 && module[0] == "pip" {
			args = module[1:]
		} else {
			return nil, false
		}
	default:
		return nil, false
	}
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		option, _, assigned := strings.Cut(args[0], "=")
		if slices.Contains(pipGeneralValueOptions, option) && !assigned {
			if len(args) < 2 {
				return []string{"unresolved"}, true
			}
			args = args[1:]
		} else if !slices.Contains(pipGeneralValueOptions, option) && !slices.Contains(pipGeneralFlags, option) && !pipVerbosity.MatchString(option) {
			return nil, true
		}
		args = args[1:]
	}
	return args, true
}

// pythonRequirementFile reads both long and compact -r/-c forms. Constraints
// restrict versions; they are never evidence that pip installs a package.
func pythonRequirementFile(argument string) (constraint bool, file string, ok bool) {
	for _, prefix := range []string{"--requirement", "--constraint", "-r", "-c"} {
		if rest, found := strings.CutPrefix(argument, prefix); found {
			if len(prefix) > 2 && rest != "" && !strings.HasPrefix(rest, "=") && !strings.HasPrefix(rest, " ") {
				continue
			}
			return prefix == "-c" || prefix == "--constraint", strings.TrimSpace(strings.TrimPrefix(rest, "=")), true
		}
	}
	return false, "", false
}

// pythonInstallTracer reads installation evidence and whether the command is
// a dry run. A dry run changes no installation, including when its metadata
// or alternate destination cannot be verified.
func pythonInstallTracer(root, directory string, args []string) (string, string, bool, bool) {
	var packages, requirements, constraints, groups []string
	dryRun, noDeps, alternate := false, false, false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if constraint, file, ok := pythonRequirementFile(arg); ok {
			if file == "" {
				i++
				if i >= len(args) {
					return "", "", false, dryRun
				}
				file = args[i]
			}
			file = path.Join(filepath.ToSlash(directory), file)
			if constraint {
				constraints = append(constraints, file)
			} else {
				requirements = append(requirements, file)
			}
			continue
		}
		option, value, assigned := strings.Cut(arg, "=")
		if strings.HasPrefix(arg, "-t") && len(arg) > 2 {
			option, value, assigned = "-t", arg[2:], true
		}
		if strings.HasPrefix(arg, "-e") && len(arg) > 2 {
			option, value, assigned = "-e", arg[2:], true
		}
		if option == "--dry-run" {
			dryRun = true
			continue
		}
		if option == "--no-deps" {
			noDeps = true
			continue
		}
		if slices.Contains([]string{"-t", "--target", "--prefix", "--root", "--python", "--user"}, option) {
			alternate = true
		}
		if option == "-e" || option == "--editable" || option == "--group" {
			if !assigned {
				i++
				if i >= len(args) {
					return "", "", false, dryRun
				}
				value = args[i]
			}
			if option == "--group" {
				groups = append(groups, value)
			} else {
				packages = append(packages, value)
			}
			continue
		}
		if slices.Contains(pipInstallValueOptions, option) || slices.Contains(pipGeneralValueOptions, option) {
			if !assigned {
				i++
				if i >= len(args) {
					return "", "", false, dryRun
				}
			}
			continue
		}
		if strings.HasPrefix(arg, "-") {
			if slices.Contains(pipInstallFlags, option) || slices.Contains(pipGeneralFlags, option) || pipInstallCluster.MatchString(arg) {
				continue
			}
			return "", "", false, dryRun
		}
		if strings.ContainsAny(arg, "$`") {
			return "", "", false, dryRun
		}
		packages = append(packages, arg)
	}
	if dryRun {
		return "", "", true, true
	}
	if alternate {
		return "", "", false, false
	}
	for _, pkg := range packages {
		if !noDeps && (pkg == "." || strings.HasPrefix(pkg, ".[")) {
			dependencies, known := pythonProjectRequirements(root, directory, pkg, "")
			if !known {
				return "", "", false, false
			}
			packages = append(packages, dependencies...)
		}
	}
	for _, group := range groups {
		dependencies, known := pythonProjectRequirements(root, directory, "", group)
		if !known {
			return "", "", false, false
		}
		packages = append(packages, dependencies...)
	}
	spec, source, known := pythonRequirementSelection(root, packages, requirements, constraints)
	return spec, source, known, false
}

// Uninstall requirements name packages to remove; unreadable files or unknown
// arguments cannot preserve a previously certified installation.
func pythonUninstallTracer(root, directory string, args []string) (bool, bool) {
	var packages, requirements []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if constraint, file, ok := pythonRequirementFile(arg); ok {
			if constraint {
				return false, false
			}
			if file == "" {
				i++
				if i >= len(args) {
					return false, false
				}
				file = args[i]
			}
			requirements = append(requirements, path.Join(filepath.ToSlash(directory), file))
			continue
		}
		option, _, assigned := strings.Cut(arg, "=")
		if option == "--python" {
			return false, false
		}
		if slices.Contains(pipGeneralValueOptions, option) || option == "--root-user-action" {
			if !assigned {
				i++
				if i >= len(args) {
					return false, false
				}
			}
			continue
		}
		if slices.Contains([]string{"-y", "--yes", "--break-system-packages"}, option) || slices.Contains(pipGeneralFlags, option) || pipVerbosity.MatchString(option) {
			continue
		}
		if strings.HasPrefix(arg, "-") || strings.ContainsAny(arg, "$`") {
			return false, false
		}
		packages = append(packages, arg)
	}
	_, source, known := pythonRequirementSelection(root, packages, requirements, nil)
	return source != "", known
}

// Read only the mandatory dependencies of an installed project, its selected
// extras, or one explicitly selected dependency group. Other declarations do
// not install packages. Dynamic metadata remains unresolved.
func pythonProjectRequirements(root, directory, pkg, group string) ([]string, bool) {
	file := path.Join(filepath.ToSlash(directory), "pyproject.toml")
	if groupFile, name, found := strings.Cut(group, ":"); found {
		file, group = path.Join(filepath.ToSlash(directory), groupFile), name
		if path.Base(file) != "pyproject.toml" {
			return nil, false
		}
	}
	info, ok := repositoryFile(root, root, filepath.FromSlash(file))
	if !ok || info.Size() > 1<<20 {
		return nil, false
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
	if err != nil {
		return nil, false
	}
	var project struct {
		Project *struct {
			Dependencies         []string            `toml:"dependencies"`
			OptionalDependencies map[string][]string `toml:"optional-dependencies"`
			Dynamic              []string            `toml:"dynamic"`
		} `toml:"project"`
		DependencyGroups map[string][]any `toml:"dependency-groups"`
	}
	if toml.Unmarshal(data, &project) != nil {
		return nil, false
	}
	normalize := func(name string) string {
		return strings.ToLower(strings.NewReplacer("_", "-", ".", "-").Replace(name))
	}
	var requirements []string
	if pkg != "" {
		if project.Project == nil || slices.Contains(project.Project.Dynamic, "dependencies") {
			return nil, false
		}
		requirements = append(requirements, project.Project.Dependencies...)
		if extras, selected := strings.CutPrefix(pkg, ".["); selected {
			if !strings.HasSuffix(extras, "]") || slices.Contains(project.Project.Dynamic, "optional-dependencies") {
				return nil, false
			}
			for _, name := range strings.Split(strings.TrimSuffix(extras, "]"), ",") {
				matches := 0
				for extra, dependencies := range project.Project.OptionalDependencies {
					if normalize(extra) == normalize(strings.TrimSpace(name)) {
						requirements = append(requirements, dependencies...)
						matches++
					}
				}
				if matches > 1 {
					return nil, false
				}
			}
		}
	}
	if group != "" {
		groups := map[string][]any{}
		for name, entries := range project.DependencyGroups {
			name = normalize(name)
			if _, duplicate := groups[name]; duplicate {
				return nil, false
			}
			groups[name] = entries
		}
		active := map[string]bool{}
		var include func(string, int) bool
		include = func(name string, depth int) bool {
			name = normalize(name)
			entries, found := groups[name]
			if !found || active[name] || depth >= 16 {
				return false
			}
			active[name] = true
			defer delete(active, name)
			for _, entry := range entries {
				switch entry := entry.(type) {
				case string:
					requirements = append(requirements, entry)
				case map[string]any:
					next, ok := entry["include-group"].(string)
					if !ok || len(entry) != 1 || !include(next, depth+1) {
						return false
					}
				default:
					return false
				}
			}
			return true
		}
		if !include(group, 0) {
			return nil, false
		}
	}
	return requirements, true
}

// pythonRequirementName is a requirement's project name, before its extras,
// version specifiers or markers.
var pythonRequirementName = regexp.MustCompile(`^\s*([A-Za-z0-9][A-Za-z0-9._-]*)\s*(?:\[[^\]]*\])?\s*([^;#]*)`)

// pythonRequirement reads a requirement (ddtrace==4.16.1, ddtrace[opentracing]>=4):
// its version specifiers, and whether it names ddtrace.
func pythonRequirement(requirement string) (specifier string, named bool) {
	match := pythonRequirementName.FindStringSubmatch(requirement)
	if match == nil || strings.ToLower(strings.NewReplacer("_", "-", ".", "-").Replace(match[1])) != "ddtrace" {
		return "", false
	}
	return strings.Join(strings.Fields(match[2]), ""), true
}

func pythonRequirementSelection(root string, packages, requirements, constraints []string) (specifier, source string, known bool) {
	known = true
	constrained, constraintSource := "", ""
	selectPin := func(spec, file string, constraint bool) {
		selected, from := &specifier, &source
		if constraint {
			selected, from = &constrained, &constraintSource
		}
		if *from != "" {
			if spec == "" {
				return
			}
			if *selected != "" && *selected != spec {
				known = false
			}
		}
		*selected, *from = spec, file
	}
	for _, pkg := range packages {
		if spec, named := pythonRequirement(pkg); named {
			if strings.Contains(pkg, ";") {
				known = false
			}
			selectPin(spec, "install command", false)
		}
	}
	seen := map[string]bool{}
	var read func(string, bool, int)
	read = func(file string, constraint bool, depth int) {
		file = path.Clean(file)
		key := file
		if constraint {
			key = "constraint:" + file
		}
		if seen[key] || depth >= 16 {
			known = false
			return
		}
		seen[key] = true
		defer delete(seen, key)
		info, ok := repositoryFile(root, root, filepath.FromSlash(file))
		if !ok || info.Size() > 1<<20 {
			known = false
			return
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			known = false
			return
		}
		text := strings.ReplaceAll(string(data), "\\\n", " ")
		for line := range strings.SplitSeq(text, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if nested, included, ok := pythonRequirementFile(line); ok {
				if included == "" {
					known = false
					continue
				}
				read(path.Join(path.Dir(file), included), constraint || nested, depth+1)
			} else if spec, named := pythonRequirement(line); named {
				if strings.Contains(line, ";") {
					known = false
				}
				selectPin(spec, file, constraint)
			}
		}
	}
	for _, file := range requirements {
		read(file, false, 0)
	}
	for _, file := range constraints {
		read(file, true, 0)
	}
	if source == "" {
		return "", "", known
	}
	if constraintSource != "" {
		if specifier == "" {
			specifier, source = constrained, constraintSource
		} else if constrained != "" && constrained != specifier {
			known = false
		}
	}
	return specifier, source, known
}
