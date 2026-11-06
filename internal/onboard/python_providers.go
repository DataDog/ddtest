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
	"strconv"
	"strings"

	"github.com/kballard/go-shellquote"
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
	// The installer always appends :$PYTHONPATH, including an empty entry
	// when it was unset. Python interprets that entry as the working directory.
	tracerPath := pythonTracerPath + ":" + current["PYTHONPATH"]
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

// GitReportExclusion prescribes the report directory's Git exclusion, which
// validation checks with git check-ignore (see testdrive's
// git_report_exclusions); standalone validation asks for it instead.
const GitReportExclusion = "Exclude .testoptimization/ from Git (add /.testoptimization/ to the repository .gitignore)."

// CodeCIEditRequired marks a CI entry whose setup is in place but whose
// test command needs the exact edit its reason gives, such as a pytest
// command that replaces the setup's PYTHONPATH.
const CodeCIEditRequired = "ci_edit_required"

// pythonLiteralPath is a PYTHONPATH value that needs no quoting inside
// double quotes.
var pythonLiteralPath = regexp.MustCompile(`^[A-Za-z0-9_./@%+,:=-]*$`)

// pythonPathCommand is a pytest or tox command whose inline assignment
// replaces PYTHONPATH: words as written, command without its assignments,
// and the value it sets.
type pythonPathCommand struct {
	words, command []string
	value          string
}

// pythonPathCommands are those of commands.
func pythonPathCommands(commands [][]string) []pythonPathCommand {
	var needed []pythonPathCommand
	for _, words := range commands {
		command, assignments, err := pythonCommandEnvironment(words)
		value, set := assignments["PYTHONPATH"]
		if err != nil || !set || pythonKeepsTracerPath(value) {
			continue
		}
		if _, tox := pythonRunnerArguments(command, "tox"); !tox && !pythonInvocation(command) {
			continue
		}
		needed = append(needed, pythonPathCommand{words, command, value})
	}
	return needed
}

// pythonInlinePathValues are the distinct non-empty PYTHONPATH values those
// of commands set: the rewrite extends PYTHONPATH with each.
func pythonInlinePathValues(commands [][]string) []string {
	var values []string
	for _, c := range pythonPathCommands(commands) {
		if c.value != "" && !slices.Contains(values, c.value) {
			values = append(values, c.value)
		}
	}
	return values
}

// pythonSavedPath is the shell variable a PYTHONPATH rewrite that is undone
// after its command saves the path in (see pythonInlinePathEdit).
const pythonSavedPath = "datadog_saved_pythonpath"

