// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const handWrittenPOM = `<project><artifactId>service</artifactId><dependencies><dependency><groupId>org.junit.jupiter</groupId><artifactId>junit-jupiter</artifactId></dependency></dependencies></project>`

const handWrittenAgentDownload = "curl -Lo dd-java-agent.jar https://repo1.maven.org/maven2/com/datadoghq/dd-java-agent/1.67.1/dd-java-agent-1.67.1.jar"

// javaHandWrittenRepository is a repository whose provider configuration
// runs command after before with env, in a JDK 21 image.
func javaHandWrittenRepository(t *testing.T, provider handWrittenProvider, env map[string]string, before []string, command string, files map[string]string) string {
	t.Helper()
	config := strings.NewReplacer("cimg/node:22.11", "cimg/openjdk:21.0", "node:22", "maven:3.9-eclipse-temurin-21").Replace(provider.config(env, before, command))
	return unvalidatedRepository(t, provider, config, files)
}

// javaHandWrittenFinding is the Java CI check's finding of the job that
// sets dd-java-agent up by hand, which onboarding no longer stops at.
func javaHandWrittenFinding(t *testing.T, root string) RuntimeFinding {
	t.Helper()
	require.NoError(t, CheckCIProviderSupport(root, "java", "junit5"), "a hand-written Java setup is checked, not refused")
	ci := JavaCIConfiguration(root, "junit5", "datadoghq.com")
	require.Len(t, ci.Jobs, 1, ci.Reason)
	require.True(t, HandWrittenSetupAction(ci.Jobs[0].Action), ci.Jobs[0].Action)
	return ci.Jobs[0]
}

