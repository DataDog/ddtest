// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Virtual environments in CI. A job commonly creates one with its Python
// (python -m venv venv), activates it (source venv/bin/activate) and runs
// pytest there. The tracer the Datadog setup installs joins it through
// PYTHONPATH, so what matters is that the environment runs the Python version
// the setup installed the tracer for: one the job creates with its own Python
// does. Activation is not replayed: local validation runs in the environment
// the user activates, as for dependency installation.

// pythonActivationReview is how discovery reports an activation it leaves
// to the user.
const pythonActivationReview = "Prepare and activate the same virtual environment locally before validation; CI's activation is not replayed."

// pythonActivated is the virtual environment directory a statement activates
// (source or . <dir>/bin/activate, a literal repository-local path); empty
// for any other statement.
func pythonActivated(stmt *syntax.Stmt) string {
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok || stmt.Negated || stmt.Background || stmt.Coprocess || len(stmt.Redirs) > 0 || len(call.Assigns) > 0 || len(call.Args) != 2 {
		return ""
	}
	if name := call.Args[0].Lit(); name != "source" && name != "." {
		return ""
	}
	script, err := literalShellWord(call.Args[1])
	if err != nil || path.Base(script) != "activate" || path.Base(path.Dir(script)) != "bin" {
		return ""
	}
	directory := path.Dir(path.Dir(script))
	if !filepath.IsLocal(directory) || strings.ContainsAny(directory, "$`~") {
		return ""
	}
	return path.Clean(directory)
}

// pythonWithoutActivations removes the statements of a shell command that
// only activate a virtual environment or deactivate one, also at the start of
// an && chain, returning the rest of the command and the environments
// activated. A command that cannot be parsed, or holds neither, is returned
// unchanged.
func pythonWithoutActivations(command string) (rest string, activated []string) {
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(command), "")
	if err != nil {
		return command, nil
	}
	// strip removes the activations leading a statement, through the left
	// operands of an && chain, which the parser associates to the left
	// (a && b && c is (a && b) && c); nil when nothing else remains.
	stripped := false
	var strip func(*syntax.Stmt) *syntax.Stmt
	strip = func(stmt *syntax.Stmt) *syntax.Stmt {
		if binary, chain := stmt.Cmd.(*syntax.BinaryCmd); chain && binary.Op == syntax.AndStmt && len(stmt.Redirs) == 0 && !stmt.Negated && !stmt.Background {
			if left := strip(binary.X); left != nil {
				binary.X = left
				return stmt
			}
			return strip(binary.Y)
		}
		if directory := pythonActivated(stmt); directory != "" {
			activated = append(activated, directory)
			stripped = true
			return nil
		}
		if pythonDeactivates(stmt) {
			stripped = true
			return nil
		}
		return stmt
	}
	var kept []*syntax.Stmt
	for _, stmt := range file.Stmts {
		if stmt := strip(stmt); stmt != nil {
			kept = append(kept, stmt)
		}
	}
	if !stripped {
		return command, nil
	}
	var lines []string
	for _, stmt := range kept {
		var text strings.Builder
		if err := syntax.NewPrinter().Print(&text, stmt); err != nil {
			return command, nil
		}
		lines = append(lines, text.String())
	}
	return strings.Join(lines, "\n"), activated
}

// pythonDeactivates reports a statement that only runs deactivate.
func pythonDeactivates(stmt *syntax.Stmt) bool {
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	return ok && !stmt.Negated && !stmt.Background && !stmt.Coprocess && len(stmt.Redirs) == 0 && len(call.Assigns) == 0 && len(call.Args) == 1 && call.Args[0].Lit() == "deactivate"
}

// pythonVenvs follows the virtual environments a job's steps create and
// activate, in order. An environment runs the Python that created it: the
// job's Python of that moment for python or python3 -m venv and virtualenv
// without an interpreter option; a Python ddtest cannot tell for any other,
// such as uv venv's or one restored from a cache. Created environments are
// the job's files and outlive the shell that created them; an activation
// lasts only as long as its shell (see shell).
type pythonVenvs struct {
	// created maps an environment's directory to the Python version that
	// created it; empty when ddtest cannot tell it.
	created map[string]string
	// active is the environment the current shell activated; empty for
	// none.
	active string
	// unresolved is the activation the current shell ran that ddtest cannot
	// resolve to a repository directory, such as source $VENV/bin/activate
	// with an unknown $VENV or one inside a condition.
	unresolved string
}

