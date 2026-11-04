// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	_ "embed"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/maven"
	"github.com/DataDog/ddtest/internal/platform"
)

// javaDiscovery discovers the Maven and Gradle CI test steps of JVM test
// frameworks.
type javaDiscovery struct{}

func (javaDiscovery) name() string { return "java" }

func (javaDiscovery) displayName() string { return "Java" }

func (javaDiscovery) actionLanguage() string { return "java" }

func (javaDiscovery) runtimeSetupAction() string { return "actions/setup-java" }

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
	return provider == gitHubActionsProvider || provider == gitLabCIProvider || provider == circleCIProvider || provider == buildkiteProvider || provider == jenkinsProvider
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
func (javaDiscovery) variantValue(workflow ciWorkflow, job runtimeJob, step runtimeStep, row map[string]any, key, value string) (string, bool, error) {
	// Deploy credentials matter only to the goals validation never runs.
	if slices.Contains(javaDeployCredentials(job), key) {
		return "", true, nil
	}
	if key != "MAVEN_OPTS" && key != "GRADLE_OPTS" && key != "JAVA_TOOL_OPTIONS" && key != "JAVA_OPTS" {
		return value, false, nil
	}
	inherited := strings.NewReplacer("${{ env."+key+" }}", "", "${{env."+key+"}}", "").Replace(value)
	if inherited != value && (job.Env[key] != "" || workflow.Env[key] != "") {
		return "", false, fmt.Errorf("%s extends workflow or job JVM options; validate with those options explicitly", key)
	}
	// The row's values, as ${{ matrix.service }}, come before the options
	// are split, since an expression's spaces are not the options'.
	if resolved, err := interpolateMatrix(inherited, row); err == nil {
		inherited = resolved
	}
	// The installer's agent, which a shell-sharing setup exported before the
	// job's own options, is local validation's too, and so are the -Ddd.*
	// properties a hand-written setup configures CI's agent with, as its
	// DD_* variables: they would send the local agent's reports elsewhere.
	// One that turns Test Optimization off in CI, or whose value only CI
	// knows, is never removed unseen.
	if value, set := javaHandWrittenProperties(key, inherited)["DD_CIVISIBILITY_ENABLED"]; set {
		if _, known := handWrittenTrue(value); !known {
			return "", false, fmt.Errorf("CI instrumentation setting -Ddd.civisibility.enabled=%s in %s requires review", value, key)
		}
		effective, from := javaEffectiveEnablement(workflow, job, step, row, key, inherited)
		if handWrittenFalse(effective) {
			return "", false, fmt.Errorf("CI instrumentation is explicitly disabled by -Ddd.civisibility.enabled=%s in %s", effective, from)
		}
		// Maven passes MAVEN_OPTS quotes on, so "true" there enables nothing.
		// A hand-written agent's own options are checked with its setup.
		if enabled, _ := handWrittenTrue(effective); effective != "" && !enabled && javaHandWrittenAgent(key, inherited) == "" {
			return "", false, fmt.Errorf("CI instrumentation is not enabled by -Ddd.civisibility.enabled=%s in %s, which the JVM does not read as true", effective, from)
		}
	}
	return javaWithoutAgentProperties(key, strings.TrimSpace(strings.ReplaceAll(inherited, javaInstallerAgent, ""))), true, nil
}

// javaEffectiveEnablement is the -Ddd.civisibility.enabled value the step's
// build applies last, and the option list that sets it, with key's resolved
// value: a later list overrides an earlier one (see javaEffectiveProperties).
// A step whose build tool ddtest cannot tell reads key's own value.
func javaEffectiveEnablement(workflow ciWorkflow, job runtimeJob, step runtimeStep, row map[string]any, key, value string) (string, string) {
	tool := javaStepBuildTool(commandResolution{}, step.Run)
	if tool == "" {
		return javaHandWrittenProperties(key, value)["DD_CIVISIBILITY_ENABLED"], key
	}
	env := map[string]string{}
	for _, scope := range []map[string]string{workflow.Env, job.Env, step.Env} {
		maps.Copy(env, scope)
	}
	for carrier, options := range env {
		if resolved, err := interpolateMatrix(options, row); err == nil {
			env[carrier] = resolved
		}
	}
	env[key] = value
	properties, from := javaEffectiveProperties(env, tool)
	return properties["DD_CIVISIBILITY_ENABLED"], from["DD_CIVISIBILITY_ENABLED"]
}

