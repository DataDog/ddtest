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

	"go.yaml.in/yaml/v3"
)

// unreadPackages collects the published packages a CI job runs that ddtest
// doesn't read: packages its Node scripts import and packages a package
// runner executes (npx @acme/publish). Such a package may start the tests, so
// in a job that also runs tests the step running it blocks; in a job that
// runs no test ddtest can find, nothing is validated, and the job's caveat
// names them instead. A nil collector counts them as possible test paths.
type unreadPackages struct{ names []string }

func (u *unreadPackages) add(names ...string) {
	if u == nil {
		return
	}
	for _, name := range names {
		if !slices.Contains(u.names, name) {
			u.names = append(u.names, name)
		}
	}
}

var (
	nodeImportSpecifier = regexp.MustCompile(`(?m)(?:\brequire|\bimport)\s*\(\s*['"]([^'"]+)['"]|\bfrom\s+['"]([^'"]+)['"]|^\s*import\s+['"]([^'"]+)['"]`)
	nodeTypeImport      = regexp.MustCompile(`(?m)^\s*(?:import|export)\s+type\b.*$`)
	// A published package's name, with an optional subpath (yargs/helpers).
	nodePackageSpecifier = regexp.MustCompile(`^((?:@[a-z0-9~][a-z0-9._~-]*/)?[a-z0-9~][a-z0-9._~-]*)(?:/.*)?$`)
)

// Node's built-in modules, which start no test runner (their process APIs
// are read separately).
var nodeBuiltinModules = []string{"assert", "async_hooks", "buffer", "child_process", "cluster", "console", "constants", "crypto", "dgram", "diagnostics_channel", "dns", "domain", "events", "fs", "http", "http2", "https", "inspector", "module", "net", "os", "path", "perf_hooks", "process", "punycode", "querystring", "readline", "stream", "string_decoder", "timers", "tls", "trace_events", "tty", "url", "util", "v8", "vm", "wasi", "worker_threads", "zlib"}

// Published libraries a Node script may import that start no test runner and
// no process: they only do what the script calls them for. The list stays
// small and explicit; it names no test runner and no generic process
// launcher (execa, shelljs, zx), whose commands ddtest reads only through
// the script's own process calls.
var nodeLibraryPackages = []string{
	// The bundlers, compilers and linters ddtest accepts as commands.
	"esbuild", "rollup", "webpack", "vite", "tsup", "unbuild", "parcel", "typescript", "terser", "prettier", "eslint", "stylelint", "typedoc", "@babel/core", "@swc/core",
	// File, path and glob helpers.
	"rimraf", "fs-extra", "graceful-fs", "mkdirp", "glob", "fast-glob", "globby",
	// Archivers.
	"archiver", "adm-zip", "tar", "jszip", "yazl", "yauzl", "unzipper",
	// HTTP clients, form encoders and URL parsers.
	"node-fetch", "axios", "got", "undici", "cross-fetch", "form-data", "urijs",
	// Terminal output and command-line arguments.
	"chalk", "picocolors", "kleur", "yargs", "commander", "minimist",
	// Versions, environment files and data formats.
	"semver", "dotenv", "js-yaml", "yaml",
}

// Uses of an accepted library that load or run modules ddtest doesn't read:
// yargs's commandDir requires a folder's modules, and Commander's executable
// subcommands (a command with a description, executableFile, executableDir)
// spawn sibling scripts.
var nodeLibraryLoaders = map[string]*regexp.Regexp{
	"yargs":     regexp.MustCompile(`\bcommandDir\s*\(`),
	"commander": regexp.MustCompile("\\.command\\s*\\(\\s*['\"`][^'\"`]*['\"`]\\s*,\\s*['\"`]|\\bexecutable(?:File|Dir)\\b"),
}

