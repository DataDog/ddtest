package onboard

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
	"mvdan.cc/sh/v3/syntax"
)

const setupCommandBudget = 256

func setupPreload(framework string) string {
	if framework == "vitest" || framework == "cucumber" || framework == "webdriverio" {
		return vitestPreload
	}
	return "-r ${{ env.DD_TRACE_PACKAGE }}"
}

// YAML values are decoded before editing, so folded/quoted newlines cannot
// determine their physical extent. Continuation lines belong to a scalar only
// while indented beyond its mapping key (including a sequence's "- " prefix).
func setupNodeEnd(node *yaml.Node, lines []string) int {
	end := node.Line
	if node.Kind == yaml.ScalarNode && node.Line > 0 {
		header := lines[node.Line-1]
		indent := len(header) - len(strings.TrimLeft(header, " "))
		if strings.HasPrefix(header[indent:], "- ") {
			indent += 2
		}
		for i := node.Line; i < len(lines); i++ {
			line := lines[i]
			if strings.TrimSpace(line) == "" {
				continue
			}
			if len(line)-len(strings.TrimLeft(line, " ")) <= indent {
				break
			}
			end = i + 1
		}
	}
	for _, child := range node.Content {
		end = max(end, setupNodeEnd(child, lines))
	}
	return end
}

// indentSetup shifts encoded lines to the step's column. Empty lines stay
// empty rather than becoming whitespace-only lines.
func indentSetup(text string, indent int) string {
	var result strings.Builder
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			result.WriteString(strings.Repeat(" ", indent) + line)
		}
		result.WriteString("\n")
	}
	return result.String()
}

// NYC's require hook can't instrument native ESM, and Mocha validation accepts
// no ESM coverage loader, so wrapping such a leaf would never report coverage.
const mochaESMCoverageGap = "native ESM Mocha source coverage can't be set up automatically: NYC's require hook doesn't instrument native ESM, and Istanbul ESM loaders such as @istanbuljs/esm-loader-hook aren't supported for Mocha validation. Keep the coverage setup manual, or run these tests as CommonJS"

// Add NYC only to statically understood Mocha leaves. The original legacy
// command remains untouched when the runtime plan partitions instrumentation.
// setupMochaCoverage wraps each Mocha invocation of a test step's command with
// nyc, resolving it from the step's working directory and any literal cd.
func setupMochaCoverage(root, directory, framework, command string) (string, error) {
	if framework != "mocha" {
		return command, nil
	}
	file, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return "", err
	}
	changesDirectory := false
	syntax.Walk(file, func(node syntax.Node) bool {
		if call, ok := node.(*syntax.CallExpr); ok && len(call.Args) > 0 && call.Args[0].Lit() == "cd" {
			changesDirectory = true
		}
		return true
	})
	var calls []*syntax.CallExpr
	if changesDirectory {
		var ok bool
		if calls, ok = setupCallSequence(file); !ok {
			return "", fmt.Errorf("a cd outside a plain command sequence requires explicit coverage placement")
		}
	} else {
		syntax.Walk(file, func(node syntax.Node) bool {
			call, ok := node.(*syntax.CallExpr)
			if ok {
				calls = append(calls, call)
			}
			return !ok
		})
	}
	var offsets []int
	directories := []string{root}
	current := directory
	esm := false
	for _, call := range calls {
		if changesDirectory && call.Args[0].Lit() == "cd" {
			if current, err = setupChangeDirectory(root, current, call); err != nil {
				return "", err
			}
			continue
		}
		remaining := setupCommandBudget
		resolution := resolveJestSequence(root, current, shellText(call), nil, &remaining, "mocha")
		// Recognized Babel/Istanbul instrumentation already reports coverage.
		if len(resolution.Tests) == 1 && !resolution.Tests[0].usesNYC && !resolution.Tests[0].mochaIstanbul {
			offsets = append(offsets, int(call.Args[0].Pos().Offset()))
			directories = append(directories, current)
			esm = esm || resolution.Tests[0].mochaESM
		}
	}
	if esm {
		return "", errors.New(mochaESMCoverageGap)
	}
	if len(offsets) > 0 && !slices.ContainsFunc(directories, declaresNYC) {
		return "", setupDependencyGap(root, []string{"nyc"})
	}
	slices.Sort(offsets)
	for _, offset := range slices.Backward(offsets) {
		command = command[:offset] + "npx --no-install nyc -- " + command[offset:]
	}
	return command, nil
}

// setupCallSequence returns the calls of a plain `a && b; c` sequence in order,
// where a cd persists to the following calls.
func setupCallSequence(file *syntax.File) ([]*syntax.CallExpr, bool) {
	var calls []*syntax.CallExpr
	var visit func(*syntax.Stmt) bool
	visit = func(stmt *syntax.Stmt) bool {
		if stmt.Background || stmt.Coprocess || stmt.Negated {
			return false
		}
		switch command := stmt.Cmd.(type) {
		case *syntax.CallExpr:
			calls = append(calls, command)
			return len(command.Args) > 0
		case *syntax.BinaryCmd:
			return (command.Op == syntax.AndStmt || command.Op == syntax.OrStmt) && visit(command.X) && visit(command.Y)
		}
		return false
	}
	for _, stmt := range file.Stmts {
		if !visit(stmt) {
			return nil, false
		}
	}
	return calls, true
}

func setupChangeDirectory(root, current string, call *syntax.CallExpr) (string, error) {
	if len(call.Args) != 2 || call.Args[1].Lit() == "" || strings.ContainsAny(call.Args[1].Lit(), "$`~*?[]{}") || filepath.IsAbs(call.Args[1].Lit()) {
		return "", fmt.Errorf("cd needs one literal repository directory for automatic coverage placement: %s", shellText(call))
	}
	target, err := filepath.EvalSymlinks(filepath.Join(current, call.Args[1].Lit()))
	if err != nil {
		return "", fmt.Errorf("cannot resolve cd directory %s", call.Args[1].Lit())
	}
	boundary, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	if relative, err := filepath.Rel(boundary, target); err != nil || !filepath.IsLocal(relative) && relative != "." {
		return "", fmt.Errorf("cd leaves the repository: %s", call.Args[1].Lit())
	}
	return target, nil
}

func declaresNYC(directory string) bool {
	var pkg struct {
		Dependencies    map[string]string
		DevDependencies map[string]string
	}
	data, err := os.ReadFile(filepath.Join(directory, "package.json"))
	return err == nil && json.Unmarshal(data, &pkg) == nil && (pkg.Dependencies["nyc"] != "" || pkg.DevDependencies["nyc"] != "")
}
