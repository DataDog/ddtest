package onboard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kballard/go-shellquote"
	"go.yaml.in/yaml/v3"
	"mvdan.cc/sh/v3/syntax"
)

// Bound shell AST parsing of an explicit terminal failure guard.
const maximumPlaywrightCommandBytes = 16 * 1024

// Recognize only a literal terminal failure guard. Arbitrary fallback commands,
// shell state changes and successful exits still need review.
func playwrightFailureGuard(command string) (string, bool) {
	if len(command) > maximumPlaywrightCommandBytes {
		return "", false
	}
	file, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil || len(file.Stmts) != 1 {
		return "", false
	}
	plain := func(stmt *syntax.Stmt) bool {
		return !stmt.Background && !stmt.Negated && !stmt.Coprocess && !stmt.Disown && len(stmt.Redirs) == 0
	}
	stmt := file.Stmts[0]
	binary, ok := stmt.Cmd.(*syntax.BinaryCmd)
	if !ok || !plain(stmt) || binary.Op != syntax.OrStmt || !plain(binary.X) || !plain(binary.Y) {
		return "", false
	}
	call, ok := binary.Y.Cmd.(*syntax.CallExpr)
	if !ok || len(call.Assigns) != 0 || len(call.Args) != 2 || call.Args[0].Lit() != "exit" || call.Args[1].Lit() != "1" {
		return "", false
	}
	if _, ok := binary.X.Cmd.(*syntax.CallExpr); !ok {
		return "", false
	}
	var inner strings.Builder
	if err := syntax.NewPrinter().Print(&inner, binary.X); err != nil {
		return "", false
	}
	return inner.String(), true
}

// PlaywrightValidationCommand preserves a simple package alias and its options.
// Only a single root invocation with safe argument forwarding is accepted.
func PlaywrightValidationCommand(root, command string) (string, error) {
	if strings.TrimSpace(command) == "" {
		scope, err := DiscoverValidationScope(root, "playwright")
		if err != nil {
			return "", err
		}
		if len(scope.Commands) == 1 && filepath.Clean(scope.Commands[0].Directory) == "." && len(scope.Commands[0].Prerequisites) == 0 && len(scope.Commands[0].UnvalidatedSetup) == 0 {
			if scope.Commands[0].ExitOnFailure {
				return "", fmt.Errorf("playwright CI uses a failure guard; use --all to preserve its exit behavior")
			}
			if len(scope.Commands[0].Environment) > 0 {
				return "", fmt.Errorf("playwright CI environment requires --all or an explicit --command")
			}
			words, err := shellquote.Split(scope.Commands[0].Command)
			if err != nil {
				return "", err
			}
			words[0] = filepath.Join(root, "node_modules", ".bin", "playwright")
			return shellquote.Join(words...), nil
		}
		if len(scope.Commands) > 1 {
			return "", fmt.Errorf("multiple Playwright CI commands discovered; use --all or select the actual command with --command")
		}
		return "", nil
	}
	remaining := 256
	resolution := resolveJestSequence(root, root, command, nil, &remaining, "playwright")
	if resolution.Reason != "" || !resolution.SingleJest || len(resolution.Tests) != 1 || filepath.Clean(resolution.Tests[0].Directory) != "." || len(resolution.Tests[0].Prerequisites) > 0 {
		reason := resolution.Reason
		if reason == "" {
			reason = "the command is not one root invocation with forwardable arguments"
		}
		return "", fmt.Errorf("playwright command cannot safely forward validation options: %s. Review required setup and use the actual Playwright command with --command", reason)
	}
	return command, nil
}

// Scope unrelated Turbo jobs only when a literal task has no dependencies and
// every workspace script can be inspected. Never assume an opaque wrapper is
// unrelated, and never replace a Turbo test task with a guessed execution plan.
func reviewPlaywrightTurbo(root, directory string, words, stack []string, remaining *int) commandResolution {
	unresolved := func() commandResolution {
		return unresolvedCommand("Turbo task requires separate scope/setup review: " + shellquote.Join(words...))
	}
	if len(words) != 2 || strings.HasPrefix(words[1], "-") || canonicalDiscoveryPath(root) != canonicalDiscoveryPath(directory) {
		return unresolved()
	}
	data, err := os.ReadFile(filepath.Join(root, "turbo.json"))
	if err != nil {
		return unresolved()
	}
	type task struct {
		DependsOn  []string `json:"dependsOn"`
		Persistent bool     `json:"persistent"`
		Extends    []string `json:"extends"`
	}
	var config struct {
		Pipeline map[string]task `json:"pipeline"`
		Tasks    map[string]task `json:"tasks"`
		Extends  []string        `json:"extends"`
	}
	if json.Unmarshal(data, &config) != nil || len(config.Extends) > 0 {
		return unresolved()
	}
	for _, tasks := range []map[string]task{config.Tasks, config.Pipeline} {
		for name := range tasks {
			if strings.HasSuffix(name, "#"+words[1]) {
				return unresolved()
			}
		}
	}
	selected, ok := config.Tasks[words[1]]
	if !ok {
		selected, ok = config.Pipeline[words[1]]
	}
	if !ok || len(selected.DependsOn) > 0 || selected.Persistent || len(selected.Extends) > 0 {
		return unresolved()
	}
	data, err = os.ReadFile(filepath.Join(root, "pnpm-workspace.yaml"))
	if err != nil {
		return unresolved()
	}
	var workspace struct {
		Packages []string `yaml:"packages"`
	}
	if yaml.Unmarshal(data, &workspace) != nil || len(workspace.Packages) == 0 {
		return unresolved()
	}
	inspected := false
	for _, pattern := range workspace.Packages {
		if !filepath.IsLocal(pattern) || strings.Contains(pattern, "**") || strings.HasPrefix(pattern, "!") {
			return unresolved()
		}
		paths, err := filepath.Glob(filepath.Join(root, pattern, "package.json"))
		if err != nil {
			return unresolved()
		}
		for _, path := range paths {
			dir := filepath.Dir(path)
			if _, err := os.Stat(filepath.Join(dir, "turbo.json")); err == nil {
				return unresolved()
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return unresolved()
			}
			var pkg struct {
				Scripts map[string]string `json:"scripts"`
			}
			if json.Unmarshal(data, &pkg) != nil {
				return unresolved()
			}
			if pkg.Scripts[words[1]] == "" {
				continue
			}
			inspected = true
			part := resolvePackageScript(root, dir, words[1], nil, stack, "pnpm run "+words[1], remaining, "playwright")
			if part.Matched || part.Reason != "" || len(part.Builds) > 0 {
				return unresolved()
			}
		}
	}
	if !inspected {
		return unresolved()
	}
	return commandResolution{ReviewReason: "Inspected Turbo workspace task contains no Playwright invocation; other frameworks remain outside this Playwright validation: " + shellquote.Join(words...)}
}

func canonicalDiscoveryPath(path string) string {
	if canonical, err := filepath.EvalSymlinks(path); err == nil {
		return canonical
	}
	return filepath.Clean(path)
}
