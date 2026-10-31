// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"context"
	"errors"
	"path"
	"regexp"
	"slices"
	"strings"
)

// Python setup outside GitHub Actions. The Datadog GitLab setup script, the
// Datadog CircleCI orb and, on Buildkite, the Datadog installer all run
// install_test_visibility (v13 or v15, whose Python installer is the same):
// it creates a virtual environment (.dd_civis_env, in the working directory)
// with the python on PATH, installs ddtrace (DD_SET_TRACER_VERSION_PYTHON,
// otherwise the latest release) and coverage there with its pip, and exports
// PYTHONPATH=<its site-packages>:<coverage>:$PYTHONPATH and
// PYTEST_ADDOPTS="--ddtrace $PYTEST_ADDOPTS" into the job's shell. The tests'
// own environment stays as it is: PYTHONPATH adds the tracer to it, so the
// tests' Python must be the version that created that virtual environment.
//
// The Datadog Jenkins plugin (PythonConfigurator) instead runs pip install -U
// ddtrace with the build agent's default pip, from the workspace and with the
// agent's own environment, on every build: the latest release, which nothing
// pins. It sets PYTHONPATH to "<that pip's site-packages>:" followed by a
// space and the PYTHONPATH of the first step it configures on the agent,
// and PYTEST_ADDOPTS to "--ddtrace" followed by a space and that step's
// options.

// pythonTracerPath is the tracer's site-packages the installer or the CI
// plugin puts on PYTHONPATH, in the workflow model's syntax: a path only CI
// knows.
const pythonTracerPath = "${{ env.DD_TRACE_PYTHON_PATH }}"

// pythonExampleRelease is an exact ddtrace release for messages.
const pythonExampleRelease = "4.16.1"

