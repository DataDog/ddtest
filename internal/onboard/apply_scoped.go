package onboard

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/kballard/go-shellquote"
	"mvdan.cc/sh/v3/syntax"
)

// SetupDependencyGap names a package-manager operation, not an invitation to
// redesign hooks. Install it with the repository package manager, then rerun onboarding.
type SetupDependencyGap struct {
	Command  []string
	Packages []string
}

func (g *SetupDependencyGap) Error() string {
	return "Missing CI coverage dependencies: " + strings.Join(g.Packages, ", ") + ". Install with: " + strings.Join(g.Command, " ")
}

func scopedEnvironment(framework string) string {
	if framework == "cucumber" {
		return "DD_CUCUMBER_NODE_OPTIONS"
	}
	if framework == "webdriverio" {
		return "DD_WDIO_NODE_OPTIONS"
	}
	return "NODE_OPTIONS"
}

// readScopedPrefix removes the prefix that makes a direct Cucumber or
// WebdriverIO invocation read its scoped variable,
// NODE_OPTIONS="${NODE_OPTIONS:-} ${DD_CUCUMBER_NODE_OPTIONS:-}" (or
// DD_WDIO_NODE_OPTIONS), from the start of a provider step's command, and
// records that variable. This is the CI command directScopedCommand
// prescribes, with Cucumber's impacted-tests workaround after it. The shell
// sets them for that command alone, so the rest is the step's command, and
// its leaf consumes the variable as a scoped package script's does (see
// markScopedLeaf). Only a command whose first
// call carries the prefix, alone or piped into others, qualifies. The tests
// of a status-preserving subshell qualify too (see readStatusSubshell).
func (s *runtimeStep) readScopedPrefix() {
	s.readStatusSubshell()
	command := strings.TrimSpace(s.Run)
	variable := ""
	for _, candidate := range []string{"DD_CUCUMBER_NODE_OPTIONS", "DD_WDIO_NODE_OPTIONS"} {
		if strings.HasPrefix(command, scopedPrefix(candidate)+" ") {
			variable = candidate
		}
	}
	if variable == "" {
		return
	}
	rest := strings.TrimSpace(strings.TrimPrefix(command, scopedPrefix(variable)))
	if variable == "DD_CUCUMBER_NODE_OPTIONS" {
		// As in a scoped package script, the impacted-tests workaround
		// follows the prefix; discovery reads the command after it.
		trimmed := strings.TrimSpace(strings.TrimPrefix(rest, cucumberImpactedWorkaround+" "))
		s.impactedWorkaround, rest = trimmed != rest, trimmed
	}
	file, ok := parseCIShell(rest)
	if !ok || len(file.Stmts) != 1 {
		return
	}
	first := file.Stmts[0]
	for {
		binary, ok := first.Cmd.(*syntax.BinaryCmd)
		if !ok || (binary.Op != syntax.Pipe && binary.Op != syntax.PipeAll) {
			break
		}
		first = binary.X
	}
	if call, ok := first.Cmd.(*syntax.CallExpr); !ok || len(call.Args) == 0 {
		return
	}
	s.Run, s.scopedVariable = rest, variable
}

// readStatusSubshell reads a CI command that runs the tests, then commands
// that must run whatever the tests' result, and exits with the tests'
// status, or with a later command's when only that command fails, as
// onboarding prescribes for a report conversion (see ReportPipeSplit and
// statusSubshell):
//
//	(status=0; rm -f report.json; cucumber-js --format json:report.json || status=$?; cat report.json | cucumber-junit > junit.xml || { converted=$?; [ "$status" -ne 0 ] || status=$converted; }; exit "$status")
//
// The rm -f before the tests is optional, and only that command qualifies
// there: it removes the report the tests write and the first command after
// them reads (see removedReport).
// The || keeps errexit from stopping at failing tests or a failing
// conversion, and the subshell keeps the exit to this command, so a passing
// run continues with the script's later commands, as a script stopping at
// errors does, and a failing one fails the step after the conversion. Run
// becomes the tests, afterTests the commands between them and the exit,
// which resolution checks can't start tests and reads as a plain sequence
// after them, and statusWrapper the text around the tests. Each of those
// commands must fall through to the exit (see fallsThrough) and record its
// failure as the prescription does (see guardedCommand); any other command
// stays as written.
func (s *runtimeStep) readStatusSubshell() {
	run := strings.TrimSpace(s.Run)
	file, ok := parseCIShell(run)
	if !ok || len(file.Stmts) != 1 {
		return
	}
	outer := file.Stmts[0]
	subshell, isSubshell := outer.Cmd.(*syntax.Subshell)
	if !isSubshell || !plainStmt(outer) || len(subshell.Stmts) < 4 {
		return
	}
	stmts := subshell.Stmts
	variable, initial := onlyAssignment(stmts[0])
	if variable == "" || initial.Lit() != "0" {
		return
	}
	report := removedReport(stmts[1])
	if report != "" {
		if len(stmts) < 5 {
			return
		}
		stmts = append(stmts[:1:1], stmts[2:]...)
	}
	record, isBinary := stmts[1].Cmd.(*syntax.BinaryCmd)
	if !plainStmt(stmts[1]) || !isBinary || record.Op != syntax.OrStmt {
		return
	}
	tests, isCall := record.X.Cmd.(*syntax.CallExpr)
	if !plainStmt(record.X) || !isCall || len(tests.Args) == 0 {
		return
	}
	if name, value := onlyAssignment(record.Y); name != variable || !lastStatus(value) {
		return
	}
	exit, isExit := stmts[len(stmts)-1].Cmd.(*syntax.CallExpr)
	if !plainStmt(stmts[len(stmts)-1]) || !isExit || len(exit.Assigns) > 0 || len(exit.Args) != 2 || exit.Args[0].Lit() != "exit" || !readsOnly(exit.Args[1], variable) {
		return
	}
	if report != "" && (!writesReport(tests, report) || !readsReport(stmts[2], report)) {
		return
	}
	var after []string
	for _, stmt := range stmts[2 : len(stmts)-1] {
		command := guardedCommand(stmt, variable)
		if command == nil {
			return
		}
		after = append(after, stmtText(run, command))
	}
	start, end := record.X.Pos().Offset(), record.X.End().Offset()
	s.Run, s.afterTests, s.statusWrapper = stmtText(run, record.X), after, [2]string{run[:start], run[end:]}
}

