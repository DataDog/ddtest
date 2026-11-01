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
// environment. ddtest recognizes such a setup and lists what it lacks, but
// the pytest CI check does not read it yet (see
// pythonDiscovery.validatesHandWrittenSetup), so it is never certified.

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
	if version, pin := pythonHandWrittenTracer(setup, root, directory, job, index); version != "" {
		setup.Tracer, setup.Pin = version, pin
		setup.Reference = handWrittenReference("ddtrace@"+version, mechanism)
	}
	// --ddtrace turns Test Optimization on; agentless reporting is the
	// environment's.
	handWrittenConfiguration(setup, provider, job, commandEnv, site, true, false)
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

// pythonHandWrittenTracer finds the exact ddtrace release a hand-written
// setup loads in directory: one an install command of the job, before the
// tests or with them, pins (pip install ddtrace==4.16.1); otherwise the one
// a requirements file those commands install (-r) pins, the one a lockfile
// at or above directory (uv.lock, poetry.lock, pdm.lock, Pipfile.lock)
// locks, or the one the project's pyproject.toml or requirements.txt pins.
// It records on setup why none is pinned.
func pythonHandWrittenTracer(setup *handWrittenSetup, root, directory string, job runtimeJob, index int) (version, source string) {
	var requirements []string
	for _, step := range job.Steps[:index+1] {
		commands, err := staticCommands(step.Run)
		if err != nil {
			continue
		}
		for _, words := range commands {
			words, _ = handWrittenCommandPrefix(words)
			arguments, ok := pythonHandWrittenInstall(words)
			if !ok {
				continue
			}
			for i := 0; i < len(arguments); i++ {
				argument := arguments[i]
				file := ""
				switch {
				case (argument == "-r" || argument == "--requirement" || argument == "-c" || argument == "--constraint") && i+1 < len(arguments):
					i++
					file = arguments[i]
				case strings.HasPrefix(argument, "--requirement=") || strings.HasPrefix(argument, "--constraint="):
					_, file, _ = strings.Cut(argument, "=")
				case strings.HasPrefix(argument, "-r") || strings.HasPrefix(argument, "-c"):
					file = argument[2:]
				}
				if file != "" {
					requirements = append(requirements, path.Join(filepath.ToSlash(directory), file))
					continue
				}
				if requested, named := pythonRequirement(argument); named {
					if release, exact := strings.CutPrefix(requested, "=="); exact && pythonHandWrittenRelease.MatchString(release) {
						version, source = release, strings.Join(words, " ")
						continue
					}
					setup.missing("The job installs ddtrace without an exact release (" + strings.Join(words, " ") + "), so each build can load another one: install an exact release, such as ddtrace==" + pythonHandWrittenExample + ".")
					return "", ""
				}
			}
		}
	}
	if version != "" {
		return version, source
	}
	requested, file := "", ""
	for _, requirement := range requirements {
		if requested, file = pythonRequirementsPin(root, requirement); file != "" {
			break
		}
	}
	if file == "" {
		requested, file = pythonLockedTracer(root, directory)
	}
	if file == "" {
		requested, file = pythonDeclaredTracer(root, directory)
	}
	if file == "" {
		setup.missing("No requirements file, lockfile or install command of the job pins ddtrace, so the tests load whatever ddtrace their environment has: add ddtrace==<exact release> (such as " + pythonHandWrittenExample + ") to the requirements the job installs, or lock it.")
		return "", ""
	}
	if release, exact := strings.CutPrefix(requested, "=="); exact && pythonHandWrittenRelease.MatchString(release) {
		return release, file
	}
	setup.missing(file + " requires ddtrace " + strings.TrimSpace(requested) + ", which is not an exact release, so each build can load another one: pin an exact release, such as ddtrace==" + pythonHandWrittenExample + ".")
	return "", ""
}

// pythonHandWrittenInstall reads a pip command that installs packages (pip
// install, python -m pip install, uv pip install): its arguments.
func pythonHandWrittenInstall(words []string) ([]string, bool) {
	switch {
	case len(words) > 2 && (path.Base(words[0]) == "pip" || path.Base(words[0]) == "pip3") && words[1] == "install":
		return words[2:], true
	case len(words) > 3 && path.Base(words[0]) == "uv" && words[1] == "pip" && words[2] == "install":
		return words[3:], true
	case len(words) > 1 && strings.HasPrefix(path.Base(words[0]), "python"):
		index, module := PythonModule(words[1:])
		if index >= 0 && len(module) > 2 && module[0] == "pip" && module[1] == "install" {
			return module[2:], true
		}
	}
	return nil, false
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

// pythonRequirementsPin is ddtrace's requirement in a requirements file
// (repository path file), following its -r and -c includes; file is empty
// when the file does not name ddtrace.
func pythonRequirementsPin(root, file string) (specifier, source string) {
	seen := map[string]bool{}
	var read func(string) (string, string)
	read = func(file string) (string, string) {
		file = path.Clean(file)
		if seen[file] || strings.HasPrefix(file, "..") || path.IsAbs(file) {
			return "", ""
		}
		seen[file] = true
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			return "", ""
		}
		for line := range strings.SplitSeq(string(data), "\n") {
			line = strings.TrimSpace(line)
			for _, prefix := range []string{"-r", "--requirement", "-c", "--constraint"} {
				if included, ok := strings.CutPrefix(line, prefix); ok && (strings.HasPrefix(included, " ") || strings.HasPrefix(included, "=")) {
					if specifier, source := read(path.Join(path.Dir(file), strings.TrimSpace(strings.TrimPrefix(included, "=")))); source != "" {
						return specifier, source
					}
				}
			}
			if specifier, named := pythonRequirement(line); named {
				return specifier, file
			}
		}
		return "", ""
	}
	return read(file)
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
