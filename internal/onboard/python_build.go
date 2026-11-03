// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Building a Python project (pip install ., python -m build) runs its PEP 517
// build backend, and through it the project's setup.py, build hooks or
// in-tree backend: code that may start any test. Installing published
// packages or wheels builds nothing of the project.

var pipExecutable = regexp.MustCompile(`^pip(?:\d+(?:\.\d+)?)?$`)

// pip's own options before its subcommand: those that take a value, and
// clusters of -q and -v.
var (
	pipGeneralValueOptions = []string{"--cache-dir", "--log", "--proxy", "--timeout", "--retries", "--python", "--exists-action", "--trusted-host", "--cert", "--client-cert", "--keyring-provider"}
	pipGeneralFlags        = []string{"--disable-pip-version-check", "--no-cache-dir", "--no-input", "--isolated", "--no-color", "--require-virtualenv", "--quiet", "--verbose"}
	pipVerbosity           = regexp.MustCompile(`^-[qv]+$`)
)

// pip subcommands that read or remove installed packages and build nothing.
var pipInertSubcommands = []string{"uninstall", "freeze", "list", "show", "check", "config", "search", "cache", "index", "hash", "completion", "debug", "help", "inspect"}

// Options of pip install, wheel, download and lock that take a value, and
// those that take none; none of them names a project to build.
var (
	pipInstallValueOptions = []string{"-i", "--index-url", "--extra-index-url", "-f", "--find-links", "-c", "--constraint", "-t", "--target", "--prefix", "--root", "--platform", "--python-version", "--implementation", "--abi", "--only-binary", "--no-binary", "--progress-bar", "--trusted-host", "--src", "--upgrade-strategy", "-C", "--config-settings", "--global-option", "--report", "-d", "--dest", "-w", "--wheel-dir", "-o", "--output", "--group", "--root-user-action", "--use-feature"}
	pipInstallFlags        = []string{"-U", "--upgrade", "--user", "--no-deps", "--pre", "--force-reinstall", "-I", "--ignore-installed", "--no-build-isolation", "--require-hashes", "--no-index", "--prefer-binary", "--no-warn-script-location", "--compile", "--no-compile", "--dry-run", "--use-pep517", "--no-use-pep517", "--check-build-dependencies", "--ignore-requires-python", "--no-clean", "--no-warn-conflicts", "--break-system-packages"}
	pipInstallCluster      = regexp.MustCompile(`^-[qvUI]+$`)
)

// pipMayStart reports whether pip, with args after the executable, may start
// tests: only by building a local project or source archive (see
// pythonProjectMayBuild). A requirements file is read for the projects it
// names; one ddtest can't read, and any option it doesn't know, may.
func pipMayStart(root, directory string, args []string) bool {
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		option, _, assigned := strings.Cut(args[0], "=")
		switch {
		case slices.Contains(pipGeneralValueOptions, option):
			if !assigned {
				args = args[1:]
			}
		case slices.Contains(pipGeneralFlags, option) || pipVerbosity.MatchString(option) || option == "--version" || option == "-V" || option == "--help" || option == "-h":
		default:
			return true
		}
		if len(args) > 0 {
			args = args[1:]
		}
	}
	if len(args) == 0 || slices.Contains(pipInertSubcommands, args[0]) {
		return false
	}
	if !slices.Contains([]string{"install", "wheel", "download", "lock"}, args[0]) {
		return true
	}
	args = args[1:]
	for i := 0; i < len(args); i++ {
		arg := args[i]
		option, value, assigned := strings.Cut(arg, "=")
		valueOf := func() (string, bool) {
			if assigned {
				return value, true
			}
			if i+1 >= len(args) {
				return "", false
			}
			i++
			return args[i], true
		}
		switch {
		case !strings.HasPrefix(arg, "-"):
			if pythonRequirementMayBuild(root, directory, arg) {
				return true
			}
		case option == "-e" || option == "--editable":
			editable, ok := valueOf()
			if !ok || pythonRequirementMayBuild(root, directory, editable) {
				return true
			}
		case option == "-r" || option == "--requirement":
			file, ok := valueOf()
			if !ok || pythonRequirementsMayBuild(root, directory, directory, file, 0) {
				return true
			}
		case slices.Contains(pipInstallValueOptions, option):
			if _, ok := valueOf(); !ok {
				return true
			}
		case slices.Contains(pipInstallFlags, option) || slices.Contains(pipGeneralFlags, option) || pipInstallCluster.MatchString(arg):
		default:
			return true // An option ddtest doesn't know may take a value or a project.
		}
	}
	return false
}