// removedReport is the file a plain rm -f <file> statement removes, as
// statusSubshell writes it before the tests: one literal relative name with
// no glob or option, so the command removes only that file. It is "" for any
// other statement.
func removedReport(stmt *syntax.Stmt) string {
	call, isCall := stmt.Cmd.(*syntax.CallExpr)
	if !plainStmt(stmt) || !isCall || len(call.Assigns) > 0 || len(call.Args) != 3 || call.Args[0].Lit() != "rm" || call.Args[1].Lit() != "-f" {
		return ""
	}
	report := call.Args[2].Lit()
	if report == "" || strings.HasPrefix(report, "-") || strings.HasPrefix(report, "/") || strings.ContainsAny(report, "*?[]{}~\\") || slices.Contains(strings.Split(report, "/"), "..") {
		return ""
	}
	return report
}

// writesReport reports tests that name report as a formatter's output, as
// --format json:report does.
func writesReport(tests *syntax.CallExpr, report string) bool {
	return slices.ContainsFunc(tests.Args[1:], func(arg *syntax.Word) bool {
		word, literal := shellLiteralWord(arg)
		return literal && strings.HasSuffix(word, ":"+report)
	})
}

// readsReport reports a guarded conversion (see guardedCommand) whose pipe
// starts with cat report, as statusSubshell writes it.
func readsReport(stmt *syntax.Stmt, report string) bool {
	guard, isBinary := stmt.Cmd.(*syntax.BinaryCmd)
	if !isBinary || guard.Op != syntax.OrStmt {
		return false
	}
	first := guard.X
	for {
		pipe, isPipe := first.Cmd.(*syntax.BinaryCmd)
		if !isPipe || pipe.Op != syntax.Pipe {
			break
		}
		first = pipe.X
	}
	if first == guard.X {
		return false
	}
	cat, isCall := first.Cmd.(*syntax.CallExpr)
	return plainStmt(first) && isCall && len(cat.Assigns) == 0 && len(cat.Args) == 2 && cat.Args[0].Lit() == "cat" && cat.Args[1].Lit() == report
}

// guardedCommand is the command of a statement between a status subshell's
// tests and its exit that records its own failure while the tests' status
// is still 0, and keeps the tests' status otherwise, as statusSubshell
// writes it:
//
//	cat report.json | cucumber-junit > junit.xml || { converted=$?; [ "$status" -ne 0 ] || status=$converted; }
//
// The command must fall through to the exit (see fallsThrough) without
// touching either variable. It is nil for any other statement, such as the
// command alone, which would let a passing run with a failing conversion
// pass when errexit is off.
func guardedCommand(stmt *syntax.Stmt, variable string) *syntax.Stmt {
	guard, isBinary := stmt.Cmd.(*syntax.BinaryCmd)
	if !plainStmt(stmt) || !isBinary || guard.Op != syntax.OrStmt {
		return nil
	}
	block, isBlock := guard.Y.Cmd.(*syntax.Block)
	if !plainStmt(guard.Y) || !isBlock || len(block.Stmts) != 2 {
		return nil
	}
	saved, value := onlyAssignment(block.Stmts[0])
	if saved == "" || saved == variable || !lastStatus(value) {
		return nil
	}
	if !fallsThrough(guard.X, variable) || !fallsThrough(guard.X, saved) {
		return nil
	}
	keep, isBinary := block.Stmts[1].Cmd.(*syntax.BinaryCmd)
	if !plainStmt(block.Stmts[1]) || !isBinary || keep.Op != syntax.OrStmt {
		return nil
	}
	check, isCall := keep.X.Cmd.(*syntax.CallExpr)
	if !plainStmt(keep.X) || !isCall || len(check.Assigns) > 0 || len(check.Args) != 5 || check.Args[0].Lit() != "[" || !readsOnly(check.Args[1], variable) || check.Args[2].Lit() != "-ne" || check.Args[3].Lit() != "0" || check.Args[4].Lit() != "]" {
		return nil
	}
	if name, value := onlyAssignment(keep.Y); name != variable || !readsOnly(value, saved) {
		return nil
	}
	return guard.X
}