// shell starts a new shell: GitHub Actions and CircleCI start one for every
// step; GitLab CI, Buildkite and Jenkins share one between the statements of
// a script (runtimeStep.sameShell).
func (v *pythonVenvs) shell() {
	v.active, v.unresolved = "", ""
}

// step follows one step's resolved command through the current shell, in
// order, and returns why ddtest cannot certify the Python its first pytest or
// tox invocation runs; empty when each runs the job's Python. python is the
// job's Python version (empty when unknown) and directory the step's.
func (v *pythonVenvs) step(command, python, directory string, environment map[string]string) string {
	if v.created == nil {
		v.created = map[string]string{}
	}
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(command), "")
	if err != nil {
		if pythonActivationText.MatchString(command) {
			v.active, v.unresolved = "", strings.TrimSpace(command)
		}
		return ""
	}
	// lost marks a directory change ddtest cannot follow: later paths are
	// not the repository's.
	lost, problem := false, ""
	invocation := func() {
		if problem == "" {
			problem = v.reason(python)
		}
	}
	var visit func(*syntax.Stmt)
	visit = func(stmt *syntax.Stmt) {
		if binary, ok := stmt.Cmd.(*syntax.BinaryCmd); ok && binary.Op == syntax.AndStmt && !stmt.Negated && !stmt.Background && len(stmt.Redirs) == 0 {
			visit(binary.X)
			visit(binary.Y)
			return
		}
		if activated := pythonActivated(stmt); activated != "" && !lost {
			v.active, v.unresolved = path.Join(directory, activated), ""
			return
		}
		words, _, err := literalCall(stmt)
		if err != nil {
			// Control flow, a redirection or an expansion: an activation
			// or cd inside it, in any order, leaves the environment
			// unresolved for the invocations it holds and later ones.
			activation, changesDirectory, invokes := pythonOpaqueStatement(stmt)
			if activation != "" {
				v.active, v.unresolved = "", activation
			}
			lost = lost || changesDirectory
			if invokes {
				invocation()
			}
			return
		}
		command, assignments, err := commandEnvironment(words)
		if err != nil || len(command) == 0 {
			if pythonTestReference.MatchString(strings.Join(words, " ")) {
				invocation()
			}
			return
		}
		words = command
		switch {
		case pythonActivates(words):
			v.active, v.unresolved = "", shellText(stmt)
		case words[0] == "deactivate" && len(words) == 1:
			v.active, v.unresolved = "", ""
		case words[0] == "pushd" || words[0] == "popd":
			lost = true
		case words[0] == "cd":
			if len(words) != 2 || !filepath.IsLocal(words[1]) || strings.ContainsAny(words[1], "$`~") {
				lost = true
			} else {
				directory = path.Join(directory, words[1])
			}
		case pythonTestInvocation(words):
			invocation()
		case !lost:
			// An active environment's Python creates the next one; an
			// activation ddtest cannot resolve leaves it unknown.
			creationPython := python
			if v.unresolved != "" {
				creationPython = ""
			} else if v.active != "" {
				creationPython = v.created[v.active]
			}
			if created, version := pythonCreatedVenv(words, creationPython, mergeEnvironment(environment, assignments)); created != "" {
				v.created[path.Join(directory, created)] = version
			}
		}
	}
	for _, stmt := range file.Stmts {
		visit(stmt)
	}
	return problem
}

// pythonActivationText finds text that may activate a virtual environment.
var pythonActivationText = regexp.MustCompile(`(?:^|[^a-zA-Z0-9_])(?:activate|deactivate)(?:[^a-zA-Z0-9_]|$)`)

// pythonActivates reports a command, by its literal words, that activates an
// environment ddtest does not resolve: source or . of an activate script
// (absolute, outside the repository, or a variable left unresolved), or
// conda's, mamba's and pyenv's activate.
func pythonActivates(words []string) bool {
	switch name := path.Base(words[0]); {
	case name == "source" || name == ".":
		return slices.ContainsFunc(words[1:], func(word string) bool { return pythonActivationText.MatchString(word) })
	case slices.Contains([]string{"conda", "mamba", "micromamba", "pyenv"}, name):
		return len(words) > 1 && (words[1] == "activate" || words[1] == "shell")
	}
	return false
}

