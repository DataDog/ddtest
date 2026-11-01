// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/DataDog/ddtest/internal/gradle"
)

// A hand-written dd-java-agent setup attaches the agent to the build JVM,
// as dd-java-agent's documentation shows: -javaagent:<path>/dd-java-agent.jar
// in JAVA_TOOL_OPTIONS, MAVEN_OPTS for Maven, GRADLE_OPTS or
// org.gradle.jvmargs for Gradle, with a release the job downloads and the
// DD_* configuration in the job's environment or as -Ddd.* system
// properties. ddtest recognizes such a setup and lists what it lacks, but
// the Java CI check does not read it yet (see
// javaDiscovery.validatesHandWrittenSetup), so it is never certified.

// javaHandWrittenExample is an exact dd-java-agent release for messages.
const javaHandWrittenExample = "1.67.1"

// javaHandWrittenDownload is an exact-release download URL for messages.
const javaHandWrittenDownload = "https://repo1.maven.org/maven2/com/datadoghq/dd-java-agent/" + javaHandWrittenExample + "/dd-java-agent-" + javaHandWrittenExample + ".jar"

var (
	// javaHandWrittenJarRelease is the release a dd-java-agent JAR's file
	// name carries, as dd-java-agent-1.67.1.jar.
	javaHandWrittenJarRelease = regexp.MustCompile(`dd-java-agent-([0-9]+\.[0-9]+\.[0-9]+)\.jar`)
	// javaHandWrittenURLRelease is the release a download URL selects: Maven
	// Central's com/datadoghq/dd-java-agent/<release>/ or a GitHub release
	// tag, v<release>.
	javaHandWrittenURLRelease = regexp.MustCompile(`(?:dd-java-agent/|/download/v)([0-9]+\.[0-9]+\.[0-9]+)/`)
)

// javaHandWrittenCarrier is a JVM option list a hand-written setup attaches
// dd-java-agent through: its name, such as MAVEN_OPTS, and value.
type javaHandWrittenCarrier struct {
	name, value string
}

// javaHandWrittenSetup recognizes a hand-written dd-java-agent setup of the
// Maven or Gradle test command at index of job (see handWrittenSetup); nil
// when no JVM option list of the command attaches the agent.
func javaHandWrittenSetup(root string, provider ciProvider, workflow ciWorkflow, job runtimeJob, index int, framework, site string) *handWrittenSetup {
	step := job.Steps[index]
	env := handWrittenEnvironment(workflow, job, step)
	directory := filepath.Clean(filepath.FromSlash(stepDirectory(workflow, job, step)))
	if strings.HasPrefix(directory, "..") || filepath.IsAbs(directory) || strings.Contains(directory, "$") {
		return nil
	}
	tool := javaStepBuildTool(resolveJavaStep(root, workflow, job, step, framework), step.Run)
	var carriers []javaHandWrittenCarrier
	commandEnv := env
	for _, key := range []string{"JAVA_TOOL_OPTIONS", "MAVEN_OPTS", "GRADLE_OPTS", "JAVA_OPTS"} {
		if value, set := env[key]; set {
			carriers = append(carriers, javaHandWrittenCarrier{key, value})
		}
	}
	if commands, err := staticCommands(step.Run); err == nil {
		for _, words := range commands {
			words, assignments := handWrittenCommandPrefix(words)
			if !mavenCommand(words) && !gradle.Command(words) {
				continue
			}
			commandEnv = mergeEnvironment(env, assignments)
			for _, key := range []string{"JAVA_TOOL_OPTIONS", "MAVEN_OPTS", "GRADLE_OPTS", "JAVA_OPTS"} {
				if value, set := assignments[key]; set {
					carriers = append([]javaHandWrittenCarrier{{key, value}}, carriers...)
				}
			}
			for _, word := range words[1:] {
				for _, property := range []string{"argLine", "org.gradle.jvmargs"} {
					if value, ok := strings.CutPrefix(word, "-D"+property+"="); ok {
						carriers = append([]javaHandWrittenCarrier{{property, value}}, carriers...)
					}
				}
			}
			break
		}
	}
	for _, file := range []struct{ name, property string }{{"gradle.properties", "org.gradle.jvmargs"}, {".mvn/jvm.config", ""}} {
		if value := javaHandWrittenFileOptions(filepath.Join(root, directory, filepath.FromSlash(file.name)), file.property); value != "" {
			carriers = append(carriers, javaHandWrittenCarrier{file.name, value})
		}
	}
	var carrier javaHandWrittenCarrier
	jar := ""
	for _, current := range carriers {
		if jar = javaHandWrittenAgent(current.value); jar != "" {
			carrier = current
			break
		}
	}
	if jar == "" {
		return nil
	}
	setup := &handWrittenSetup{Reference: handWrittenReference("dd-java-agent", carrier.name)}
	if _, dynamic := handWrittenReads(carrier.value); dynamic && !strings.Contains(jar, "$") {
		setup.unresolved(carrier.name + " (" + carrier.value + ") holds values only CI knows, so ddtest cannot tell which options it passes the JVM.")
	}
	switch {
	case carrier.name == "argLine":
		setup.unresolved("-DargLine attaches dd-java-agent to the forked test JVMs only, not to the Maven build that Datadog's documented setup instruments (MAVEN_OPTS), so ddtest cannot tell whether it reports the build's test sessions and modules.")
	case carrier.name == ".mvn/jvm.config":
		setup.unresolved(".mvn/jvm.config attaches dd-java-agent to every Maven build, local ones included, so local validation cannot run the build without it.")
	case tool == javaGradle && (carrier.name == "MAVEN_OPTS"):
		setup.missing("MAVEN_OPTS attaches dd-java-agent, but this job runs Gradle, which does not read it: attach the agent through GRADLE_OPTS, org.gradle.jvmargs or JAVA_TOOL_OPTIONS instead.")
	case tool == javaMaven && (carrier.name == "GRADLE_OPTS" || carrier.name == "org.gradle.jvmargs" || carrier.name == "gradle.properties" || carrier.name == "JAVA_OPTS"):
		setup.missing(carrier.name + " attaches dd-java-agent, but this job runs Maven, which does not read it: attach the agent through MAVEN_OPTS or JAVA_TOOL_OPTIONS instead.")
	}
	if version := javaHandWrittenTracer(setup, jar, job, index, commandEnv); version != "" {
		setup.Tracer = version
		setup.Reference = handWrittenReference("dd-java-agent "+version, carrier.name)
	}
	// -Ddd.* system properties in the agent's options configure it like
	// DD_* variables, and take precedence over them.
	handWrittenConfiguration(setup, provider, job, mergeEnvironment(commandEnv, javaHandWrittenProperties(carrier.value)), site, false, false)
	return setup
}

