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

	"mvdan.cc/sh/v3/syntax"
)

// A deploy or packaging job (sentry-cli, git archive, web-ext build, upload
// scripts) never starts the selected framework when nothing it runs names
// it: not its commands, the package scripts they run, nor the repository
// scripts those run. Actions and unreadable configuration may, except the
// checkout, setup, cache and artifact actions of setupOnlyAction.
//
// Published packages ddtest doesn't read, imported by those scripts or a
// package merely named publish, release or deploy that a package runner runs
// (npx @acme/publish, see unreadRunnerPackage), don't keep such a job in scope:
// it runs no test ddtest can find, so nothing in it is validated, and the
// caveat names them. A step of a job that also runs tests still blocks on
// them (see mayStartTestsIn).
func (javascriptDiscovery) outsideFrameworkJob(root string, workflow ciWorkflow, job runtimeJob, row map[string]any, framework string) (bool, []string) {
	unread := &unreadPackages{}
	for _, step := range job.Steps {
		if active, err := stepCondition(step, row, conditionEnvironment(workflow, job, runtimeStep{}, row)); err == nil && !active {
			continue
		}
		if strings.TrimSpace(step.Run) == "" {
			if step.ResolutionError != "" || (step.Uses != "" && !setupOnlyAction(step.Uses)) {
				return false, nil
			}
			continue
		}
		directory, err := repositoryDirectory(root, root, stepDirectory(workflow, job, step))
		if err != nil || mentionsFrameworkReading(root, directory, step.Run, framework, 0, unread) {
			return false, nil
		}
	}
	return true, unread.names
}

// setupOnlyActions are official GitHub Actions that check out the
// repository, install a runtime, or save and restore caches and artifacts,
// with each registered language's runtime setup action (see
// discoveryLanguage.runtimeSetupAction: actions/setup-node and the other
// languages' setup actions). None runs a project script or command, so none
// can start the project's tests, and a job of them and steps that can't
// start tests runs none. They may change test inputs (a restored cache, a
// downloaded artifact), so a job that runs tests still reads them as it
// reads any other action (see mayStartTestsIn). Any other action,
// third-party or unknown, may run anything and keeps its job in scope. Keep
// this list explicit: add an action only when it can't run repository code.
var setupOnlyActions = []string{
	"actions/checkout",
	"actions/setup-go", "actions/setup-dotnet",
	"actions/cache", "actions/cache/restore", "actions/cache/save",
	"actions/upload-artifact", "actions/download-artifact",
}

// setupOnlyAction reports whether uses names one of setupOnlyActions, or a
// registered language's runtime setup action, at a tag, branch or commit SHA
// (actions/checkout@v4, actions/checkout@<sha>).
func setupOnlyAction(uses string) bool {
	name, ref, pinned := strings.Cut(strings.ToLower(strings.TrimSpace(uses)), "@")
	if !pinned || ref == "" {
		return false
	}
	return slices.Contains(setupOnlyActions, name) || slices.ContainsFunc(discoveryLanguages, func(language discoveryLanguage) bool { return language.runtimeSetupAction() == name })
}

// mentionsFramework reports whether shell text, or a package or repository
// script it runs, names framework. Anything it cannot read counts as naming
// it.
func mentionsFramework(root, directory, text, framework string, depth int) bool {
	return mentionsFrameworkReading(root, directory, text, framework, depth, nil)
}

// mentionsFrameworkReading is mentionsFramework that, with unread, records
// the published packages ddtest doesn't read instead of counting them as
// naming the framework (see unreadPackages).
func mentionsFrameworkReading(root, directory, text, framework string, depth int, unread *unreadPackages) bool {
	if depth > 8 || namesFramework(text, framework, true) || strings.Contains(strings.ToLower(text), framework) {
		return true
	}
	file, err := syntax.NewParser().Parse(strings.NewReader(text), "")
	if err != nil {
		return true
	}
	mentioned := false
	syntax.Walk(file, func(node syntax.Node) bool {
		call, ok := node.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 || mentioned {
			return !mentioned
		}
		var all []string
		for _, word := range call.Args {
			all = append(all, word.Lit())
		}
		// Wrappers (npx nyc -- npm test, env, time) run a later command.
		for i := range all {
			if mentioned = mentionsCommand(root, directory, all[i:], framework, depth, i == 0, unread); mentioned {
				break
			}
		}
		return !mentioned
	})
	return mentioned
}

