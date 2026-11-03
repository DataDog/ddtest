// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	_ "embed"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

//go:embed instructions/pytest.md
var pytestInstructions string

// pythonDiscovery onboards pytest with ddtrace.
type pythonDiscovery struct{}

func (pythonDiscovery) name() string { return "python" }

func (pythonDiscovery) displayName() string { return "Python" }

func (pythonDiscovery) actionLanguage() string { return "python" }

func (pythonDiscovery) runtimeSetupAction() string { return "actions/setup-python" }

func (pythonDiscovery) bootstrap(string) string { return pythonBootstrap }

func (pythonDiscovery) resolvesTestSteps(framework string) bool { return framework == "pytest" }

func (pythonDiscovery) coordinated() bool { return false }

func (pythonDiscovery) automaticCISetup() bool { return false }

// Python setup uses the Datadog GitHub Actions setup action only.
func (pythonDiscovery) supportsCIProvider(provider string) bool {
	return provider == gitHubActionsProvider
}

func (pythonDiscovery) ciSetupConfigured(string, string, string) bool { return false }

func (pythonDiscovery) agentInstructions(string, string) string { return "" }

func (pythonDiscovery) frameworks() []string { return []string{"pytest"} }

func (pythonDiscovery) resolveStep(root string, workflow ciWorkflow, job runtimeJob, step runtimeStep, _ string) (commandResolution, bool) {
	return resolvePythonStep(root, workflow, job, step), true
}

func (pythonDiscovery) localCommands(root, command, _ string) ([]TestCommand, bool) {
	return resolvePythonStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: command}).Tests, true
}

// The action's CI-only PYTHONPATH and PYTEST_ADDOPTS entries are replaced by
// the application's own values; local validation loads its own tracer.
func (pythonDiscovery) variantValue(workflow ciWorkflow, job runtimeJob, step runtimeStep, row map[string]any, key, _ string) (string, bool, error) {
	switch key {
	case "PYTEST_ADDOPTS":
		value, err := pythonAddopts(workflow, job, step, row)
		return value, true, err
	case "PYTHONPATH":
		return pythonApplicationPath(workflow, job, step, row), true, nil
	}
	return "", false, nil
}

func (pythonDiscovery) skipsUnsupportedNode() bool { return false }

func (pythonDiscovery) instructions(string) string { return pytestInstructions }

func (pythonDiscovery) onboardingScope(string) (bool, bool) { return true, true }

func (pythonDiscovery) jobConsumer(root string, workflow ciWorkflow, job runtimeJob, _ string) (bool, bool) {
	return pythonJobCandidate(root, workflow, job), true
}

// Preserve application identity and feature settings. The local evaluator owns
// tracer activation, transport and credentials, so those never reach replay.
func (pythonDiscovery) keepsCISetting(key string) bool {
	return slices.Contains([]string{"DD_ENV", "DD_SERVICE", "DD_VERSION", "DD_TAGS", "DD_TEST_SESSION_NAME", "DD_CIVISIBILITY_ITR_ENABLED", "DD_CIVISIBILITY_CODE_COVERAGE_ENABLED", "DD_CIVISIBILITY_IMPACTED_TESTS_DETECTION_ENABLED"}, key) ||
		strings.HasPrefix(key, "DD_GIT_") || strings.HasPrefix(key, "DD_CIVISIBILITY_FLAKY_RETRY_") ||
		strings.HasPrefix(key, "DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_") || strings.HasPrefix(key, "DD_CIVISIBILITY_TEST_MANAGEMENT_")
}

const pythonBootstrap = "The action exports PYTHONPATH and PYTEST_ADDOPTS=--ddtrace for pytest. Preserve these variables on the existing test step; do not replace its current arguments. Activate the same Python environment used for the tests before the action. If CI uses tox or nox, pass DD_*, PYTHONPATH, and PYTEST_ADDOPTS into the test environment."

// Setup runs on GitHub Actions only, whose steps don't share a shell.
func (pythonDiscovery) setupShellExports(map[string]string) (map[string]string, map[string]string) {
	return nil, nil
}

// Setup runs on GitHub Actions only.
func (pythonDiscovery) ciSetupRequest(string, string, string, ciSetupTerms) ciSetupRequest {
	return ciSetupRequest{}
}

func (pythonDiscovery) automaticProviderSetup(string) bool { return false }

func (pythonDiscovery) unsupportedImage(string) string { return "" }

// Its own step rules decide which uninstrumented commands are outside scope.
func (pythonDiscovery) outsideFrameworkJob(string, ciWorkflow, runtimeJob, map[string]any, string) (bool, []string) {
	return false, nil
}

// The shared scan follows this language's package and repository scripts.
func (pythonDiscovery) stepMayStartTests(string, ciWorkflow, runtimeJob, runtimeStep) (bool, bool) {
	return false, false
}

// A hand-written setup's PYTEST_ADDOPTS prefix stays in the pytest command,
// which pythonHandWrittenSetup reads with its environment.
func (pythonDiscovery) preloadVariable(string) bool { return false }

func (pythonDiscovery) handWrittenSetup(root string, provider ciProvider, workflow ciWorkflow, job runtimeJob, index int, _ map[string]any, framework, site string) *handWrittenSetup {
	return pythonHandWrittenSetup(root, provider, workflow, job, index, site)
}

