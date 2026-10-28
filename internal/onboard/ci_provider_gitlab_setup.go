// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"fmt"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// gitLabSetupRelease is the release of Datadog's GitLab setup script ddtest
// prescribes, and gitLabSetupChecksums the SHA256 of every release it
// accepts. The script runs the same installer as the GitHub Actions setup
// action (install_test_visibility_v15.sh).
const gitLabSetupRelease = "v1.2.10"

var gitLabSetupChecksums = map[string]string{
	"v1.2.10": "c82030bdbc7cbbbb139751885ca0392a7191c858f5a080ae364e0ec16218b783",
}

const gitLabSetupDownload = "https://github.com/DataDog/test-optimization-gitlab-script/releases/download/"

var (
	gitLabSetupURL      = regexp.MustCompile(`https://github\.com/DataDog/test-optimization-gitlab-script/releases/download/([^/\s"']+)/script\.sh`)
	gitLabSetupChecksum = regexp.MustCompile(`sha256sum\s*\|\s*grep\s+-q\s+['"]\^([0-9a-f]{64})['"]\s*&&\s*(?:\\\s*)?(?:source|\.)\s+<\(\s*echo\s+"\$SRC"\s*\)`)
	gitLabSetupVersion  = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)
	gitLabVariableName  = regexp.MustCompile(`^\$(?:[A-Za-z_][A-Za-z0-9_]*|\{[A-Za-z_][A-Za-z0-9_]*\})$`)
)

// gitLabSetupInput maps the script's variables to the setup action's input
// names; X_TRACER_VERSION becomes x-tracer-version.
func gitLabSetupInput(variable string) (string, bool) {
	switch variable {
	case "LANGUAGES":
		return "languages", true
	case "API_KEY":
		return "api_key", true
	case "SITE":
		return "site", true
	case "SERVICE":
		return "service", true
	}
	if language, ok := strings.CutSuffix(variable, "_TRACER_VERSION"); ok && language != "" {
		return strings.ToLower(language) + "-tracer-version", true
	}
	return "", false
}

func (gitLabCI) setupInput(name string) string {
	return strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
}

// gitLabSetupBody returns the statements that source the setup script:
// the README's entry as is, or that entry inside a guard that skips it when
// the API key variable is empty (if [ -n "$KEY" ]; then ...; else ...; fi),
// returning the guard's variable. Any other shape is not recognized.
func gitLabSetupBody(stmts []*syntax.Stmt) (body []*syntax.Stmt, guard string, ok bool) {
	if len(stmts) == 1 {
		if clause, isIf := stmts[0].Cmd.(*syntax.IfClause); isIf {
			guard = gitLabKeyGuard(clause.Cond)
			if guard == "" || clause.Else != nil && (len(clause.Else.Cond) > 0 || gitLabSources(clause.Else.Then)) {
				return nil, "", false
			}
			stmts = clause.Then
		}
	}
	// Inside a guard, statements after the setup run only with it, such as a
	// loader registration; they must only change shell state.
	if len(stmts) < 2 || len(stmts) > 2 && guard == "" || gitLabSources(stmts[2:]) {
		return nil, "", false
	}
	if call, isCall := stmts[0].Cmd.(*syntax.CallExpr); !isCall || len(call.Args) != 0 {
		return nil, "", false
	}
	return stmts, guard, true
}

// gitLabStateOnly reports whether a statement only changes shell state:
// exports, assignments, set, and key-guarded branches of those.
func gitLabStateOnly(stmt *syntax.Stmt) bool {
	if stmt.Background || stmt.Negated || stmt.Coprocess || len(stmt.Redirs) > 0 {
		return false
	}
	switch command := stmt.Cmd.(type) {
	case *syntax.DeclClause:
		return command.Variant.Value == "export"
	case *syntax.CallExpr:
		return len(command.Args) == 0 || command.Args[0].Lit() == "set"
	case *syntax.IfClause:
		if gitLabKeyGuard(command.Cond) == "" || command.Else != nil {
			return false
		}
		for _, inner := range command.Then {
			if !gitLabStateOnly(inner) {
				return false
			}
		}
		return true
	}
	return false
}

