package onboard

import (
	"fmt"
	"maps"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kballard/go-shellquote"
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// Resolve only declared scalar environment values and simple parameter reads.
// Never evaluate a command substitution or inherit the evaluator's environment.
func resolveVariantStep(workflow ciWorkflow, job runtimeJob, step runtimeStep, row map[string]any) (runtimeStep, map[string]string, error) {
	env := map[string]string{}
	for _, layer := range []map[string]string{workflow.Env, job.Env, step.Env} {
		maps.Copy(env, layer)
	}
	for key, value := range env {
		if key == "PATH" || key == "NODE_PATH" {
			return step, nil, fmt.Errorf("CI environment overrides executable/module selection: %s", key)
		}
		if key == "NODE_OPTIONS" || strings.HasPrefix(key, "DD_") {
			delete(env, key)
			continue
		}
		resolved, err := runtimeValue(value, row)
		if err != nil {
			return step, nil, err
		}
		env[key] = resolved
	}
	if len(env) == 0 {
		env = nil
	}
	if len(env) == 0 || !strings.Contains(step.Run, "$") {
		return step, env, nil
	}
	file, err := syntax.NewParser().Parse(strings.NewReader(step.Run), "")
	if err != nil {
		return step, env, nil
	}
	var expansionErr error
	syntax.Walk(file, func(node syntax.Node) bool {
		if parameter, ok := node.(*syntax.ParamExp); ok {
			value, exists := env[parameter.Param.Value]
			if !exists || parameter.Index != nil || parameter.Slice != nil || parameter.Repl != nil || parameter.Exp != nil || parameter.Excl || parameter.Length || strings.ContainsAny(value, " \t\n*?[") {
				expansionErr = fmt.Errorf("CI parameter %s requires explicit environment review", parameter.Param.Value)
			}
		}
		switch node.(type) {
		case *syntax.CmdSubst, *syntax.ProcSubst, *syntax.ArithmExp:
			expansionErr = fmt.Errorf("dynamic CI shell setup requires review")
		}
		return true
	})
	if expansionErr != nil {
		return step, env, expansionErr
	}
	pairs := []string{}
	for key, value := range env {
		pairs = append(pairs, key+"="+value)
	}
	var commands []string
	var visit func(*syntax.Stmt) bool
	visit = func(stmt *syntax.Stmt) bool {
		if len(stmt.Redirs) > 0 || stmt.Background || stmt.Negated || stmt.Coprocess {
			return false
		}
		if binary, ok := stmt.Cmd.(*syntax.BinaryCmd); ok {
			return binary.Op == syntax.AndStmt && visit(binary.X) && visit(binary.Y)
		}
		call, ok := stmt.Cmd.(*syntax.CallExpr)
		if !ok || len(call.Assigns) > 0 || len(call.Args) == 0 {
			return false
		}
		words, err := expand.Fields(&expand.Config{Env: expand.ListEnviron(pairs...)}, call.Args...)
		if err != nil {
			return false
		}
		commands = append(commands, shellquote.Join(words...))
		return true
	}
	for _, stmt := range file.Stmts {
		if !visit(stmt) {
			return step, env, fmt.Errorf("CI shell control flow requires review")
		}
	}
	step.Run = strings.Join(commands, " && ")
	return step, env, nil
}

func localPreparation(root, directory string, words []string) (BuildCommand, bool) {
	if len(words) == 0 {
		return BuildCommand{}, false
	}
	kind := ""
	if len(words) >= 4 && words[0] == "pnpm" && words[1] == "add" && (words[2] == "-D" || words[2] == "--save-dev") {
		for _, dependency := range words[3:] {
			name, version, ok := strings.Cut(dependency, "@")
			if strings.HasPrefix(dependency, "@") {
				name, version, ok = strings.Cut(dependency[1:], "@")
				name = "@" + name
			}
			if !ok || name == "" || version == "" || strings.ContainsAny(dependency, "$` :\\\n*?[") || strings.HasPrefix(name, "-") || strings.Contains(version, "/") {
				return BuildCommand{}, false
			}
		}
		kind = "dependencies"
	}
	if len(words) == 4 && words[0] == "sed" && strings.HasPrefix(words[1], "-i") && !strings.ContainsAny(words[1], "/\\") {
		script := strings.TrimPrefix(words[2], "1")
		if literalSedSubstitution(script) && filepath.IsLocal(words[3]) && !strings.HasPrefix(words[3], "-") && !strings.ContainsAny(words[3], "$`\n:") {
			kind = "patch"
		}
	}
	if kind == "" {
		return BuildCommand{}, false
	}
	relative, err := filepath.Rel(root, directory)
	if err != nil {
		return BuildCommand{}, false
	}
	return BuildCommand{Directory: filepath.ToSlash(relative), Command: shellquote.Join(words...), Kind: kind}, true
}

// Recognize pnpm's bounded build-script selector while keeping the original
// command so its ordering, prebuild and postbuild behavior are replayed intact.
func orchestratedBuild(root, directory, script string, scripts map[string]string, stack []string, remaining *int, frameworks ...string) bool {
	commands, err := staticCommands(script)
	if err != nil {
		return false
	}
	selected := false
	for _, words := range commands {
		if len(words) != 3 || words[0] != "pnpm" || words[1] != "run" {
			return false
		}
		name := words[2]
		if name == "prebuild" || name == "postbuild" {
			if scripts[name] == "" {
				return false
			}
			continue
		}
		if len(name) < 3 || name[0] != '/' || name[len(name)-1] != '/' || scripts[name] != "" {
			return false
		}
		pattern, err := regexp.Compile(name[1 : len(name)-1])
		if err != nil {
			return false
		}
		count := 0
		for candidate, body := range scripts {
			if !pattern.MatchString(candidate) {
				continue
			}
			if !strings.HasPrefix(candidate, "build:") {
				return false
			}
			resolution := resolveJestSequence(root, directory, body, stack, remaining, frameworks...)
			if resolution.Matched || resolution.Reason != "" || len(resolution.Builds) == 0 {
				return false
			}
			count++
		}
		if count == 0 {
			return false
		}
		selected = true
	}
	return selected
}

// EqualPreparation compares executable preparation, including its step-local environment.
func EqualPreparation(a, b BuildCommand) bool {
	return a.Kind == b.Kind && a.Directory == b.Directory && a.Command == b.Command && maps.Equal(a.Environment, b.Environment)
}