// mentionsCommand reports whether one command, as words, runs a package or
// repository script that names framework.
func mentionsCommand(root, directory string, words []string, framework string, depth int, executable bool, unread *unreadPackages) bool {
	name := filepath.Base(words[0])
	// Package runners execute their target as an executable.
	if target := packageRunnerTarget(words); target != nil {
		if unread != nil && unreadRunnerPackage(root, directory, target) {
			unread.add(target[0])
			return false
		}
		return mentionsCommand(root, directory, target, framework, depth, true, unread)
	}
	if executable {
		if mayStart, handled := recognizedTool(root, directory, words, framework, depth); handled {
			return mayStart
		}
	}
	switch {
	case oneOf(name, "npm", "pnpm", "yarn", "bun"):
		// An option before the subcommand may select another package: follow
		// one literal directory; any other selection may run anything.
		if len(words) > 1 && strings.HasPrefix(words[1], "-") && !slices.Contains(interpreterMetadataFlags, words[1]) {
			option, value, assigned := strings.Cut(words[1], "=")
			rest := words[2:]
			if !assigned && len(rest) > 0 {
				value, rest = rest[0], rest[1:]
			}
			if !slices.Contains([]string{"--prefix", "-C", "--dir", "--cwd"}, option) || depth > 8 {
				return true
			}
			target, err := repositoryDirectory(root, directory, value)
			if err != nil {
				return true
			}
			return mentionsCommand(root, target, append([]string{words[0]}, rest...), framework, depth+1, executable, unread)
		}
		script := ""
		switch {
		case len(words) > 2 && (words[1] == "run" || words[1] == "run-script"):
			script = words[2]
		case len(words) > 1 && (words[1] == "test" || words[1] == "t"):
			script = "test"
		case len(words) > 1 && oneOf(words[1], "publish", "pack"):
			target, ok := publishTarget(root, directory, words[2:])
			return !ok || mentionsPublishLifecycle(root, target, framework, depth)
		case len(words) > 1 && name != "npm" && !strings.HasPrefix(words[1], "-") && !slices.Contains(managerOnlySubcommands, words[1]) && !slices.Contains(scriptSubcommands, words[1]):
			script = words[1] // yarn, pnpm and bun run a script by its name.
		}
		if script == "" {
			return false
		}
		body, ok := readPackageScript(directory, script)
		if !ok {
			return true
		}
		// npm and Yarn run a script's pre and post scripts around it.
		pre, _ := readPackageScript(directory, "pre"+script)
		post, _ := readPackageScript(directory, "post"+script)
		return mentionsFrameworkReading(root, directory, strings.Join([]string{pre, body, post}, "\n"), framework, depth+1, unread)
	case oneOf(name, "node", "ts-node", "tsx", "sh", "bash") && len(words) > 1 && !strings.HasPrefix(words[1], "-"):
		return repositoryScriptMayRun(root, directory, words[1], framework, 0, unread)
	case executable && strings.Contains(words[0], "/"):
		return repositoryScriptMayRun(root, directory, words[0], framework, 0, unread)
	case executable && !oneOf(name, releaseTools...):
		return true // An unknown executable or wrapper can run anything.
	}
	return false
}

// releaseTools never start a test framework: file handling, archives,
// version control, release, deploy and packaging tools, bundlers, compilers
// and linters. Package
// managers are followed through the scripts they run.
var releaseTools = []string{
	"cp", "mv", "rm", "mkdir", "rmdir", "ln", "touch", "cat", "echo", "printf", "ls", "pwd", "cd", "pushd", "popd", "true", "chmod", "realpath", "dirname", "basename", "which",
	"zip", "unzip", "tar", "gzip", "gunzip", "sha256sum", "shasum", "md5sum",
	"git", "sentry-cli", "web-ext", "gh", "glab", "release-cli", "aws", "gsutil", "az",
	"webpack", "rollup", "esbuild", "tsc", "tsup", "babel", "parcel", "swc", "terser", "eslint", "prettier", "stylelint", "formatjs",
	"npm", "pnpm", "yarn", "bun",
}

