// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// ciShellDialect is what a provider's shell scripts differ in: how findings
// name them, the variable naming the checkout and the variables the runner
// exports to every process.
type ciShellDialect struct {
	// label names the scripts in findings, such as "GitLab CI script".
	label string
	// projectVariable names the checkout directory, such as CI_PROJECT_DIR.
	projectVariable string
	// runnerPrefixes are the prefixes of variables the runner exports.
	runnerPrefixes []string
	// setupVariable reports a variable that is an input of the provider's
	// setup mechanism, read as written.
	setupVariable func(name string) bool
}

// ciProjectDirectory is the shared model's expression for the checkout root.
const ciProjectDirectory = "${{ github.workspace }}"

func (s *ciShellState) setupVariable(name string) bool {
	return s.dialect.setupVariable != nil && s.dialect.setupVariable(name)
}

// ciShellState is the shell state a provider's script entries share: the
// variables they export, the directory they change to, and changes ddtest
// cannot follow.
type ciShellState struct {
	dialect ciShellDialect
	// env are the variables entries exported for later ones.
	env map[string]string
	// references are values later entries may expand without exporting.
	references map[string]string
	// job are the job's variables, which the shell exports.
	job       map[string]string
	directory string
	// problem makes later entries unresolved.
	problem string
	// curl reports an earlier entry that installed curl.
	curl bool
	// literal marks setup inputs assigned without expansion ('$NAME').
	literal map[string]bool
}

// applySetup adds what the setup script exports for each language it selects.
func (s *ciShellState) applySetup(setup ciSetup, current map[string]string) {
	languages := strings.Fields(setup.Inputs["languages"])
	for _, language := range discoveryLanguages {
		if setup.Inputs["languages"] != "all" && !slices.Contains(languages, language.actionLanguage()) {
			continue
		}
		exported, references := language.setupShellExports(mergeEnvironment(current, s.env))
		maps.Copy(s.env, exported)
		maps.Copy(s.references, references)
	}
}

// apply records the state one entry changes and reports whether the entry
// only changed state, so it runs nothing later steps must replay.
func (s *ciShellState) apply(command string, current map[string]string) bool {
	file, ok := parseCIShell(command)
	if !ok {
		return false
	}
	known := mergeEnvironment(current, s.references)
	onlyState := len(file.Stmts) > 0
	for _, stmt := range file.Stmts {
		onlyState = s.statement(stmt, known) && onlyState
		known = mergeEnvironment(mergeEnvironment(current, s.env), s.references)
	}
	return onlyState
}

// ciShellWord evaluates a shell word: literal text and $NAME/${NAME} reads of
// known values. self is the variable being assigned; reading it while unset
// is empty. Other unknown reads stay as written and make the value dynamic.
func ciShellWord(word *syntax.Word, known map[string]string, self, project string) (value string, literal bool) {
	var text strings.Builder
	literal = true
	var parts func([]syntax.WordPart)
	parts = func(items []syntax.WordPart) {
		for _, part := range items {
			switch part := part.(type) {
			case *syntax.Lit:
				text.WriteString(part.Value)
			case *syntax.SglQuoted:
				text.WriteString(part.Value)
			case *syntax.DblQuoted:
				parts(part.Parts)
			case *syntax.ParamExp:
				name := part.Param.Value
				if trimmed, ok := ciShellTrim(part, known, self, project); ok {
					// ${NAME%pattern} and the like on a known value, as when a
					// scoped preload moves the setup's out of NODE_OPTIONS.
					text.WriteString(trimmed)
					continue
				}
				if part.Index != nil || part.Slice != nil || part.Repl != nil || !ciEmptyDefault(part.Exp) || part.Excl || part.Length {
					literal = false
					text.WriteString("${" + name + "...}")
					continue
				}
				if project != "" && name == project {
					text.WriteString(ciProjectDirectory)
					continue
				}
				if value, ok := known[name]; ok {
					text.WriteString(value)
					continue
				}
				if name == self {
					continue
				}
				literal = false
				text.WriteString("$" + name)
			default:
				literal = false
				text.WriteString("$(…)")
			}
		}
	}
	parts(word.Parts)
	return text.String(), literal
}