// javaWithoutAgentProperties removes the -Ddd.* system properties of
// carrier's JVM options, split as its reader splits them, keeping those of tracer plugin settings, which change test
// behavior as their DD_* variables do (see TracerPluginSetting).
func javaWithoutAgentProperties(carrier, options string) string {
	words := platform.JavaCarrierWords(carrier, options)
	kept := slices.DeleteFunc(slices.Clone(words), func(word string) bool {
		key, _, ok := javaAgentProperty(carrier, word)
		return ok && !TracerPluginSetting(key)
	})
	if len(kept) == len(words) {
		return options
	}
	return strings.Join(kept, " ")
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
	if terms.Plugin {
		return javaPluginSetupRequest(root, framework, site, terms)
	}
	request := ciSetupRequest{Language: "java", Site: site, TracerInput: "java-tracer-version", TracerVersion: "<exact dd-java-agent release>"}
	request.Notes = []string{
		"Replace <exact dd-java-agent release> with an exact dd-java-agent release, such as " + javaRecommendedTracer + "; local validation runs that release, and a floating one cannot be validated. Keep the existing build files, Maven or Gradle wrapper, JDK images, Maven goals or Gradle tasks, profiles, modules or projects, plugins, JVM options, test arguments and matrices. One Maven or Gradle command runs every framework the build declares: do not split it or filter it by " + framework + ". Local validation runs only the Maven test lifecycle or Gradle test tasks and never installs or publishes.",
		"The " + terms.Installer + " adds -javaagent for " + terms.Later + " " + javaInstallerCarriers(activeOrDefaultCIProvider().name()) + ", before the options the job set earlier; its build system input, if set, must name the build tool the tests run (maven or gradle). A later command that sets one of these variables must keep the agent: extend it with " + terms.Persist(`export MAVEN_OPTS="$MAVEN_OPTS <options>"`) + ", never replace it. Do not add -javaagent options or set DD_CIVISIBILITY_ENABLED yourself.",
		"ddtest reads the JDK of each instrumented job from its image, such as maven:3.9-eclipse-temurin-21, gradle:8.10-jdk17, eclipse-temurin:17-jdk or cimg/openjdk:17.0. A job that runs on the build agent's own JDK, or in an image whose name selects no JDK version, stays unverified: tell the user, and give it such an image only with their approval.",
		setupImageNote(root, "java", framework, site),
		setupUnsupportedNote(root, "java", framework),
		"If a job or matrix entry runs a JDK the agent cannot instrument (early-access builds, Java older than 8), keep its tests running uninstrumented in a job without " + terms.Entry + "; ddtest reports such jobs as excluded. Report each exclusion and its reason; do not drop entries, guard the tests or change JDK versions to obtain a pass. At least one instrumented job is required.",
		"Exclude .testoptimization/ from Git (add /.testoptimization/ to the repository .gitignore).",
	}
	request.Notes = slices.DeleteFunc(request.Notes, func(note string) bool { return note == "" })
	return request
}

func (javaDiscovery) automaticProviderSetup(string) bool { return false }

func (javaDiscovery) unsupportedImage(string) string { return "" }

// A hand-written setup's JVM option prefixes stay in the build command,
// which javaHandWrittenSetup reads with its environment.
func (javaDiscovery) preloadVariable(string) bool { return false }

func (javaDiscovery) handWrittenSetup(root string, provider ciProvider, workflow ciWorkflow, job runtimeJob, index int, row map[string]any, framework, site string) *handWrittenSetup {
	return javaHandWrittenSetup(root, provider, workflow, job, index, row, framework, site)
}

// The Java CI check reads a hand-written dd-java-agent setup where no
// provider setup precedes the tests (see JavaCIConfiguration).
func (javaDiscovery) validatesHandWrittenSetup() bool { return true }

func (javaDiscovery) runtimeVariable(name string) bool {
	return name == "JAVA_HOME" || name == "JAVA_TOOL_OPTIONS"
}

// Gradle reads ORG_GRADLE_PROJECT_<name> as the project property <name>.
func (javaDiscovery) toolVariable(name string) bool {
	return slices.Contains([]string{"MAVEN_ARGS", "MAVEN_OPTS", "MAVEN_CONFIG", "GRADLE_OPTS", "JAVA_OPTS", "JDK_JAVA_OPTIONS", "_JAVA_OPTIONS"}, name) ||
		strings.HasPrefix(name, "ORG_GRADLE_PROJECT_")
}

func (javaDiscovery) cacheVariable(name string) bool {
	return name == "GRADLE_USER_HOME"
}

// No CI components are modeled for this language.
func (javaDiscovery) ciComponents(string) []ciComponent { return nil }

func (javaDiscovery) statedRuntime() *statedRuntime { return javaStatedRuntime }

func (javaDiscovery) ciPluginLanguage() string { return "JAVA" }

// The Datadog Jenkins plugin's JavaConfigurator puts -javaagent, with the
// dd-java-agent it downloads into the workspace, before the existing value
// of MAVEN_OPTS and GRADLE_OPTS, joined with a space. It also does so for
// SBT_OPTS, ANT_OPTS and DD_TRACE_OPTS, which no build ddtest validates
// reads.
func (javaDiscovery) ciPluginExports(current map[string]string) (map[string]string, map[string]string) {
	exported := map[string]string{}
	for _, carrier := range javaPluginCarriers {
		exported[carrier] = javaInstallerAgent
		if existing, set := current[carrier]; set {
			exported[carrier] += " " + existing
		}
	}
	return exported, nil
}

