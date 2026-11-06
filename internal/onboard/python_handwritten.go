// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"encoding/json"
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
	if version, pin := pythonHandWrittenTracer(setup, root, directory, workflow, job, index); version != "" {
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
func pythonHandWrittenTracer(setup *handWrittenSetup, root, directory string, workflow ciWorkflow, job runtimeJob, index int) (version, source string) {
	requested, pin, problem := "", "", ""
	explicit, observed := false, false
	selected, selectedPin, selectedProblem := "", "", ""
	for stepIndex, step := range job.Steps[:index+1] {
		commands, err := staticCommands(step.Run)
		if err != nil {
			continue
		}
		cwd := stepDirectory(workflow, job, step)
		for _, words := range commands {
			words, _ = handWrittenCommandPrefix(words)
			if len(words) == 2 && words[0] == "cd" && filepath.IsLocal(words[1]) {
				cwd = filepath.Join(cwd, words[1])
				continue
			}
			// uv sync installs the lockfile into the environment, replacing an
			// earlier pip selection. uv run does the same unless sync is off.
			if len(words) > 1 && path.Base(words[0]) == "uv" && (words[1] == "sync" || words[1] == "run" && !slices.Contains(words, "--no-sync")) {
				requested, pin = pythonLockedTracer(root, cwd)
				problem, explicit = "", true
			}
			if pythonInvocation(words) && stepIndex == index {
				current, currentPin := requested, pin
				if !explicit {
					current, currentPin = pythonLockedTracer(root, directory)
					if currentPin == "" {
						current, currentPin = pythonDeclaredTracer(root, directory)
					}
				}
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
				explicit = true
				next, nextPin, known := pythonInstallTracer(root, cwd, args[1:])
				if !known {
					requested, pin, problem = "", "", "The job changes Python dependencies with an installation ddtest cannot resolve statically ("+strings.Join(words, " ")+")."
				} else if nextPin != "" {
					requested, pin, problem = next, nextPin, ""
					if pin == "install command" {
						pin = strings.Join(words, " ")
					}
				}
			case "uninstall":
				for _, arg := range args[1:] {
					if _, named := pythonRequirement(arg); named {
						explicit = true
						requested, pin, problem = "", "", "The job removes ddtrace ("+strings.Join(words, " ")+") before this pytest invocation; install an exact release again."
					}
				}
			}
		}
	}
	// Wrappers remain unresolved by the caller, but still describe the
	// dependency state reached before the wrapper starts.
	if !observed {
		selected, selectedPin, selectedProblem = requested, pin, problem
		if !explicit {
			selected, selectedPin = pythonLockedTracer(root, directory)
			if selectedPin == "" {
				selected, selectedPin = pythonDeclaredTracer(root, directory)
			}
		}
	}
	if selectedProblem != "" {
		setup.unresolved(selectedProblem)
		return "", ""
	}
	if selectedPin == "" {
		setup.missing("No requirements file, lockfile or install command of the job pins ddtrace, so the tests load whatever ddtrace their environment has: add ddtrace==<exact release> (such as " + pythonHandWrittenExample + ") to the requirements the job installs, or lock it.")
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

func pythonInstallTracer(root, directory string, args []string) (string, string, bool) {
	var packages, requirements, constraints []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if constraint, file, ok := pythonRequirementFile(arg); ok {
			if file == "" {
				i++
				if i >= len(args) {
					return "", "", false
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
		if option == "-e" || option == "--editable" {
			if !assigned {
				i++
				if i >= len(args) {
					return "", "", false
				}
				value = args[i]
			}
			packages = append(packages, value)
			continue
		}
		if slices.Contains(pipInstallValueOptions, option) || slices.Contains(pipGeneralValueOptions, option) {
			if !assigned {
				i++
				if i >= len(args) {
					return "", "", false
				}
			}
			continue
		}
		if strings.HasPrefix(arg, "-") {
			if slices.Contains(pipInstallFlags, option) || slices.Contains(pipGeneralFlags, option) || pipInstallCluster.MatchString(arg) {
				continue
			}
			return "", "", false
		}
		packages = append(packages, arg)
	}
	for _, pkg := range packages {
		if pkg == "." || strings.HasPrefix(pkg, ".[") {
			if spec, file := pythonDeclaredTracer(root, directory); file != "" {
				packages = append(packages, "ddtrace"+spec)
			}
		}
	}
	return pythonRequirementSelection(root, packages, requirements, constraints)
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

// pythonRequirementsPin reads installed requirements, with constraints applied
// only when those requirements actually name ddtrace.
func pythonRequirementsPin(root, file string) (specifier, source string) {
	specifier, source, known := pythonRequirementSelection(root, nil, []string{file}, nil)
	if !known {
		return "", ""
	}
	return specifier, source
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

// pythonLockedTracer is the ddtrace release a lockfile at or above
// directory locks (uv.lock, poetry.lock, pdm.lock, Pipfile.lock), as an
// == specifier; source is empty without one.
func pythonLockedTracer(root, directory string) (specifier, source string) {
	for current := directory; ; current = filepath.Dir(current) {
		for _, name := range []string{"uv.lock", "poetry.lock", "pdm.lock", "Pipfile.lock"} {
			data, err := os.ReadFile(filepath.Join(root, current, name))
			if err != nil {
				continue
			}
			file := path.Join(filepath.ToSlash(current), name)
			if name == "Pipfile.lock" {
				var lock map[string]map[string]struct{ Version string }
				if json.Unmarshal(data, &lock) == nil {
					for _, section := range []string{"default", "develop"} {
						if entry, ok := lock[section]["ddtrace"]; ok {
							return entry.Version, file
						}
					}
				}
				continue
			}
			var lock struct {
				Package []struct{ Name, Version string } `toml:"package"`
			}
			if toml.Unmarshal(data, &lock) == nil {
				for _, entry := range lock.Package {
					if strings.EqualFold(entry.Name, "ddtrace") {
						return "==" + entry.Version, file
					}
				}
			}
		}
		if current == "." || current == "" {
			return "", ""
		}
	}
}

// pythonDeclaredTracer is ddtrace's requirement in the pyproject.toml
// ([project] dependencies, optional-dependencies or dependency-groups) or
// requirements.txt at or above directory; source is empty without one.
func pythonDeclaredTracer(root, directory string) (specifier, source string) {
	for current := directory; ; current = filepath.Dir(current) {
		file := path.Join(filepath.ToSlash(current), "pyproject.toml")
		if data, err := os.ReadFile(filepath.Join(root, current, "pyproject.toml")); err == nil {
			var project struct {
				Project struct {
					Dependencies         []string            `toml:"dependencies"`
					OptionalDependencies map[string][]string `toml:"optional-dependencies"`
				} `toml:"project"`
				DependencyGroups map[string][]any `toml:"dependency-groups"`
			}
			if toml.Unmarshal(data, &project) == nil {
				requirements := slices.Clone(project.Project.Dependencies)
				for _, group := range project.Project.OptionalDependencies {
					requirements = append(requirements, group...)
				}
				for _, group := range project.DependencyGroups {
					for _, entry := range group {
						if requirement, ok := entry.(string); ok {
							requirements = append(requirements, requirement)
						}
					}
				}
				for _, requirement := range requirements {
					if specifier, named := pythonRequirement(requirement); named {
						return specifier, file
					}
				}
			}
		}
		if specifier, source := pythonRequirementsPin(root, path.Join(filepath.ToSlash(current), "requirements.txt")); source != "" {
			return specifier, source
		}
		if current == "." || current == "" {
			return "", ""
		}
	}
}
