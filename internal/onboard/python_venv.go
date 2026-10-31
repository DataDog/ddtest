// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"path"
	"path/filepath"
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
// only activate a virtual environment, also at the start of an && chain,
// returning the rest of the command and the environments activated. A
// command that cannot be parsed, or activates none, is returned unchanged.
func pythonWithoutActivations(command string) (rest string, activated []string) {
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(command), "")
	if err != nil {
		return command, nil
	}
	var kept []*syntax.Stmt
	for _, stmt := range file.Stmts {
		for {
			binary, chain := stmt.Cmd.(*syntax.BinaryCmd)
			if !chain || binary.Op != syntax.AndStmt || len(stmt.Redirs) > 0 || stmt.Negated || stmt.Background {
				break
			}
			directory := pythonActivated(binary.X)
			if directory == "" {
				break
			}
			activated = append(activated, directory)
			stmt = binary.Y
		}
		if directory := pythonActivated(stmt); directory != "" {
			activated = append(activated, directory)
			continue
		}
		kept = append(kept, stmt)
	}
	if len(activated) == 0 {
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

// pythonVenvs follows the virtual environments a job's steps create and
// activate, in order: an environment the job creates with its own Python
// (python or python3 -m venv, virtualenv without an interpreter option)
// runs that Python; any other one, such as uv venv's or one restored from a
// cache, runs a Python ddtest cannot tell.
type pythonVenvs struct {
	// created maps an environment's directory to whether the job's Python
	// created it.
	created map[string]bool
	// unknown is the active environment whose Python ddtest cannot tell.
	unknown string
}

// step records what one step's command does to the job's environments;
// python is the job's Python version and directory the step's.
func (v *pythonVenvs) step(command, python, directory string, environment map[string]string) {
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(command), "")
	if err != nil {
		return
	}
	if v.created == nil {
		v.created = map[string]bool{}
	}
	var visit func(*syntax.Stmt)
	visit = func(stmt *syntax.Stmt) {
		if binary, ok := stmt.Cmd.(*syntax.BinaryCmd); ok && binary.Op == syntax.AndStmt {
			visit(binary.X)
			visit(binary.Y)
			return
		}
		if activated := pythonActivated(stmt); activated != "" {
			activated = path.Join(directory, activated)
			v.unknown = ""
			if !v.created[activated] {
				v.unknown = activated
			}
			return
		}
		words, _, err := literalCall(stmt)
		if err != nil {
			return
		}
		words, _, err = commandEnvironment(words)
		if err != nil || len(words) == 0 {
			return
		}
		if created, own := pythonCreatedVenv(words, python, environment); created != "" {
			v.created[path.Join(directory, created)] = own
		}
	}
	for _, stmt := range file.Stmts {
		visit(stmt)
	}
}

// pythonCreatedVenv is the directory a command creates a virtual environment
// in, and whether it runs the job's Python (version python).
func pythonCreatedVenv(words []string, python string, environment map[string]string) (string, bool) {
	arguments, own := []string(nil), false
	switch base := path.Base(words[0]); {
	case base == "virtualenv":
		arguments, own = words[1:], true
	case base == "uv" && len(words) > 1 && words[1] == "venv":
		// uv selects its own interpreter, possibly one it downloads.
		arguments = words[2:]
		if !slices.ContainsFunc(arguments, func(argument string) bool { return !strings.HasPrefix(argument, "-") }) {
			return ".venv", false
		}
	default:
		index, module := PythonModule(words[1:])
		if index < 0 || len(module) == 0 || module[0] != "venv" && module[0] != "virtualenv" {
			return "", false
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
		return "", false
	}
	return path.Clean(directory), own
}

// pythonVenvReason explains a test step that runs in a virtual environment
// whose Python ddtest cannot tell; empty when there is none.
func (v *pythonVenvs) reason() string {
	if v.unknown == "" {
		return ""
	}
	return "The tests run in the virtual environment " + v.unknown + ", which this job does not create with its own Python before activating it (such as python -m venv " + v.unknown + "), so ddtest cannot tell which Python version runs them and loads the tracer. Create it in the job with the job's Python, or validate that environment separately."
}
