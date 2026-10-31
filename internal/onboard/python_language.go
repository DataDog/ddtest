// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	_ "embed"
	"slices"
	"strings"
)

//go:embed instructions/pytest.md
var pytestInstructions string

// pythonDiscovery onboards pytest with ddtrace.
type pythonDiscovery struct{}

func (pythonDiscovery) name() string { return "python" }

func (pythonDiscovery) actionLanguage() string { return "python" }

func (pythonDiscovery) bootstrap(string) string { return pythonBootstrap }

func (pythonDiscovery) resolvesTestSteps(framework string) bool { return framework == "pytest" }

func (pythonDiscovery) coordinated() bool { return false }

func (pythonDiscovery) automaticCISetup() bool { return false }

// Python setup uses the Datadog GitHub Actions setup action, the Datadog
// GitLab setup script, the Datadog CircleCI orb, on Buildkite the Datadog
// installer, or the Datadog Jenkins plugin (see python_providers.go).
func (pythonDiscovery) supportsCIProvider(provider string) bool {
	return provider == gitHubActionsProvider || provider == gitLabCIProvider || provider == circleCIProvider || provider == buildkiteProvider || provider == jenkinsProvider
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

// The installer puts its tracer before the job's PYTHONPATH and --ddtrace
// before its pytest options.
func (pythonDiscovery) setupShellExports(current map[string]string) (map[string]string, map[string]string) {
	return pythonSetupExports(current)
}

func (pythonDiscovery) ciSetupRequest(root, framework, site string, terms ciSetupTerms) ciSetupRequest {
	if terms.Plugin {
		return pythonPluginSetupRequest(root, framework, site, terms)
	}
	return pythonSetupRequest(root, framework, site, terms)
}

func (pythonDiscovery) automaticProviderSetup(string) bool { return false }

// PyPy images run an engine ddtrace does not instrument.
func (pythonDiscovery) unsupportedImage(image string) string {
	_, engine := pythonImageRuntime(image)
	return engine
}

// Its own step rules decide which uninstrumented commands are outside scope.
func (pythonDiscovery) outsideFrameworkJob(string, ciWorkflow, runtimeJob, map[string]any, string) bool {
	return false
}

func (pythonDiscovery) runtimeVariable(name string) bool {
	return name == "PYTHONPATH" || name == "PYTHONHOME"
}

func (pythonDiscovery) cacheVariable(name string) bool {
	return name == "PIP_TARGET" || name == "POETRY_VIRTUALENVS_PATH"
}

// No CI components are modeled for this language.
func (pythonDiscovery) ciComponents(string) []ciComponent { return nil }

func (pythonDiscovery) statedRuntime() *statedRuntime { return pythonStatedRuntime }

func (pythonDiscovery) ciPluginLanguage() string { return "PYTHON" }

// The Datadog Jenkins plugin prepends its tracer to PYTHONPATH and --ddtrace
// to PYTEST_ADDOPTS, each joined to the existing value with a space.
func (pythonDiscovery) ciPluginExports(current map[string]string) (map[string]string, map[string]string) {
	return pythonPluginExports(current)
}

// The Datadog Jenkins plugin computes its variables once per agent, from the
// first step it configures there, and sets them on every later step.
func (pythonDiscovery) ciPluginComposes(key string) string {
	if key != "PYTHONPATH" && key != "PYTEST_ADDOPTS" {
		return ""
	}
	return "on each agent it sets " + key + " for every step to its own value followed by a space and the " + key + " of the first step it configures there, so this value is lost, or, when its step is that first one, it reaches every later step on the agent. ddtest validates with the pipeline's " + key
}

func (pythonDiscovery) selectsShellRuntime(words []string) bool {
	return pythonSelectsShellRuntime(words)
}