// gitLabKeyGuard is the variable a [ -n "$NAME" ] or test -n "$NAME"
// condition checks, or "".
func gitLabKeyGuard(condition []*syntax.Stmt) string {
	if len(condition) != 1 {
		return ""
	}
	call, ok := condition[0].Cmd.(*syntax.CallExpr)
	if !ok || len(call.Assigns) > 0 || len(call.Args) == 0 {
		return ""
	}
	var words []*syntax.Word
	switch call.Args[0].Lit() {
	case "[":
		if len(call.Args) != 4 || call.Args[3].Lit() != "]" {
			return ""
		}
		words = call.Args[1:3]
	case "test":
		if len(call.Args) != 3 {
			return ""
		}
		words = call.Args[1:3]
	default:
		return ""
	}
	if words[0].Lit() != "-n" || len(words[1].Parts) != 1 {
		return ""
	}
	quoted, ok := words[1].Parts[0].(*syntax.DblQuoted)
	if !ok || len(quoted.Parts) != 1 {
		return ""
	}
	parameter, ok := quoted.Parts[0].(*syntax.ParamExp)
	if !ok || !gitLabEmptyDefault(parameter.Exp) || parameter.Repl != nil || parameter.Slice != nil || parameter.Index != nil || parameter.Length || parameter.Excl {
		return ""
	}
	return parameter.Param.Value
}

// gitLabSources reports whether statements source anything.
func gitLabSources(stmts []*syntax.Stmt) bool {
	found := false
	for _, stmt := range stmts {
		syntax.Walk(stmt, func(node syntax.Node) bool {
			if call, ok := node.(*syntax.CallExpr); ok && len(call.Args) > 0 && (call.Args[0].Lit() == "source" || call.Args[0].Lit() == ".") {
				found = true
			}
			return !found
		})
	}
	return found
}

// gitLabSetupInvocation recognizes a script entry that sources the setup
// script. Supported means a release ddtest knows, sourced only after its
// SHA256 check passes. Inputs come from the entry's own assignments, then
// from the job's variables and earlier exports (known).
func gitLabSetupInvocation(command string, known map[string]string) (ciSetup, bool) {
	match := gitLabSetupURL.FindStringSubmatch(command)
	if match == nil {
		return ciSetup{}, false
	}
	release := match[1]
	setup := ciSetup{Reference: "test-optimization-gitlab-script " + release, Pinned: gitLabSetupVersion.MatchString(release), DefaultLatest: true, Inputs: map[string]string{}}
	checksum := gitLabSetupChecksum.FindStringSubmatch(command)
	for variable, value := range known {
		if input, ok := gitLabSetupInput(variable); ok {
			setup.Inputs[input] = value
		}
	}
	if site, ok := known["DD_SITE"]; ok && setup.Inputs["site"] == "" {
		setup.Inputs["site"] = site
	}
	file, parsed := parseCIShell(command)
	if !parsed {
		return setup, true
	}
	body, guard, ok := gitLabSetupBody(file.Stmts)
	if !ok {
		return setup, true // Sourced in a way ddtest does not recognize.
	}
	setup.shaped = true
	if len(body) > 2 {
		setup.after = body[2:]
	}
	for _, stmt := range setup.after {
		if !gitLabStateOnly(stmt) {
			setup.shaped = false // It runs a command, which would disappear with the setup.
		}
	}
	if call, ok := body[0].Cmd.(*syntax.CallExpr); ok && len(call.Args) == 0 {
		for _, assign := range call.Assigns {
			input, ok := gitLabSetupInput(assign.Name.Value)
			if !ok || assign.Value == nil {
				continue
			}
			value, _ := gitLabWord(assign.Value, known, "")
			setup.Inputs[input] = value
		}
	}
	// A guard must test the variable API_KEY reads, so the script runs
	// whenever its key is present.
	guarded := guard == "" || strings.Trim(strings.TrimPrefix(setup.Inputs["api_key"], "$"), "{}") == guard
	setup.Supported = checksum != nil && setup.Pinned && guarded && gitLabSetupChecksums[release] == checksum[1]
	return setup, true
}