// A Maven or Gradle job that attaches dd-java-agent by hand is checked on
// every provider without the shared syntax: certified when it pins an exact
// release and configures reporting, with what it lacks otherwise.
func TestHandWrittenJavaSetups(t *testing.T) {
	agentless := map[string]string{"MAVEN_OPTS": "-javaagent:dd-java-agent.jar", "DD_CIVISIBILITY_ENABLED": "true", "DD_CIVISIBILITY_AGENTLESS_ENABLED": "true", "DD_SITE": "datadoghq.com"}
	for _, provider := range handWrittenProviders {
		t.Run(provider.name, func(t *testing.T) {
			root := javaHandWrittenRepository(t, provider, agentless, []string{handWrittenAgentDownload}, "mvn -B test", map[string]string{"pom.xml": handWrittenPOM})
			setup := firstJobHandWrittenSetup(t, root, "java", "junit5", "datadoghq.com")
			require.NotNil(t, setup)
			require.True(t, setup.complete(), setup.problem())
			finding := javaHandWrittenFinding(t, root)
			require.Equal(t, "hand-written dd-java-agent 1.67.1 setup (MAVEN_OPTS)", finding.Action)
			require.Equal(t, "1.67.1", finding.TracerRequested, "validation runs the release the job attaches")
			require.Equal(t, "not checked", finding.Status, finding.Reason)
			require.Contains(t, finding.Reason, "Validate dd-java-agent 1.67.1 on Java 21")
		})
	}
	provider := handWrittenProviders[0]
	gradleBuild := map[string]string{"settings.gradle": "rootProject.name = 'service'\n", "build.gradle": "plugins { id 'java' }\ndependencies { testImplementation 'org.junit.jupiter:junit-jupiter:5.11.0' }\ntest { useJUnitPlatform() }\n", "gradlew": "#!/bin/sh\n"}
	for _, tc := range []struct {
		name    string
		env     map[string]string
		before  []string
		command string
		files   map[string]string
		// reference is the setup's; problem and unresolved, one of what it
		// lacks and of what ddtest cannot read, each keeping it uncertified.
		reference, problem, unresolved string
	}{
		{name: "tool options and properties", env: map[string]string{"JAVA_TOOL_OPTIONS": "-javaagent:/opt/dd-java-agent-1.67.1.jar -Ddd.civisibility.enabled=true -Ddd.civisibility.agentless.enabled=true -Ddd.site=datadoghq.com"}, before: []string{"curl -Lo /opt/dd-java-agent-1.67.1.jar https://repo1.maven.org/maven2/com/datadoghq/dd-java-agent/1.67.1/dd-java-agent-1.67.1.jar"}, command: "mvn test", reference: "hand-written dd-java-agent 1.67.1 setup (JAVA_TOOL_OPTIONS)"},
		// The download and the attached agent are matched by full path.
		{name: "download elsewhere", env: with(agentless, "MAVEN_OPTS", "-javaagent:/opt/dd-java-agent.jar"), command: "mvn test", unresolved: "cannot tell whether it is the /opt/dd-java-agent.jar the tests attach"},
		{name: "other absolute path", env: with(agentless, "MAVEN_OPTS", "-javaagent:/opt/dd-java-agent.jar"), before: []string{"curl -Lo /tmp/dd-java-agent.jar https://repo1.maven.org/maven2/com/datadoghq/dd-java-agent/1.67.1/dd-java-agent-1.67.1.jar"}, command: "mvn test", problem: "No command of the job downloads /opt/dd-java-agent.jar"},
		{name: "same relative path", env: with(agentless, "MAVEN_OPTS", "-javaagent:./dd-java-agent.jar"), command: "mvn test", reference: "hand-written dd-java-agent 1.67.1 setup (MAVEN_OPTS)"},
		{name: "Maven Central's other host", env: agentless, before: []string{"curl -Lo dd-java-agent.jar https://repo.maven.apache.org/maven2/com/datadoghq/dd-java-agent/1.67.1/dd-java-agent-1.67.1.jar"}, command: "mvn test", reference: "hand-written dd-java-agent 1.67.1 setup (MAVEN_OPTS)"},
		// A release in the attached file's name says nothing of its bytes.
		{name: "versioned file, no download", env: with(agentless, "MAVEN_OPTS", "-javaagent:/opt/dd-java-agent-1.67.1.jar"), before: []string{}, command: "mvn test", problem: "No command of the job downloads /opt/dd-java-agent-1.67.1.jar with an exact release"},
		{name: "versioned file, latest download", env: with(agentless, "MAVEN_OPTS", "-javaagent:dd-java-agent-1.67.1.jar"), before: []string{"curl -Lo dd-java-agent-1.67.1.jar https://dtdg.co/latest-java-tracer"}, command: "mvn test", problem: "The job downloads the latest dd-java-agent"},
		{name: "custom mirror", before: []string{"curl -Lo dd-java-agent.jar https://mirror.example/com/datadoghq/dd-java-agent/1.67.1/dd-java-agent-1.67.1.jar"}, command: "mvn test", problem: "which is not an exact release's artifact on Maven Central or the DataDog/dd-trace-java GitHub releases"},
		{name: "GitHub release", before: []string{"wget -O dd-java-agent.jar https://github.com/DataDog/dd-trace-java/releases/download/v1.68.0/dd-java-agent-1.68.0.jar"}, command: "mvn test", reference: "hand-written dd-java-agent 1.68.0 setup (MAVEN_OPTS)"},
		{name: "latest", before: []string{"curl -Lo dd-java-agent.jar https://dtdg.co/latest-java-tracer"}, command: "mvn test", reference: "hand-written dd-java-agent setup (MAVEN_OPTS)", problem: "The job downloads the latest dd-java-agent"},
		{name: "no download", before: []string{}, command: "mvn test", problem: "No command of the job downloads dd-java-agent.jar with an exact release"},
		{name: "another tool's release", before: []string{"curl -Lo java-helper.zip https://github.com/example/tools/releases/download/v1.67.1/java-helper.zip"}, command: "mvn test", problem: "No command of the job downloads dd-java-agent.jar with an exact release"},
		{name: "not enabled", env: with(agentless, "DD_CIVISIBILITY_ENABLED", ""), command: "mvn test", problem: "set DD_CIVISIBILITY_ENABLED=true"},
		{name: "literal key", env: with(agentless, "DD_API_KEY", "0123456789abcdef"), command: "mvn test", problem: "never commit the key"},
		{name: "Gradle reads no MAVEN_OPTS", command: "./gradlew test", files: gradleBuild, problem: "this job runs Gradle, which does not read it"},
		{name: "Gradle JVM arguments", env: with(agentless, "MAVEN_OPTS", ""), command: "./gradlew test -Dorg.gradle.jvmargs=-javaagent:dd-java-agent.jar", files: gradleBuild, reference: "hand-written dd-java-agent 1.67.1 setup (org.gradle.jvmargs)", unresolved: "which local validation cannot remove from the build"},
		{name: "APM tracing off", env: with(agentless, "DD_TRACE_ENABLED", "false"), command: "mvn test", reference: "hand-written dd-java-agent 1.67.1 setup (MAVEN_OPTS)"},
		{name: "curl --output=", before: []string{"curl -L --output=dd-java-agent.jar https://repo1.maven.org/maven2/com/datadoghq/dd-java-agent/1.67.1/dd-java-agent-1.67.1.jar"}, command: "mvn test", reference: "hand-written dd-java-agent 1.67.1 setup (MAVEN_OPTS)"},
		{name: "downloader path", before: []string{"/usr/bin/wget -O dd-java-agent.jar https://repo1.maven.org/maven2/com/datadoghq/dd-java-agent/1.67.1/dd-java-agent-1.67.1.jar"}, command: "mvn test", reference: "hand-written dd-java-agent 1.67.1 setup (MAVEN_OPTS)"},
		{name: "Agent mode in another option list", env: with(agentless, "JAVA_TOOL_OPTIONS", "-Ddd.civisibility.agentless.enabled=false"), command: "mvn test", unresolved: "--ci-confirm datadog-agent"},
		{name: "test JVMs only", env: with(agentless, "MAVEN_OPTS", ""), command: "mvn test -DargLine=-javaagent:dd-java-agent.jar", unresolved: "-DargLine attaches dd-java-agent to the forked test JVMs only"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := tc.env
			if env == nil {
				env = agentless
			}
			files := map[string]string{"pom.xml": handWrittenPOM}
			if tc.files != nil {
				files = tc.files
			}
			before := tc.before
			if before == nil {
				before = []string{handWrittenAgentDownload}
			}
			root := javaHandWrittenRepository(t, provider, env, before, tc.command, files)
			finding := javaHandWrittenFinding(t, root)
			if tc.reference != "" {
				require.Equal(t, tc.reference, finding.Action)
			}
			switch {
			case tc.problem != "":
				require.Equal(t, CodeIncompleteCISetup, finding.Code)
				require.Contains(t, finding.Reason, "keep it, without adding a second setup")
				require.Contains(t, finding.Reason, tc.problem)
			case tc.unresolved != "":
				require.Equal(t, CodeUnverifiedInitialization, finding.Code)
				require.Contains(t, finding.Reason, tc.unresolved)
			default:
				require.Equal(t, "not checked", finding.Status, finding.Reason)
			}
		})
	}
	// Without the agent there is no hand-written setup, and the job is
	// reported as uninstrumented.
	root := javaHandWrittenRepository(t, provider, map[string]string{"MAVEN_OPTS": "-Xmx1g"}, nil, "mvn test", map[string]string{"pom.xml": handWrittenPOM})
	require.Nil(t, firstJobHandWrittenSetup(t, root, "java", "junit5", ""))
	ci := JavaCIConfiguration(root, "junit5", "")
	require.Len(t, ci.Jobs, 1)
	require.Contains(t, ci.Jobs[0].Reason, "so its tests are not instrumented")
}

