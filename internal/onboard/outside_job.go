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
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// A deploy or packaging job (sentry-cli, git archive, web-ext build, upload
// scripts) never starts the selected framework when nothing it runs names
// it: not its commands, the package scripts they run, nor the repository
// scripts those run. Opaque actions and unreadable configuration may.
func (javascriptDiscovery) outsideFrameworkJob(root string, workflow ciWorkflow, job runtimeJob, row map[string]any, framework string) bool {
	for _, step := range job.Steps {
		if active, err := stepCondition(step, row, conditionEnvironment(workflow, job, runtimeStep{}, row)); err == nil && !active {
			continue
		}
		if strings.TrimSpace(step.Run) == "" {
			if step.Uses != "" || step.ResolutionError != "" {
				return false
			}
			continue
		}
		directory, err := repositoryDirectory(root, root, stepDirectory(workflow, job, step))
		if err != nil || mentionsFramework(root, directory, step.Run, framework, 0) {
			return false
		}
	}
	return true
}

// mentionsFramework reports whether shell text, or a package or repository
// script it runs, names framework. Anything it cannot read counts as naming
// it.
func mentionsFramework(root, directory, text, framework string, depth int) bool {
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
			if mentioned = mentionsCommand(root, directory, all[i:], framework, depth, i == 0); mentioned {
				break
			}
		}
		return !mentioned
	})
	return mentioned
}

// mentionsCommand reports whether one command, as words, runs a package or
// repository script that names framework.
func mentionsCommand(root, directory string, words []string, framework string, depth int, executable bool) bool {
	name := filepath.Base(words[0])
	// Package runners execute their target as an executable.
	if target := packageRunnerTarget(words); target != nil {
		return mentionsCommand(root, directory, target, framework, depth, true)
	}
	switch {
	case oneOf(name, "npm", "pnpm", "yarn", "bun"):
		script := ""
		switch {
		case len(words) > 2 && (words[1] == "run" || words[1] == "run-script"):
			script = words[2]
		case len(words) > 1 && (words[1] == "test" || words[1] == "t"):
			script = "test"
		case len(words) > 1 && name == "yarn" && !strings.HasPrefix(words[1], "-") && words[1] != "install" && words[1] != "add":
			script = words[1]
		}
		if script == "" {
			return false
		}
		body, ok := readPackageScript(directory, script)
		return !ok || mentionsFramework(root, directory, body, framework, depth+1)
	case oneOf(name, "node", "ts-node", "tsx", "sh", "bash") && len(words) > 1 && !strings.HasPrefix(words[1], "-"):
		return repositoryScriptMentions(root, directory, words[1], framework)
	case executable && strings.Contains(words[0], "/"):
		return repositoryScriptMentions(root, directory, words[0], framework)
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

// repositoryScriptMentions reports whether a repository script names
// framework; one that cannot be read counts as naming it.
func repositoryScriptMentions(root, directory, path, framework string) bool {
	return repositoryScriptMayRun(root, directory, path, framework, 0)
}

// repositoryScriptMayRun reports whether a repository script may start
// framework's tests. Node scripts are inspected with their local imports;
// other scripts are followed as shell. Anything unreadable may.
func repositoryScriptMayRun(root, directory, path, framework string, depth int) bool {
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
		return nodeScriptMayRun(root, filepath.Join(directory, path), framework, map[string]bool{})
	}
	// A shell wrapper runs its commands: follow them like a CI step's.
	return mentionsFramework(root, directory, string(data), framework, depth+1)
}

var (
	nodeLocalImport   = regexp.MustCompile(`(?m)(?:require|import)\s*\(\s*['"](\.{1,2}/[^'"]+)['"]\s*\)|from\s+['"](\.{1,2}/[^'"]+)['"]|^\s*import\s+['"](\.{1,2}/[^'"]+)['"]`)
	nodeDynamicImport = regexp.MustCompile(`(?:require|import)\s*\(\s*[^'"\s)]`)
	nodeProcessCall   = regexp.MustCompile("\\b(?:execSync|execFileSync|execFile|exec|spawnSync|spawn|fork|execa|execaSync)\\s*\\(\\s*(['\"`])((?:[^'\"`\\\\]|\\\\.)*)['\"`]")
	nodeProcessStart  = regexp.MustCompile(`\b(?:execSync|execFileSync|execFile|exec|spawnSync|spawn|fork|execa|execaSync)\s*\(`)
	nodeProcessUse    = regexp.MustCompile(`\b(?:child_process|execa|shelljs|zx)\b`)
)

// nodeScriptMayRun reports whether a repository Node script, or a local
// module it imports, may start framework's tests: it names the framework,
// looks like a test itself (assertions, node:test, a test or spec name),
// imports what ddtest cannot read, or starts processes whose literal
// commands may run tests. Inspection is bounded; past the bound it may.
func nodeScriptMayRun(root, path, framework string, seen map[string]bool) bool {
	if seen[path] {
		return false
	}
	if len(seen) >= 32 {
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
	if strings.Contains(source, framework) || strings.Contains(source, "assert") || strings.Contains(source, "node:test") || strings.Contains(name, "test") || strings.Contains(name, "spec") || nodeDynamicImport.MatchString(string(data)) {
		return true
	}
	if nodeProcessUse.MatchString(string(data)) {
		calls := nodeProcessCall.FindAllStringSubmatch(string(data), -1)
		// Every invocation counts: one computed command (process.argv,
		// process.env, a variable) may run anything.
		if len(calls) == 0 || len(nodeProcessStart.FindAllString(string(data), -1)) > len(calls) {
			return true
		}
		for _, call := range calls {
			command := regexp.MustCompile(`\$\{[^}]*\}`).ReplaceAllString(call[2], "x")
			if mentionsFramework(root, filepath.Dir(path), command, framework, 1) {
				return true
			}
		}
	}
	for _, match := range nodeLocalImport.FindAllStringSubmatch(string(data), -1) {
		target := match[1] + match[2] + match[3]
		resolved := ""
		for _, candidate := range []string{target, target + ".js", target + ".cjs", target + ".mjs", target + ".ts", filepath.Join(target, "index.js")} {
			if info, err := os.Stat(filepath.Join(filepath.Dir(path), candidate)); err == nil && info.Mode().IsRegular() {
				resolved = filepath.Join(filepath.Dir(path), candidate)
				break
			}
		}
		if resolved == "" || nodeScriptMayRun(root, resolved, framework, seen) {
			return true
		}
	}
	return false
}
