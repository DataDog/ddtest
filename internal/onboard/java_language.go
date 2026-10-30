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

// Java setup uses the Datadog GitHub Actions setup action, the Datadog GitLab
// setup script, the Datadog CircleCI orb, or on Buildkite the Datadog
// installer: each runs the installer that attaches dd-java-agent to the build
// JVM's options.
func (javaDiscovery) supportsCIProvider(provider string) bool {
	return provider == gitHubActionsProvider || provider == gitLabCIProvider || provider == circleCIProvider || provider == buildkiteProvider
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
	// The installer's agent, which a shell-sharing setup exported before the
	// job's own options, is local validation's too.
	return strings.Join(strings.Fields(strings.ReplaceAll(inherited, javaInstallerAgent, "")), " "), true, nil
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

// javaInstallerAgent is the -javaagent option the Datadog installer adds, in
// the workflow model's syntax: a path only CI knows.
const javaInstallerAgent = "-javaagent:${{ env.DD_JAVA_AGENT }}"

// javaAgentCarriers are the JVM option variables the installer adds
// dd-java-agent to: MAVEN_OPTS and GRADLE_OPTS by default, or
// JAVA_TOOL_OPTIONS for every Java process.
var javaAgentCarriers = []string{"MAVEN_OPTS", "GRADLE_OPTS", "JAVA_TOOL_OPTIONS"}

// javaInstallerCarriers names, in a sentence, the variables a provider's setup
// adds the agent to without a build system input: the installer of the
// Datadog CircleCI orb instruments every Java process.
func javaInstallerCarriers(provider string) string {
	if provider == circleCIProvider {
		return "to JAVA_TOOL_OPTIONS, for every Java process (to MAVEN_OPTS or GRADLE_OPTS when java_instrumented_build_system names maven or gradle)"
	}
	return "to MAVEN_OPTS and GRADLE_OPTS"
}

// The installer puts -javaagent before the options each variable has where
// it runs; which of them carries the agent depends on its build system
// input, which the CI check reads.
func (javaDiscovery) setupShellExports(current map[string]string) (map[string]string, map[string]string) {
	exported := map[string]string{}
	for _, carrier := range javaAgentCarriers {
		exported[carrier] = strings.TrimSpace(javaInstallerAgent + " " + current[carrier])
	}
	return exported, nil
}

// ciSetupRequest is the Java part of a provider's setup: an exact agent
// release, the build JVM options that carry it, and a JDK ddtest can read.
func (javaDiscovery) ciSetupRequest(root, framework, site string, terms ciSetupTerms) ciSetupRequest {
	request := ciSetupRequest{Language: "java", Site: site, TracerInput: "java-tracer-version", TracerVersion: "<exact dd-java-agent release>"}
	request.Notes = []string{
		"Replace <exact dd-java-agent release> with an exact dd-java-agent release, such as 1.67.1; local validation runs that release, and a floating one cannot be validated. Keep the existing build files, Maven or Gradle wrapper, JDK images, Maven goals or Gradle tasks, profiles, modules or projects, plugins, JVM options, test arguments and matrices. One Maven or Gradle command runs every framework the build declares: do not split it or filter it by " + framework + ". Local validation runs only the Maven test lifecycle or Gradle test tasks and never installs or publishes.",
		"The " + terms.Installer + " adds -javaagent for " + terms.Later + " " + javaInstallerCarriers(activeOrDefaultCIProvider().name()) + ", before the options the job set earlier; its build system input, if set, must name the build tool the tests run (maven or gradle). A later command that sets one of these variables must keep the agent: extend it with " + terms.Persist(`export MAVEN_OPTS="$MAVEN_OPTS <options>"`) + ", never replace it. Do not add -javaagent options or set DD_CIVISIBILITY_ENABLED yourself.",
		"ddtest reads the JDK of each instrumented job from its image, such as maven:3.9-eclipse-temurin-21, gradle:8.10-jdk17, eclipse-temurin:17-jdk or cimg/openjdk:17.0. A job that runs on the build agent's own JDK, or in an image whose name selects no JDK version, stays unverified: tell the user, and give it such an image only with their approval.",
		setupImageNote(root, "java", framework),
		setupUnsupportedNote(root, "java", framework),
		"If a job or matrix entry runs a JDK the agent cannot instrument (early-access builds, Java older than 8), keep its tests running uninstrumented in a job without " + terms.Entry + "; ddtest reports such jobs as excluded. Report each exclusion and its reason; do not drop entries, guard the tests or change JDK versions to obtain a pass. At least one instrumented job is required.",
		"Exclude .testoptimization/ from Git (add /.testoptimization/ to the repository .gitignore).",
	}
	request.Notes = slices.DeleteFunc(request.Notes, func(note string) bool { return note == "" })
	return request
}

func (javaDiscovery) automaticProviderSetup(string) bool { return false }

func (javaDiscovery) unsupportedImage(string) string { return "" }

func (javaDiscovery) runtimeVariable(name string) bool {
	return name == "JAVA_HOME" || name == "JAVA_TOOL_OPTIONS"
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