// plainStmt reports a statement that runs in the foreground, as written,
// without redirections.
func plainStmt(stmt *syntax.Stmt) bool {
	return !stmt.Background && !stmt.Negated && !stmt.Coprocess && !stmt.Disown && len(stmt.Redirs) == 0
}

// onlyAssignment is the variable a plain statement that only assigns one
// sets, and the value it assigns; name is "" for any other statement.
func onlyAssignment(stmt *syntax.Stmt) (name string, value *syntax.Word) {
	call, isCall := stmt.Cmd.(*syntax.CallExpr)
	if !plainStmt(stmt) || !isCall || len(call.Args) > 0 || len(call.Assigns) != 1 {
		return "", nil
	}
	assign := call.Assigns[0]
	if assign.Name == nil || assign.Index != nil || assign.Array != nil || assign.Append || assign.Naked || assign.Value == nil {
		return "", nil
	}
	return assign.Name.Value, assign.Value
}

// lastStatus reports a word that is only $?.
func lastStatus(word *syntax.Word) bool {
	if word == nil || len(word.Parts) != 1 {
		return false
	}
	expansion, isExpansion := word.Parts[0].(*syntax.ParamExp)
	return isExpansion && expansion.Short && expansion.Param != nil && expansion.Param.Value == "?"
}

// stmtText is a statement of src as written, without the ; that ends it
// or the comments after it.
func stmtText(src string, stmt *syntax.Stmt) string {
	end := stmt.End()
	if stmt.Semicolon.IsValid() {
		end = stmt.Semicolon
	}
	return strings.TrimSpace(src[stmt.Pos().Offset():end.Offset()])
}

// The builtins of bash and POSIX shells (bash's enable -a), which run in
// the shell itself, so can change its control flow or state, such as the
// variables a status subshell exits with: read status <<<0, printf -v
// status 0, mapfile, getopts, declare, eval, source and many more. A command
// between a status subshell's tests and its exit runs an external program
// instead, which can't, or one of statusSafeBuiltins.
var shellBuiltins = []string{".", ":", "[", "alias", "bg", "bind", "break", "builtin", "caller", "cd", "command", "compgen", "complete", "compopt", "continue", "declare", "dirs", "disown", "echo", "enable", "eval", "exec", "exit", "export", "false", "fc", "fg", "getopts", "hash", "help", "history", "jobs", "kill", "let", "local", "logout", "mapfile", "popd", "printf", "pushd", "pwd", "read", "readarray", "readonly", "return", "set", "shift", "shopt", "source", "suspend", "test", "times", "trap", "true", "type", "typeset", "ulimit", "umask", "unalias", "unset", "wait"}

// The builtins that only print or test, and set no variable. printf is not
// one: bash's printf -v sets a variable, and so does a %n in its format.
var statusSafeBuiltins = []string{":", "[", "echo", "false", "pwd", "test", "true"}

