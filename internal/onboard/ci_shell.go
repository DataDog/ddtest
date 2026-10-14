// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/kballard/go-shellquote"
	"mvdan.cc/sh/v3/syntax"
)

var embeddedMatrix = regexp.MustCompile(`\$\{\{\s*matrix\.([a-zA-Z0-9_-]+)\s*\}\}`)
var shellMatrixScalar = regexp.MustCompile(`^[a-zA-Z0-9_./@:+ -]*$`)

// Substitute only known scalar matrix data, never evaluate workflow or shell
// code. Reject shell metacharacters rather than changing the command's quoting.
func interpolateMatrix(command string, row map[string]any) (string, error) {
	if row == nil {
		return command, nil
	}
	var err error
	result := embeddedMatrix.ReplaceAllStringFunc(command, func(reference string) string {
		value, e := runtimeValue(reference, row)
		if e != nil || !shellMatrixScalar.MatchString(value) {
			err = fmt.Errorf("matrix substitution requires shell quoting review: %s", reference)
			return reference
		}
		return value
	})
	return result, err
}

func conditionEnvironment(workflow ciWorkflow, job runtimeJob, step runtimeStep, row map[string]any) map[string]string {
	values := map[string]string{}
	for _, layer := range []map[string]string{workflow.Env, job.Env, step.Env} {
		maps.Copy(values, layer)
	}
	for key, value := range values {
		resolved, err := runtimeValue(value, row)
		if err != nil || strings.ContainsRune(resolved, 0) {
			delete(values, key)
		} else {
			values[key] = resolved
		}
	}
	return values
}

func supportedCIShell(shell string) bool {
	if shell == "" || shell == "bash" || shell == "sh" {
		return true
	}
	words, err := shellquote.Split(shell)
	if err != nil || len(words) < 2 || !slices.Contains([]string{"bash", "sh"}, words[0]) || words[len(words)-1] != "{0}" {
		return false
	}
	for i := 1; i < len(words)-1; i++ {
		switch words[i] {
		case "-o", "-eo":
			i++
			if i >= len(words)-1 || words[i] != "pipefail" {
				return false
			}
		case "-e", "-l", "--login", "--noprofile", "--norc":
		default:
			return false
		}
	}
	return true
}

func shellText(node syntax.Node) string {
	var text strings.Builder
	_ = syntax.NewPrinter().Print(&text, node)
	return text.String()
}

func parseCIShell(command string) (*syntax.File, bool) {
	if len(command) > 16384 {
		return nil, false
	}
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(command), "")
	return file, err == nil
}

// Enumerate both literal branches of a read-only npm package-presence check.
// Each original command is validated locally; no shell condition is executed or
// assumed true based on the evaluator's installed dependencies.
func resolveCIShell(root string, workflow ciWorkflow, job runtimeJob, step runtimeStep, framework string) (commandResolution, bool) {
	file, ok := parseCIShell(step.Run)
	if !ok {
		return commandResolution{}, false
	}
	if len(file.Stmts) == 1 {
		stmt := file.Stmts[0]
		branch, ok := stmt.Cmd.(*syntax.IfClause)
		if ok && len(stmt.Redirs) == 0 && !stmt.Background && !stmt.Negated && npmPresenceCondition(branch.Cond) && branch.Else != nil && len(branch.Else.Cond) == 0 {
			var result commandResolution
			for _, body := range [][]*syntax.Stmt{branch.Then, branch.Else.Then} {
				var commands []string
				for _, entry := range body {
					commands = append(commands, shellText(entry))
				}
				child := step
				child.Run = strings.Join(commands, "\n")
				part := resolveTestStep(root, workflow, job, child, "javascript", framework)
				if part.Reason != "" || !part.Matched {
					return commandResolution{}, false
				}
				result.Matched = true
				result.Tests = append(result.Tests, part.Tests...)
				result.Builds = append(result.Builds, part.Builds...)
			}
			result.Evidence = "Both branches of the npm package-presence condition; original commands retained."
			return result, true
		}
	}
	if knownCISetup(file) {
		changesRuntime := false
		syntax.Walk(file, func(n syntax.Node) bool {
			if call, ok := n.(*syntax.CallExpr); ok && len(call.Args) > 1 && call.Args[0].Lit() == "nvm" && slices.Contains([]string{"install", "use"}, call.Args[1].Lit()) {
				changesRuntime = true
			}
			return true
		})
		if changesRuntime && nvmRuntime(step.Run) == "" {
			return unresolvedCommand("NVM runtime selection could not be resolved; preserve this setup and review it."), true
		}
		return commandResolution{Review: true, Reason: "Recognized runtime/dependency/environment setup, not a test entry point. CI setup itself is not executed locally."}, true
	}
	return commandResolution{}, false
}

func npmPresenceCondition(stmts []*syntax.Stmt) bool {
	if len(stmts) != 1 || len(stmts[0].Redirs) != 0 || stmts[0].Negated {
		return false
	}
	binary, ok := stmts[0].Cmd.(*syntax.BinaryCmd)
	if !ok || binary.Op != syntax.Pipe {
		return false
	}
	left, e1 := shellquote.Split(shellText(binary.X))
	right, e2 := shellquote.Split(shellText(binary.Y))
	return e1 == nil && e2 == nil && len(left) == 4 && left[0] == "npm" && left[1] == "-ps" && left[2] == "ls" && len(right) == 3 && right[0] == "grep" && right[1] == "-q" && right[2] == left[3]
}

