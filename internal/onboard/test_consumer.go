// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"mvdan.cc/sh/v3/syntax"
)

// Executable names of each framework, as they appear in CI commands.
var frameworkExecutables = map[string][]string{
	"jest": {"jest"}, "vitest": {"vitest"}, "mocha": {"mocha", "_mocha"}, "playwright": {"playwright"},
	"cypress": {"cypress"}, "cucumber": {"cucumber", "cucumber-js"}, "webdriverio": {"wdio", "webdriverio"},
}

// Task runners and package binaries runners that can start any test suite.
var taskRunners = []string{"npx", "bunx", "make", "just", "task", "nx", "turbo", "lerna", "rush", "gulp", "grunt"}

var interpreters = []string{"node", "bash", "sh", "zsh", "deno", "tsx", "ts-node"}

// Interpreter invocations that only print metadata.
var interpreterMetadataFlags = []string{"--version", "-v", "-V", "--help", "-h"}

// Package-manager subcommands that start a package script or binary.
var scriptSubcommands = []string{"test", "t", "tst", "run", "run-script", "exec", "x", "dlx", "start"}

// Package-manager subcommands that never start a script or test binary.
var managerOnlySubcommands = []string{"install", "i", "ci", "add", "remove", "uninstall", "audit", "config", "publish", "pack", "why", "list", "ls", "outdated", "dedupe", "store", "version", "view", "info", "cache", "stage", "login", "whoami", "dist-tag"}

var commandSeparator = regexp.MustCompile(`\n|&&|\|\||;|\||\$\(|` + "`" + `|\bthen\b|\bdo\b|\belse\b|\belif\b`)

var heredocStart = regexp.MustCompile(`<<-?\s*['"]?([A-Za-z_][A-Za-z0-9_]*)['"]?`)

// mayStartTests reports whether an unresolved step could hide a test of the
// selected framework. Only steps made of shell builtins, known non-test tools
// (system packages, coverage merging, linters), package-manager setup and
// publishing, or another framework are outside the test path; an unknown
// executable, action, package script or repository file may wrap a test.
func mayStartTests(step runtimeStep, framework string) bool {
	return mayStartTestsIn("", step, framework, 0)
}

// stepMayStartTests is mayStartTests with the step's package scripts readable,
// so a known script whose body can't start a test is not a possible wrapper.
func stepMayStartTests(root string, workflow ciWorkflow, job runtimeJob, step runtimeStep, framework string) bool {
	directory, err := repositoryDirectory(root, root, stepDirectory(workflow, job, step))
	if err != nil {
		directory = ""
	}
	return mayStartTestsIn(directory, step, framework, 0)
}