func (gitLabCI) setupName() string { return "Datadog setup script" }

func (g gitLabCI) setupProblem(setup ciSetup, tracerInput string, exact func(string) bool, example, site string) string {
	if setup.Problem != "" {
		return setup.Problem
	}
	if !setup.Supported {
		return "The Datadog setup script " + setup.Reference + " is not a supported, checksum-verified release; source " + gitLabSetupDownload + gitLabSetupRelease + "/script.sh after its SHA256 check, as ddtest's instructions show."
	}
	key := strings.TrimSpace(setup.Inputs["api_key"])
	if key == "" {
		return "The Datadog setup script has no API_KEY; set API_KEY=\"$DD_API_KEY\" and store DD_API_KEY as a masked GitLab CI/CD variable."
	}
	if !gitLabVariableName.MatchString(key) {
		return "The Datadog setup script's API_KEY is not a CI/CD variable reference; never commit the key: set API_KEY=\"$DD_API_KEY\" and store DD_API_KEY as a masked GitLab CI/CD variable."
	}
	if tracer := setup.Inputs[tracerInput]; !exact(tracer) {
		return "The Datadog setup script's " + g.setupInput(tracerInput) + " " + fmt.Sprintf("%q", tracer) + " is not an exact release; set an exact version such as " + example + "."
	}
	configured := setup.Inputs["site"]
	if configured == "" {
		configured = defaultDatadogSite
	}
	if site != "" && configured != site {
		return "The Datadog setup script reports to " + configured + ", but the selected Datadog site is " + site + "; set SITE=\"" + site + "\" in its entry."
	}
	return ""
}

func (gitLabCI) apiKeyAction(bool) string {
	return "Add DD_API_KEY as a masked GitLab CI/CD variable (Settings > CI/CD > Variables), available to every branch whose pipelines run the tests (a protected variable only reaches protected branches). Until it exists, Test Optimization is disabled for those jobs and their tests run untraced."
}

// gitLabMissingKeyMessage is what a job without DD_API_KEY logs: the tests
// still run, untraced, like the GitHub action's non-blocking setup.
const gitLabMissingKeyMessage = "DD_API_KEY is not set; Datadog Test Optimization is disabled for this job."

// gitLabSetupText is the script entry that sources the setup script, as a
// block scalar like the script's README, inside a guard that skips it when
// DD_API_KEY is unset: the sourced script would otherwise exit the job before
// its tests. indent prefixes every line.
func gitLabSetupText(request ciSetupRequest, indent string) string {
	assignments := []string{`LANGUAGES="` + request.Language + `"`, `SITE="` + request.Site + `"`, `API_KEY="$DD_API_KEY"`}
	if request.TracerInput != "" {
		assignments = append(assignments, (gitLabCI{}).setupInput(request.TracerInput)+`="`+request.TracerVersion+`"`)
	}
	lines := []string{
		"- |",
		`  if [ -n "${DD_API_KEY:-}" ]; then`,
		"    " + strings.Join(assignments, " ") + ` \`,
		"    SRC=$(curl -fsSL " + gitLabSetupDownload + gitLabSetupRelease + `/script.sh); \`,
		`    echo "$SRC" | sha256sum | grep -q '^` + gitLabSetupChecksums[gitLabSetupRelease] + `' && \`,
		`      source <(echo "$SRC") || \`,
		`      echo "ERROR: SHA256 mismatch. Datadog Test Optimization autoinstrumentation not enabled." >&2`,
	}
	// Commands that need the setup run only with it.
	for _, command := range request.After {
		lines = append(lines, "    "+command)
	}
	lines = append(lines, "  else", `    echo "`+gitLabMissingKeyMessage+`" >&2`, "  fi")
	return indent + strings.Join(lines, "\n"+indent) + "\n"
}

func (g gitLabCI) setupInstructions(request ciSetupRequest) string {
	var text strings.Builder
	text.WriteString("Set up Datadog Test Optimization in GitLab CI with Datadog's GitLab setup script (" + gitLabSetupRelease + "), which installs the tracer and exports its configuration into the job's shell:\n\n")
	text.WriteString("1. Ask the user to add DD_API_KEY as a masked CI/CD variable in GitLab (Settings > CI/CD > Variables). Never write the key into the repository. A protected variable only reaches protected branches; jobs without the key skip the setup, log that Test Optimization is disabled, and run their tests untraced.\n")
	text.WriteString("2. In every GitLab CI job that runs the tests listed above, add this entry to its script immediately before the first test command, after dependency installation and any cd into the test directory. Keep every existing command, variable and rule unchanged; where a template or !reference supplies the script, add it there only when every job using it runs these tests.\n\n```yaml\n")
	text.WriteString(gitLabSetupText(request, ""))
	text.WriteString("```\n")
	text.WriteString("\nThe entry needs Bash: in jobs whose image lacks it (Alpine variants, BusyBox, distroless), GitLab runs the script with sh, so switch those jobs to a Bash-capable image such as the non-Alpine variant; installing Bash in the script does not change the job's shell.\n")
	text.WriteString("\n3. Set the environment and working directory the tests depend on before the entry. After it, extend the variables it exports instead of replacing them.\n")
	for _, note := range request.Notes {
		text.WriteString("\n- " + strings.ReplaceAll(note, "\n", "\n  "))
	}
	return strings.TrimSpace(text.String())
}