// packageRunnerTarget returns the command a package runner executes: npx,
// bunx and pnpx targets, npm exec/x, and yarn and pnpm dlx/exec. nil when
// words are not such a runner.
func packageRunnerTarget(words []string) []string {
	name := filepath.Base(words[0])
	rest := words[1:]
	switch {
	case oneOf(name, "npx", "bunx", "pnpx"):
	case name == "npm" && len(rest) > 0 && oneOf(rest[0], "exec", "x"),
		oneOf(name, "yarn", "pnpm") && len(rest) > 0 && oneOf(rest[0], "dlx", "exec"):
		rest = rest[1:]
	default:
		return nil
	}
	for len(rest) > 0 && strings.HasPrefix(rest[0], "-") {
		if oneOf(rest[0], "-p", "--package", "-c", "--call") && len(rest) > 1 {
			rest = rest[1:]
		}
		if rest[0] == "--" {
			rest = rest[1:]
			break
		}
		rest = rest[1:]
	}
	if len(rest) == 0 {
		return []string{"(package runner without a command)"}
	}
	return rest
}

func oneOf(name string, candidates ...string) bool {
	for _, candidate := range candidates {
		if name == candidate {
			return true
		}
	}
	return false
}

// readPackageScript reads a script of the package.json in directory.
func readPackageScript(directory, name string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(directory, "package.json"))
	if err != nil {
		return "", false
	}
	var manifest struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(data, &manifest) != nil {
		return "", false
	}
	body, ok := manifest.Scripts[name]
	return body, ok
}

// repositoryScriptMayRun reports whether a repository script may start
// framework's tests. Node scripts are inspected with their local imports;
// other scripts are followed as shell. Anything unreadable may. With unread,
// published packages ddtest doesn't read are recorded there instead.
func repositoryScriptMayRun(root, directory, path, framework string, depth int, unread *unreadPackages) bool {
	if strings.ContainsAny(path, "$`*?") || filepath.IsAbs(path) || !filepath.IsLocal(path) {
		return true
	}
	info, ok := repositoryFile(root, directory, path)
	if !ok || info.IsDir() || info.Size() > 1<<20 {
		return true
	}
	data, err := os.ReadFile(filepath.Join(directory, path))
	if err != nil {
		return true
	}
	switch filepath.Ext(path) {
	case ".js", ".cjs", ".mjs", ".ts", ".cts", ".mts":
		return nodeScriptInspect(root, directory, filepath.Join(directory, path), framework, map[string]bool{}, true, unread)
	}
	// A shell wrapper runs its commands: follow them like a CI step's.
	return mentionsFrameworkReading(root, directory, string(data), framework, depth+1, unread)
}

var (
	nodeLocalImport   = regexp.MustCompile(`(?m)(?:require|import)\s*\(\s*['"](\.{1,2}/[^'"]+)['"]\s*\)|from\s+['"](\.{1,2}/[^'"]+)['"]|^\s*import\s+['"](\.{1,2}/[^'"]+)['"]`)
	nodeDynamicImport = regexp.MustCompile(`(?:require|import)\s*\(\s*[^'"\s)]`)
	nodeProcessCall   = regexp.MustCompile("\\b(execSync|execFileSync|execFile|exec|spawnSync|spawn|fork|execa|execaSync)\\s*\\(\\s*(['\"`])((?:[^'\"`\\\\]|\\\\.)*)['\"`]")
	nodeProcessStart  = regexp.MustCompile(`\b(?:execSync|execFileSync|execFile|exec|spawnSync|spawn|fork|execa|execaSync)\s*\(`)
	nodeProcessUse    = regexp.MustCompile(`\b(?:child_process|execa|shelljs|zx)\b`)
)