func mayStartTestsIn(directory string, step runtimeStep, framework string, depth int) bool {
	if strings.TrimSpace(step.Run) == "" {
		return step.Uses != "" // An unresolved action can run anything.
	}
	var lines []string
	terminator := ""
	type heredoc struct{ command, body string }
	var heredocs []heredoc
	for line := range strings.SplitSeq(step.Run, "\n") {
		if terminator != "" {
			if strings.TrimSpace(line) == terminator {
				terminator = ""
			} else {
				heredocs[len(heredocs)-1].body += line + "\n"
			}
			continue // An inline script body is data for its interpreter.
		}
		if match := heredocStart.FindStringSubmatch(line); match != nil {
			terminator = match[1]
			command := ""
			for _, word := range strings.Fields(line) {
				if !strings.Contains(word, "=") {
					command = filepath.Base(word)
					break
				}
			}
			heredocs = append(heredocs, heredoc{command: command})
		}
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		lines = append(lines, line)
	}
	text := strings.ToLower(strings.Join(lines, "\n"))
	if namesFramework(text, framework, true) {
		return true
	}
	text = shellCommandText(text)
	scripts := packageScripts(directory)
	inlineCodeSafe := inlineEvalsCannotLaunch(step.Run, directory, framework, depth)
	// A heredoc feeds its body to the interpreter on stdin: JavaScript is read
	// like -e code, and a shell body like any other step.
	heredocsSafe := true
	for _, doc := range heredocs {
		switch {
		case slices.Contains(javaScriptInterpreters, doc.command):
			heredocsSafe = heredocsSafe && inlineCodeCannotLaunch(doc.body, directory, framework, depth)
		case slices.Contains(interpreters, doc.command):
			heredocsSafe = heredocsSafe && depth < 8 && !mayStartTestsIn(directory, runtimeStep{Run: doc.body}, framework, depth+1)
		}
	}
	for _, segment := range commandSeparator.Split(text, -1) {
		words := strings.Fields(segment)
		for len(words) > 0 && (strings.Contains(words[0], "=") || slices.Contains([]string{"sudo", "env", "time", "exec", "xargs", "command", "if", "while", "until", "!", "{", "("}, words[0])) {
			words = words[1:]
		}
		if len(words) > 0 {
			words[0] = strings.TrimLeft(words[0], "({!")
		}
		for len(words) > 0 && strings.HasPrefix(words[0], "-") {
			words = words[1:] // xargs/env options before the command.
		}
		if len(words) == 0 {
			continue
		}
		// A segment that runs another framework is outside this one's scope.
		other := namesFramework(segment, framework, false)
		command := strings.Trim(words[0], `"'`)
		base := filepath.Base(command)
		switch {
		case strings.HasPrefix(command, "./") || strings.HasPrefix(command, "../") || slices.Contains([]string{".sh", ".js", ".mjs", ".cjs", ".ts"}, filepath.Ext(command)):
			return true
		case slices.Contains(taskRunners, base):
			if !other {
				return true
			}
		case slices.Contains(interpreters, base):
			// -c/-e, a file or stdin can all start a test launcher; version
			// and help queries never do.
			metadataOnly := len(words) > 1 && !slices.ContainsFunc(words[1:], func(word string) bool { return !slices.Contains(interpreterMetadataFlags, word) })
			// Print mode evaluates one expression to print a value, such as a version.
			if metadataOnly || len(words) > 1 && (words[1] == "-p" || words[1] == "--print") {
				continue
			}
			// Inline code that can't spawn a process or load a repository file
			// can't start a test launcher.
			if inlineCodeSafe && evaluatesInlineCode(words[1:]) || heredocsSafe && readsHeredoc(words[1:]) {
				continue
			}
			return true
		case slices.Contains([]string{"npm", "pnpm", "yarn", "bun"}, base):
			words, elsewhere := managerSubcommand(words)
			starts := len(words) == 1 || slices.Contains(scriptSubcommands, words[1])
			// pnpm/yarn/bun <name> run a script or binary of that name.
			starts = starts || base != "npm" && !strings.HasPrefix(words[1], "-") && !slices.Contains(managerOnlySubcommands, words[1])
			if !starts || other {
				continue
			}
			// A known package script is as safe as its body (and npm's hooks).
			if body, ok := scriptBody(scripts, base, words); ok && !elsewhere && depth < 8 && !mayStartTestsIn(directory, runtimeStep{Run: body}, framework, depth+1) {
				continue
			}
			return true
		case base == "" || slices.Contains(nonTestTools, base):
		default:
			if !other {
				return true // An unknown executable may wrap tests.
			}
		}
	}
	return false
}

// Shell builtins and tools that never start a JavaScript test suite.
var nonTestTools = []string{
	"echo", "printf", "cd", "pushd", "popd", "export", "set", "unset", "shopt", "exit", "return", "true", "false", ":", "[", "[[", "test", "fi", "then", "else", "elif", "done", "esac", "for", "case", "select", "function", "}", ")", "read", "sleep", "wait", "trap", "local", "declare",
	"mkdir", "cp", "mv", "rm", "rmdir", "cat", "ls", "touch", "chmod", "chown", "ln", "tee", "sed", "awk", "grep", "find", "jq", "yq", "tar", "zip", "unzip", "gzip", "curl", "wget", "git", "gh", "head", "tail", "sort", "uniq", "wc", "cut", "tr", "base64", "sha256sum", "shasum", "md5sum", "date", "uname", "whoami", "hostname", "nproc", "df", "du", "basename", "dirname", "realpath", "which", "type",
	"apt", "apt-get", "dpkg", "brew", "choco", "winget", "yum", "dnf", "apk", "pip", "pip3", "pipx", "corepack", "nvm", "volta", "fnm", "asdf", "rustup", "cargo", "go",
	"lcov", "genhtml", "codecov", "coveralls", "cspell", "markdownlint", "markdownlint-cli2", "prettier", "eslint", "tsc", "stylelint", "commitlint", "biome", "oxlint",
	"tsup", "unbuild", "esbuild", "rollup", "webpack", "vite", "babel", "swc", "parcel", "typedoc", "rimraf",
}