// fallsThrough reports a command between a status subshell's tests and its
// exit that always continues to that exit with the tests' status, as a
// report conversion does: one plain command, or a pipeline of them, that
// runs in the foreground, starts with a literal program that is an
// external program or a builtin that sets nothing (see shellBuiltins), and
// neither reads nor assigns the status variable. Lists such as a || b,
// compound commands, functions, arithmetic and bare assignments don't
// qualify.
func fallsThrough(stmt *syntax.Stmt, variable string) bool {
	if stmt.Background || stmt.Negated || stmt.Coprocess || stmt.Disown {
		return false
	}
	switch command := stmt.Cmd.(type) {
	case *syntax.BinaryCmd:
		if command.Op != syntax.Pipe && command.Op != syntax.PipeAll {
			return false
		}
		return fallsThrough(command.X, variable) && fallsThrough(command.Y, variable)
	case *syntax.CallExpr:
		if len(command.Args) == 0 {
			return false
		}
		program, literal := shellLiteralWord(command.Args[0])
		if !literal || program == "" || strings.Contains(program, `\`) {
			return false
		}
		if slices.Contains(shellBuiltins, program) && !slices.Contains(statusSafeBuiltins, program) {
			return false
		}
	default:
		return false
	}
	clean := true
	syntax.Walk(stmt, func(node syntax.Node) bool {
		switch node := node.(type) {
		case *syntax.ParamExp:
			clean = clean && (node.Param == nil || node.Param.Value != variable)
		case *syntax.Assign:
			clean = clean && (node.Name == nil || node.Name.Value != variable)
		case *syntax.ArithmExp, *syntax.ArithmCmd, *syntax.LetClause:
			clean = false
		}
		return clean
	})
	return clean
}

// readsOnly reports a word that is only the variable's value, as $status or
// "$status".
func readsOnly(word *syntax.Word, variable string) bool {
	parts := word.Parts
	if len(parts) == 1 {
		if quoted, ok := parts[0].(*syntax.DblQuoted); ok {
			parts = quoted.Parts
		}
	}
	if len(parts) != 1 {
		return false
	}
	expansion, ok := parts[0].(*syntax.ParamExp)
	return ok && expansion.Param != nil && expansion.Param.Value == variable && expansion.Exp == nil && expansion.Repl == nil && expansion.Slice == nil && expansion.Index == nil && !expansion.Length && !expansion.Width && !expansion.Excl
}

// foldInlineEnvironment moves the variables a single command assigns inline
// for itself into the step's environment (see foldCommandEnvironment), and
// records their names: a CI command rebuilt from Run alone would drop them.
func (s *runtimeStep) foldInlineEnvironment(known map[string]string, project string) {
	if run, assigned, ok := foldCommandEnvironment(s.Run, known, project); ok {
		s.Run, s.Env = run, mergeEnvironment(s.Env, assigned)
		s.inline = slices.Sorted(maps.Keys(assigned))
	}
}

// scopedPrefix is the prefix with which a command reads variable on top of
// the NODE_OPTIONS it inherits.
func scopedPrefix(variable string) string {
	return `NODE_OPTIONS="${NODE_OPTIONS:-} ${` + variable + `:-}"`
}

// markScopedLeaf marks the direct leaf of a step whose reader removed its
// scoped prefix as consuming that variable.
func markScopedLeaf(result *commandResolution, variable, framework string) {
	if variable != scopedEnvironment(framework) || result.Reason != "" || result.OtherFramework || len(result.Tests) != 1 || len(result.Tests[0].packageScripts) > 0 {
		return
	}
	if framework == "cucumber" {
		result.Tests[0].cucumberScoped = true
	} else {
		result.Tests[0].webdriverIOScoped = true
	}
}

// directScopedCommand is the CI command that runs a direct Cucumber or
// WebdriverIO invocation (no package script) with the scoped setup: the
// step's command after the scoped prefix and, for Cucumber, the
// impacted-tests workaround, with cucumber-js wrapped by NYC, as onboard
// --apply writes a package-script leaf. It is "" when the step already has
// that form. root is the repository, in whose test.Directory a report
// pipe's intermediate report must name no existing file (see
// cucumberReportPipe).
func directScopedCommand(root string, step runtimeStep, test TestCommand, framework string) (string, error) {
	if step.statusWrapper != [2]string{} {
		// The tests of a status subshell: the edit replaces them inside it,
		// keeping the commands after them and the exit with their status.
		tests := step
		tests.statusWrapper, tests.afterTests = [2]string{}, nil
		command, err := directScopedCommand(root, tests, test, framework)
		if command == "" || err != nil {
			return command, err
		}
		return step.statusWrapper[0] + command + step.statusWrapper[1], nil
	}
	command := strings.TrimSpace(step.Run)
	if framework == "webdriverio" {
		if step.scopedVariable == scopedEnvironment(framework) {
			return "", nil
		}
		if err := inlineAssignmentsError(step); err != nil {
			return "", err
		}
		if err := compoundCommandError(command); err != nil {
			return "", err
		}
		return scopedPrefix(scopedEnvironment(framework)) + " " + command, nil
	}
	if step.scopedVariable == scopedEnvironment(framework) && test.usesNYC && (step.impactedWorkaround || handWrittenFalse(step.Env["DD_CIVISIBILITY_IMPACTED_TESTS_DETECTION_ENABLED"])) {
		return "", nil
	}
	if err := inlineAssignmentsError(step); err != nil {
		return "", err
	}
	command = strings.TrimSpace(strings.TrimPrefix(command, cucumberImpactedWorkaround+" "))
	if err := compoundCommandError(command); err != nil {
		tests, conversion, report, ok := cucumberReportPipe(command, reportNameTaken(root, test.Directory))
		if !ok {
			return "", err
		}
		// The tests write their report to a file, read as one simple command
		// with the scoped setup; the conversion follows as its own command.
		split := step
		split.Run, split.scopedVariable, split.impactedWorkaround = tests, "", false
		scoped, scopedErr := directScopedCommand(root, split, test, framework)
		if scopedErr != nil {
			return "", scopedErr
		}
		// ddtest prescribes only a command it reads back as these tests and
		// this conversion on the next onboard --apply; for any other, such as
		// a conversion through a { ...; } group, the general advice stands.
		prescribed := statusSubshell(report, scoped, conversion)
		read := runtimeStep{Run: prescribed}
		read.readStatusSubshell()
		if read.Run != scoped || !slices.Equal(read.afterTests, []string{conversion}) {
			return "", err
		}
		return "", &ReportPipeSplit{Original: step.Run, Command: prescribed}
	}
	if !test.usesNYC {
		start, end, insertion, err := cucumberCoverageEdit(command)
		if err != nil {
			return "", err
		}
		command = command[:start] + insertion + command[end:]
	}
	return scopedPrefix(scopedEnvironment(framework)) + " " + cucumberImpactedWorkaround + " " + command, nil
}

// compoundCommandError explains why ddtest prescribes no scoped command for
// a CI command that is not one simple command, such as
// npm run build && npx cucumber-js: the variables the scoped prefix assigns
// would reach only its first command, not necessarily the tests.
func compoundCommandError(command string) error {
	file, ok := parseCIShell(command)
	if ok && len(file.Stmts) == 1 {
		stmt := file.Stmts[0]
		if call, simple := stmt.Cmd.(*syntax.CallExpr); simple && len(call.Args) > 0 && !stmt.Background && !stmt.Negated && !stmt.Coprocess {
			return nil
		}
	}
	return fmt.Errorf("CI runs %s as part of a compound shell command, which ddtest does not rewrite into the scoped command: the variables its prefix assigns would reach only the first command. Run the test command as a CI command of its own, then rerun onboard --apply. When CI pipes the tests' output into a report converter, the tests write that report to a file instead, and the conversion of the file runs after them whatever their result: ddtest reads that conversion as outside the tests", command)
}

// ReportPipeSplit is the rewrite of a direct Cucumber invocation that CI
// pipes into a report converter, as
// cucumber-js --format json | cucumber-junit > junit.xml: the scoped prefix
// and NYC, which prints its coverage summary to standard output, can't share
// the converter's input. The tests write the same report to a file, and the
// same CI command then converts that file whatever their result and exits
// with their status, or the conversion's when only it fails (see
// statusSubshell): failing tests still fail CI, and the report is still
// written, on providers whose scripts stop at the first failing command too. ddtest reads the conversion as outside the tests (see
// readStatusSubshell and reportTools).
type ReportPipeSplit struct {
	// Original is the CI command; Command replaces it.
	Original, Command string
}

func (s *ReportPipeSplit) Error() string {
	return "CI pipes the report of " + s.Original + " into a converter in one command, which ddtest does not rewrite into the scoped command. Replace this CI command with exactly this one, which removes any earlier intermediate report, writes the report to a file, converts it whether the tests pass or fail, and exits with the tests' status, or the conversion's when only the conversion fails: " + s.Command + ". Then rerun onboard --apply"
}

// statusSubshell is the CI command that runs tests, then conversion whatever
// their result, and exits with the tests' status, or the conversion's when
// the tests pass and it fails, as the original pipe failed then too (see
// readStatusSubshell). Each status is recorded through ||, so the command
// behaves the same with errexit on or off. The conversion's own status is
// its pipe's, under whatever pipefail setting the CI shell has, as the
// original pipe's was (GitLab and CircleCI enable pipefail, Buildkite and
// Jenkins don't): without it, that is the converter's status, and the only
// earlier stage, cat, fails only on a missing report, which tests that pass
// have written. ddtest adds no set -o pipefail, which not every sh supports.
// The command first removes report, the intermediate file the tests write
// and conversion reads, so tests that fail before writing it, in a reused
// workspace or after another command that wrote it, leave cat a missing
// file, never an earlier run's report.
func statusSubshell(report, tests, conversion string) string {
	return `(status=0; rm -f ` + report + `; ` + tests + ` || status=$?; ` + conversion + ` || { converted=$?; [ "$status" -ne 0 ] || status=$converted; }; exit "$status")`
}

// Cucumber's formatters by the extension of the file their output is
// written to.
var cucumberReportExtensions = map[string]string{"json": "json", "message": "ndjson", "junit": "xml", "html": "html"}

// cucumberReportPipe splits a pipeline whose first command runs Cucumber
// with one formatter writing to standard output, such as
// cucumber-js ./features --format json | cucumber-junit > junit.xml, into
// that command writing the formatter's output to a file and a later command
// that pipes the file, report, into the rest of the pipeline. taken reports an
// intermediate report name that is already a file where the command runs.
// ok is false for any other command, or when the rest of the pipeline may
// start tests.
func cucumberReportPipe(command string, taken func(name string) bool) (tests, conversion, report string, ok bool) {
	file, parsed := parseCIShell(command)
	if !parsed || len(file.Stmts) != 1 || file.Stmts[0].Background || file.Stmts[0].Negated || file.Stmts[0].Coprocess || len(file.Stmts[0].Redirs) > 0 {
		return "", "", "", false
	}
	var stages []*syntax.Stmt
	var flatten func(*syntax.Stmt) bool
	flatten = func(stmt *syntax.Stmt) bool {
		if binary, ok := stmt.Cmd.(*syntax.BinaryCmd); ok {
			return binary.Op == syntax.Pipe && flatten(binary.X) && flatten(binary.Y)
		}
		stages = append(stages, stmt)
		return true
	}
	if !flatten(file.Stmts[0]) || len(stages) < 2 || len(stages[0].Redirs) > 0 || stages[0].Negated {
		return "", "", "", false
	}
	call, isCall := stages[0].Cmd.(*syntax.CallExpr)
	start, end := int(stages[0].Pos().Offset()), int(stages[0].End().Offset())
	if !isCall || len(call.Assigns) > 0 || len(call.Args) == 0 || !namesFramework(command[start:end], "cucumber", true) {
		return "", "", "", false
	}
	var target *syntax.Word
	value := ""
	for i, arg := range call.Args[1:] {
		word, literal := shellLiteralWord(arg)
		option, inline, assigned := strings.Cut(word, "=")
		if !literal || option != "--format" && option != "-f" {
			continue
		}
		next := arg
		if !assigned {
			if i+2 >= len(call.Args) {
				return "", "", "", false
			}
			next = call.Args[i+2]
			if inline, literal = shellLiteralWord(next); !literal {
				return "", "", "", false
			}
		}
		if strings.Contains(inline, ":") {
			continue // Already written to a file.
		}
		if target != nil {
			return "", "", "", false // Two formatters share standard output.
		}
		target, value = next, inline
	}
	extension, known := cucumberReportExtensions[value]
	if target == nil || !known {
		return "", "", "", false
	}
	// The intermediate report is a file the pipeline names nowhere, so the
	// conversion never writes to the file it reads, as
	// cucumber-js --format junit | tee cucumber-report.xml would, and that
	// is no file of the step's directory, such as a tracked fixture, which
	// the tests would overwrite.
	report = "cucumber-report." + extension
	for suffix := 1; strings.Contains(command, report) || taken(report); suffix++ {
		report = fmt.Sprintf("cucumber-report-%d.%s", suffix, extension)
	}
	wordStart, wordEnd := int(target.Pos().Offset()), int(target.End().Offset())
	replacement := value + ":" + report
	if option, _, assigned := strings.Cut(shellLiteral(target), "="); assigned {
		replacement = option + "=" + replacement
	}
	// The word as the shell reads it, quoted only when it needs to be.
	tests = command[start:wordStart] + shellquote.Join(replacement) + command[wordEnd:end]
	// The rest of the pipeline as written, without the ; or comment after
	// it, which would end the prescription early.
	last := stages[len(stages)-1]
	rest := strings.TrimSpace(command[stages[1].Pos().Offset():last.End().Offset()])
	if mayStartTests(runtimeStep{Run: rest}, "cucumber") {
		return "", "", "", false
	}
	return tests, "cat " + report + " | " + rest, report, true
}

// reportNameTaken reports a name that is a file, or a path git tracks, in
// directory of the repository at root, where a CI command runs (see
// TestCommand.Directory).
func reportNameTaken(root, directory string) func(name string) bool {
	return func(name string) bool {
		path := filepath.Join(filepath.FromSlash(directory), name)
		if _, err := os.Lstat(filepath.Join(root, path)); !errors.Is(err, fs.ErrNotExist) {
			return true
		}
		tracked, err := exec.Command("git", "-C", root, "ls-files", "--cached", "--", filepath.ToSlash(path)).Output()
		return err == nil && len(bytes.TrimSpace(tracked)) > 0
	}
}

// inlineAssignmentsError explains why ddtest does not rebuild a direct
// invocation whose command assigns variables inline (step.inline), such as a
// hand-written NODE_OPTIONS preload: the provider's reader moved them out of
// Run, and placing them around the scoped prefix needs a person's review.
func inlineAssignmentsError(step runtimeStep) error {
	if len(step.inline) == 0 {
		return nil
	}
	return fmt.Errorf("CI runs %s with inline variable assignments (%s), which ddtest does not rewrite into the scoped command: move them out of the command, keeping their values, into the variables of the commands that run after the job's dependency install (a preload such as NODE_OPTIONS must not reach the install), then rerun onboard --apply", step.Run, strings.Join(step.inline, ", "))
}

// Only literal package-script leaves are changed. Framework configs, profiles,
// imports, reporters, services and other framework consumers remain untouched.
// With directLeaves, the caller handles direct invocations itself (see
// directScopedCommand); otherwise they need explicit initialization.
func scopedSetupEdits(root, framework string, directLeaves bool) ([]setupEdit, string, error) {
	if framework != "cucumber" && framework != "webdriverio" {
		return nil, "", nil
	}
	scope, err := DiscoverValidationScope(root, framework)
	if err != nil {
		return nil, "", err
	}
	selected := map[string]bool{}
	for _, entry := range scope.Commands {
		if len(entry.packageScripts) == 0 {
			if directLeaves {
				continue
			}
			return nil, "", &SetupGap{entry.Command, "Scoped initialization requires an identified package-script consumer; initialize this direct invocation explicitly."}
		}
		for _, script := range entry.packageScripts {
			if !script.Leaf {
				continue
			}
			if script.Directory != "." {
				return nil, "", &SetupGap{script.Directory, "Scoped package edits require explicit initialization in this package"}
			}
			selected[script.Name] = true
		}
	}
	edit, err := readSetupEdit(root, "package.json")
	if err != nil {
		return nil, "", err
	}
	var pkg struct {
		Type                          string
		Scripts                       map[string]string
		Dependencies, DevDependencies map[string]string
	}
	if err := json.Unmarshal(edit.before, &pkg); err != nil {
		return nil, "", err
	}
	options := strings.TrimPrefix(setupPreload(framework), "${{ env.NODE_OPTIONS }} ")
	if framework == "cucumber" {
		// The loader follows the selected inputs, as validation does (see
		// checkCucumberBootstrap).
		esm := cucumberNativeESM(root, pkg.Type, scope.Commands)
		packages := []string{}
		for _, name := range []string{"nyc", "@istanbuljs/esm-loader-hook"} {
			if name != "nyc" && !esm {
				continue
			}
			if pkg.Dependencies[name] == "" && pkg.DevDependencies[name] == "" {
				packages = append(packages, name)
			}
		}
		if len(packages) > 0 {
			return nil, "", setupDependencyGap(root, packages)
		}

		if esm {
			options += " " + cucumberESMLoaderHook
		}
	}
	changed := false
	for _, name := range slices.Sorted(maps.Keys(pkg.Scripts)) {
		if !selected[name] {
			continue
		}
		script := pkg.Scripts[name]
		prefix := cucumberScopedOptions
		if framework == "webdriverio" {
			prefix = webdriverIOScopedOptions
		}
		loaders := ""
		if framework == "cucumber" {
			// An earlier apply already wrote the scoped prefix, possibly with the
			// loaders it kept; adding another would hide them from probes.
			if found, scoped := cucumberScopedLoaders(script); scoped {
				if strings.Contains(script, cucumberImpactedWorkaround) {
					continue
				}
				if match := cucumberScopedPrefix.FindString(strings.TrimSpace(script)); match != "" {
					script, loaders = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(script), match)), found
				}
			} else if rest, found, ok := cucumberLoaderOverride(script); ok {
				// Keep in-script source loaders after the inherited options instead
				// of letting their NODE_OPTIONS replace the tracer preload.
				script, loaders = rest, found
			}
		}
		if strings.HasPrefix(script, prefix+" ") {
			if framework != "cucumber" || strings.Contains(script, cucumberImpactedWorkaround) {
				continue
			}
			script = strings.TrimPrefix(script, prefix+" ")
		}
		binary := "cucumber-js"
		if framework == "webdriverio" {
			binary = "wdio"
		}
		// A leaf must contain the literal framework binary and resolve to one
		// supported invocation; orchestration aliases retain their original text.
		if !strings.Contains(script, binary) {
			continue
		}
		remaining := &scriptBudget{left: 256}
		command := script
		if framework == "cucumber" {
			command = cucumberDiscoveryScript(command)
		}
		resolution := resolveJestSequence(root, root, command, nil, remaining, framework)
		if resolution.Reason != "" || len(resolution.Tests) != 1 || resolution.OtherFramework || !resolution.SingleJest {
			return nil, "", &SetupGap{"package.json / scripts / " + name, "Custom framework wrapper requires an explicit scoped initialization; preserve its command."}
		}
		if framework == "webdriverio" {
			local, err := localWDIOLeaf(root, ".", script)
			if err != nil {
				return nil, "", &SetupGap{"package.json / scripts / " + name, err.Error()}
			}
			if !local {
				continue
			} // Explicit browser runner remains outside instrumentation.
		} else if !resolution.Tests[0].usesNYC {
			start, end, insertion, err := cucumberCoverageEdit(script)
			if err != nil {
				return nil, "", &SetupGap{"package.json / scripts / " + name, err.Error()}
			}
			script = script[:start] + insertion + script[end:]
		}
		extra := ""
		if framework == "cucumber" {
			extra = " " + cucumberImpactedWorkaround
		}
		if loaders != "" {
			prefix = strings.TrimSuffix(prefix, `"`) + " " + loaders + `"`
		}
		replacement := prefix + extra + " " + script
		key, _ := json.Marshal(name)
		pattern := regexp.MustCompile(`(` + regexp.QuoteMeta(string(key)) + `\s*:\s*)("(?:\\.|[^"\\])*")`)
		matches := pattern.FindAllSubmatchIndex(edit.before, -1)
		if len(matches) != 1 {
			return nil, "", &SetupGap{"package.json / scripts / " + name, "Ambiguous script property; edit only its framework leaf explicitly."}
		}
		value := setupJSON(replacement)
		match := matches[0]
		edit.before = append(slices.Clone(edit.before[:match[4]]), append(value, edit.before[match[5]:]...)...)
		changed = true
	}
	if !changed {
		return nil, options, nil
	}
	edit.after = edit.before
	original, err := readSetupEdit(root, "package.json")
	if err != nil {
		return nil, "", err
	}
	edit.before = original.before
	return []setupEdit{edit}, options, nil
}