// statement records the state one statement changes and reports whether it
// only changed state.
func (s *ciShellState) statement(stmt *syntax.Stmt, known map[string]string) bool {
	if stmt.Background || stmt.Negated || stmt.Coprocess || len(stmt.Redirs) > 0 {
		s.unfollowed(stmt)
		return false
	}
	switch command := stmt.Cmd.(type) {
	case *syntax.BinaryCmd:
		if command.Op == syntax.AndStmt {
			left := s.statement(command.X, known)
			right := s.statement(command.Y, mergeEnvironment(known, s.env))
			return left && right
		}
		s.unfollowed(stmt)
		return false
	case *syntax.DeclClause:
		if command.Variant.Value != "export" {
			s.unfollowed(stmt)
			return false
		}
		s.assign(command.Args, known, true)
		return true
	case *syntax.CallExpr:
		if len(command.Args) == 0 {
			s.assign(command.Assigns, known, false)
			return true
		}
		var words []string
		for _, word := range command.Args {
			value, literal := ciShellWord(word, known, "", s.dialect.projectVariable)
			if !literal {
				words = nil
				break
			}
			words = append(words, value)
		}
		if len(words) == 0 {
			return false
		}
		switch words[0] {
		case "cd":
			s.changeDirectory(words[1:])
			return true
		case "set":
			return len(command.Assigns) == 0
		case "unset":
			for _, name := range words[1:] {
				s.env[name] = ""
			}
			return true
		}
		return false
	case *syntax.Subshell:
		return false
	case *syntax.IfClause:
		// A condition on known values: follow the branch that runs.
		if branch, ok := ciStaticBranch(command, known, s.dialect.projectVariable); ok {
			onlyState := true
			for _, inner := range branch {
				onlyState = s.statement(inner, mergeEnvironment(known, s.env)) && onlyState
			}
			return onlyState
		}
	}
	s.unfollowed(stmt)
	return false
}

// assign records assignments; values ddtest cannot evaluate keep their
// dynamic text, so later readers stay unresolved.
func (s *ciShellState) assign(assigns []*syntax.Assign, known map[string]string, export bool) {
	for _, assign := range assigns {
		if assign.Name == nil || assign.Index != nil || assign.Array != nil {
			s.problem = s.dialect.label + " assigns an array or indexed variable ddtest cannot follow"
			continue
		}
		name := assign.Name.Value
		if assign.Naked {
			if value, ok := mergeEnvironment(known, s.references)[name]; ok && export {
				s.env[name] = value
				delete(s.references, name)
			}
			continue
		}
		if name == "PATH" && assign.Value != nil && ciRepositoryBinPath(assign.Value) {
			// Prepending the repository's node_modules/.bin selects the
			// tools local validation already resolves from it.
			continue
		}
		visible := mergeEnvironment(mergeEnvironment(known, s.references), s.env)
		value, literal := "", true
		if assign.Value != nil {
			value, literal = ciShellWord(assign.Value, visible, name, s.dialect.projectVariable)
		}
		if assign.Append {
			value = visible[name] + value
		}
		if input := s.setupVariable(name); !literal && !input {
			// A computed value or an unknown read stays unresolved wherever the
			// value is used, rather than replayed as text.
			value = unresolvedCIValue(value)
		}
		// A plain assignment stays in the shell unless the variable is
		// already exported, as the job's variables are.
		_, exported := s.env[name]
		if _, inJob := s.job[name]; inJob {
			exported = true
		}
		if input := s.setupVariable(name); input && assign.Value != nil {
			if s.literal == nil {
				s.literal = map[string]bool{}
			}
			s.literal[name] = !ciShellExpands(assign.Value)
		}
		if export || exported || ciRunnerExported(name, s.dialect.runnerPrefixes) {
			s.env[name] = value
			delete(s.references, name)
		} else {
			s.references[name] = value
		}
	}
}

// changeDirectory follows a literal cd within the repository.
func (s *ciShellState) changeDirectory(arguments []string) {
	if len(arguments) != 1 || arguments[0] == "-" || strings.HasPrefix(arguments[0], "~") {
		s.problem = s.dialect.label + " changes directory with cd " + strings.Join(arguments, " ") + ", which ddtest cannot follow"
		return
	}
	target := arguments[0]
	base := s.directory
	if rest, ok := strings.CutPrefix(target, ciProjectDirectory); ok {
		target, base = strings.TrimPrefix(rest, "/"), ""
	}
	if strings.HasPrefix(target, "/") || strings.Contains(target, "$") {
		s.problem = s.dialect.label + " changes directory to " + arguments[0] + ", outside the repository ddtest reads"
		return
	}
	next := path.Clean(path.Join(base, target))
	if next == "." {
		next = ""
	}
	if strings.HasPrefix(next, "..") {
		s.problem = s.dialect.label + " changes directory to " + arguments[0] + ", outside the repository"
		return
	}
	s.directory = next
}

