// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	_ "embed"
	"fmt"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/maven"
)

// javaDiscovery discovers the Maven and Gradle CI test steps of JVM test
// frameworks.
type javaDiscovery struct{}

func (javaDiscovery) name() string { return "java" }

func (javaDiscovery) actionLanguage() string { return "java" }

func (javaDiscovery) bootstrap(string) string { return javaBootstrap }

func (javaDiscovery) resolvesTestSteps(string) bool { return true }

// Agents apply Maven CI setup by hand; validation then runs through the
// coordinator.
func (javaDiscovery) coordinated() bool { return true }

// Java CI discovery reads GitHub Actions only.
func (javaDiscovery) supportsCIProvider(provider string) bool {
	return provider == gitHubActionsProvider
}

func (javaDiscovery) automaticCISetup() bool { return false }

func (javaDiscovery) keepsCISetting(string) bool { return false }

func (javaDiscovery) jobConsumer(string, ciWorkflow, runtimeJob, string) (bool, bool) {
	return false, false
}

// Every discovered Maven test step runs after a complete Datadog action with an
// exact agent release, or explicitly skips it; at least one stays instrumented.
// Coordinated validation enforces the same rule through JavaCIConfiguration.
func (javaDiscovery) ciSetupConfigured(root, framework, site string) bool {
	instrumented := false
	for _, job := range JavaCIConfiguration(root, framework, site).Jobs {
		if job.Status == "excluded" {
			continue
		}
		if job.Status != "not checked" {
			return false
		}
		instrumented = true
	}
	return instrumented
}

//go:embed instructions/java_agent.md
var javaAgentInstructions string

func (javaDiscovery) agentInstructions(framework, site string) string {
	siteInput := ""
	if site != "" && site != defaultDatadogSite {
		siteInput = ", `site: " + site + "` (the selected Datadog site)"
	}
	return strings.NewReplacer("__FRAMEWORK__", framework, "__SITE_INPUT__", siteInput).Replace(javaAgentInstructions)
}

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
	// Deploy credentials matter only to the goals validation never runs.
	if slices.Contains(javaDeployCredentials(job), key) {
		return "", true, nil
	}
	if key != "MAVEN_OPTS" && key != "GRADLE_OPTS" && key != "JAVA_TOOL_OPTIONS" {
		return value, false, nil
	}
	inherited := strings.NewReplacer("${{ env."+key+" }}", "", "${{env."+key+"}}", "").Replace(value)
	if inherited != value && (job.Env[key] != "" || workflow.Env[key] != "") {
		return "", false, fmt.Errorf("%s extends workflow or job JVM options; validate with those options explicitly", key)
	}
	return strings.TrimSpace(inherited), true, nil
}

// javaDeployCredentials are the environment variables that the job's
// actions/setup-java steps with a server-id explicitly name for Maven's
// settings.xml as repository credentials or the GPG passphrase. Maven reads
// them only to deploy or sign, which local validation never does. The
// action's default names, such as GITHUB_TOKEN, may serve tests too.
func javaDeployCredentials(job runtimeJob) []string {
	var names []string
	for _, step := range job.Steps {
		if uses, _, _ := strings.Cut(strings.ToLower(step.Uses), "@"); uses != "actions/setup-java" || step.With["server-id"] == "" {
			continue
		}
		for _, input := range []string{"server-username", "server-password", "gpg-passphrase"} {
			if name := step.With[input]; name != "" {
				names = append(names, name)
			}
		}
	}
	return names
}

func (javaDiscovery) skipsUnsupportedNode() bool { return false }

//go:embed instructions/java.md
var javaInstructions string

func (javaDiscovery) instructions(framework string) string {
	return strings.ReplaceAll(javaInstructions, "__FRAMEWORK__", framework)
}

func (javaDiscovery) onboardingScope(string) (discover, instructionsWhenConfigured bool) {
	return true, true
}

const javaBootstrap = "Place the action after the JDK setup step. By default it adds dd-java-agent to MAVEN_OPTS and GRADLE_OPTS; a later step that sets either must include `${{ env.MAVEN_OPTS }}` or `${{ env.GRADLE_OPTS }}` to keep the agent. Keep the existing Maven or Gradle commands, profiles, modules, tasks, JVM options and matrices unchanged, and use Maven 3.2.1 or newer. One Maven or Gradle command runs every framework the build declares: selecting a framework does not filter the suite."

// Setup runs on GitHub Actions only, whose steps don't share a shell.
func (javaDiscovery) setupShellExports(map[string]string) (map[string]string, map[string]string) {
	return nil, nil
}

// Setup runs on GitHub Actions only.
func (javaDiscovery) ciSetupRequest(string, string, string, ciSetupTerms) ciSetupRequest {
	return ciSetupRequest{}
}

func (javaDiscovery) automaticProviderSetup(string) bool { return false }

func (javaDiscovery) unsupportedImage(string) string { return "" }

func (javaDiscovery) runtimeVariable(name string) bool {
	return name == "JAVA_HOME" || name == "JAVA_TOOL_OPTIONS"
}

func (javaDiscovery) toolVariable(name string) bool {
	return slices.Contains([]string{"MAVEN_ARGS", "MAVEN_OPTS", "MAVEN_CONFIG", "GRADLE_OPTS", "JAVA_OPTS"}, name)
}

func (javaDiscovery) cacheVariable(name string) bool {
	return name == "GRADLE_USER_HOME"
}

// No CI components are modeled for this language.
func (javaDiscovery) ciComponents(string) []ciComponent { return nil }

// No CI runtime statements yet.
func (javaDiscovery) statedRuntime() *statedRuntime { return nil }

func (javaDiscovery) ciPluginLanguage() string { return "JAVA" }

// Java has no CI plugin setup yet.
func (javaDiscovery) ciPluginExports(map[string]string) (map[string]string, map[string]string) {
	return nil, nil
}

func (javaDiscovery) ciPluginComposes(string) string { return "" }

// No shell runtime selection is modeled for this language.
func (javaDiscovery) selectsShellRuntime([]string) bool { return false }