// javaPluginCarriers are the build JVM option variables the Datadog Jenkins
// plugin adds dd-java-agent to that ddtest validates builds of.
var javaPluginCarriers = []string{"MAVEN_OPTS", "GRADLE_OPTS"}

// The plugin computes its variables once per agent, from the first step it
// configures there, and sets them on every later step.
func (javaDiscovery) ciPluginComposes(key string) string {
	if !slices.Contains(javaPluginCarriers, key) {
		return ""
	}
	return "on each agent it sets " + key + " for every step to its -javaagent option followed by a space and the " + key + " of the first step it configures there, so this value is lost, or, when its step is that first one, it reaches every later step on the agent. ddtest validates with the pipeline's " + key
}

// javaPluginTracerURL is the additionalVariables entry that replaces the
// dd-java-agent download of the Datadog Jenkins plugin, which otherwise
// downloads the latest release; the plugin verifies a replaced download's
// signature.
const javaPluginTracerURL = "DATADOG_JENKINS_PLUGIN_TRACER_DISTRIBUTION_URL"

// The release a replaced download is, when it is a canonical dd-java-agent
// artifact: Maven Central's or the DataDog/dd-trace-java GitHub release's.
// Another URL, even one whose path names a release, may serve any agent the
// plugin accepts, so it is the URL itself, which is not an exact release.
func (javaDiscovery) ciPluginTracerRelease(variables map[string]string) (string, bool) {
	url, set := variables[javaPluginTracerURL]
	if !set {
		return "", false
	}
	if release, ok := javaCanonicalRelease(url); ok {
		return release, true
	}
	return url, true
}

// javaCanonicalRelease is the exact release a canonical dd-java-agent
// download is (see javaCanonicalAgentURL): a file name that names a release
// must name its path's.
func javaCanonicalRelease(url string) (string, bool) {
	match := javaCanonicalAgentURL.FindStringSubmatch(url)
	switch {
	case match == nil:
		return "", false
	case match[1] != "" && match[1] == match[2]:
		return match[1], true
	case match[3] != "" && (match[4] == "" || match[3] == match[4]):
		// The release's unversioned asset, dd-java-agent.jar, is its tag's.
		return match[3], true
	}
	return "", false
}

// javaCanonicalAgentURL is a dd-java-agent JAR's canonical download, with its
// release twice: in Maven Central's path and file name, or in a GitHub
// release's tag and file name.
var javaCanonicalAgentURL = regexp.MustCompile(`^(?:https://(?:repo1\.maven\.org|repo\.maven\.apache\.org)/maven2/com/datadoghq/dd-java-agent/([0-9]+\.[0-9]+\.[0-9]+)/dd-java-agent-([0-9]+\.[0-9]+\.[0-9]+)\.jar|https://github\.com/DataDog/dd-trace-java/releases/download/v([0-9]+\.[0-9]+\.[0-9]+)/dd-java-agent(?:-([0-9]+\.[0-9]+\.[0-9]+))?\.jar)$`)

func (javaDiscovery) ciShellAfter(ciWorkflow, runtimeJob, runtimeStep, string, commandResolution) (bool, string) {
	return false, ""
}

// No shell runtime selection is modeled for this language.
func (javaDiscovery) selectsShellRuntime([]string) bool { return false }

// Java's build tools may run tests; ddtest classifies them in its own rules.
func (javaDiscovery) checkTool(string, string, []string) (bool, bool) { return false, false }

// withSonarQubeEnv sets the Maven goal its scanner runs, sonar:sonar or the
// plugin's qualified sonar goal, neither of which runs tests.
func (javaDiscovery) ciStepVariables(step string) map[string]string {
	if step == "withSonarQubeEnv" {
		return map[string]string{"SONAR_MAVEN_GOAL": "sonar:sonar"}
	}
	return nil
}

// dd-java-agent is no project dependency, so ddtest recommends its release.
func (javaDiscovery) recommendedTracer() string { return javaRecommendedTracer }

// ciPluginReset removes the agent the Datadog Jenkins plugin puts before the
// step's MAVEN_OPTS and GRADLE_OPTS, keeping the step's own options.
func (javaDiscovery) ciPluginReset() string {
	var resets []string
	for _, key := range []string{"MAVEN_OPTS", "GRADLE_OPTS"} {
		resets = append(resets, `case "${`+key+`:-}" in -javaagent:*/.datadog/dd-java-agent.jar*) `+key+`="${`+key+`#-javaagent:*/.datadog/dd-java-agent.jar}"; `+key+`="${`+key+`# }";; esac`)
	}
	// One compound statement, as a reset the snippet's check reads.
	return "{ " + strings.Join(resets, "; ") + "; }"
}