// The pytest CI check reads only the Datadog setup outside GitHub Actions
// (see checkPythonCIRuntimes): a hand-written ddtrace setup is recognized,
// kept and reported, never certified, until it reads that setup too.
func (pythonDiscovery) validatesHandWrittenSetup() bool { return false }

func (pythonDiscovery) runtimeVariable(name string) bool {
	return name == "PYTHONPATH" || name == "PYTHONHOME"
}

// The interpreter, pip, uv, pytest and tox read these.
func (pythonDiscovery) toolVariable(name string) bool {
	return slices.Contains([]string{"TOX_ENV", "TOXENV"}, name) || slices.ContainsFunc([]string{"PYTHON", "PYTEST_", "PIP_", "UV_", "VIRTUAL_ENV", "COVERAGE_"}, func(prefix string) bool { return strings.HasPrefix(name, prefix) })
}

func (pythonDiscovery) cacheVariable(name string) bool {
	return name == "PIP_TARGET" || name == "POETRY_VIRTUALENVS_PATH"
}

// No CI components are modeled for this language.
func (pythonDiscovery) ciComponents(string) []ciComponent { return nil }

// No CI runtime statements yet.
func (pythonDiscovery) statedRuntime() *statedRuntime { return nil }

func (pythonDiscovery) ciPluginLanguage() string { return "PYTHON" }

// Python has no CI plugin setup yet.
func (pythonDiscovery) ciPluginExports(map[string]string) (map[string]string, map[string]string) {
	return nil, nil
}

func (pythonDiscovery) ciPluginComposes(string) string { return "" }

func (pythonDiscovery) ciShellAfter(ciWorkflow, runtimeJob, runtimeStep, string, commandResolution) (bool, string) {
	return false, ""
}

// No shell runtime selection is modeled for this language.
func (pythonDiscovery) selectsShellRuntime([]string) bool { return false }

// pythonCheckTools never start a test runner: linters, formatters, type
// checkers, audits and packaging upload tools. Documentation builders are
// not among them: Sphinx executes the project's conf.py and MkDocs its
// hooks, which may run anything.
var pythonCheckTools = []string{"twine", "ruff", "flake8", "black", "isort", "mypy", "pyright", "pylint", "bandit", "pip-audit", "safety", "pydocstyle", "pycodestyle", "autopep8"}

// pythonToolModules never start a test runner when run as python -m <module>.
var pythonToolModules = []string{"twine", "ruff", "flake8", "black", "isort", "mypy", "pylint", "bandit", "venv", "virtualenv", "pip_audit", "pyflakes", "pycodestyle", "pydocstyle", "wheel", "codespell", "pyright", "setuptools_scm"}

var (
	pythonExecutable    = regexp.MustCompile(`^(?:python|pypy)(?:\d+(?:\.\d+)?)?$|^py$`)
	pythonModuleCluster = regexp.MustCompile(`^-[IuBEsSOqbdvRPx]*m$`)
	pythonOptionCluster = regexp.MustCompile(`^-[IuBEsSOqbdvRPx]+$`)
)

// Python's tools, and the interpreter running one as a module: coverage run
// starts a program, its reports only read data; pip, build and check-manifest
// may build the project, which runs its build backend (see
// pythonProjectMayBuild). A script, inline code or any other module may run
// anything.
func (pythonDiscovery) checkTool(root, directory string, words []string) (bool, bool) {
	name := strings.ToLower(filepath.Base(words[0]))
	switch {
	case slices.Contains(pythonCheckTools, name):
		return false, true
	case pipExecutable.MatchString(name):
		return pipMayStart(root, directory, words[1:]), true
	case name == "pyproject-build":
		return pythonBuildMayStart(root, directory, words[1:]), true
	case name == "check-manifest":
		return checkManifestMayStart(root, directory, words[1:]), true
	case name == "coverage":
		return len(words) < 2 || !slices.Contains([]string{"report", "xml", "html", "json", "lcov", "combine", "erase", "debug", "annotate"}, words[1]), true
	case pythonExecutable.MatchString(name):
		module, ok := pythonModule(words[1:])
		if !ok {
			return true, true
		}
		switch module[0] {
		case "coverage", "pip":
			return pythonDiscovery{}.checkTool(root, directory, module)
		case "build":
			return pythonBuildMayStart(root, directory, module[1:]), true
		case "check_manifest":
			return checkManifestMayStart(root, directory, module[1:]), true
		}
		return !slices.Contains(pythonToolModules, module[0]), true
	}
	return false, false
}

// pythonModule returns the module and its arguments for python -m, skipping
// the interpreter's own options, as in python -Im pip install. ok is false for
// any other invocation: a script, inline code or stdin.
func pythonModule(args []string) ([]string, bool) {
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case pythonModuleCluster.MatchString(arg):
			if i+1 < len(args) {
				return args[i+1:], true
			}
			return nil, false
		case arg == "-W" || arg == "-X":
			i++ // These options take a value.
		case pythonOptionCluster.MatchString(arg):
		default:
			return nil, false
		}
	}
	return nil, false
}
