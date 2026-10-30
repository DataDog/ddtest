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

func TestJavaImageRuntime(t *testing.T) {
	for image, version := range map[string]string{
		"maven:3.9-eclipse-temurin-21":              "21",
		"maven:3.9.6-eclipse-temurin-17-alpine":     "17",
		"maven:3-openjdk-11":                        "11",
		"maven:3.8-jdk-8":                           "8",
		"maven:3.9-amazoncorretto-17":               "17",
		"gradle:8.10.2-jdk17":                       "17",
		"gradle:8-jdk21-alpine":                     "21",
		"gradle:jdk11":                              "11",
		"eclipse-temurin:17-jdk":                    "17",
		"eclipse-temurin:8u412-b08-jdk":             "8",
		"docker.io/library/eclipse-temurin:21":      "21",
		"openjdk:11-jdk-slim":                       "11",
		"amazoncorretto:21-alpine":                  "21",
		"cimg/openjdk:17.0":                         "17",
		"cimg/openjdk:21.0.2@sha256:0123abc":        "21",
		"circleci/openjdk:11-jdk":                   "11",
		"maven:3":                                   "",
		"gradle:8.10":                               "",
		"eclipse-temurin":                           "",
		"ubuntu:24.04":                              "",
		"registry.example.com/team/builder:java-17": "",
	} {
		require.Equal(t, version, javaImageRuntime(image), image)
	}
}

const gitLabJavaSetup = `LANGUAGES="java" SITE="datadoghq.com" API_KEY="$DD_API_KEY" JAVA_TRACER_VERSION="1.67.1"`

// gitLabJavaRepository is a JUnit 5 Maven repository whose only CI is
// pipeline, with GitLab selected.
func gitLabJavaRepository(t *testing.T, pipeline string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "pom.xml", javaJUnit5POM)
	writeFile(t, root, gitLabCIFile, pipeline)
	t.Chdir(root)
	useCIProvider(t, "gitlab")
	return root
}

func gitLabJavaJob(image, setup, before, after string) string {
	return "test:\n  image: " + image + "\n  script:\n" + before + gitLabSetupEntry(setup) + after + "    - mvn -B verify\n"
}

// The Datadog GitLab setup script runs the installer, which adds the agent to
// MAVEN_OPTS and GRADLE_OPTS in the job's shell; the job image selects the
// JDK the row is certified on.
func TestJavaGitLabSetupCompleteness(t *testing.T) {
	complete := gitLabJavaJob("maven:3.9-eclipse-temurin-21", gitLabJavaSetup, "", "")
	for _, tc := range []struct {
		name, pipeline, site, status, reason string
	}{
		{"complete", complete, "", "not checked", "Validate dd-java-agent 1.67.1 on Java 21"},
		{"selected site", complete, "datadoghq.com", "not checked", ""},
		{"other site", complete, "datadoghq.eu", "inconclusive", "datadoghq.eu"},
		{"missing", "test:\n  image: maven:3.9-eclipse-temurin-21\n  script:\n    - mvn -B verify\n", "", "inconclusive", "No Datadog setup script selects java before this test step"},
		{"another language", gitLabJavaJob("maven:3.9-eclipse-temurin-21", strings.Replace(gitLabJavaSetup, `"java"`, `"ruby"`, 1), "", ""), "", "inconclusive", "No Datadog setup script selects java"},
		{"latest agent", gitLabJavaJob("maven:3.9-eclipse-temurin-21", strings.Replace(gitLabJavaSetup, `"1.67.1"`, `"latest"`, 1), "", ""), "", "inconclusive", "JAVA_TRACER_VERSION"},
		{"image without a JDK version", gitLabJavaJob("maven:3", gitLabJavaSetup, "", ""), "", "inconclusive", "image maven:3 selects no JDK version"},
		{"replaced MAVEN_OPTS", gitLabJavaJob("maven:3.9-eclipse-temurin-21", gitLabJavaSetup, "", "    - export MAVEN_OPTS=-Xmx1g\n"), "", "inconclusive", `replacing the -javaagent option the Datadog setup script added. Extend it instead, with export MAVEN_OPTS="$MAVEN_OPTS <options>"`},
		{"extended MAVEN_OPTS", gitLabJavaJob("maven:3.9-eclipse-temurin-21", gitLabJavaSetup, "", "    - export MAVEN_OPTS=\"$MAVEN_OPTS -Xmx1g\"\n"), "", "not checked", ""},
		{"options before the setup", gitLabJavaJob("maven:3.9-eclipse-temurin-21", gitLabJavaSetup, "    - export MAVEN_OPTS=-Xmx1g\n", ""), "", "not checked", ""},
		{"Gradle only", gitLabJavaJob("maven:3.9-eclipse-temurin-21", gitLabJavaSetup+` JAVA_INSTRUMENTED_BUILD_SYSTEM="gradle"`, "", ""), "", "inconclusive", `JAVA_INSTRUMENTED_BUILD_SYSTEM "gradle" does not instrument Maven`},
		{"Java 7 image", gitLabJavaJob("maven:3.6-jdk-7", gitLabJavaSetup, "", ""), "", "inconclusive", "older than Java 8"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := gitLabJavaRepository(t, tc.pipeline)
			ci := JavaCIConfiguration(root, "junit5", tc.site)
			require.Len(t, ci.Jobs, 1, ci.Reason)
			require.Equal(t, tc.status, ci.Jobs[0].Status, ci.Jobs[0].Reason)
			require.Contains(t, ci.Jobs[0].Reason, tc.reason)
			require.Equal(t, tc.status == "not checked", javaDiscovery{}.ciSetupConfigured(root, "junit5", tc.site))
		})
	}
}

