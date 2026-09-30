package onboard

import (
	"fmt"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

type commandFragment struct {
	Words  []string
	Reason string
}

// Discovery is not execution: retain literal tests after dynamic external
// commands, but keep those preceding commands as unvalidated setup. Shell state
// changes and control flow cannot be safely separated and remain unresolved.
func discoverCommands(command string) ([]commandFragment, error) {
	if words, err := staticCommands(command); err == nil {
		fragments := make([]commandFragment, len(words))
		for i, entry := range words {
			if len(entry) > 0 && slices.Contains([]string{"if", "then", "fi", "for", "while", "until", "case", "do", "done", "else", "elif", "function", "source", ".", "eval", "exec", "export", "unset", "set"}, entry[0]) {
				return nil, fmt.Errorf("shell control flow or state changes require review")
			}
			fragments[i].Words = entry
		}
		return fragments, nil
	}
	if len(command) > 16384 {
		return nil, fmt.Errorf("shell sequence exceeds discovery limit")
	}
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(command), "")
	if err != nil {
		return nil, fmt.Errorf("cannot parse shell sequence: %w", err)
	}
	var result []commandFragment
	var visit func(*syntax.Stmt, int) error
	visit = func(stmt *syntax.Stmt, depth int) error {
		if depth > 16 || len(result) >= 64 || stmt.Background || stmt.Negated || stmt.Coprocess || stmt.Disown {
			return fmt.Errorf("shell control flow requires review")
		}
		if binary, ok := stmt.Cmd.(*syntax.BinaryCmd); ok {
			if binary.Op != syntax.AndStmt || len(stmt.Redirs) != 0 {
				return fmt.Errorf("shell control flow requires review")
			}
			if err := visit(binary.X, depth+1); err != nil {
				return err
			}
			return visit(binary.Y, depth+1)
		}
		call, ok := stmt.Cmd.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 {
			return fmt.Errorf("shell state changes require review")
		}
		name := call.Args[0].Lit()
		if name == "" || slices.Contains([]string{"eval", "exec", "source", ".", "export", "unset", "set", "alias", "unalias", "read", "trap", "pushd", "popd"}, name) {
			return fmt.Errorf("shell state changes require review")
		}
		var printed strings.Builder
		if err := syntax.NewPrinter().Print(&printed, stmt); err != nil {
			return err
		}
		words, err := staticCommands(printed.String())
		if err == nil && len(words) == 1 {
			result = append(result, commandFragment{Words: words[0]})
		} else {
			if name == "cd" {
				return fmt.Errorf("dynamic directory change requires review")
			}
			result = append(result, commandFragment{Reason: "Unvalidated setup or command with dynamic shell syntax: " + printed.String()})
		}
		return nil
	}
	for _, stmt := range file.Stmts {
		if err := visit(stmt, 0); err != nil {
			return nil, err
		}
	}
	return result, nil
}
