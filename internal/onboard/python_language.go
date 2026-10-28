// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

// pythonDiscovery onboards pytest with ddtrace.
type pythonDiscovery struct{}

func (pythonDiscovery) name() string { return "python" }

func (pythonDiscovery) actionLanguage() string { return "python" }

func (pythonDiscovery) bootstrap(string) string { return pythonBootstrap }

func (pythonDiscovery) resolvesTestSteps(string) bool { return false }

func (pythonDiscovery) coordinated() bool { return false }

func (pythonDiscovery) automaticCISetup() bool { return false }

// Python setup uses the Datadog GitHub Actions setup action only.
func (pythonDiscovery) supportsCIProvider(provider string) bool {
	return provider == gitHubActionsProvider
}

func (pythonDiscovery) ciSetupConfigured(string, string, string) bool { return false }

func (pythonDiscovery) agentInstructions(string, string) string { return "" }

func (pythonDiscovery) frameworks() []string { return []string{"pytest"} }

func (pythonDiscovery) resolveStep(string, ciWorkflow, runtimeJob, runtimeStep, string) (commandResolution, bool) {
	return commandResolution{}, false
}

func (pythonDiscovery) localCommands(string, string, string) ([]TestCommand, bool) { return nil, false }

func (pythonDiscovery) variantValue(ciWorkflow, runtimeJob, runtimeStep, map[string]any, string, string) (string, bool, error) {
	return "", false, nil
}

func (pythonDiscovery) skipsUnsupportedNode() bool { return false }

func (pythonDiscovery) instructions(string) string { return "" }

func (pythonDiscovery) onboardingScope(string) (bool, bool) { return false, false }

const pythonBootstrap = "The action exports PYTHONPATH and PYTEST_ADDOPTS=--ddtrace for pytest. Preserve these variables on the existing test step; do not replace its current arguments. Activate the same Python environment used for the tests before the action. If CI uses tox or nox, pass DD_*, PYTHONPATH, and PYTEST_ADDOPTS into the test environment."

// Setup runs on GitHub Actions only, whose steps don't share a shell.
func (pythonDiscovery) setupShellExports(map[string]string) (map[string]string, map[string]string) {
	return nil, nil
}

// Setup runs on GitHub Actions only.
func (pythonDiscovery) ciSetupRequest(string, string, string) ciSetupRequest { return ciSetupRequest{} }

func (pythonDiscovery) automaticProviderSetup(string) bool { return false }

func (pythonDiscovery) unsupportedImage(string) string { return "" }

// Its own step rules decide which uninstrumented commands are outside scope.
func (pythonDiscovery) outsideFrameworkJob(string, ciWorkflow, runtimeJob, map[string]any, string) bool {
	return false
}