// Locate the executable word, not an occurrence in an assignment or argument.
// Offsets refer to the original script, including its quoting and local path.
func cucumberCoverageEdit(script string) (int, int, string, error) {
	file, err := syntax.NewParser().Parse(strings.NewReader(script), "")
	if err != nil {
		return 0, 0, "", err
	}
	offset, end := -1, -1
	insertion := "npx --no-install nyc -- "
	valid := true
	syntax.Walk(file, func(node syntax.Node) bool {
		call, ok := node.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		i := 0
		if call.Args[i].Lit() == "cross-env" {
			i++
			for i < len(call.Args) && strings.Contains(call.Args[i].Lit(), "=") {
				i++
			}
		}
		launcher := i
		if i < len(call.Args) && slices.Contains([]string{"npx", "pnpx"}, call.Args[i].Lit()) {
			i++
		} else if i+1 < len(call.Args) && slices.Contains([]string{"npm", "pnpm"}, call.Args[i].Lit()) && call.Args[i+1].Lit() == "exec" {
			i += 2
		}
		for i < len(call.Args) && slices.Contains([]string{"--no-install", "--no", "--"}, call.Args[i].Lit()) {
			i++
		}
		if i < len(call.Args) && filepath.Base(call.Args[i].Lit()) == "cucumber-js" {
			if offset >= 0 {
				valid = false
			}
			offset = int(call.Args[i].Pos().Offset())
			end = offset
			if i > launcher {
				insertion = "nyc -- "
				if call.Args[launcher].Lit() == "npx" {
					offset = int(call.Args[launcher].Pos().Offset())
					insertion = "npx --no-install nyc -- "
				} else if call.Args[launcher].Lit() == "npm" {
					offset = int(call.Args[launcher].Pos().Offset())
					insertion = "npm exec --no -- nyc -- "
				}
			}
		}
		return true
	})
	if !valid || offset < 0 {
		return 0, 0, "", fmt.Errorf("cannot locate one literal Cucumber executable; preserve this script and configure coverage explicitly")
	}
	return offset, end, insertion, nil
}

