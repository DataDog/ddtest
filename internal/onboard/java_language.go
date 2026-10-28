// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"fmt"
	"strings"

	"github.com/DataDog/ddtest/internal/maven"
)

// javaDiscovery discovers the Maven CI test steps of JVM test frameworks.
type javaDiscovery struct{}

func (javaDiscovery) name() string { return "java" }

func (javaDiscovery) actionLanguage() string { return "java" }

func (javaDiscovery) bootstrap(string) string { return javaBootstrap }

func (javaDiscovery) resolvesTestSteps(string) bool { return true }

func (javaDiscovery) coordinated() bool { return false }

func (javaDiscovery) automaticCISetup() bool { return false }

func (javaDiscovery) ciSetupConfigured(string, string, string) bool { return false }

func (javaDiscovery) agentInstructions(string, string) string { return "" }

func (javaDiscovery) frameworks() []string { return maven.FrameworkNames }

func (javaDiscovery) resolveStep(root string, workflow ciWorkflow, job runtimeJob, step runtimeStep, framework string) (commandResolution, bool) {
	return resolveJavaStep(root, workflow, job, step, framework), true
}

func (javaDiscovery) localCommands(root, command, framework string) ([]TestCommand, bool) {
	return resolveJavaStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: command}, framework).Tests, true
}

// Local validation owns the agent option; keep the customer's other JVM
// options. A reference to workflow or job values needs them explicitly.
func (javaDiscovery) variantValue(workflow ciWorkflow, job runtimeJob, _ runtimeStep, _ map[string]any, key, value string) (string, bool, error) {
	if key != "MAVEN_OPTS" && key != "JAVA_TOOL_OPTIONS" {
		return value, false, nil
	}
	inherited := strings.NewReplacer("${{ env."+key+" }}", "", "${{env."+key+"}}", "").Replace(value)
	if inherited != value && (job.Env[key] != "" || workflow.Env[key] != "") {
		return "", false, fmt.Errorf("%s extends workflow or job JVM options; validate with those options explicitly", key)
	}
	return strings.TrimSpace(inherited), true, nil
}

func (javaDiscovery) skipsUnsupportedNode() bool { return false }

func (javaDiscovery) instructions(string) string { return "" }

func (javaDiscovery) onboardingScope(string) (discover, instructionsWhenConfigured bool) {
	return true, true
}

const javaBootstrap = "Place the action after the JDK setup step. By default it adds dd-java-agent to MAVEN_OPTS; a later step that sets MAVEN_OPTS must include `${{ env.MAVEN_OPTS }}` to keep the agent. Keep the existing Maven commands, profiles, modules, JVM options and matrices unchanged, and use Maven 3.2.1 or newer. One Maven command runs every framework the project declares: selecting a framework does not filter the suite."