// pythonInlinePathEdit is the exact rewrite of a pytest or tox command whose
// inline assignment replaces PYTHONPATH (PYTHONPATH=src pytest): every setup
// puts the tracer on PYTHONPATH before the tests run (the installer exports
// PYTHONPATH=<tracer>:<coverage>:$PYTHONPATH into the job's shell, the
// action into the job's environment, the CI plugin into each step's), so the
// command's own value drops it and its tests run untraced. The rewrite
// extends the variable instead, in the form the CI check follows: an export
// in the same script, or on GitHub Actions the step's env. A step's env
// holds one PYTHONPATH, so a GitHub Actions step whose commands set
// different ones is split first: each command keeps its own path. restore
// reports other commands of the export's shell (behind the CircleCI orb, of
// its job, which $BASH_ENV reaches) that set another path: each export is
// then undone after its command, which keeps every path to its own command,
// and a CircleCI step behind the orb whose commands set different ones is
// split first. The setup without Bash or curl runs in the test's shell, so
// its run step's rewrite goes there, as in a script. Empty when no command
// needs it.
func pythonInlinePathEdit(provider ciProvider, commands [][]string, restore bool) string {
	needed := pythonPathCommands(commands)
	if len(pythonInlinePathValues(commands)) > 1 && (provider.sharedSyntax() || pythonBashEnvSetup(provider)) {
		var listed []string
		for _, c := range needed {
			listed = append(listed, "`"+shellCommand(c.words)+"`")
		}
		split := "A step's env holds one PYTHONPATH: move each of these commands into its own step, unchanged and in the same order, with the step's other settings."
		if !provider.sharedSyntax() {
			split = "$BASH_ENV extends PYTHONPATH for a whole run step: move each of these commands into its own run step, unchanged and in the same order, with the step's other settings."
		}
		return strings.Join(listed, ", ") + " set different PYTHONPATH values in one step, and each replaces the PYTHONPATH the " + provider.setupName() + " sets, which carries the tracer, so its tests run untraced. " + split + " ddtest then gives each step its own edit."
	}
	var edits []string
	for _, c := range needed {
		words, command, value := c.words, c.command, c.value
		var rest []string
		for i, word := range words {
			if i < len(words)-len(command) && strings.HasPrefix(word, "PYTHONPATH=") {
				continue
			}
			rest = append(rest, word)
		}
		original, kept := shellCommand(words), shellCommand(rest)
		replaces := "`" + original + "` replaces the PYTHONPATH the " + provider.setupName() + " sets, which carries the tracer, so its tests run untraced."
		if value == "" {
			edits = append(edits, replaces+" Remove its empty PYTHONPATH= assignment: run `"+kept+"` instead.")
			continue
		}
		if provider.sharedSyntax() {
			edits = append(edits, replaces+" Run `"+kept+"` instead, and add PYTHONPATH: ${{ env.PYTHONPATH }}:"+value+" to the step's env, which extends the action's path.")
			continue
		}
		// The separator only follows a set PYTHONPATH: with the setup
		// skipped (no API key, failed install) a leading empty entry would
		// put the working directory on sys.path.
		export := `export PYTHONPATH="${PYTHONPATH:+$PYTHONPATH:}` + value + `"`
		if !pythonLiteralPath.MatchString(value) {
			export = `export PYTHONPATH="${PYTHONPATH:+$PYTHONPATH:}"` + shellquote.Join(value)
		}
		// The undo restores the value saved before the export, which may be
		// empty or unset, when the export added no separator: an empty
		// PYTHONPATH adds nothing to sys.path.
		undo := `export PYTHONPATH="$` + pythonSavedPath + `"`
		if restore {
			export = pythonSavedPath + `="${PYTHONPATH:-}"; ` + export
		}
		where := "in the same place of the same script"
		switch {
		case provider.name() == circleCIProvider && !pythonBashEnvSetup(provider):
			// The setup without Bash or curl exports into its run step's
			// shell only, before the test command.
			where = "in the same place of the same run step's command, after the " + provider.setupName()
		case provider.name() == circleCIProvider:
			// A run step follows no exports; $BASH_ENV carries them.
			if restore {
				edits = append(edits, replaces+" Run `"+kept+"` in its place, add the step `"+provider.setupTerms().PersistEntry(export)+"` before that step, after the orb step, and the step `"+provider.setupTerms().PersistEntry(undo)+"` right after it: $BASH_ENV gives the step the extended path, and the later steps, which set other paths, the one they had.")
				continue
			}
			edits = append(edits, replaces+" Run `"+kept+"` in its place, and add the step `"+provider.setupTerms().PersistEntry(export)+"` before that step, after the orb step: $BASH_ENV gives the step the extended path.")
			continue
		case provider.name() == buildkiteProvider:
			export, kept, undo = buildkiteEscape(export), buildkiteEscape(kept), buildkiteEscape(undo)
			where = "in the same place of the step's commands (every $ written as $$)"
		case provider.name() == jenkinsProvider:
			where = "in the same place of the same sh script (inside a double-quoted Groovy string, write each $ as \\$)"
		}
		if restore {
			edits = append(edits, replaces+" Replace it, "+where+", with the three commands `"+export+"`, `"+kept+"` and `"+undo+"`: the last one gives the later commands, which set other paths, the PYTHONPATH they had.")
			continue
		}
		edits = append(edits, replaces+" Replace it, "+where+", with the two commands `"+export+"` and `"+kept+"`.")
	}
	return strings.Join(edits, " ")
}

// pythonBashEnvSetup reports a setup whose exports reach a job's later steps
// through $BASH_ENV, the CircleCI orb's: a path edit there goes in a step of
// its own. The setup without Bash or curl a CircleCI job runs instead
// exports into its run step's shell only, as a script's setup does.
func pythonBashEnvSetup(provider ciProvider) bool {
	_, fallback := provider.(imageFallback)
	return provider.name() == circleCIProvider && !fallback
}

// CIEditRequired lists the CI test commands that need an exact edit before
// their setup instruments them, as "location: edit".
type CIEditRequired struct {
	Locations, Edits []string
}

func (e *CIEditRequired) Error() string {
	return "These CI test commands drop the tracer path the Datadog setup puts on PYTHONPATH; make exactly these edits: " + strings.Join(ciEditList(e), " ")
}

func ciEditList(e *CIEditRequired) []string {
	var edits []string
	for i, location := range e.Locations {
		edits = append(edits, location+": "+e.Edits[i])
	}
	return edits
}

// CIEditRequiredResult hands the edits to the agent, then resume.
func CIEditRequiredResult(err *CIEditRequired, resume []string) AgentResult {
	return AgentResult{
		Status: "needs_input", Phase: "onboarding", Code: CodeCIEditRequired, NextAction: "apply_source_edit",
		Reason: err.Error(), Location: strings.Join(err.Locations, "; "), ResumeCommand: resume,
		RequiredInput: []string{"Only the edits reason names, exactly as given, at " + strings.Join(err.Locations, "; ") + "; then run resume_command."},
		Instruction:   "Make only the edits reason names, exactly as given: change nothing else in those commands or steps, and do not try other forms. Then run resume_command once in the foreground and wait for its exit.",
	}
}