// nodeUnreadPackages returns the published packages a Node script in
// directory imports that ddtest doesn't read: anything but a relative import
// (inspected itself), a built-in module or an accepted library used as a
// library. Type-only imports load nothing. readable is false when an import
// may load a repository module ddtest doesn't follow, so the script may start
// anything: a path alias or subpath import, a URL, a require of another name
// (createRequire), a workspace or linked package, or a package whose name
// suggests tests.
func nodeUnreadPackages(root, directory, source string) (names []string, readable bool) {
	source = nodeTypeImport.ReplaceAllString(nodeComments(source), "")
	if strings.Contains(source, "createRequire") {
		return nil, false // A require of another name.
	}
	for _, match := range nodeImportSpecifier.FindAllStringSubmatch(source, -1) {
		specifier := match[1] + match[2] + match[3]
		if strings.HasPrefix(specifier, "./") || strings.HasPrefix(specifier, "../") || strings.HasPrefix(specifier, "node:") {
			continue
		}
		parsed := nodePackageSpecifier.FindStringSubmatch(specifier)
		if parsed == nil {
			return nil, false // @/lib, ~/lib, #lib, /abs or a URL: not a package.
		}
		name := parsed[1]
		switch {
		case slices.Contains(nodeBuiltinModules, name):
			continue
		case slices.Contains(nodeLibraryPackages, name):
			if loader, ok := nodeLibraryLoaders[name]; !ok || !loader.MatchString(source) {
				continue
			}
			return nil, false
		case packageNamesTests(name) || repositoryPackage(root, directory, name):
			return nil, false
		}
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names, true
}

// packageNamesTests reports a package or command name that suggests running
// tests, such as @acme/test-runner or ci-spec.
func packageNamesTests(name string) bool {
	name = strings.ToLower(name)
	return strings.Contains(name, "test") || strings.Contains(name, "spec") || strings.Contains(name, "runner")
}

// unreadRunnerPackage reports whether a package runner's target, with its
// arguments, is a published package merely named publish, release or deploy
// (npx @manuscripts/publish): one that publishes the project by its name but
// whose behavior ddtest doesn't read. Any other unknown package, such as npx
// company-runner, may be the job's test wrapper and keeps it in scope; so does
// the repository's own package or command, or arguments that suggest tests.
func unreadRunnerPackage(root, directory string, target []string) bool {
	spec := strings.ToLower(target[0])
	if at := strings.LastIndex(spec, "@"); at > 0 {
		spec = spec[:at] // A pinned version: tool@1.2.3 or @org/tool@1.2.3.
	}
	parsed := nodePackageSpecifier.FindStringSubmatch(spec)
	if parsed == nil || parsed[1] != spec || !oneOf(publishToolName(spec), "publish", "release", "deploy") || slices.ContainsFunc(target, packageNamesTests) {
		return false
	}
	if repositoryPackage(root, directory, spec) {
		return false
	}
	directories, complete := repositoryPackageDirectories(root)
	if !complete {
		return false
	}
	for _, packageDirectory := range append(directories, directory) {
		if slices.Contains(packageCommands(packageDirectory), spec) {
			return false // The repository's own command.
		}
	}
	return true
}

// repositoryPackage reports whether a package name a script in directory
// loads is a package of this repository: a workspace, a dependency on a
// local folder (file:, link:, workspace:, portal:) or an installed link into
// the repository. ddtest doesn't follow those, so they may start anything.
// Without a complete list of the workspaces, any package may be one.
func repositoryPackage(root, directory, name string) bool {
	directories, complete := repositoryPackageDirectories(root)
	if !complete {
		return true
	}
	for _, packageDirectory := range directories {
		if npmPackageName(packageDirectory) == name {
			return true
		}
	}
	for current := directory; ; current = filepath.Dir(current) {
		if spec, ok := packageDependencySpec(current, name); ok {
			for _, local := range []string{"file:", "link:", "workspace:", "portal:", ".", "/"} {
				if strings.HasPrefix(spec, local) {
					return true
				}
			}
		}
		if installed, err := filepath.EvalSymlinks(filepath.Join(current, "node_modules", name)); err == nil {
			canonicalRoot, rootErr := filepath.EvalSymlinks(root)
			relative, relErr := filepath.Rel(canonicalRoot, installed)
			if rootErr == nil && relErr == nil && filepath.IsLocal(relative) && !slices.Contains(strings.Split(filepath.ToSlash(relative), "/"), "node_modules") {
				return true
			}
		}
		if relative, err := filepath.Rel(root, current); err != nil || relative == "." || !filepath.IsLocal(relative) {
			return false
		}
	}
}

// repositoryPackageDirectories returns the repository's root package and its
// npm, Yarn, pnpm and Lerna workspaces. complete is false when a workspace
// pattern ddtest doesn't expand (a negation or **) or an unreadable
// workspace configuration may name others.
func repositoryPackageDirectories(root string) ([]string, bool) {
	patterns, _ := npmWorkspacePatterns(root)
	if data, err := os.ReadFile(filepath.Join(root, "pnpm-workspace.yaml")); err == nil {
		var workspace struct {
			Packages []string `yaml:"packages"`
		}
		if yaml.Unmarshal(data, &workspace) != nil {
			return nil, false
		}
		patterns = append(patterns, workspace.Packages...)
	} else if !os.IsNotExist(err) {
		return nil, false
	}
	if data, err := os.ReadFile(filepath.Join(root, "lerna.json")); err == nil {
		var lerna struct {
			Packages []string `json:"packages"`
		}
		if json.Unmarshal(data, &lerna) != nil {
			return nil, false
		}
		patterns = append(patterns, lerna.Packages...)
	}
	directories := []string{root}
	for _, pattern := range patterns {
		if strings.HasPrefix(pattern, "!") || strings.Contains(pattern, "**") {
			return nil, false
		}
		matches, err := filepath.Glob(filepath.Join(root, pattern))
		if err != nil {
			return nil, false
		}
		directories = append(directories, matches...)
	}
	return directories, true
}

// packageDependencySpec returns the version spec package.json in directory
// gives a dependency of any kind.
func packageDependencySpec(directory, name string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(directory, "package.json"))
	if err != nil {
		return "", false
	}
	var manifest map[string]json.RawMessage
	if json.Unmarshal(data, &manifest) != nil {
		return "", false
	}
	for _, field := range []string{"dependencies", "devDependencies", "optionalDependencies", "peerDependencies"} {
		var dependencies map[string]string
		if json.Unmarshal(manifest[field], &dependencies) == nil {
			if spec, ok := dependencies[name]; ok {
				return spec, true
			}
		}
	}
	return "", false
}

// packageCommands returns the commands the package in directory installs:
// its bin names, or its unscoped name for a single bin path.
func packageCommands(directory string) []string {
	data, err := os.ReadFile(filepath.Join(directory, "package.json"))
	if err != nil {
		return nil
	}
	var manifest struct {
		Name string          `json:"name"`
		Bin  json.RawMessage `json:"bin"`
	}
	if json.Unmarshal(data, &manifest) != nil || len(manifest.Bin) == 0 {
		return nil
	}
	var single string
	if json.Unmarshal(manifest.Bin, &single) == nil {
		_, unscoped, scoped := strings.Cut(manifest.Name, "/")
		if !scoped {
			unscoped = manifest.Name
		}
		return []string{manifest.Name, unscoped}
	}
	var bins map[string]string
	_ = json.Unmarshal(manifest.Bin, &bins)
	commands := []string{manifest.Name}
	for command := range bins {
		commands = append(commands, command)
	}
	return commands
}