// gitLabWord evaluates a shell word: literal text and $NAME/${NAME} reads of
// known values. self is the variable being assigned; reading it while unset
// is empty. Other unknown reads stay as written and make the value dynamic.
func gitLabWord(word *syntax.Word, known map[string]string, self string) (value string, literal bool) {
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
				if part.Index != nil || part.Slice != nil || part.Repl != nil || !gitLabEmptyDefault(part.Exp) || part.Excl || part.Length {
					literal = false
					text.WriteString("${" + name + "...}")
					continue
				}
				if name == "CI_PROJECT_DIR" {
					text.WriteString(gitLabProjectDirectory)
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
func (s *gitLabState) statement(stmt *syntax.Stmt, known map[string]string) bool {
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
			value, literal := gitLabWord(word, known, "")
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
		if branch, ok := gitLabStaticBranch(command, known); ok {
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
func (s *gitLabState) assign(assigns []*syntax.Assign, known map[string]string, export bool) {
	for _, assign := range assigns {
		if assign.Name == nil || assign.Index != nil || assign.Array != nil {
			s.problem = "GitLab CI script assigns an array or indexed variable ddtest cannot follow"
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
		visible := mergeEnvironment(mergeEnvironment(known, s.references), s.env)
		value, literal := "", true
		if assign.Value != nil {
			value, literal = gitLabWord(assign.Value, visible, name)
		}
		if assign.Append {
			value = visible[name] + value
		}
		if !literal {
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
		if export || exported {
			s.env[name] = value
			delete(s.references, name)
		} else {
			s.references[name] = value
		}
	}
}

// changeDirectory follows a literal cd within the repository.
func (s *gitLabState) changeDirectory(arguments []string) {
	if len(arguments) != 1 || arguments[0] == "-" || strings.HasPrefix(arguments[0], "~") {
		s.problem = "GitLab CI script changes directory with cd " + strings.Join(arguments, " ") + ", which ddtest cannot follow"
		return
	}
	target := arguments[0]
	base := s.directory
	if rest, ok := strings.CutPrefix(target, gitLabProjectDirectory); ok {
		target, base = strings.TrimPrefix(rest, "/"), ""
	}
	if strings.HasPrefix(target, "/") || strings.Contains(target, "$") {
		s.problem = "GitLab CI script changes directory to " + arguments[0] + ", outside the repository ddtest reads"
		return
	}
	next := path.Clean(path.Join(base, target))
	if next == "." {
		next = ""
	}
	if strings.HasPrefix(next, "..") {
		s.problem = "GitLab CI script changes directory to " + arguments[0] + ", outside the repository"
		return
	}
	s.directory = next
}

// unfollowed records state changes inside control flow ddtest does not
// evaluate: they make later entries unresolved.
func (s *gitLabState) unfollowed(stmt *syntax.Stmt) {
	syntax.Walk(stmt, func(node syntax.Node) bool {
		switch node := node.(type) {
		case *syntax.Subshell, *syntax.CmdSubst:
			return false
		case *syntax.DeclClause:
			if node.Variant.Value == "export" {
				s.problem = "GitLab CI script exports variables conditionally, which ddtest cannot follow"
			}
		case *syntax.CallExpr:
			if len(node.Args) == 0 && len(node.Assigns) > 0 {
				s.problem = "GitLab CI script assigns variables conditionally, which ddtest cannot follow"
			}
			if len(node.Args) > 0 && node.Args[0].Lit() == "cd" {
				s.problem = "GitLab CI script changes directory conditionally, which ddtest cannot follow"
			}
		}
		return true
	})
}

// testWorkflowsWithSetup lists the workflows whose jobs run framework's tests
// and those where an active setup selecting language precedes every test step.
func testWorkflowsWithSetup(root string, workflows []ciWorkflow, language, framework string) (found, configured []string) {
	token := setupActionLanguage(language)
	for _, workflow := range workflows {
		tests, complete := false, true
		for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			job := workflow.Jobs[name]
			if disabledCondition(job.If) {
				continue
			}
			setup := false
			for _, step := range job.Steps {
				if current, ok := datadogSetup(step); ok {
					selected, err := setupSelectsLanguage(current, nil, token)
					setup = setup || err == nil && selected
				}
				resolution := resolveTestStep(root, workflow, job, step, language, framework)
				if resolution.Review || !resolution.Matched && resolution.Reason == "" {
					continue
				}
				tests = true
				complete = complete && setup && resolution.Reason == ""
			}
		}
		if tests {
			found = append(found, workflow.Path)
			if complete {
				configured = append(configured, workflow.Path)
			}
		}
	}
	return found, configured
}

// gitLabStaticBranch returns the statements an if clause runs when its
// conditions are tests on known values: [ -n "$X" ], [ -z "$X" ] and
// [ "$X" = "value" ] (also != and ==, or test ...).
func gitLabStaticBranch(clause *syntax.IfClause, known map[string]string) ([]*syntax.Stmt, bool) {
	for current := clause; current != nil; current = current.Else {
		if len(current.Cond) == 0 {
			return current.Then, true // else
		}
		truth, ok := gitLabStaticTest(current.Cond, known)
		if !ok {
			return nil, false
		}
		if truth {
			return current.Then, true
		}
	}
	return nil, true
}

// gitLabStaticTest evaluates one [ ... ] or test ... condition whose words
// are literal or read known values.
func gitLabStaticTest(condition []*syntax.Stmt, known map[string]string) (bool, bool) {
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
		value, literal := gitLabWord(word, known, "")
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

// gitLabResolveBranches replaces a script entry that is one if clause on
// known values with the branch it runs, so the job's commands are those of
// its matrix entry, as in if [ "$RUBY_IMAGE" = "ruby:3.0" ]; then ...; fi.
func gitLabResolveBranches(command string, known map[string]string) string {
	file, ok := parseCIShell(command)
	if !ok || len(file.Stmts) != 1 {
		return command
	}
	clause, ok := file.Stmts[0].Cmd.(*syntax.IfClause)
	if !ok || file.Stmts[0].Background || len(file.Stmts[0].Redirs) > 0 {
		return command
	}
	branch, ok := gitLabStaticBranch(clause, known)
	if !ok {
		return command
	}
	var text strings.Builder
	printer := syntax.NewPrinter()
	for i, stmt := range branch {
		if i > 0 {
			text.WriteString("\n")
		}
		if err := printer.Print(&text, stmt); err != nil {
			return command
		}
	}
	return text.String()
}

// gitLabEmptyDefault reports no expansion operator, or ${NAME:-} (empty
// when unset, which keeps set -u scripts running).
func gitLabEmptyDefault(expansion *syntax.Expansion) bool {
	return expansion == nil || expansion.Op == syntax.DefaultUnsetOrNull && (expansion.Word == nil || len(expansion.Word.Parts) == 0)
}