var localWDIORunner = regexp.MustCompile(`(?m)^\s*runner:\s*['"]local['"]\s*,`)
var browserWDIORunner = regexp.MustCompile(`(?m)^\s*runner:\s*(?:\[\s*)?['"]browser['"]`)
var wdioBaseConfig = regexp.MustCompile(`(?m)^import\s+\{\s*config(?:\s+as\s+\w+)?\s*\}\s+from\s+['"]([^'"]+)['"]`)

// wdioConfigSource returns the file a config import names. TypeScript ESM
// imports name the emitted .js file while the repository holds the .ts source.
func wdioConfigSource(root, config string) string {
	if _, err := os.Stat(filepath.Join(root, config)); err == nil {
		return config
	}
	for _, from := range []string{".js", ".mjs", ".cjs"} {
		if stem, ok := strings.CutSuffix(config, from); ok {
			source := stem + strings.Replace(from, "j", "t", 1)
			if _, err := os.Stat(filepath.Join(root, source)); err == nil {
				return source
			}
		}
	}
	return config
}

// localWDIOLeaf reports whether the WebdriverIO command script, run in
// directory (relative to root), runs its tests with the local runner: its
// configuration, read relative to directory, or one it extends, says so.
func localWDIOLeaf(root, directory, script string) (bool, error) {
	parts, err := staticCommands(script)
	if err != nil || len(parts) != 1 {
		return false, fmt.Errorf("wDIO leaf needs a literal CLI command")
	}
	words := parts[0]
	for i, word := range words {
		if filepath.Base(word) != "wdio" || i+2 >= len(words) || words[i+1] != "run" {
			continue
		}
		config := filepath.Join(filepath.FromSlash(directory), words[i+2])
		for range 4 {
			if !filepath.IsLocal(config) {
				return false, fmt.Errorf("wDIO config must be repository-local")
			}
			edit, err := readSetupEdit(root, config)
			if err != nil {
				return false, err
			}
			if browserWDIORunner.Match(edit.before) {
				return false, nil
			}
			if localWDIORunner.Match(edit.before) {
				return true, nil
			}
			base := wdioBaseConfig.FindSubmatch(edit.before)
			if len(base) != 2 {
				break
			}
			config = wdioConfigSource(root, filepath.Join(filepath.Dir(config), string(base[1])))
		}
		return false, fmt.Errorf("cannot identify a literal local/browser runner in %s; review its scoped worker initialization", words[i+2])
	}
	return false, fmt.Errorf("cannot identify the WDIO configuration argument")
}