// unfollowed records state changes inside control flow ddtest does not
// evaluate: they make later entries unresolved.
func (s *ciShellState) unfollowed(stmt *syntax.Stmt) {
	syntax.Walk(stmt, func(node syntax.Node) bool {
		switch node := node.(type) {
		case *syntax.Subshell, *syntax.CmdSubst:
			return false
		case *syntax.DeclClause:
			if node.Variant.Value == "export" {
				s.problem = s.dialect.label + " exports variables conditionally, which ddtest cannot follow"
			}
		case *syntax.CallExpr:
			if len(node.Args) == 0 && len(node.Assigns) > 0 {
				s.problem = s.dialect.label + " assigns variables conditionally, which ddtest cannot follow"
			}
			if len(node.Args) > 0 && node.Args[0].Lit() == "cd" {
				s.problem = s.dialect.label + " changes directory conditionally, which ddtest cannot follow"
			}
		}
		return true
	})
}

// ciStaticBranch returns the statements an if clause runs when its
// conditions are tests on known values: [ -n "$X" ], [ -z "$X" ] and
// [ "$X" = "value" ] (also != and ==, or test ...).
func ciStaticBranch(clause *syntax.IfClause, known map[string]string, project string) ([]*syntax.Stmt, bool) {
	for current := clause; current != nil; current = current.Else {
		if len(current.Cond) == 0 {
			return current.Then, true // else
		}
		truth, ok := ciStaticTest(current.Cond, known, project)
		if !ok {
			return nil, false
		}
		if truth {
			return current.Then, true
		}
	}
	return nil, true
}

// ciStaticTest evaluates one [ ... ] or test ... condition whose words
// are literal or read known values.
func ciStaticTest(condition []*syntax.Stmt, known map[string]string, project string) (bool, bool) {
	if len(condition) != 1 || condition[0].Negated || len(condition[0].Redirs) > 0 {
		return false, false
	}
	call, ok := condition[0].Cmd.(*syntax.CallExpr)
	if !ok || len(call.Assigns) > 0 || len(call.Args) == 0 {
		return false, false
	}
	args := call.Args[1:]
	switch call.Args[0].Lit() {
	case "[":
		if len(args) == 0 || args[len(args)-1].Lit() != "]" {
			return false, false
		}
		args = args[:len(args)-1]
	case "test":
	default:
		return false, false
	}
	var words []string
	for _, word := range args {
		value, literal := ciShellWord(word, known, "", project)
		if !literal || strings.Contains(strings.ReplaceAll(value, "${{ env.", ""), "$") {
			return false, false
		}
		words = append(words, value)
	}
	switch {
	case len(words) == 2 && words[0] == "-n":
		return words[1] != "", true
	case len(words) == 2 && words[0] == "-z":
		return words[1] == "", true
	case len(words) == 3 && (words[1] == "=" || words[1] == "=="):
		return words[0] == words[2], true
	case len(words) == 3 && words[1] == "!=":
		return words[0] != words[2], true
	}
	return false, false
}

// ciShellTrim evaluates ${NAME%pattern}, ${NAME%%pattern}, ${NAME#pattern}
// and ${NAME##pattern} on a known value, for a pattern of literal text,
// known values and unquoted * and ? wildcards.
func ciShellTrim(part *syntax.ParamExp, known map[string]string, self, project string) (string, bool) {
	if part.Exp == nil || part.Exp.Word == nil || part.Index != nil || part.Slice != nil || part.Repl != nil || part.Excl || part.Length {
		return "", false
	}
	suffix := part.Exp.Op == syntax.RemSmallSuffix || part.Exp.Op == syntax.RemLargeSuffix
	longest := part.Exp.Op == syntax.RemLargeSuffix || part.Exp.Op == syntax.RemLargePrefix
	if !suffix && part.Exp.Op != syntax.RemSmallPrefix && part.Exp.Op != syntax.RemLargePrefix {
		return "", false
	}
	value, set := known[part.Param.Value]
	if !set {
		return "", false
	}
	pattern, ok := ciShellPattern(part.Exp.Word, known, self, project)
	if !ok {
		return "", false
	}
	// Prefix and suffix lengths, shortest first.
	lengths := make([]int, 0, len(value)+1)
	for length := range len(value) + 1 {
		lengths = append(lengths, length)
	}
	if longest {
		slices.Reverse(lengths)
	}
	for _, length := range lengths {
		if suffix && pattern.MatchString(value[len(value)-length:]) {
			return value[:len(value)-length], true
		}
		if !suffix && pattern.MatchString(value[:length]) {
			return value[length:], true
		}
	}
	return value, true
}