// Local validation attaches its own agent: the replay keeps the job's own
// JVM options and drops the installer's -javaagent.
func TestJavaGitLabReplayKeepsTheJobsJVMOptions(t *testing.T) {
	root := gitLabJavaRepository(t, gitLabJavaJob("maven:3.9-eclipse-temurin-21", gitLabJavaSetup, "", "    - export MAVEN_OPTS=\"$MAVEN_OPTS -Xmx1g\"\n"))
	scope, err := DiscoverValidationScope(root, "junit5")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	require.Equal(t, "mvn -B verify", scope.Commands[0].Command)
	require.Equal(t, "-Xmx1g", scope.Commands[0].Environment["MAVEN_OPTS"])
	require.NotContains(t, scope.Commands[0].Environment["GRADLE_OPTS"], "javaagent")
}

func TestJavaGitLabInstructions(t *testing.T) {
	root := gitLabJavaRepository(t, "test:\n  image: maven:3.9-eclipse-temurin-21\n  script:\n    - mvn -B verify\n")
	text := providerSetupInstructions(root, gitLabCI{}, "java", "junit5", "", true)
	for _, phrase := range []string{
		`LANGUAGES="java"`,
		`JAVA_TRACER_VERSION="<exact dd-java-agent release>"`,
		"Replace <exact dd-java-agent release> with an exact dd-java-agent release",
		"adds -javaagent for later commands of the job to MAVEN_OPTS and GRADLE_OPTS",
		`export MAVEN_OPTS="$MAVEN_OPTS <options>"`,
		"ddtest reads the JDK of each instrumented job from its image",
		"Run resume_command once",
	} {
		require.Contains(t, text, phrase)
	}
}

// circleJavaConfig is a Gradle job in image with the Datadog orb step's
// parameters, then commands, then the tests.
func circleJavaConfig(image, parameters, commands string) string {
	return "version: 2.1\norbs:\n  test-optimization-circleci-orb: datadog/test-optimization-circleci-orb@2\njobs:\n  test:\n    docker:\n      - image: " + image + "\n    steps:\n      - checkout\n      - test-optimization-circleci-orb/autoinstrument:\n          languages: java\n" + parameters + commands + "      - run: ./gradlew test\n" + circleWorkflow("      - test\n")
}