// nodeScriptMayRun reports whether a repository Node script, or a local
// module it imports, may start framework's tests: it names the framework,
// looks like a test itself (assertions, node:test, a test or spec name),
// imports what ddtest cannot read, or starts processes whose literal
// commands may run tests. Inspection is bounded; past the bound it may.
// directory is where the script is invoked, which its processes inherit.
func nodeScriptMayRun(root, directory, path, framework string, seen map[string]bool) bool {
	return nodeScriptInspect(root, directory, path, framework, seen, true, nil)
}

// nodeScriptMayStart reports whether a repository Node script, or a local
// module it imports, may start framework's runner. Unlike nodeScriptMayRun,
// a script that is a test of its own (assertions, a test name) but neither
// names the framework nor starts a process can't: its tests are not
// framework's. A local import's path never counts as naming the framework
// (the module itself is inspected), and any process API counts as starting
// one. directory is where the script is invoked, which its processes
// inherit.
func nodeScriptMayStart(root, directory, path, framework string) bool {
	return nodeScriptInspect(root, directory, path, framework, map[string]bool{}, false, nil)
}

var nodeBareProcessStart = regexp.MustCompile(`(?:^|[^.\w$])(?:execSync|execFileSync|execFile|exec|spawnSync|spawn|fork|execa|execaSync)\s*\(`)

// nodeComments drops the comments a line starts (// and /* blocks), as in
// file headers and JSDoc. Comments after code stay: telling them from string
// contents needs a tokenizer.
func nodeComments(source string) string {
	var kept strings.Builder
	block := false
	for line := range strings.Lines(source) {
		trimmed := strings.TrimSpace(line)
		switch {
		case block:
			_, after, closed := strings.Cut(line, "*/")
			if closed {
				block = false
				kept.WriteString(after)
			}
			continue
		case strings.HasPrefix(trimmed, "//"):
			continue
		case strings.HasPrefix(trimmed, "/*"):
			if _, after, closed := strings.Cut(trimmed[2:], "*/"); closed {
				kept.WriteString(after + "\n")
			} else {
				block = true
			}
			continue
		}
		kept.WriteString(line)
	}
	return kept.String()
}

var nodeAliasImport = regexp.MustCompile(`(?m)(?:from\s+|(?:require|import)\s*\(\s*|^\s*import\s+)['"](?:@/|~/|#|/)`)

// Process and thread APIs whose commands ddtest can't read: workers and
// cluster forks run other modules, and other runtimes' process APIs.
var nodeProcessPackages = regexp.MustCompile(`\b(?:worker_threads|cluster)\b|bun\.spawn|deno\.(?:command|run)`)

