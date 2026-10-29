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

// gradleFixture is a two-project Gradle build: app tests with JUnit 5, lib with TestNG.
func gradleFixture() map[string]string {
	return map[string]string{
		"settings.gradle.kts":  "rootProject.name = \"shop\"\ninclude(\":app\", \":lib\")\n",
		"gradlew":              "#!/bin/sh\n",
		"app/build.gradle.kts": "dependencies { testImplementation(\"org.junit.jupiter:junit-jupiter:5.11.4\") }\n",
		"lib/build.gradle":     "dependencies { testImplementation 'org.testng:testng:7.10.2' }\n",
	}
}

func TestJavaResolvesGradleTestSteps(t *testing.T) {
	root := javaWorkflowFixture(t, "", gradleFixture())
	for run, command := range map[string]string{
		"./gradlew test":                                 "./gradlew test",
		"./gradlew --no-daemon -Pci=true build":          "./gradlew --no-daemon -Pci=true build",
		"gradle :app:test --tests com.example.ApiTest":   "gradle :app:test --tests com.example.ApiTest",
		"./gradlew clean build jacocoTestReport publish": "./gradlew build",
	} {
		resolution := javaStep(t, root, run)
		require.True(t, resolution.Matched, "%s: %s", run, resolution.Reason)
		require.Len(t, resolution.Tests, 1)
		require.Equal(t, command, resolution.Tests[0].Command, run)
	}
	notes := strings.Join(javaStep(t, root, "./gradlew clean build jacocoTestReport publish").Tests[0].StepScopeNotes, "\n")
	require.Contains(t, notes, "also runs clean, jacocoTestReport, publish", "tasks outside testing are reported, and publishing never runs")
	require.Contains(t, notes, ":lib (testng)", "projects of other frameworks are reported")
}

func TestJavaGradleStepsOutsideOrNeedingReview(t *testing.T) {
	root := javaWorkflowFixture(t, "", gradleFixture())
	for run, reason := range map[string]string{
		"./gradlew assemble javadoc": "outside " + "junit5 test scope",
		"./gradlew build -x test":    "excludes the test task",
		"./gradlew --version":        "outside junit5 test scope",
	} {
		resolution := javaStep(t, root, run)
		require.True(t, resolution.Review, run)
		require.Contains(t, resolution.Reason, reason, run)
	}
	for run, reason := range map[string]string{
		"./gradlew -b ci.gradle test":      "changes the build Gradle runs",
		"./gradlew -I ci/init.gradle test": "changes the build Gradle runs",
		"./gradlew -p ../other test":       "leaves the repository",
	} {
		resolution := javaStep(t, root, run)
		require.False(t, resolution.Matched, run)
		require.Contains(t, resolution.Reason, reason, run)
	}
	files := gradleFixture()
	files["settings.gradle.kts"] = "file(\"modules\").listFiles().forEach { include(\":${it.name}\") }\n"
	dynamic := javaWorkflowFixture(t, "", files)
	require.Contains(t, javaStep(t, dynamic, "./gradlew test").Reason, "could not be read statically")
	files = gradleFixture()
	files["build.gradle"] = "defaultTasks 'test'\n"
	defaults := javaWorkflowFixture(t, "", files)
	require.Contains(t, javaStep(t, defaults, "./gradlew").Reason, "default tasks could not be read statically")
	require.True(t, javaStep(t, root, "./gradlew").Review, "without default tasks, Gradle runs help")
}

func TestJavaCIConfigurationChecksGradleInstrumentation(t *testing.T) {
	workflow := func(input, env string) string {
		return "jobs:\n  tests:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/setup-java@v4\n        with: {java-version: '21', distribution: temurin}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: java, api_key: '${{ secrets.DD_API_KEY }}', java-tracer-version: '1.67.1'" + input + "}\n      - run: ./gradlew test\n" + env
	}
	check := func(input, env string) RuntimeFinding {
		t.Helper()
		result := JavaCIConfiguration(javaWorkflowFixture(t, workflow(input, env), gradleFixture()), "junit5", "")
		require.Len(t, result.Jobs, 1)
		return result.Jobs[0]
	}
	for _, input := range []string{"", ", java-instrumented-build-system: gradle", ", java-instrumented-build-system: all"} {
		require.Equal(t, "not checked", check(input, "").Status, "the action instruments Gradle: %q", input)
	}
	finding := check(", java-instrumented-build-system: maven", "")
	require.Equal(t, "inconclusive", finding.Status)
	require.Contains(t, finding.Reason, "does not instrument Gradle")
	finding = check("", "        env: {GRADLE_OPTS: -Xmx2g}\n")
	require.Contains(t, finding.Reason, "sets GRADLE_OPTS, replacing the -javaagent option")
	require.Equal(t, "not checked", check("", "        env: {GRADLE_OPTS: '${{ env.GRADLE_OPTS }} -Xmx2g'}\n").Status, "a value that keeps the agent is fine")
	// The Maven variable doesn't carry the agent for a Gradle build.
	require.Equal(t, "not checked", check("", "        env: {MAVEN_OPTS: -Xmx2g}\n").Status)
}

func TestJavaCIConfigurationNamesTheBuildToolOfAStepUnderReview(t *testing.T) {
	workflow := "jobs:\n  tests:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/setup-java@v4\n        with: {java-version: '21', distribution: temurin}\n      - run: ./gradlew test\n        if: steps.cache.outputs.hit != 'true'\n"
	result := JavaCIConfiguration(javaWorkflowFixture(t, workflow, gradleFixture()), "junit5", "")
	require.Len(t, result.Jobs, 1)
	require.True(t, strings.HasPrefix(result.Jobs[0].Reason, "Gradle CI scope needs review: cannot statically resolve CI condition"), result.Jobs[0].Reason)
}