// This legacy workaround rewrites only the installed npm launcher shebang. Its
// exact shape matters: arbitrary sed rewrites cannot be treated as metadata.
const npmShebangRepair = `sed -i '1s;^.*$;'"$(printf '#!%q' "$(nvm which npm)")"';' "$(readlink -f "$(which npm)")"`

func knownCISetup(file *syntax.File) bool {
	valid, calls := true, 0
	syntax.Walk(file, func(node syntax.Node) bool {
		if !valid {
			return false
		}
		switch part := node.(type) {
		case *syntax.Stmt:
			valid = !part.Background && !part.Coprocess && !part.Disown
			for _, redir := range part.Redirs {
				target := strings.Trim(shellText(redir.Word), `"`)
				valid = valid && ((redir.Op == syntax.AppOut && (target == "$GITHUB_OUTPUT" || target == "$GITHUB_PATH")) || redir.Op == syntax.WordHdoc)
			}
		case *syntax.ForClause, *syntax.WhileClause, *syntax.FuncDecl, *syntax.CaseClause, *syntax.ArithmCmd, *syntax.ProcSubst:
			valid = false
		case *syntax.CallExpr:
			calls++
			valid = calls <= 64 && len(part.Assigns) == 0 && setupCall(part)
		}
		return valid
	})
	return valid && calls > 0
}

func setupCall(call *syntax.CallExpr) bool {
	if len(call.Args) == 0 {
		return false
	}
	name := call.Args[0].Lit()
	text := shellText(call)
	if name == "sed" {
		return text == npmShebangRepair
	}
	if name == "dirname" {
		return strings.HasPrefix(text, `dirname "$(nvm which `) && strings.HasSuffix(text, `)"`)
	}
	if name == "readlink" {
		return text == `readlink -f "$(which npm)"`
	}
	if name == "echo" || name == "printf" {
		return len(call.Args) > 1 && call.Args[1].Lit() != "-v"
	}
	words, err := staticCommands(text)
	if err != nil || len(words) != 1 {
		return false
	}
	args := words[0]
	switch name {
	case ":":
		return len(args) == 1
	case "nvm":
		if len(args) < 3 || !slices.Contains([]string{"install", "use", "which"}, args[1]) {
			return false
		}
		for _, arg := range args[2:] {
			if arg != "--default" && arg != "--alias=npm" && arg != "npm" && !numericNode.MatchString(arg) {
				return false
			}
		}
		return true
	case "which":
		return slices.Equal(args, []string{"which", "npm"})
	case "node":
		return slices.Equal(args, []string{"node", "-v"}) || (len(args) == 3 && args[1] == "-pe" && args[2] == `Object.keys(require("./package").devDependencies).join("\n")`)
	case "npm":
		if len(args) > 1 && slices.Contains([]string{"-v", "--version", "install", "rm"}, args[1]) {
			return true
		}
		if len(args) > 2 && slices.Contains([]string{"-s", "-ps"}, args[1]) && args[2] == "ls" {
			return true
		}
		if len(args) >= 4 && args[1] == "config" && slices.Contains([]string{"get", "set"}, args[2]) && slices.Contains([]string{"package-lock", "shrinkwrap", "strict-ssl"}, args[3]) {
			return true
		}
	case "cut":
		return slices.Equal(args, []string{"cut", "-d.", "-f1"}) || slices.Equal(args, []string{"cut", "-d.", "-f2"})
	case "grep":
		return len(args) == 3 && (args[1] == "-q" || args[1] == "-E")
	case "sort":
		return slices.Equal(args, []string{"sort", "-r"})
	case "xargs":
		return slices.Equal(args, []string{"xargs", "-n1", "npm", "rm", "--silent", "--save-dev"})
	case "awk":
		return len(args) == 3 && args[1] == "-F[ @]" && args[2] == `NR>1 && $2 { print "" $2 "=" $3 }`
	}
	return false
}

// Read the explicitly selected NVM runtime only when the complete step is known
// setup. Secondary installs may provide npm for a legacy runtime; conflicting
// runtime selections are never inferred from a first matching line.
func nvmRuntime(command string) string {
	file, ok := parseCIShell(command)
	if !ok || !knownCISetup(file) || len(file.Stmts) == 0 {
		return ""
	}
	first, err := staticCommands(shellText(file.Stmts[0]))
	if err != nil || len(first) != 1 {
		return ""
	}
	args := first[0]
	if len(args) < 3 || args[0] != "nvm" || args[1] != "install" {
		return ""
	}
	node := args[len(args)-1]
	if !numericNode.MatchString(node) {
		return ""
	}
	valid := true
	syntax.Walk(file, func(n syntax.Node) bool {
		if call, ok := n.(*syntax.CallExpr); ok && call.Args[0].Lit() == "nvm" {
			words, _ := shellquote.Split(shellText(call))
			if len(words) >= 3 && (words[1] == "use" || words[1] == "install" && !slices.Contains(words, "--alias=npm")) {
				valid = valid && words[len(words)-1] == node
			}
		}
		return true
	})
	if valid {
		return node
	}
	return ""
}