// The Datadog CircleCI orb's installer instruments every Java process through
// JAVA_TOOL_OPTIONS unless its build system parameter names Maven or Gradle.
func TestJavaCircleCISetupCompleteness(t *testing.T) {
	tracer := "          java_tracer_version: 1.67.1\n"
	for _, tc := range []struct {
		name, config, status, reason string
	}{
		{"complete", circleJavaConfig("cimg/openjdk:17.0", tracer, ""), "not checked", "Validate dd-java-agent 1.67.1 on Java 17"},
		{"orb default agent", circleJavaConfig("cimg/openjdk:17.0", "", ""), "inconclusive", "sets no java_tracer_version"},
		{"replaced JAVA_TOOL_OPTIONS", circleJavaConfig("cimg/openjdk:17.0", tracer, "      - run: echo 'export JAVA_TOOL_OPTIONS=-Dfile.encoding=UTF-8' >> \"$BASH_ENV\"\n"), "inconclusive", "replacing the -javaagent option the Datadog Test Optimization orb added"},
		{"GRADLE_OPTS set with the orb default", circleJavaConfig("cimg/openjdk:17.0", tracer, "      - run: echo 'export GRADLE_OPTS=-Xmx1g' >> \"$BASH_ENV\"\n"), "not checked", ""},
		{"Gradle build system", circleJavaConfig("cimg/openjdk:17.0", tracer+"          java_instrumented_build_system: gradle\n", ""), "not checked", ""},
		{"Maven build system for Gradle tests", circleJavaConfig("cimg/openjdk:17.0", tracer+"          java_instrumented_build_system: maven\n", ""), "inconclusive", `java_instrumented_build_system "maven" does not instrument Gradle`},
		{"image without a JDK version", circleJavaConfig("cimg/base:2024.01", tracer, ""), "inconclusive", "image cimg/base:2024.01 selects no JDK version"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, root, "settings.gradle", "rootProject.name = 'demo'\n")
			writeFile(t, root, "build.gradle", "dependencies { testImplementation 'org.junit.jupiter:junit-jupiter:5.11.4' }\n")
			writeFile(t, root, "gradlew", "#!/bin/sh\n")
			writeFile(t, root, circleCIFile, tc.config)
			t.Chdir(root)
			useCIProvider(t, "circleci")
			ci := JavaCIConfiguration(root, "junit5", "")
			require.Len(t, ci.Jobs, 1, ci.Reason)
			require.Equal(t, tc.status, ci.Jobs[0].Status, ci.Jobs[0].Reason)
			require.Contains(t, ci.Jobs[0].Reason, tc.reason)
		})
	}
}

// On Buildkite the installer runs in the test step's own commands; the docker
// plugin's image selects the JDK, and an agent host's JDK stays unverified.
func TestJavaBuildkiteSetupCompleteness(t *testing.T) {
	snippet := buildkiteSnippetText("java", "1.67.1", "datadoghq.com", "      ")
	step := func(plugins string) string {
		return "steps:\n  - label: test\n    commands:\n" + snippet + "      - mvn -B verify\n" + plugins
	}
	docker := "    plugins:\n      - docker#v5.12.0:\n          image: maven:3.9-eclipse-temurin-17\n          environment:\n            - DD_API_KEY\n"
	for _, tc := range []struct {
		name, pipeline, status, reason string
	}{
		{"complete", step(docker), "not checked", "Validate dd-java-agent 1.67.1 on Java 17"},
		{"missing", strings.Replace(step(docker), snippet, "", 1), "inconclusive", "No Datadog setup snippet selects java"},
		{"agent host", step(""), "inconclusive", "run them in an image whose name selects a JDK version"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, root, "pom.xml", javaJUnit5POM)
			writeFile(t, root, buildkiteFile, tc.pipeline)
			t.Chdir(root)
			useCIProvider(t, "buildkite")
			ci := JavaCIConfiguration(root, "junit5", "")
			require.Len(t, ci.Jobs, 1, ci.Reason)
			require.Equal(t, tc.status, ci.Jobs[0].Status, ci.Jobs[0].Reason)
			require.Contains(t, ci.Jobs[0].Reason, tc.reason)
		})
	}
}

// A setup for every language exports Java's JVM options too; they do not
// reach another language's local replay.
func TestJavaSetupExportsStayOutOfOtherLanguagesReplays(t *testing.T) {
	require.True(t, otherLanguageSetupExport(rubyDiscovery{}, "MAVEN_OPTS"))
	require.False(t, otherLanguageSetupExport(javaDiscovery{}, "MAVEN_OPTS"))
	require.False(t, otherLanguageSetupExport(rubyDiscovery{}, "BUNDLE_GEMFILE"))
}
