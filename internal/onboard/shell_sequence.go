package onboard

import (
	"fmt"
	"os"
	"path/filepath"
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
	// Patterns indexes Words that are unquoted pathname patterns, kept as
	// written. The shell expands them in the command's working directory, so
	// only a caller that knows that directory may accept the fragment.
	Patterns []int
	// Text is the command as written, for diagnostics about its patterns.
	Text string
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
		words, patterns, err := literalCall(stmt)
		if err == nil {
			result = append(result, commandFragment{Words: words, Chain: chain, Patterns: patterns, Text: printed.String()})
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

func literalCall(stmt *syntax.Stmt) ([]string, []int, error) {
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok {
		return nil, nil, fmt.Errorf("shell control flow or redirection requires review")
	}
	// Discarding stdout/stderr changes presentation, not the selected command.
	// File writes, input redirects, descriptor duplication and substitutions stay
	// unresolved; the original npm alias is still executed without rewriting.
	if len(stmt.Redirs) > 0 && (len(call.Args) < 2 || call.Args[0].Lit() != "npm" || !slices.Contains([]string{"ls", "list"}, call.Args[1].Lit())) {
		return nil, nil, fmt.Errorf("shell redirection requires review")
	}
	for _, redirect := range stmt.Redirs {
		target, err := literalShellWord(redirect.Word)
		if err != nil || target != "/dev/null" || redirect.Op != syntax.RdrOut || (redirect.N != nil && redirect.N.Value != "1" && redirect.N.Value != "2") {
			return nil, nil, fmt.Errorf("shell redirection requires review")
		}
	}
	var words []string
	for _, assignment := range call.Assigns {
		if assignment.Append || assignment.Array != nil || assignment.Index != nil || assignment.Name == nil {
			return nil, nil, fmt.Errorf("shell assignment requires review")
		}
		value := ""
		if assignment.Value != nil {
			var err error
			value, err = literalShellWord(assignment.Value)
			if err != nil {
				return nil, nil, err
			}
		}
		words = append(words, assignment.Name.Value+"="+value)
	}
	var patterns []int
	for i, arg := range call.Args {
		value, err := literalShellWord(arg)
		if err != nil && slices.Contains([]string{"npm-run-all", "npm-run-all2", "run-s", "run-p"}, call.Args[0].Lit()) && scriptSelectorGlob.MatchString(arg.Lit()) {
			value, err = arg.Lit(), nil
		}
		if err != nil && i > 0 && pathnamePattern(arg) {
			patterns = append(patterns, len(words))
			value, err = arg.Lit(), nil
		}
		if err != nil {
			return nil, nil, err
		}
		words = append(words, value)
	}
	return words, patterns, nil
}

// pathnamePattern accepts only a plain unquoted argument whose sole shell
// expansion is pathname expansion. Escapes, braces (bash expands them, dash
// does not), tildes, options and anything with quoting or substitutions stay
// unresolved.
func pathnamePattern(word *syntax.Word) bool {
	// Lit is empty unless every part is unquoted literal text.
	value := word.Lit()
	return value != "" && unquotedGlob(word) && !strings.ContainsAny(value, "\\{}~$`'\"") && !strings.HasPrefix(value, "-")
}

// The longest pathname expansion ddtest replays as explicit arguments.
const pathnameExpansionLimit = 1000

// expandPathnames applies sh pathname expansion (no globstar, so ** matches
// like *, no dotglob, no brace expansion) to a fragment's patterns in the
// directory the shell would run it from. This is the argument list CI passes
// to the runner. A pattern that matches nothing is unresolved rather than
// passed literally: the runner would then expand it with its own glob rules
// (often with globstar), which differ from the shell's.
func expandPathnames(root, directory string, fragment commandFragment) ([]string, error) {
	if len(fragment.Patterns) == 0 {
		return fragment.Words, nil
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	canonicalDirectory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return nil, err
	}
	// The traversal reads every directory the shell would, including ones
	// outside the repository: the glob probes each symlinked entry before
	// matching its name, so refusing them here would either reject a pattern
	// over an unrelated linked directory or silently drop matches beneath a
	// selected one. The repository boundary is checked on the final matches
	// instead. Without globstar each pattern part reads one directory level,
	// so following outside links cannot recurse without bound.
	config := &expand.Config{
		Env:      expand.ListEnviron("PWD=" + canonicalDirectory),
		ReadDir2: os.ReadDir,
	}
	var words []string
	total := 0
	for i, word := range fragment.Words {
		if !slices.Contains(fragment.Patterns, i) {
			words = append(words, word)
			continue
		}
		file, err := syntax.NewParser(syntax.Variant(syntax.LangPOSIX)).Parse(strings.NewReader(": "+word), "")
		if err != nil || len(file.Stmts) != 1 {
			return nil, fmt.Errorf("cannot parse shell pathname pattern %s", word)
		}
		call, ok := file.Stmts[0].Cmd.(*syntax.CallExpr)
		if !ok || len(call.Args) != 2 || !pathnamePattern(call.Args[1]) {
			return nil, fmt.Errorf("cannot parse shell pathname pattern %s", word)
		}
		matches, err := expand.Fields(config, call.Args[1])
		if err != nil {
			return nil, err
		}
		if len(matches) == 1 && matches[0] == word {
			return nil, fmt.Errorf("unquoted shell pathname pattern %s matches no files in %s; the runner would expand it with different rules", word, repositoryPath(root, directory))
		}
		for _, match := range matches {
			if err := insideRepository(canonicalRoot, canonicalDirectory, match); err != nil {
				return nil, err
			}
		}
		total += len(matches)
		if total > pathnameExpansionLimit {
			return nil, fmt.Errorf("unquoted shell pathname expansion exceeds %d files", pathnameExpansionLimit)
		}
		words = append(words, matches...)
	}
	return words, nil
}

// insideRepository rejects a pathname match whose target, after following
// every symlink in it (the match itself or any parent directory), lies outside
// the repository.
func insideRepository(canonicalRoot, canonicalDirectory, match string) error {
	path := match
	if !filepath.IsAbs(path) {
		path = filepath.Join(canonicalDirectory, path)
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("pathname expansion matches %s, which cannot be resolved: %w", filepath.ToSlash(match), err)
	}
	if relative, err := filepath.Rel(canonicalRoot, canonical); err != nil || !filepath.IsLocal(relative) && relative != "." {
		return fmt.Errorf("pathname expansion matches %s, which leaves the repository, requires review", filepath.ToSlash(match))
	}
	return nil
}

// unquotedPathnamePattern reports whether any word of command, including
// words inside substitutions, has an unquoted * ? or [, which bash would treat
// as a pathname pattern. A command bash can't parse counts as one.
func unquotedPathnamePattern(command string) bool {
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(command), "")
	if err != nil {
		return true
	}
	found := false
	syntax.Walk(file, func(node syntax.Node) bool {
		if word, ok := node.(*syntax.Word); ok && unquotedGlob(word) {
			found = true
		}
		return !found
	})
	return found
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