func setupPinnedDependencies(packages []string) []string {
	result := make([]string, len(packages))
	for i, name := range packages {
		if name == "nyc" {
			result[i] = name + "@17.1.0"
		} else {
			result[i] = name + "@0.3.0"
		}
	}
	return result
}

// OnlyPrescribedDependencyChanges reports whether every dirty file is a package
// manifest or lockfile, and every dirty manifest declares all the dev
// dependencies a setup handoff prescribed. Applying ddtest's own setup edits on
// top of that install needs no approval; any other uncommitted change still does.
func OnlyPrescribedDependencyChanges(root string, dirty, packages []string) bool {
	if len(dirty) == 0 || len(packages) == 0 {
		return false
	}
	for _, name := range dirty {
		switch filepath.Base(name) {
		case "package-lock.json", "npm-shrinkwrap.json", "pnpm-lock.yaml", "yarn.lock":
		case "package.json":
			data, err := os.ReadFile(filepath.Join(root, name))
			if err != nil {
				return false
			}
			var manifest struct {
				DevDependencies map[string]string `json:"devDependencies"`
			}
			if json.Unmarshal(data, &manifest) != nil {
				return false
			}
			for _, pkg := range packages {
				if _, ok := manifest.DevDependencies[pkg]; !ok {
					return false
				}
			}
		default:
			return false
		}
	}
	return true
}

func setupDependencyGap(root string, packages []string) error {
	manager := "npm"
	for _, lock := range []struct{ path, manager string }{{"pnpm-lock.yaml", "pnpm"}, {"yarn.lock", "yarn"}} {
		edit, err := readSetupEdit(root, lock.path)
		if err != nil {
			return err
		}
		if edit.before != nil {
			if manager != "npm" {
				return &SetupGap{"dependencies", "Multiple package-manager lockfiles require review before installing coverage dependencies."}
			}
			manager = lock.manager
		}
	}
	command := map[string][]string{"npm": {"npm", "install", "--save-dev", "--no-audit", "--no-fund"}, "pnpm": {"pnpm", "add", "--save-dev"}, "yarn": {"yarn", "add", "--dev"}}[manager]
	return &SetupDependencyGap{Packages: packages, Command: append(command, setupPinnedDependencies(packages)...)}
}
