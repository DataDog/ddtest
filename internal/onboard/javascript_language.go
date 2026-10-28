// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import "slices"

// javascriptDiscovery onboards Node.js test frameworks with dd-trace.
type javascriptDiscovery struct{}

func (javascriptDiscovery) name() string { return "javascript" }

func (javascriptDiscovery) actionLanguage() string { return "js" }

func (javascriptDiscovery) bootstrap(framework string) string {
	if framework == "cucumber" {
		return javascriptBootstrap + "\n\nFor Cucumber, also set DD_CIVISIBILITY_IMPACTED_TESTS_DETECTION_ENABLED=false on the test step. This avoids a tracer crash on Cucumber Background/Rule nodes; basic reporting is unaffected."
	}
	return javascriptBootstrap
}

func (language javascriptDiscovery) resolvesTestSteps(framework string) bool {
	return slices.Contains(language.frameworks(), framework)
}

func (javascriptDiscovery) coordinated() bool { return true }

func (javascriptDiscovery) automaticCISetup() bool { return true }

func (javascriptDiscovery) ciSetupConfigured(string, string, string) bool { return false }

func (javascriptDiscovery) agentInstructions(string, string) string { return "" }

func (javascriptDiscovery) frameworks() []string {
	return []string{"jest", "vitest", "mocha", "playwright", "cypress", "cucumber", "webdriverio"}
}

func (javascriptDiscovery) resolveStep(string, ciWorkflow, runtimeJob, runtimeStep, string) (commandResolution, bool) {
	return commandResolution{}, false
}

func (javascriptDiscovery) localCommands(string, string, string) ([]TestCommand, bool) {
	return nil, false
}

func (javascriptDiscovery) variantValue(ciWorkflow, runtimeJob, runtimeStep, map[string]any, string, string) (string, bool, error) {
	return "", false, nil
}

func (javascriptDiscovery) skipsUnsupportedNode() bool { return true }

func (javascriptDiscovery) instructions(string) string { return "" }

func (javascriptDiscovery) onboardingScope(framework string) (bool, bool) {
	discover := slices.Contains([]string{"mocha", "playwright", "cypress", "cucumber", "webdriverio"}, framework)
	return discover, slices.Contains([]string{"webdriverio", "cypress", "cucumber"}, framework)
}

const javascriptBootstrap = "Use Node.js 22 or newer. GitHub Actions cannot set NODE_OPTIONS for later steps, so merge this into the existing test step, preserving any current Node options:\n\n```yaml\nenv:\n  NODE_OPTIONS: -r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}\n```\n\nThe --import loader is required for Vitest and other ESM tests."