// Package-manager options that select another package or directory.
var managerDirectoryOptions = []string{"--prefix", "-c", "--dir", "--cwd", "--workspace", "-w", "--filter", "-f"}

// managerSubcommand drops options before a package manager's subcommand, and
// reports whether they run it in another package, whose scripts aren't read.
func managerSubcommand(words []string) ([]string, bool) {
	rest, elsewhere := words[1:], false
	for len(rest) > 0 && strings.HasPrefix(rest[0], "-") {
		option, _, assigned := strings.Cut(rest[0], "=")
		directory := slices.Contains(managerDirectoryOptions, option)
		elsewhere = elsewhere || directory || option == "--workspaces" || option == "-ws" || option == "-r" || option == "--recursive"
		rest = rest[1:]
		if directory && !assigned && len(rest) > 0 {
			rest = rest[1:]
		}
	}
	return append([]string{words[0]}, rest...), elsewhere
}

// Package scripts of directory, keyed by lowercase name ("" reads none).
func packageScripts(directory string) map[string]string {
	if directory == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(directory, "package.json"))
	if err != nil {
		return nil
	}
	var manifest struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(data, &manifest) != nil {
		return nil
	}
	scripts := map[string]string{}
	for name, body := range manifest.Scripts {
		scripts[strings.ToLower(name)] = body
	}
	return scripts
}

// scriptBody returns the package script a package-manager invocation runs,
// joined with the pre/post hooks npm runs around it.
func scriptBody(scripts map[string]string, manager string, words []string) (string, bool) {
	name := "test"
	if len(words) > 1 && slices.Contains([]string{"run", "run-script"}, words[1]) {
		if len(words) < 3 {
			return "", false
		}
		name = words[2]
	} else if len(words) > 1 && !slices.Contains([]string{"test", "t", "tst"}, words[1]) {
		if manager == "npm" || slices.Contains(scriptSubcommands, words[1]) {
			return "", false // npm start/exec and binaries run something else.
		}
		name = words[1]
	}
	body, ok := scripts[name]
	if !ok {
		return "", false
	}
	return strings.Join([]string{scripts["pre"+name], body, scripts["post"+name]}, "\n"), true
}

// Inline code loading a repository module, a shell helper library or another
// runtime's process API could start a test launcher.
var inlineModuleLauncher = regexp.MustCompile(`(?i)\bzx\b|shelljs|\bexeca\b|worker_threads|\bcluster\b|Bun\.spawn|Deno\.(?:Command|run)|(require|import)\s*\(?\s*['"]\.{1,2}/|\bfrom\s+['"]\.{1,2}/`)

const childProcessModule = `['"](?:node:)?child_process['"]`

var childProcessUse = regexp.MustCompile(childProcessModule)
var childProcessAlias = regexp.MustCompile(`(?:const|let|var)\s+(\w+)\s*=\s*require\s*\(\s*` + childProcessModule + `\s*\)|import\s+(?:\*\s+as\s+)?(\w+)\s+from\s+` + childProcessModule)

const childProcessMethods = `execFileSync|execFile|spawnSync|spawn|execSync|exec|fork`

var argvProcessCall = regexp.MustCompile(`^(?:execFileSync|execFile|spawnSync|spawn)\s*\(\s*(['"])([^'"]+)['"]\s*(?:,\s*\[([^\]]*)\])?`)
var shellProcessCall = regexp.MustCompile("^(?:execSync|exec)\\s*\\(\\s*['\"`]([^'\"`$]*)['\"`]")
var literalArgument = regexp.MustCompile(`^\s*['"]([^'"]*)['"]\s*$`)

// javaScriptInterpreters read code from -e or stdin.
var javaScriptInterpreters = []string{"node", "deno", "tsx", "ts-node"}

