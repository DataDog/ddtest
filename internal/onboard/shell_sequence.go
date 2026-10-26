package onboard

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

type commandFragment struct {
	Words  []string
	Reason string
	Chain  int
}

// Discovery is not execution: retain literal tests after dynamic external
// commands, but keep those preceding commands as unvalidated setup. Shell state
// changes and control flow cannot be safely separated and remain unresolved.
func discoverCommands(command string) ([]commandFragment, error) {
	if len(command) > 16384 {
		return nil, fmt.Errorf("shell sequence exceeds discovery limit")
	}
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(command), "")
	if err != nil {
		return nil, fmt.Errorf("cannot parse shell sequence: %w", err)
	}
	var result []commandFragment
	chain := 0
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
		words, err := literalCall(stmt)
		if err == nil {
			result = append(result, commandFragment{Words: words, Chain: chain})
		} else {
			if name == "cd" {
				return fmt.Errorf("dynamic directory change requires review")
			}
			result = append(result, commandFragment{Reason: "Unvalidated setup or command with dynamic shell syntax: " + printed.String(), Chain: chain})
		}
		return nil
	}
	for _, stmt := range file.Stmts {
		chain++
		if err := visit(stmt, 0); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// Expand quoting/escapes from the parsed word, never variables or executable
// substitutions. Discovery and scoped edits share this one shell grammar.
func literalShellWord(word *syntax.Word) (string, error) {
	if unquotedGlob(word) {
		return "", fmt.Errorf("unquoted shell pathname expansion requires review")
	}
	if len(word.Parts) > 0 {
		if literal, ok := word.Parts[0].(*syntax.Lit); ok && strings.HasPrefix(literal.Value, "~") {
			return "", fmt.Errorf("shell tilde expansion requires review")
		}
	}
	valid := true
	syntax.Walk(word, func(node syntax.Node) bool {
		switch node.(type) {
		case *syntax.Word, *syntax.Lit, *syntax.SglQuoted, *syntax.DblQuoted, nil:
		default:
			valid = false
			return false
		}
		return true
	})
	if !valid {
		return "", fmt.Errorf("dynamic shell expansion requires review")
	}
	fields, err := expand.Fields(nil, word)
	if err != nil {
		return "", err
	}
	if len(fields) != 1 {
		return "", fmt.Errorf("shell word must resolve to one literal argument")
	}
	return fields[0], nil
}

var scriptSelectorGlob = regexp.MustCompile(`^[A-Za-z0-9_:-]+:\*$`)

func literalCall(stmt *syntax.Stmt) ([]string, error) {
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok {
		return nil, fmt.Errorf("shell control flow or redirection requires review")
	}
	// Discarding stdout/stderr changes presentation, not the selected command.
	// File writes, input redirects, descriptor duplication and substitutions stay
	// unresolved; the original npm alias is still executed without rewriting.
	if len(stmt.Redirs) > 0 && (len(call.Args) < 2 || call.Args[0].Lit() != "npm" || !slices.Contains([]string{"ls", "list"}, call.Args[1].Lit())) {
		return nil, fmt.Errorf("shell redirection requires review")
	}
	for _, redirect := range stmt.Redirs {
		target, err := literalShellWord(redirect.Word)
		if err != nil || target != "/dev/null" || redirect.Op != syntax.RdrOut || (redirect.N != nil && redirect.N.Value != "1" && redirect.N.Value != "2") {
			return nil, fmt.Errorf("shell redirection requires review")
		}
	}
	var words []string
	for _, assignment := range call.Assigns {
		if assignment.Append || assignment.Array != nil || assignment.Index != nil || assignment.Name == nil {
			return nil, fmt.Errorf("shell assignment requires review")
		}
		value := ""
		if assignment.Value != nil {
			var err error
			value, err = literalShellWord(assignment.Value)
			if err != nil {
				return nil, err
			}
		}
		words = append(words, assignment.Name.Value+"="+value)
	}
	for _, arg := range call.Args {
		value, err := literalShellWord(arg)
		if err != nil && slices.Contains([]string{"npm-run-all", "npm-run-all2", "run-s", "run-p"}, call.Args[0].Lit()) && scriptSelectorGlob.MatchString(arg.Lit()) {
			value, err = arg.Lit(), nil
		}
		if err != nil {
			return nil, err
		}
		words = append(words, value)
	}
	return words, nil
}

func unquotedGlob(word *syntax.Word) bool {
	for _, part := range word.Parts {
		if literal, ok := part.(*syntax.Lit); ok {
			for i := 0; i < len(literal.Value); i++ {
				if literal.Value[i] == '\\' {
					i++
					continue
				}
				if strings.ContainsRune("*?[", rune(literal.Value[i])) {
					return true
				}
			}
		}
	}
	return false
}