// exactPythonRelease is one ddtrace release (PEP 440 final or pre-release),
// never a range or "latest".
var exactPythonRelease = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:(?:a|b|rc)[0-9]+)?$`)

// pythonImage matches the official images (python:3.12, python:3.12.7-slim,
// docker.io/library/python:3.13-alpine), CircleCI's (cimg/python:3.12,
// circleci/python:3.9-node) and PyPy's, whose engine the tracer cannot
// instrument.
var pythonImage = regexp.MustCompile(`^(?:cimg/|circleci/|(?:docker\.io/)?(?:library/)?)(python|pypy):([^@]+)(?:@.*)?$`)

// pythonUVImage matches uv's images, whose tag names their Python
// (ghcr.io/astral-sh/uv:python3.12-bookworm, :0.10.0-python3.13-trixie-slim).
var pythonUVImage = regexp.MustCompile(`^(?:ghcr\.io/)?astral-sh/uv:(?:[0-9][^-@]*-)?python([^-@]+)(?:-[^@]*)?(?:@.*)?$`)

// pythonVersion is a CPython version ddtest compares: major.minor, with an
// optional patch.
var pythonVersion = regexp.MustCompile(`^3\.[0-9]+(?:\.[0-9]+)?$`)

// pythonImageRuntime is the Python version a CI job's image selects, or the
// engine the tracer cannot instrument (PyPy). Both are empty when the image
// selects no static CPython minor version: no image, another image, a
// floating tag such as latest or 3, a digest, or an unresolved variable.
func pythonImageRuntime(image string) (version, engine string) {
	image = strings.TrimSpace(image)
	tag := ""
	if match := pythonUVImage.FindStringSubmatch(image); match != nil {
		tag = match[1]
	} else if match := pythonImage.FindStringSubmatch(image); match != nil {
		if match[1] == "pypy" {
			return "", "PyPy"
		}
		tag, _, _ = strings.Cut(match[2], "-")
	}
	if !pythonVersion.MatchString(tag) {
		return "", ""
	}
	return tag, ""
}

// pythonImageSelection is the Python version or engine a job's image selects,
// or a Python image's floating tag (latest, 3, slim): that image's runtime is
// not the user's to state.
func pythonImageSelection(job runtimeJob) string {
	if version, engine := pythonImageRuntime(job.Image); version+engine != "" {
		return version + engine
	}
	if match := pythonUVImage.FindStringSubmatch(withImplicitTag(job.Image)); match != nil {
		return match[1]
	}
	if match := pythonImage.FindStringSubmatch(withImplicitTag(job.Image)); match != nil {
		return match[2]
	}
	return ""
}

// pythonStatedRuntime takes --ci-runtime python=<version>, compared like a
// python:<version> image tag's, so it needs a minor version.
var pythonStatedRuntime = &statedRuntime{
	Names:   []string{"python"},
	Display: "Python",
	Version: pythonVersion,
	Example: "3.12 or 3.12.7",
	base:    pythonImageSelection,
	configured: func(root string, workflow ciWorkflow, job runtimeJob) (string, string) {
		rows, err := runtimeMatrix(job.Strategy.Matrix)
		return installedRuntime(rows, err, func(row map[string]any) (string, string, bool) {
			if active, err := possibleJobCondition(job.If, row); err == nil && !active {
				return "", "", false
			}
			// The tests run on the last selection before them.
			version, source := "", ""
			for _, step := range stepsThroughFirstTest(job.Steps, func(step runtimeStep) bool { return pythonTestStep(root, workflow, job, step, row) }) {
				if active, err := stepCondition(step, row, conditionEnvironment(workflow, job, step, row)); err != nil || !active {
					continue
				}
				if selected := step.Runtime["python"]; selected != "" {
					version, source = selected, step.Source
				} else if uses, _, _ := strings.Cut(strings.ToLower(step.Uses), "@"); uses == "actions/setup-python" {
					if selected, err := runtimeValue(step.With["python-version"], row); err == nil && selected != "" {
						version, source = selected, "actions/setup-python"
					}
				}
			}
			return version, source, true
		})
	},
	check: func(ctx context.Context, root, framework string) RuntimeCheck {
		// Offline: no release metadata is needed to find unknown runtimes.
		offline := func(context.Context, string) (PythonRelease, error) {
			return PythonRelease{}, errors.New("ddtrace metadata is not read offline")
		}
		return checkPythonCIRuntimes(ctx, root, "", "", offline, func(context.Context, string) (string, error) {
			return "", errors.New("action metadata is not read offline")
		})
	},
	runtime:  func(finding RuntimeFinding) string { return finding.Runtime },
	testStep: pythonTestStep,
}

// pythonTestStep reports whether step runs pytest on a matrix row.
func pythonTestStep(root string, workflow ciWorkflow, job runtimeJob, step runtimeStep, row map[string]any) bool {
	if active, err := stepCondition(step, row, conditionEnvironment(workflow, job, step, row)); err == nil && !active {
		return false
	}
	resolved, _, err := resolveVariantStep(workflow, job, step, row, "pytest")
	if err != nil {
		resolved = step
	}
	return resolvePythonStep(root, workflow, job, resolved).Matched
}

// pythonSelectsShellRuntime reports a shell command that puts another Python
// first on PATH: a virtual environment's activate script, pyenv, conda or
// mamba activate.
func pythonSelectsShellRuntime(words []string) bool {
	if len(words) == 0 {
		return false
	}
	switch name := path.Base(words[0]); name {
	case "pyenv":
		return len(words) > 1 && slices.Contains([]string{"shell", "local", "global", "activate"}, words[1])
	case "conda", "mamba", "micromamba":
		return len(words) > 1 && words[1] == "activate"
	case "source", ".":
		return len(words) > 1 && path.Base(words[1]) == "activate"
	}
	return false
}

// pythonSetupExports are the installer's exports: the tracer before the
// job's PYTHONPATH, and --ddtrace before its pytest options. Later commands
// expand them; a variable the job already has also changes in their
// environment. Otherwise they stay references, as on GitHub Actions, where
// the tests' environment receives them without a step's own value: other
// languages' commands, after a setup for several, keep their environment.
func pythonSetupExports(current map[string]string) (environment, references map[string]string) {
	tracerPath := pythonTracerPath
	if existing := current["PYTHONPATH"]; existing != "" {
		tracerPath += ":" + existing
	}
	return pythonExports(current, tracerPath, strings.TrimSpace("--ddtrace "+current["PYTEST_ADDOPTS"]))
}

// pythonPluginExports are the CI plugin's: it joins each value and the
// existing one with a space, so an existing PYTHONPATH's first entry starts
// with one.
func pythonPluginExports(current map[string]string) (environment, references map[string]string) {
	tracerPath, addopts := pythonTracerPath+":", "--ddtrace"
	if existing, set := current["PYTHONPATH"]; set {
		tracerPath += " " + existing
	}
	if existing, set := current["PYTEST_ADDOPTS"]; set {
		addopts += " " + existing
	}
	return pythonExports(current, tracerPath, addopts)
}

// pythonExports splits a setup's PYTHONPATH and PYTEST_ADDOPTS into the
// variables it changes in the environment (those current already has) and
// references later commands expand.
func pythonExports(current map[string]string, tracerPath, addopts string) (environment, references map[string]string) {
	references = map[string]string{"PYTHONPATH": tracerPath, "PYTEST_ADDOPTS": addopts}
	environment = map[string]string{}
	for key, value := range references {
		if _, set := current[key]; set {
			environment[key] = value
		}
	}
	return environment, references
}

// pythonKeepsTracerPath reports a PYTHONPATH value that still holds the
// tracer the setup put there: the setup action's exported value on GitHub
// Actions, or the installer's or plugin's path.
func pythonKeepsTracerPath(value string) bool {
	return slices.ContainsFunc(strings.FieldsFunc(value, func(r rune) bool { return r == ':' || r == ';' }), func(part string) bool {
		return pythonPathReference(part) || strings.TrimSpace(part) == pythonTracerPath
	})
}

// pythonPluginEnvironmentProblem explains a PYTHONPATH or PYTEST_ADDOPTS a
// pipeline's, stage's or withEnv environment sets for a step the CI plugin
// configures: the plugin joins its PYTHONPATH and the pipeline's with a space,
// so Python reads that entry as a directory starting with one, and sets both
// variables once per agent, so a stage's own value is lost. Empty otherwise.
func pythonPluginEnvironmentProblem(provider ciProvider, workflow ciWorkflow, job runtimeJob, step runtimeStep) string {
	var keys []string
	for _, key := range []string{"PYTHONPATH", "PYTEST_ADDOPTS"} {
		_, pipeline := workflow.Env[key]
		_, stage := job.Env[key]
		if pipeline || stage || strings.HasPrefix(step.Env[key], pythonTracerPath+": ") {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	return "This test command's " + strings.Join(keys, " and ") + " comes from an environment directive or withEnv, which the " + provider.setupName() + " overwrites: it sets PYTHONPATH and PYTEST_ADDOPTS once per agent, joining its tracer path and the existing PYTHONPATH with a space, so Python reads that entry as a directory starting with a space. Remove " + strings.Join(keys, " and ") + " from the environment directive or withEnv and extend it inside the sh script that runs the tests instead, such as export PYTHONPATH=\"$PYTHONPATH:<paths>\" before the test command."
}

// pythonSetupProblem explains why the setup before an instrumented CI entry
// is not complete as its provider reads it: a supported release, an API key
// from the provider's secrets, an exact ddtrace release and the selected
// site (empty: any). Empty when it is.
func pythonSetupProblem(provider ciProvider, setup ciSetup, inputs map[string]string, requested, site string) string {
	resolved := map[string]string{}
	for key, value := range inputs {
		resolved[key] = value
	}
	if _, set := setup.Inputs["python-tracer-version"]; set || !setup.Floating {
		resolved["python-tracer-version"] = requested
	}
	return provider.setupProblem(ciSetup{Reference: setup.Reference, Pinned: true, Supported: setup.Supported, Problem: setup.Problem, Floating: setup.Floating, Inputs: resolved}, "python-tracer-version", exactPythonRelease.MatchString, pythonExampleRelease, site)
}

// pythonConfirmationProblem lists the provider facts the user has not
// confirmed (--ci-confirm) that this entry's setup depends on, and how to
// confirm them; empty when every one is confirmed.
func pythonConfirmationProblem(provider ciProvider, setup ciSetup) string {
	var missing []ciFactDefinition
	for _, definition := range ciProviderFacts[provider.name()] {
		if _, stated := statedFact(definition.Key); slices.Contains(setup.Confirmations, definition.Key) && !stated {
			missing = append(missing, definition)
		}
	}
	if len(missing) == 0 {
		return ""
	}
	var questions, flags []string
	for _, fact := range missing {
		flag := fact.Key
		if fact.Values != "" {
			flag += "=" + fact.Values
		}
		questions = append(questions, strings.TrimSpace(fact.Key+": "+fact.Question+" "+fact.Remedy))
		flags = append(flags, "--ci-confirm "+flag)
	}
	return "The " + provider.setupName() + " setup depends on facts no CI configuration shows, so ddtest cannot certify it until the user confirms them. Ask the user; never assume or check them from this machine. " + strings.Join(questions, " ") + " Then rerun testdrive adding " + strings.Join(flags, " ") + " with the values they confirmed."
}

// pythonUnknownRuntimeReason explains a test entry whose Python ddtest cannot
// read outside GitHub Actions: the job's host's, an image that names no
// minor version, or an image ddtest cannot read.
func pythonUnknownRuntimeReason(provider ciProvider, job runtimeJob, host string) string {
	switch {
	case job.ImageProblem != "":
		return "The job's image cannot be read (" + job.ImageProblem + "), so ddtest cannot tell which Python runs its tests."
	case host != "":
		return "The CI Python is unknown: the tests run on " + host + ", and the configuration selects no Python version. Ask the user which Python version it runs (python --version there), then rerun testdrive with --ci-runtime python=<version> (or <job>:python=<version> per job); never guess it."
	case job.Image != "":
		return "The job's image " + job.Image + " selects no Python minor version; run the tests in an image whose tag names one, such as python:3.12, cimg/python:3.12 or ghcr.io/astral-sh/uv:python3.12-bookworm, to certify this job."
	}
	return "The " + provider.displayName() + " job's Python is unknown; run its tests in an image whose tag names a Python version, such as python:3.12."
}

// pythonMissingSetupReason asks for the provider's setup before a pytest
// entry.
func pythonMissingSetupReason(provider ciProvider) string {
	if provider.sharedSyntax() {
		return "No Datadog Python action runs before this pytest step. Preserve the test command and add instrumentation."
	}
	return "No " + provider.setupName() + " selecting python runs before this pytest command. Preserve the test command and add the setup before it."
}

// pythonFloatingCaveat states the exception to an exact release: a setup
// that installs the latest ddtrace when each build runs.
func pythonFloatingCaveat(setup ciSetup, version string) []string {
	return append(slices.Clone(setup.Caveats), "ddtrace version: the "+setup.Reference+" runs pip install -U ddtrace on the build agent when each build runs and cannot pin a release; ddtest checked the CI Python against, and validated locally with, ddtrace@"+version+", the latest release when it ran. A later release can change behavior or drop support for the agents' Python.",
		"PYTHONPATH: the "+setup.Reference+" ends its PYTHONPATH with a separator, which also puts each test process's working directory on its import path; local validation does not.")
}

// pythonSetupRequest is the Python part of an installer setup (the GitLab
// script, the CircleCI orb, the Buildkite installer).
func pythonSetupRequest(root, framework, site string, terms ciSetupTerms) ciSetupRequest {
	request := ciSetupRequest{Language: "python", Site: site, TracerInput: "python-tracer-version", TracerVersion: "<exact ddtrace release>"}
	request.Notes = []string{
		"Replace <exact ddtrace release> with an exact ddtrace release that supports every instrumented Python, such as " + pythonExampleRelease + " (which requires Python >=3.9,<3.15); when the tests' environment already installs ddtrace, use that release. Local validation runs that release (ddtest testdrive --tracer-version), and a floating one cannot be validated. Keep the existing requirements, lockfiles, Python images, virtualenv, uv, poetry and tox commands, pytest options and test arguments.",
		"The " + terms.Installer + " creates its own virtual environment (.dd_civis_env, in the working directory) with the python on PATH, installs ddtrace and coverage there with pip, and exports PYTHONPATH (the tracer before the job's PYTHONPATH) and PYTEST_ADDOPTS=--ddtrace (before the job's options) for " + terms.Later + ". The tests keep their own environment (virtualenv, uv run, poetry run): PYTHONPATH adds the tracer to it. Its compiled modules load only on the Python version that installed them, so put " + terms.SetupEntry + " where python and pip on PATH are the tests' Python version: after any step that selects another Python, such as pyenv, conda activate or a uv-managed Python.",
		"A later command that sets PYTHONPATH or PYTEST_ADDOPTS must extend them with " + terms.Persist(`export PYTHONPATH="$PYTHONPATH:<paths>"`) + " (likewise PYTEST_ADDOPTS), never replace them. Do not add --ddtrace or set DD_CIVISIBILITY_ENABLED yourself. If CI disables pytest plugin autoload (PYTEST_DISABLE_PLUGIN_AUTOLOAD or --disable-plugin-autoload), load the plugin explicitly with -p ddtrace in the existing pytest command.",
		"If CI runs pytest through tox, the " + terms.Installer + "'s variables must reach the tox environment: extend its pass_env (passenv in INI) with DD_*, PYTHONPATH and PYTEST_ADDOPTS, preserving its entries, and run tox on the same Python version as " + terms.Entry + ". For nox or another wrapper, the same variables must reach its pytest process.",
		"ddtest reads the Python of each instrumented job from its image, such as python:3.12, python:3.12-slim, cimg/python:3.12 or ghcr.io/astral-sh/uv:python3.12-bookworm. A job on the runner's or agent's own Python, or in an image whose name selects no Python minor version (python:latest, python:3, an image by digest), stays unverified: ask the user which Python it runs and validate with --ci-runtime python=<version> (or <job>:python=<version>); never guess it. Those jobs also need python and pip on PATH for the installer.",
		setupImageNote(root, "python", framework),
		setupUnsupportedNote(root, "python", framework),
		pythonUnsupportedEntriesNote(terms),
		"Exclude .testoptimization/ from Git (add /.testoptimization/ to the repository .gitignore).",
	}
	request.Notes = slices.DeleteFunc(request.Notes, func(note string) bool { return note == "" })
	return request
}

// pythonUnsupportedEntriesNote says how a job or matrix entry on a Python
// the tracer does not support keeps its tests: without the setup, and without
// leaving the matrix, which pytest validation keeps as the first check saw it.
// Only CircleCI can skip a shared setup for one entry, with a condition on its
// parameter that the configuration resolves.
func pythonUnsupportedEntriesNote(terms ciSetupTerms) string {
	shared := "tell the user that ddtest cannot certify those entries, and do not remove, move or change them"
	if activeOrDefaultCIProvider().name() == circleCIProvider {
		shared = "skip " + terms.Entry + " for them with a condition on the parameter that selects the Python, such as unless: {condition: {equal: [\"3.15\", << parameters.python_version >>]}, steps: [<the orb step>]} (pass the parameter to a reusable command that holds the step)"
	}
	return "If a job or matrix entry runs a Python the tracer does not support (such as 3.15 for ddtrace " + pythonExampleRelease + ", 3.8 or PyPy), keep the entry and its tests. A job that runs only such a Python stays without " + terms.Entry + ", and ddtest reports it as excluded. When " + terms.SharedMatrix + ", or a template or reusable command holds " + terms.Entry + ", for such entries and supported ones alike, " + shared + ". Never drop matrix entries or move them to another job: pytest validation refuses a matrix whose entries changed after its first check. Report each exclusion and its reason; do not guard the tests or change Python versions to obtain a pass. At least one instrumented job is required."
}

// pythonPluginSetupRequest is the Python part of a CI server plugin's setup
// (the Datadog Jenkins plugin): it installs the latest ddtrace with the
// agent's default pip and sets PYTHONPATH and PYTEST_ADDOPTS for every step.
func pythonPluginSetupRequest(root, framework, site string, terms ciSetupTerms) ciSetupRequest {
	request := ciSetupRequest{Language: "python", Site: site}
	request.Notes = []string{
		"The " + terms.Installer + " runs pip install -U ddtrace with the build agent's default pip (the one on the agent's PATH, with the agent's own environment, not the pipeline's) when each build runs, so it always installs the latest ddtrace release and cannot pin one: ddtest validates locally with the latest release (run testdrive without --tracer-version) and reports that it floats. It then sets PYTHONPATH to that pip's site-packages and PYTEST_ADDOPTS=--ddtrace for " + terms.Later + ".",
		"That pip must belong to the Python version the tests run with, since the tracer's compiled modules load only there, and must be able to install packages: on agents with only pip3 on PATH, or whose system Python refuses pip installs (PEP 668, as on Debian 12 or Ubuntu 24.04), the plugin fails and the tests run untraced. Tests in a virtualenv a stage creates keep working when it uses that Python version: PYTHONPATH adds the tracer to it. ddtest asks the user which Python the agents run (--ci-runtime python=<version>) and, when a stage activates a virtualenv or another Python (source .venv/bin/activate, pyenv, conda activate), to confirm that the agent's default pip is the tests' Python (--ci-confirm " + jenkinsRuntimeMatches + ").",
		"The " + terms.Installer + " joins its PYTHONPATH and the pipeline's with a space, which breaks the pipeline's entries, and sets both variables once per build: do not set PYTHONPATH or PYTEST_ADDOPTS in an environment directive or withEnv; extend them inside the sh script that runs the tests (export PYTHONPATH=\"$PYTHONPATH:<paths>\" before the test command). Do not add --ddtrace or set DD_CIVISIBILITY_ENABLED yourself. If CI disables pytest plugin autoload, load the plugin explicitly with -p ddtrace in the existing pytest command.",
		"If CI runs pytest through tox, extend the tox environment's pass_env (passenv in INI) with DD_*, PYTHONPATH and PYTEST_ADDOPTS, preserving its entries; for nox or another wrapper, the same variables must reach its pytest process.",
		setupUnsupportedNote(root, "python", framework),
		"If a stage runs a Python the tracer does not support (such as 3.15 for ddtrace " + pythonExampleRelease + ", 3.8 or PyPy), tell the user: the " + terms.Installer + " instruments every stage of the pipeline, so it cannot leave that stage out. Report it; do not drop stages or change Python versions to obtain a pass.",
		"Exclude .testoptimization/ from Git (add /.testoptimization/ to the repository .gitignore).",
	}
	request.Notes = slices.DeleteFunc(request.Notes, func(note string) bool { return note == "" })
	return request
}