// nodeScriptInspect implements nodeScriptMayRun (testLike) and
// nodeScriptMayStart. With unread, published packages the script imports
// that ddtest doesn't read are recorded there instead of counting. Imports
// resolve from each module's folder; the processes any module starts run in
// directory, the folder the script was invoked from, as Node's child
// processes inherit its working directory.
func nodeScriptInspect(root, directory, path, framework string, seen map[string]bool, testLike bool, unread *unreadPackages) bool {
	if seen[path] {
		return false
	}
	// The bound keeps inspection cheap; a launcher's own graph is small, while
	// a script importing the project's modules reads more of them.
	if len(seen) >= 32 && testLike || len(seen) >= 256 {
		return true
	}
	seen[path] = true
	relative, err := filepath.Rel(root, path)
	if canonical, canonicalErr := filepath.EvalSymlinks(root); canonicalErr == nil && (err != nil || !filepath.IsLocal(relative)) {
		relative, err = filepath.Rel(canonical, path)
	}
	if err != nil || !filepath.IsLocal(relative) {
		return true
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 1<<20 {
		return true
	}
	source, name := strings.ToLower(string(data)), strings.ToLower(filepath.Base(path))
	if testLike && (strings.Contains(source, framework) || strings.Contains(source, "assert") || strings.Contains(source, "node:test") || strings.Contains(name, "test") || strings.Contains(name, "spec")) || nodeDynamicImport.MatchString(string(data)) {
		return true
	}
	if !testLike {
		// Local imports are inspected themselves, and comments run nothing;
		// any other mention of the framework, such as a package import or a
		// command, may start it. Process calls below read the whole source.
		named := nodeLocalImport.ReplaceAllString(nodeComments(source), "")
		if strings.Contains(named, framework) || slices.ContainsFunc(frameworkExecutables[framework], func(executable string) bool { return strings.Contains(named, executable) }) {
			return true
		}
		// Process APIs ddtest can't read, and path aliases and subpath
		// imports, which name repository modules ddtest doesn't resolve.
		if nodeProcessPackages.MatchString(source) || nodeAliasImport.MatchString(source) {
			return true
		}
	}
	// A package ddtest doesn't read, such as an internal @acme/ci, may start
	// the framework itself.
	packages, readable := nodeUnreadPackages(root, filepath.Dir(path), string(data))
	if !readable || len(packages) > 0 && unread == nil {
		return true
	}
	unread.add(packages...)
	// A method call such as pattern.exec(text) starts no process unless the
	// module uses a process API, which the first pattern finds.
	if nodeProcessUse.MatchString(string(data)) || !testLike && nodeBareProcessStart.MatchString(string(data)) {
		calls := nodeProcessCall.FindAllStringSubmatchIndex(string(data), -1)
		// Every invocation counts: one computed command (process.argv,
		// process.env, a variable) may run anything.
		if len(calls) == 0 || len(nodeProcessStart.FindAllString(string(data), -1)) > len(calls) {
			return true
		}
		// A literal command runs in the folder the script was invoked
		// from unless the script changes it.
		if nodeDirectoryChange.MatchString(string(data)) {
			return true
		}
		for _, call := range calls {
			command, ok := nodeProcessCommand(string(data), call)
			if !ok || mentionsFrameworkReading(root, directory, command, framework, 1, unread) {
				return true
			}
		}
	}
	for _, match := range nodeLocalImport.FindAllStringSubmatch(string(data), -1) {
		resolved, ok := nodeLocalModule(filepath.Dir(path), match[1]+match[2]+match[3])
		if !ok {
			return true // A module ddtest can't find may be anything.
		}
		if filepath.Ext(resolved) == ".json" {
			continue // Data: it runs nothing.
		}
		if nodeScriptInspect(root, directory, resolved, framework, seen, testLike, unread) {
			return true
		}
	}
	return false
}

// A directory change (process.chdir, shelljs's cd and pushd) makes a later
// literal command run another folder's scripts.
var nodeDirectoryChange = regexp.MustCompile(`\bchdir\s*\(|(?:^|[^\w$.])(?:cd|pushd)\s*\(|\.(?:cd|pushd)\s*\(`)

// nodeLocalModule resolves a relative import as Node and TypeScript do: the
// file itself, with a module extension, a TypeScript source for a .js
// specifier, or a folder's index. ok is false when none exists.
func nodeLocalModule(directory, specifier string) (string, bool) {
	candidates := []string{specifier}
	for _, extension := range []string{".js", ".cjs", ".mjs", ".jsx", ".ts", ".cts", ".mts", ".tsx", ".json"} {
		candidates = append(candidates, specifier+extension)
	}
	// TypeScript's ESM imports name the emitted file: ./util.js is util.ts.
	for _, emitted := range [][2]string{{".js", ".ts"}, {".js", ".tsx"}, {".mjs", ".mts"}, {".cjs", ".cts"}, {".jsx", ".tsx"}} {
		if base, found := strings.CutSuffix(specifier, emitted[0]); found {
			candidates = append(candidates, base+emitted[1])
		}
	}
	for _, index := range []string{"index.js", "index.cjs", "index.mjs", "index.ts", "index.tsx"} {
		candidates = append(candidates, filepath.Join(specifier, index))
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(filepath.Join(directory, candidate)); err == nil && info.Mode().IsRegular() {
			return filepath.Join(directory, candidate), true
		}
	}
	return "", false
}

// Process APIs whose second argument is the command's arguments, not an
// options object or a callback.
var nodeArgumentAPIs = []string{"execFileSync", "execFile", "spawnSync", "spawn", "fork", "execa", "execaSync"}

var (
	nodeTemplateExpression = regexp.MustCompile(`\$\{[^}]*\}`)
	nodeStringElement      = regexp.MustCompile("^\\s*(['\"`])((?:[^'\"`\\\\]|\\\\.)*)['\"`]\\s*(?:,|$)")
)

// nodeComputedValue stands for a template expression's value in a command
// ddtest reads: no command, script or package is named so.
const nodeComputedValue = "__ddtest_computed__"

// nodeLiteralWords returns text, a process call's command or one of its
// arguments, with each template expression replaced by nodeComputedValue.
// ok is false when a computed value may choose what runs: when it is (part
// of) the executable, which command reports text starts, or a whole word or
// part of a word without a literal folder, such as a subcommand
// (npm ${task}), a script name (npm run build:${target}) or an option. A
// value inside a literal path (src/locales/${name}) only names a file there.
func nodeLiteralWords(text string, command bool) (string, bool) {
	replaced := nodeTemplateExpression.ReplaceAllString(text, nodeComputedValue)
	for i, word := range strings.Fields(replaced) {
		if !strings.Contains(word, nodeComputedValue) {
			continue
		}
		literal := strings.ReplaceAll(word, nodeComputedValue, "")
		if command && i == 0 || !strings.Contains(literal, "/") || strings.ContainsAny(literal, "${}") {
			return "", false
		}
	}
	return replaced, true
}

// nodeProcessCommand returns the command a process call matched by
// nodeProcessCall runs: its literal executable or shell command, with the
// literal arguments of spawn('npm', ['test']) and the like. ok is false when
// ddtest can't read it: computed arguments, a computed value that may choose
// what runs (see nodeLiteralWords), or options that may select another
// working directory.
func nodeProcessCommand(source string, call []int) (string, bool) {
	api := source[call[2]:call[3]]
	command, ok := nodeLiteralWords(source[call[6]:call[7]], true)
	if !ok {
		return "", false
	}
	rest, ok := nodeCallRemainder(source, call[1])
	if !ok {
		return "", false
	}
	rest = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(strings.TrimSuffix(rest, ")")), ","))
	if strings.HasPrefix(rest, "[") && slices.Contains(nodeArgumentAPIs, api) {
		end, closed := nodeCallRemainder(rest, 1)
		if !closed {
			return "", false
		}
		for elements := strings.TrimSpace(rest[1 : 1+len(end)-1]); elements != ""; {
			match := nodeStringElement.FindStringSubmatch(elements)
			if match == nil {
				return "", false // A computed argument may be anything.
			}
			argument, ok := nodeLiteralWords(match[2], false)
			if !ok {
				return "", false // A computed argument may choose what runs.
			}
			command += " " + argument
			elements = strings.TrimSpace(elements[len(match[0]):])
		}
		rest = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rest[1+len(end):]), ","))
	}
	switch {
	case strings.Contains(rest, "cwd"):
		return "", false // Another directory's package scripts.
	case rest == "", strings.HasPrefix(rest, "{"):
	case !slices.Contains(nodeArgumentAPIs, api) && (strings.HasPrefix(rest, "(") || strings.HasPrefix(rest, "function") || strings.HasPrefix(rest, "async")):
		// exec's completion callback.
	default:
		return "", false // Computed arguments or options.
	}
	return command, true
}

// nodeCallRemainder returns the source from offset up to the bracket that
// closes the enclosing call or array, skipping nested brackets and strings,
// including that closing bracket. ok is false when it isn't closed.
func nodeCallRemainder(source string, offset int) (string, bool) {
	depth := 0
	var quote byte
	for i := offset; i < len(source); i++ {
		c := source[i]
		switch {
		case quote != 0 && c == '\\':
			i++ // An escaped character.
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"' || c == '`':
			quote = c
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			if depth == 0 {
				return source[offset : i+1], true
			}
			depth--
		}
	}
	return "", false
}

// mentionsPublishLifecycle reports whether a publish lifecycle script of the
// package in directory names framework (see mentionsFramework).
func mentionsPublishLifecycle(root, directory, framework string, depth int) bool {
	for _, hook := range publishLifecycle {
		if body, ok := readPackageScript(directory, hook); ok && mentionsFramework(root, directory, body, framework, depth+1) {
			return true
		}
	}
	return false
}