// pythonTestInvocation reports a command, by its words, that runs pytest or
// tox.
func pythonTestInvocation(words []string) bool {
	words, _, err := pythonCommandEnvironment(words)
	if err != nil {
		return true // An invocation whose environment ddtest refuses.
	}
	_, tox := pythonRunnerArguments(words, "tox")
	return tox || pythonInvocation(words)
}

// pythonOpaqueStatement reads a statement ddtest does not follow: the first
// activation it holds, whether it may change directory, and whether it may
// run pytest or tox.
func pythonOpaqueStatement(stmt *syntax.Stmt) (activation string, changesDirectory, invokes bool) {
	syntax.Walk(stmt, func(node syntax.Node) bool {
		call, ok := node.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		var words []string
		for _, word := range call.Args {
			words = append(words, shellText(word))
		}
		if activation == "" && (pythonActivates(words) || words[0] == "deactivate") {
			activation = shellText(stmt)
		}
		changesDirectory = changesDirectory || slices.Contains([]string{"cd", "pushd", "popd"}, words[0])
		invokes = invokes || pythonTestReference.MatchString(strings.Join(words, " "))
		return true
	})
	return activation, changesDirectory, invokes
}

// pythonCreatedVenv is the directory a command creates a virtual environment
// in, and the version of the Python that runs it: the job's (version python)
// when the command uses it, empty when ddtest cannot tell.
func pythonCreatedVenv(words []string, python string, environment map[string]string) (string, string) {
	arguments, own := []string(nil), false
	switch base := path.Base(words[0]); {
	case base == "virtualenv":
		arguments, own = words[1:], true
	case base == "uv" && len(words) > 1 && words[1] == "venv":
		// uv selects its own interpreter, possibly one it downloads.
		arguments = words[2:]
		if !slices.ContainsFunc(arguments, func(argument string) bool { return !strings.HasPrefix(argument, "-") }) {
			return ".venv", ""
		}
	default:
		index, module := PythonModule(words[1:])
		if index < 0 || len(module) == 0 || module[0] != "venv" && module[0] != "virtualenv" {
			return "", ""
		}
		arguments, own = module[1:], !pythonRuntimeOverride(words[:1], python, environment)
	}
	directory := ""
	for i := 0; i < len(arguments); i++ {
		option, _, assigned := strings.Cut(arguments[i], "=")
		switch {
		case option == "-p" || option == "--python":
			own = false
			if !assigned {
				i++
			}
		case option == "--prompt":
			if !assigned {
				i++
			}
		case strings.HasPrefix(arguments[i], "-"):
		default:
			directory = arguments[i]
		}
	}
	if directory == "" || !filepath.IsLocal(directory) || strings.ContainsAny(directory, "$`~") {
		return "", ""
	}
	if !own {
		python = ""
	}
	return path.Clean(directory), python
}

// reason explains why ddtest cannot certify the Python the current shell
// runs a test with, against the job's Python python; empty when it runs that
// Python.
func (v *pythonVenvs) reason(python string) string {
	if v.unresolved != "" {
		return "The tests run after `" + v.unresolved + "`, which activates a virtual environment ddtest cannot resolve to one this job creates, so ddtest cannot tell which Python version runs them and loads the tracer. Activate a repository-local environment the job creates with its own Python (such as python -m venv venv), or validate that environment separately."
	}
	if v.active == "" {
		return ""
	}
	version, created := v.created[v.active]
	switch {
	case !created || version == "":
		return "The tests run in the virtual environment " + v.active + ", which this job does not create with its own Python before activating it (such as python -m venv " + v.active + "), so ddtest cannot tell which Python version runs them and loads the tracer. Create it in the job with the job's Python, or validate that environment separately."
	case version != python:
		current := "a Python version ddtest cannot tell"
		if python != "" {
			current = "Python " + python
		}
		return "The tests run in the virtual environment " + v.active + ", which this job created with Python " + version + ", but the job selects " + current + " before they run: the version the tracer is installed and validated for, and its compiled modules load only on that version. Create the environment after selecting the tests' Python, or validate that environment separately."
	}
	return ""
}