// javaHandWrittenAgent is the dd-java-agent JAR a JVM option list attaches
// (-javaagent:<path>), empty when it attaches none.
func javaHandWrittenAgent(options string) string {
	for _, word := range strings.Fields(options) {
		jar, ok := strings.CutPrefix(strings.Trim(word, `"'`), "-javaagent:")
		if !ok {
			continue
		}
		jar, _, _ = strings.Cut(jar, "=")
		if strings.Contains(strings.ToLower(filepath.Base(jar)), "dd-java-agent") {
			return jar
		}
	}
	return ""
}

// javaHandWrittenProperties are the DD_* variables an option list's -Ddd.*
// system properties set, such as -Ddd.civisibility.enabled=true for
// DD_CIVISIBILITY_ENABLED.
func javaHandWrittenProperties(options string) map[string]string {
	properties := map[string]string{}
	for _, word := range strings.Fields(options) {
		property, ok := strings.CutPrefix(strings.Trim(word, `"'`), "-Ddd.")
		if !ok {
			continue
		}
		key, value, _ := strings.Cut(property, "=")
		properties["DD_"+strings.ToUpper(strings.NewReplacer(".", "_", "-", "_").Replace(key))] = value
	}
	return properties
}

// javaHandWrittenFileOptions reads the JVM options a build file passes the
// build: property's value in a properties file, or a whole jvm.config
// (property empty); empty when the file has none.
func javaHandWrittenFileOptions(file, property string) string {
	data, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	if property == "" {
		return strings.Join(strings.Fields(string(data)), " ")
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if ok && strings.TrimSpace(key) == property {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// javaHandWrittenTracer finds the exact dd-java-agent release jar is: the
// release its file name carries, otherwise the one a download command of the
// job, before the tests, selects. It records on setup why none is pinned.
func javaHandWrittenTracer(setup *handWrittenSetup, jar string, job runtimeJob, index int, env map[string]string) string {
	if match := javaHandWrittenJarRelease.FindStringSubmatch(jar); match != nil {
		return match[1]
	}
	// The last download before the tests is the JAR they attach.
	download := ""
	for _, step := range job.Steps[:index+1] {
		for _, word := range strings.Fields(step.Run) {
			address := strings.Trim(word, `"'`)
			if !strings.Contains(address, "://") || !strings.Contains(address, "java") {
				continue
			}
			for key, value := range env {
				address = strings.ReplaceAll(strings.ReplaceAll(address, "${"+key+"}", value), "$"+key, value)
			}
			download = address
		}
	}
	switch {
	case download == "":
		setup.missing("No command of the job downloads " + jar + " with an exact release, so ddtest cannot tell which dd-java-agent it attaches: download an exact release before the tests, such as curl -Lo dd-java-agent.jar " + javaHandWrittenDownload + ".")
	case strings.Contains(download, "dtdg.co/latest-java-tracer") || strings.Contains(download, "/latest/"):
		setup.missing("The job downloads the latest dd-java-agent (" + download + "), so each build can attach another release: download an exact release instead, such as " + javaHandWrittenDownload + ".")
	case strings.Contains(download, "$"):
		setup.unresolved("the job downloads dd-java-agent from " + download + ", whose release only CI knows.")
	case javaHandWrittenURLRelease.MatchString(download):
		return javaHandWrittenURLRelease.FindStringSubmatch(download)[1]
	default:
		setup.missing("The job downloads dd-java-agent from " + download + ", which names no exact release: download an exact release instead, such as " + javaHandWrittenDownload + ".")
	}
	return ""
}