// inlineCodeCannotLaunch reports whether inline JavaScript can't start a test:
// it loads no repository module or shell helper, and every child_process call
// names a literal command that can't start one either. A call whose command
// isn't literal could run anything.
func inlineCodeCannotLaunch(code, directory, framework string, depth int) bool {
	if inlineModuleLauncher.MatchString(code) || depth >= 8 {
		return false
	}
	if !childProcessUse.MatchString(code) {
		return true
	}
	receivers := []string{`require\s*\(\s*` + childProcessModule + `\s*\)`}
	for _, alias := range childProcessAlias.FindAllStringSubmatch(code, -1) {
		receivers = append(receivers, regexp.QuoteMeta(alias[1]+alias[2]))
	}
	calls := regexp.MustCompile(`(?:^|[^.\w$])(` + childProcessMethods + `)\s*\(|(?:` + strings.Join(receivers, "|") + `)\s*\.\s*(` + childProcessMethods + `)\s*\(`)
	for _, match := range calls.FindAllStringSubmatchIndex(code, -1) {
		start := match[2]
		if start < 0 {
			start = match[4]
		}
		call := code[start:]
		var command string
		if argv := argvProcessCall.FindStringSubmatch(call); argv != nil {
			words := []string{argv[2]}
			for _, argument := range strings.Split(argv[3], ",") {
				if strings.TrimSpace(argument) == "" {
					continue
				}
				if literal := literalArgument.FindStringSubmatch(argument); literal != nil {
					words = append(words, literal[1])
				} else {
					words = append(words, "_") // A computed argument names no script or test.
				}
			}
			command = strings.Join(words, " ")
		} else if shell := shellProcessCall.FindStringSubmatch(call); shell != nil {
			command = shell[1]
		} else {
			return false
		}
		if mayStartTestsIn(directory, runtimeStep{Run: command}, framework, depth+1) {
			return false
		}
	}
	return true
}

// readsHeredoc reports whether interpreter arguments read a heredoc on stdin
// rather than a script file.
func readsHeredoc(args []string) bool {
	for _, arg := range args {
		switch {
		case strings.HasPrefix(arg, "<<"):
			return true
		case !strings.HasPrefix(arg, "-"):
			return false
		}
	}
	return false
}

// evaluatesInlineCode reports whether interpreter arguments pass code with -e/--eval.
func evaluatesInlineCode(args []string) bool {
	for _, arg := range args {
		switch {
		case arg == "-e" || arg == "--eval" || strings.HasPrefix(arg, "--eval="):
			return true
		case !strings.HasPrefix(arg, "-"):
			return false // A script file or stdin runs before any later flag.
		}
	}
	return false
}

// inlineEvalsCannotLaunch reports whether no interpreter -e/--eval body in a
// script can start a test (see inlineCodeCannotLaunch).
func inlineEvalsCannotLaunch(run, directory, framework string, depth int) bool {
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(run), "")
	if err != nil {
		return false
	}
	safe := true
	syntax.Walk(file, func(node syntax.Node) bool {
		call, ok := node.(*syntax.CallExpr)
		if !ok || !safe || len(call.Args) == 0 || !slices.Contains(interpreters, filepath.Base(call.Args[0].Lit())) {
			return safe
		}
		for i, arg := range call.Args[1:] {
			var printed strings.Builder
			if syntax.NewPrinter().Print(&printed, arg) != nil {
				safe = false
				break
			}
			code, eval := "", false
			switch word := printed.String(); {
			case word == "-e" || word == "--eval":
				if i+2 < len(call.Args) {
					var body strings.Builder
					if syntax.NewPrinter().Print(&body, call.Args[i+2]) != nil {
						safe = false
						break
					}
					code, eval = body.String(), true
				}
			case strings.HasPrefix(word, "--eval="):
				code, eval = strings.TrimPrefix(word, "--eval="), true
			}
			if eval && !inlineCodeCannotLaunch(code, directory, framework, depth) {
				safe = false
			}
		}
		return safe
	})
	return safe
}

// namesFramework reports whether text names an executable of the selected
// framework (selected) or of any other framework (!selected).
func namesFramework(text, framework string, selected bool) bool {
	var words []string
	for _, token := range strings.FieldsFunc(text, func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune("\"'`;|&()=,", r) }) {
		if strings.HasPrefix(token, "@") {
			continue // A scoped package spec is an install argument, not an executable.
		}
		word, _, _ := strings.Cut(filepath.Base(token), "@")
		words = append(words, word)
	}
	for name, executables := range frameworkExecutables {
		if (name == framework) == selected && slices.ContainsFunc(words, func(word string) bool { return slices.Contains(executables, word) }) {
			return true
		}
	}
	return false
}