// RequiredCIEdits returns the instrumented CI test commands of language that
// need an exact edit before validation, or nil; pending lists them before
// the setup is in place, too. It reads the configuration offline.
func RequiredCIEdits(ctx context.Context, root, language, framework string, pending bool) *CIEditRequired {
	if language != "python" {
		return nil
	}
	required := &CIEditRequired{}
	for _, finding := range pythonStatedRuntime.check(ctx, root, framework).Jobs {
		edit := finding.pathEdit
		if !pending && finding.Code != CodeCIEditRequired || finding.Status == StatusExcluded || edit == "" {
			continue
		}
		location := finding.Workflow + " / " + finding.Job
		if finding.Step > 0 {
			location += " / step " + strconv.Itoa(finding.Step)
		}
		if !slices.Contains(required.Locations, location) {
			required.Locations = append(required.Locations, location)
			required.Edits = append(required.Edits, edit)
		}
	}
	if len(required.Locations) == 0 {
		return nil
	}
	return required
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
		return "The job's image " + job.Image + " selects no Python minor version, so ddtest cannot certify this job; it reads the Python from image tags that name one, such as python:3.12, cimg/python:3.12 or ghcr.io/astral-sh/uv:python3.12-bookworm. Tell the user; never change the job's image to certify it."
	}
	return "The " + provider.displayName() + " job's Python is unknown, so ddtest cannot certify it; it reads the Python from image tags that name a Python version, such as python:3.12. Tell the user; never change the job's image to certify it."
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
		"PYTHONPATH: the "+setup.Reference+" ends its PYTHONPATH with a separator, which also puts each test process's working directory on its import path; local validation preserves that entry as . (the working directory).")
}

// pythonSetupRequest is the Python part of an installer setup (the GitLab
// script, the CircleCI orb, the Buildkite installer).
// pythonInstallNote says where the setup installs ddtrace: the installer's
// virtual environment, or the fallback setup's folder (see
// pythonDiscovery.ciFallbackInstall).
func pythonInstallNote(terms ciSetupTerms) string {
	if terms.Fallback {
		return "The " + terms.Installer + " installs ddtrace and coverage with python -m pip, using the python on PATH, into a folder of its own ($DD_TRACER_FOLDER/python, in the working directory's .datadog),"
	}
	return "The " + terms.Installer + " creates its own virtual environment (.dd_civis_env, in the working directory) with the python on PATH, installs ddtrace and coverage there with pip,"
}

func pythonSetupRequest(root, framework, site string, terms ciSetupTerms) ciSetupRequest {
	request := ciSetupRequest{Language: "python", Site: site, TracerInput: "python-tracer-version", TracerVersion: pythonSetupRelease()}
	request.Notes = []string{
		pythonReleaseNote(request.TracerVersion) + " Keep the existing requirements, lockfiles, Python images, virtualenv, uv, poetry and tox commands, pytest options and test arguments.",
		pythonInstallNote(terms) + " and exports PYTHONPATH (the tracer before the job's PYTHONPATH) and PYTEST_ADDOPTS=--ddtrace (before the job's options) for " + terms.Later + ". The tests keep their own environment (virtualenv, uv run, poetry run): PYTHONPATH adds the tracer to it. Its compiled modules load only on the Python version that installed them, so put " + terms.SetupEntry + " where python and pip on PATH are the tests' Python version: after any step that selects another Python, such as pyenv, conda activate or a uv-managed Python.",
		"A later command that sets PYTHONPATH or PYTEST_ADDOPTS must extend them with " + terms.Persist(`export PYTHONPATH="${PYTHONPATH:+$PYTHONPATH:}<paths>"`) + " (likewise PYTEST_ADDOPTS), never replace them. Do not add --ddtrace or set DD_CIVISIBILITY_ENABLED yourself. If CI disables pytest plugin autoload (PYTEST_DISABLE_PLUGIN_AUTOLOAD or --disable-plugin-autoload), load the plugin explicitly with -p ddtrace in the existing pytest command.",
		"If CI runs pytest through tox, the " + terms.Installer + "'s variables must reach the tox environment: extend its pass_env (passenv in INI) with DD_*, PYTHONPATH and PYTEST_ADDOPTS, preserving its entries, and run tox on the same Python version as " + terms.Entry + ". For nox or another wrapper, the same variables must reach its pytest process.",
		"ddtest reads the Python of each instrumented job from its image, such as python:3.12, python:3.12-slim, cimg/python:3.12 or ghcr.io/astral-sh/uv:python3.12-bookworm. A job on the runner's or agent's own Python, or in an image whose name selects no Python minor version (python:latest, python:3, an image by digest), stays unverified: ask the user which Python it runs and validate with --ci-runtime python=<version> (or <job>:python=<version>); never guess it. Those jobs also need python and pip on PATH for the installer.",
		setupImageNote(root, "python", framework),
		setupUnsupportedNote(root, "python", framework),
		pythonUnsupportedEntriesNote(terms),
		GitReportExclusion,
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
		GitReportExclusion,
	}
	request.Notes = slices.DeleteFunc(request.Notes, func(note string) bool { return note == "" })
	return request
}