// pythonRequirementMayBuild reports whether installing one requirement, as
// pip takes it, may build a local project: a local directory is inspected, a
// wheel builds nothing, a published package or remote URL is not the
// project's, and a local source archive or anything computed may.
func pythonRequirementMayBuild(root, directory, requirement string) bool {
	requirement = strings.Trim(requirement, `"'`)
	if requirement == "" || strings.ContainsAny(requirement, "$`") || strings.Contains(requirement, "file:") {
		return true
	}
	if strings.Contains(requirement, "://") {
		return false
	}
	if extras := strings.Index(requirement, "["); extras > 0 && strings.HasSuffix(requirement, "]") {
		requirement = requirement[:extras]
	}
	if strings.HasSuffix(requirement, ".whl") {
		return false
	}
	path := strings.HasPrefix(requirement, ".") || strings.ContainsAny(requirement, "/\\")
	if !path {
		if directory == "" {
			return false // pip reads a bare name as a package unless it is a local path.
		}
		if _, err := os.Stat(filepath.Join(directory, requirement)); err != nil {
			return false
		}
	}
	if directory == "" || strings.ContainsAny(requirement, "*?[~") {
		return true
	}
	return pythonProjectMayBuild(root, directory, requirement)
}

// pythonRequirementsMayBuild reports whether a requirements file, relative to
// base, names a requirement that may build a local project (paths in it are
// relative to the working directory, nested files to the file). One ddtest
// can't read may.
func pythonRequirementsMayBuild(root, directory, base, file string, depth int) bool {
	if depth >= 4 || base == "" || strings.ContainsAny(file, "$`*?~") {
		return true
	}
	info, ok := repositoryFile(root, base, file)
	if !ok || info.Size() > 1<<20 {
		return true
	}
	data, err := os.ReadFile(filepath.Join(base, file))
	if err != nil {
		return true
	}
	text := strings.ReplaceAll(string(data), "\\\n", " ")
	for line := range strings.Lines(text) {
		if hash := strings.Index(line, "#"); hash == 0 || hash > 0 && strings.ContainsAny(line[hash-1:hash], " \t") {
			line = line[:hash]
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if strings.Contains(line, "file:") || strings.ContainsAny(line, "$`") {
			return true
		}
		option, value, assigned := strings.Cut(fields[0], "=")
		if !assigned && len(fields) > 1 {
			value = fields[1]
		}
		switch {
		case option == "-r" || option == "--requirement":
			if pythonRequirementsMayBuild(root, directory, filepath.Dir(filepath.Join(base, file)), value, depth+1) {
				return true
			}
		case option == "-e" || option == "--editable":
			if pythonRequirementMayBuild(root, directory, value) {
				return true
			}
		case strings.HasPrefix(option, "-"):
			// Index, constraint and binary selections name no project.
		default:
			requirement, _, _ := strings.Cut(fields[0], ";")
			if pythonRequirementMayBuild(root, directory, requirement) {
				return true
			}
		}
	}
	return false
}

// Options of python -m build (pyproject-build) that take a value, and those
// that take none.
var (
	pythonBuildValueOptions = []string{"-o", "--outdir", "-C", "--config-setting", "--installer", "--config-json"}
	pythonBuildFlags        = []string{"-s", "--sdist", "-w", "--wheel", "-n", "--no-isolation", "-x", "--skip-dependency-check", "-v", "--verbose", "-q", "--quiet"}
)

// pythonBuildMayStart reports whether python -m build, with args after the
// module, may start tests: it builds the project in its source directory.
func pythonBuildMayStart(root, directory string, args []string) bool {
	source := "."
	for i := 0; i < len(args); i++ {
		option, _, assigned := strings.Cut(args[i], "=")
		switch {
		case !strings.HasPrefix(args[i], "-"):
			if source != "." {
				return true
			}
			source = args[i]
		case slices.Contains(pythonBuildValueOptions, option):
			if !assigned {
				i++
			}
		case slices.Contains(pythonBuildFlags, option):
		default:
			return true
		}
	}
	return directory == "" || pythonProjectMayBuild(root, directory, source)
}

// check-manifest's options that select no other project.
var checkManifestFlags = []string{"-v", "--verbose", "-c", "--create", "-u", "--update", "--no-build-isolation"}

// checkManifestMayStart reports whether check-manifest may start tests: it
// builds the project's source distribution.
func checkManifestMayStart(root, directory string, args []string) bool {
	if directory == "" || slices.ContainsFunc(args, func(arg string) bool { return !slices.Contains(checkManifestFlags, arg) }) {
		return true
	}
	return pythonProjectMayBuild(root, directory, ".")
}

// pythonProjectMayBuild reports whether building the Python project at path,
// relative to directory, may run code that starts tests: a setup.py, an
// in-tree backend, a backend ddtest doesn't know, or a known backend's build
// hooks or module imports. A project ddtest can't read may.
func pythonProjectMayBuild(root, directory, path string) bool {
	project, err := repositoryDirectory(root, directory, path)
	if err != nil {
		return true
	}
	exists := func(name string) bool {
		_, err := os.Lstat(filepath.Join(project, name))
		return err == nil
	}
	if exists("setup.py") {
		return true // Arbitrary code.
	}
	data, err := os.ReadFile(filepath.Join(project, "pyproject.toml"))
	if os.IsNotExist(err) {
		// Without either file there is nothing to build; setup.cfg alone may
		// still name command classes.
		return exists("setup.cfg") && pythonSetuptoolsHooks(project, "")
	}
	if err != nil {
		return true
	}
	var configuration struct {
		BuildSystem struct {
			Backend     string   `toml:"build-backend"`
			BackendPath []string `toml:"backend-path"`
		} `toml:"build-system"`
		Project struct {
			Dynamic []string `toml:"dynamic"`
		} `toml:"project"`
		Tool struct {
			Poetry map[string]any `toml:"poetry"`
		} `toml:"tool"`
	}
	if toml.Unmarshal(data, &configuration) != nil || len(configuration.BuildSystem.BackendPath) > 0 {
		return true
	}
	text := string(data)
	switch configuration.BuildSystem.Backend {
	case "", "setuptools.build_meta", "setuptools.build_meta:__legacy__":
		return pythonSetuptoolsHooks(project, text)
	case "hatchling.build":
		hatch, _ := os.ReadFile(filepath.Join(project, "hatch.toml"))
		configured := text + string(hatch)
		return exists("hatch_build.py") || strings.Contains(configured, "hooks") || strings.Contains(configured, `"code"`)
	case "flit_core.buildapi":
		return len(configuration.Project.Dynamic) > 0 // Flit may import the module for them.
	case "poetry.core.masonry.api":
		_, script := configuration.Tool.Poetry["build"]
		return script
	case "pdm.backend":
		return exists("pdm_build.py") || strings.Contains(text, "custom-hook") || strings.Contains(text, `"call"`)
	}
	return true
}

// pythonSetuptoolsHooks reports whether setuptools may run the project's code
// while building it: command classes, or a version or attribute it imports
// from the project's modules.
func pythonSetuptoolsHooks(project, pyproject string) bool {
	setup, err := os.ReadFile(filepath.Join(project, "setup.cfg"))
	if err != nil && !os.IsNotExist(err) {
		return true
	}
	text := pyproject + string(setup)
	return strings.Contains(text, "cmdclass") || pythonAttributeDirective.MatchString(text)
}

// A setuptools attr: directive (setup.cfg) or attr key (pyproject.toml).
var pythonAttributeDirective = regexp.MustCompile(`\battr\s*[=:]`)