// shellCommandText keeps the command structure of a script: quoted text becomes
// one placeholder word in its position (command substitutions inside double
// quotes start their own segment) and line continuations are joined.
func shellCommandText(script string) string {
	script = strings.ReplaceAll(script, "\\\n", " ")
	var out strings.Builder
	var quote rune
	substitution := 0 // Open $( levels inside a double-quoted string.
	backtick := false // Inside a `...` substitution of a double-quoted string.
	for i := 0; i < len(script); i++ {
		c := rune(script[i])
		switch {
		case backtick:
			if c == '`' {
				backtick = false
				out.WriteString("\n")
			} else {
				out.WriteRune(c)
			}
		case quote == '"' && substitution == 0 && c == '`':
			backtick = true
			out.WriteString("\n")
		case quote == '\'':
			if c == '\'' {
				quote = 0
			}
		case quote == '"' && substitution == 0:
			switch {
			case c == '\\':
				i++
			case c == '"':
				quote = 0
			case strings.HasPrefix(script[i:], "$("):
				substitution = 1
				i++
				out.WriteString("\n")
			}
		case quote == '"' && c == '(':
			substitution++
			out.WriteRune(c)
		case quote == '"' && c == ')':
			substitution--
			if substitution > 0 {
				out.WriteRune(c)
			} else {
				out.WriteString("\n")
			}
		case c == '\'' || c == '"':
			if quote == 0 {
				quote = c
				// The quoted word, in its position; NAME="..." stays one assignment.
				if text := out.String(); text != "" && !unicode.IsSpace(rune(text[len(text)-1])) {
					out.WriteString("q ")
				} else {
					out.WriteString(" q ")
				}
			} else {
				out.WriteRune(c)
			}
		default:
			out.WriteRune(c)
		}
	}
	return out.String()
}

// Commands that only read state or print. Each CI step runs in a fresh shell,
// so their shell options and variables don't reach later steps either.
var readOnlyCommands = []string{"echo", "printf", "test", "[", "exit", "return", "true", "false", ":", "set", "grep", "egrep", "fgrep", "cat", "head", "tail", "wc", "jq", "sort", "uniq", "cut", "tr", "basename", "dirname", "date", "uname", "which", "type", "command", "sleep"}

// Package managers that may be installed globally as CI toolchain setup.
var packageManagerPackages = []string{"npm", "pnpm", "yarn", "corepack"}

// checkOnlyStep reports whether a step only reads state, prints, or upgrades
// the package manager itself: it writes no repository file, no CI file such as
// GITHUB_ENV, and installs no project dependency, so it can't prepare a test.
func checkOnlyStep(run string) bool {
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(run), "")
	if err != nil || len(file.Stmts) == 0 {
		return false
	}
	safe := true
	syntax.Walk(file, func(node syntax.Node) bool {
		switch node := node.(type) {
		case *syntax.FuncDecl, *syntax.CoprocClause:
			safe = false
		case *syntax.Redirect:
			if node.Op == syntax.RdrOut || node.Op == syntax.AppOut || node.Op == syntax.RdrAll || node.Op == syntax.AppAll || node.Op == syntax.RdrClob || node.Op == syntax.RdrInOut {
				target := strings.Trim(shellText(node.Word), `"`)
				safe = safe && (target == "/dev/null" || target == "$GITHUB_STEP_SUMMARY" || target == "${GITHUB_STEP_SUMMARY}")
			}
		case *syntax.CallExpr:
			if len(node.Args) > 0 {
				safe = safe && readOnlyCall(node)
			}
		}
		return safe
	})
	return safe
}

func readOnlyCall(call *syntax.CallExpr) bool {
	var words []string
	for _, arg := range call.Args {
		words = append(words, arg.Lit())
	}
	name := words[0]
	switch {
	case slices.Contains(readOnlyCommands, name):
		return true
	case name == "node":
		return len(words) == 2 && slices.Contains([]string{"-p", "--print", "-v", "--version"}, words[1]) || len(words) == 3 && (words[1] == "-p" || words[1] == "--print")
	case slices.Contains([]string{"npm", "pnpm", "yarn"}, name):
		if len(words) < 2 {
			return false
		}
		if slices.Contains([]string{"view", "info", "show", "--version", "-v", "whoami", "ls", "list", "why", "outdated"}, words[1]) || words[1] == "config" && len(words) > 2 && words[2] == "get" {
			return true
		}
		// Installing the package manager itself globally is toolchain setup.
		if !slices.Contains([]string{"install", "i", "add"}, words[1]) || !slices.Contains(words, "-g") && !slices.Contains(words, "--global") {
			return false
		}
		packages := 0
		for _, word := range words[2:] {
			if strings.HasPrefix(word, "-") {
				continue
			}
			name, _, _ := strings.Cut(word, "@")
			if !slices.Contains(packageManagerPackages, name) {
				return false
			}
			packages++
		}
		return packages > 0
	}
	return false
}