// Java's provider setups and its hand-written setups never handle the same
// job: a job the Datadog setup script sets up is checked as the script's,
// whatever its JVM options name, and one that attaches dd-java-agent by hand
// beside it is checked as that setup, without the script prescribed on top.
func TestHandWrittenJavaBesideTheProviderSetup(t *testing.T) {
	handWrittenJob := "manual:\n  image: maven:3.9-eclipse-temurin-21\n  variables:\n    MAVEN_OPTS: -javaagent:dd-java-agent.jar\n    DD_CIVISIBILITY_ENABLED: \"true\"\n    DD_CIVISIBILITY_AGENTLESS_ENABLED: \"true\"\n  script:\n    - " + handWrittenAgentDownload + "\n    - mvn -B verify\n"
	root := gitLabJavaRepository(t, gitLabJavaJob("maven:3.9-eclipse-temurin-21", gitLabJavaSetup, "", "    - export MAVEN_OPTS=\"$MAVEN_OPTS -javaagent:dd-java-agent.jar\"\n"))
	require.Nil(t, firstJobHandWrittenSetup(t, root, "java", "junit5", ""))
	require.NoError(t, CheckCIProviderSupport(root, "java", "junit5"))
	ci := JavaCIConfiguration(root, "junit5", "")
	require.Len(t, ci.Jobs, 1, ci.Reason)
	require.False(t, HandWrittenSetupAction(ci.Jobs[0].Action), ci.Jobs[0].Action)

	root = gitLabJavaRepository(t, gitLabJavaJob("maven:3.9-eclipse-temurin-21", gitLabJavaSetup, "", "")+handWrittenJob)
	require.NoError(t, CheckCIProviderSupport(root, "java", "junit5"))
	ci = JavaCIConfiguration(root, "junit5", "")
	require.Len(t, ci.Jobs, 2, ci.Reason)
	actions := map[string]string{}
	for _, job := range ci.Jobs {
		actions[job.Job] = job.Action
	}
	require.Equal(t, "hand-written dd-java-agent 1.67.1 setup (MAVEN_OPTS)", actions["manual"])
	require.False(t, HandWrittenSetupAction(actions["test"]), actions["test"])
}

// Local replay drops the -Ddd.* properties a hand-written setup configures
// CI's agent with, as it drops those DD_* variables, and keeps tracer
// plugin settings, which change test behavior.
func TestJavaReplayDropsAgentProperties(t *testing.T) {
	value, handled, err := (javaDiscovery{}).variantValue(ciWorkflow{}, runtimeJob{}, runtimeStep{}, nil, "MAVEN_OPTS", "-Xmx1g -Ddd.civisibility.agentless.enabled=true -Ddd.site=datadoghq.com -Ddd.trace.jdbc.enabled=false")
	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, "-Xmx1g -Ddd.trace.jdbc.enabled=false", value)
	key, setting, ok := javaAgentProperty("-Ddd.civisibility.enabled=true")
	require.True(t, ok)
	require.Equal(t, "DD_CIVISIBILITY_ENABLED", key)
	require.Equal(t, "true", setting)
	_, _, ok = javaAgentProperty("-Dmaven.repo.local=/cache")
	require.False(t, ok)
}

// A curl command continued on the next line is one download.
func TestJavaDownloadCommandsJoinContinuations(t *testing.T) {
	commands := javaDownloadCommands("curl -L -o dd-java-agent.jar \\\n  https://repo1.maven.org/maven2/com/datadoghq/dd-java-agent/1.67.1/dd-java-agent-1.67.1.jar\nmvn test")
	require.Len(t, commands, 1)
	address, target := javaDownload(commands[0])
	require.Equal(t, "dd-java-agent.jar", target)
	require.Contains(t, address, "/1.67.1/")
}