// ciShellPattern is a shell pattern word as an anchored regular expression:
// quoted text and known values match themselves, unquoted * and ? are
// wildcards. Bracket expressions, escapes and unknown values are not read.
func ciShellPattern(word *syntax.Word, known map[string]string, self, project string) (*regexp.Regexp, bool) {
	var expression strings.Builder
	for _, part := range word.Parts {
		switch part := part.(type) {
		case *syntax.Lit:
			if strings.ContainsAny(part.Value, `[\`) {
				return nil, false
			}
			for _, char := range part.Value {
				switch char {
				case '*':
					expression.WriteString(".*")
				case '?':
					expression.WriteString(".")
				default:
					expression.WriteString(regexp.QuoteMeta(string(char)))
				}
			}
		default:
			// Quoted text and expansions match literally (an unquoted
			// expansion's wildcards are refused).
			value, literal := ciShellWord(&syntax.Word{Parts: []syntax.WordPart{part}}, known, self, project)
			if _, unquoted := part.(*syntax.ParamExp); !literal || unquoted && strings.ContainsAny(value, `*?[\`) {
				return nil, false
			}
			expression.WriteString(regexp.QuoteMeta(value))
		}
	}
	pattern, err := regexp.Compile(`^(?s:` + expression.String() + `)$`)
	return pattern, err == nil
}

// ciEmptyDefault reports no expansion operator, or ${NAME:-} (empty
// when unset, which keeps set -u scripts running).
func ciEmptyDefault(expansion *syntax.Expansion) bool {
	return expansion == nil || expansion.Op == syntax.DefaultUnsetOrNull && (expansion.Word == nil || len(expansion.Word.Parts) == 0)
}

// ciShellExpands reports whether a shell word expands a variable, outside
// single quotes.
func ciShellExpands(word *syntax.Word) bool {
	expands := false
	syntax.Walk(word, func(node syntax.Node) bool {
		switch node.(type) {
		case *syntax.SglQuoted:
			return false
		case *syntax.ParamExp:
			expands = true
		}
		return !expands
	})
	return expands
}

// ciRunnerExported reports variables the runner or the image exports
// to every process, such as PATH or a language runtime's options: assigning
// one changes the environment of later commands even without export.
func ciRunnerExported(name string, prefixes []string) bool {
	if slices.Contains([]string{"PATH", "HOME", "LANG", "TZ"}, name) {
		return true
	}
	for _, prefix := range append([]string{"LC_", "DD_"}, prefixes...) {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return slices.ContainsFunc(discoveryLanguages, func(language discoveryLanguage) bool { return language.runtimeVariable(name) })
}

// ciRepositoryBinPath reports a PATH value that only prepends the
// repository's node_modules/.bin to $PATH, as in
// PATH=$(realpath ./node_modules/.bin):$PATH.
func ciRepositoryBinPath(word *syntax.Word) bool {
	var text strings.Builder
	if err := syntax.NewPrinter().Print(&text, word); err != nil {
		return false
	}
	parts := strings.Split(strings.Trim(text.String(), `"`), ":")
	sawPath := false
	for _, part := range parts {
		switch strings.TrimSuffix(part, "/") {
		case "$PATH", "${PATH}":
			sawPath = true
		case "./node_modules/.bin", "node_modules/.bin", "$(realpath ./node_modules/.bin)", "$(realpath node_modules/.bin)", "$PWD/node_modules/.bin", "${PWD}/node_modules/.bin", "$CI_PROJECT_DIR/node_modules/.bin", "${CI_PROJECT_DIR}/node_modules/.bin":
		default:
			return false
		}
	}
	return sawPath
}

// imageWithoutBash reports a container image known to lack Bash: Alpine
// variants, BusyBox and distroless images. Others are assumed to have it.
func imageWithoutBash(image string) bool {
	name := strings.ToLower(image)
	repository, tag, _ := strings.Cut(name[strings.LastIndex(name, "/")+1:], ":")
	return strings.Contains(tag, "alpine") || repository == "alpine" || repository == "busybox" || strings.Contains(name, "distroless") || strings.Contains(tag, "busybox")
}

// imageWithoutDownloader reports an official Docker Hub slim image: they
// remove curl and wget after building (verified on the slim language
// runtime images).
func imageWithoutDownloader(image string) bool {
	name := strings.ToLower(image)
	official := !strings.Contains(dockerHubName(name), "/")
	_, tag, _ := strings.Cut(name[strings.LastIndex(name, "/")+1:], ":")
	return official && strings.Contains(tag, "slim")
}

// dockerHubName strips Docker Hub's registry and library prefixes, so
// docker.io/library/node, index.docker.io/node and library/node all read as
// the official node image.
func dockerHubName(image string) string {
	for _, registry := range []string{"docker.io/", "index.docker.io/", "registry-1.docker.io/", "registry.hub.docker.com/"} {
		if rest, ok := strings.CutPrefix(image, registry); ok {
			image = rest
			break
		}
	}
	return strings.TrimPrefix(image, "library/")
}

// installsCurl reports a command installing curl with apt.
func installsCurl(command string) bool {
	words := strings.Fields(command)
	return (slices.Contains(words, "apt-get") || slices.Contains(words, "apt")) && slices.Contains(words, "install") && slices.Contains(words, "curl")
}
