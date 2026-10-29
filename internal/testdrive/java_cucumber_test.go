// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestJavaCucumberProbeUsesTheRunnerAndItsGlue(t *testing.T) {
	for _, tc := range []struct {
		name, runner, properties, glue string
		suite                          bool
	}{
		{"junit4 runner", "package com.acme;\nimport io.cucumber.junit.Cucumber;\n@RunWith(Cucumber.class)\n@CucumberOptions(features = \"classpath:features\", glue = {\"com.acme.steps\", \"com.acme.hooks\"})\npublic class RunCucumberTest {}\n", "", "com.acme.steps", false},
		{"qualified junit4 runner", "package com.acme;\n@org.junit.runner.RunWith(io.cucumber.junit.Cucumber.class)\npublic class RunCucumberTest {}\n", "", "com.acme", false},
		{"platform suite", "package com.acme;\n@Suite\n@IncludeEngines(\"cucumber\")\n@ConfigurationParameter(key = GLUE_PROPERTY_NAME, value = \"classpath:com/acme/glue\")\npublic class RunCucumberTest {}\n", "", "com.acme.glue", true},
		{"qualified suite key", "package com.acme;\n@org.junit.platform.suite.api.Suite\n@org.junit.platform.suite.api.IncludeEngines(\"cucumber\")\n@ConfigurationParameter(key = io.cucumber.junit.platform.engine.Constants.GLUE_PROPERTY_NAME, value = \"com.acme.qualified\")\npublic class RunCucumberTest {}\n", "", "com.acme.qualified", true},
		{"platform properties", "package com.acme;\n@Suite\n@IncludeEngines(\"cucumber\")\npublic class RunCucumberTest {}\n", "cucumber.glue = com.acme.props\n", "com.acme.props", true},
		{"value before key", "package com.acme;\n@Suite\n@IncludeEngines(\"cucumber\")\n@ConfigurationParameter(value = \"com.acme.reversed\", key = GLUE_PROPERTY_NAME)\npublic class RunCucumberTest {}\n", "", "com.acme.reversed", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeJavaSource(t, filepath.Join(root, "service", "src", "test", "java", "com", "acme", "RunCucumberTest.java"), tc.runner)
			writeJavaSource(t, filepath.Join(root, "service", "src", "test", "java", "com", "acme", "CalcTest.java"), "package com.acme;\npublic class CalcTest {}\n")
			if tc.properties != "" {
				writeJavaSource(t, filepath.Join(root, "service", "src", "test", "resources", "junit-platform.properties"), tc.properties)
			}
			probe, reason, err := javaCucumberProbe(root, "service", nil)
			require.NoError(t, err)
			require.Empty(t, reason)
			require.Equal(t, filepath.Join("service", "src", "test", "resources", "ddtest", "DdtestValidation.feature"), probe.files[0].path)
			require.Contains(t, probe.files[0].source, "Feature: "+javaProbeClass, "Surefire reports the feature name as the probe's class")
			require.Equal(t, filepath.Join("service", "src", "test", "java", filepath.FromSlash(strings.ReplaceAll(tc.glue, ".", "/")), "DdtestValidationSteps.java"), probe.files[1].path)
			require.Contains(t, probe.files[1].source, "package "+tc.glue+";", "the step joins the project's glue, hooks and object factory")
			require.Contains(t, probe.args, "-Dtest=com.acme.RunCucumberTest")
			require.Contains(t, probe.args, "-Dcucumber.features=classpath:ddtest/DdtestValidation.feature")
			require.Equal(t, tc.suite, slices.Contains(probe.args, "-Dsurefire.excludeJUnit5Engines=cucumber"), "only a suite also runs the probe through the root engine")
		})
	}
	root := t.TempDir()
	writeJavaSource(t, filepath.Join(root, "src", "test", "java", "CalcTest.java"), "public class CalcTest {}\n")
	_, reason, err := javaCucumberProbe(root, ".", nil)
	require.NoError(t, err)
	require.Contains(t, reason, "No Cucumber runner class", "without a runner the probe cannot be selected alone")
	_, reason, err = javaCucumberProbe(t.TempDir(), ".", nil)
	require.NoError(t, err)
	require.NotEmpty(t, reason, "a module without test sources has no runner")
	require.NoError(t, os.RemoveAll(root))
}

// writeJavaSource writes a file, creating its directories.
func writeJavaSource(t *testing.T, path, contents string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	requireWriteFile(t, path, contents)
}

func TestJavaTelemetryMatchesCucumberFeaturesByName(t *testing.T) {
	native := []nativeTest{{File: "service / Calculator", Name: "adds", Status: "passed"}}
	for _, suite := range []string{"classpath:features/calc.feature:Calculator", "file:src/test/resources/features/calc.feature:Calculator"} {
		run := javaTelemetryRun(native, intake.Test{Module: "service maven-surefire-plugin default-test", Suite: suite, Name: "adds", Attempts: []intake.TestRun{{Status: "pass", SpanID: 1}}})
		require.Empty(t, verifyJavaTelemetry(run).Status, "the agent puts the feature file before its name: %s", suite)
	}
	run := javaTelemetryRun(native, intake.Test{Module: "service", Suite: "classpath:features/other.feature:Other", Name: "adds", Attempts: []intake.TestRun{{Status: "pass", SpanID: 1}}})
	require.Equal(t, "inconclusive", verifyJavaTelemetry(run).Status, "another feature does not match")
}

func TestJavaProbeControlsMatchTelemetryWithoutASourceFile(t *testing.T) {
	name := "ddtestProbe"
	run := validationRun{framework: "cucumber-java", ProbeExecutions: new(int), Tests: []nativeTest{{File: "service / " + javaProbeClass, Name: "ddtestProbe", Status: "passed", probe: true, telemetryName: &name}},
		Facts: intake.Facts{TestEventCount: 1, Tests: []intake.Test{{Module: "service", Suite: "classpath:ddtest/DdtestValidation.feature:" + javaProbeClass, Name: "ddtestProbe", Attempts: []intake.TestRun{{Status: "pass", SpanID: 1}}}}}}
	*run.ProbeExecutions = 1
	require.True(t, validProbeControl(run, "passed"), "Cucumber reports no source file; the probe is matched by module, class and name")
	require.True(t, isSkippedProbeTest(intake.Event{Type: "test", Tags: map[string]string{"test.module": "service", "test.suite": "s", "test.name": "n", "test.status": "skip", "test.skipped_by_itr": "true"}}, intake.Test{Module: "service", Suite: "s", Name: "n"}, true), "test-level skipping needs no source file")
	require.False(t, isSkippedProbeTest(intake.Event{Type: "test", Tags: map[string]string{"test.module": "service", "test.suite": "s", "test.name": "n", "test.status": "skip", "test.skipped_by_itr": "true"}}, intake.Test{Module: "service", Suite: "s", Name: "n"}, false))
}

func TestJavaCucumberStepsMatchTheGlueBackendAndPackage(t *testing.T) {
	annotated := javaCucumberSteps("com.acme", false)
	require.True(t, strings.HasPrefix(annotated, "package com.acme;"))
	require.Contains(t, annotated, "@io.cucumber.java.en.When(\"the ddtest validation probe runs\")")
	require.False(t, strings.HasPrefix(javaCucumberSteps("", false), "package"), "default-package glue has no package declaration")
	lambda := javaCucumberSteps("com.acme", true)
	require.Contains(t, lambda, "implements io.cucumber.java8.En")
	require.Contains(t, lambda, "When(\"the ddtest validation probe runs\", () -> {")
	require.NotContains(t, lambda, "io.cucumber.java.en", "the lambda backend has no step annotations")
	require.Contains(t, lambda, "ddtest intentional failure")

	root := t.TempDir()
	pom := func(dependencies string) {
		writeJavaSource(t, filepath.Join(root, "service", "pom.xml"), "<project><artifactId>service</artifactId><dependencies>"+dependencies+"</dependencies></project>")
	}
	java8 := "<dependency><groupId>io.cucumber</groupId><artifactId>cucumber-java8</artifactId></dependency>"
	annotations := "<dependency><groupId>io.cucumber</groupId><artifactId>cucumber-java</artifactId></dependency>"
	pom(java8)
	require.True(t, javaCucumberLambda(root, "service", backendUse{}), "only the lambda backend")
	pom(java8 + annotations)
	require.False(t, javaCucumberLambda(root, "service", backendUse{}), "annotations are available")
	pom(annotations)
	require.False(t, javaCucumberLambda(root, "service", backendUse{}))
}

func TestJavaCucumberProbeHonorsTheCommandGlueAndInheritedBackends(t *testing.T) {
	root := t.TempDir()
	writeJavaSource(t, filepath.Join(root, "src", "test", "java", "example", "runner", "RunCucumberTest.java"), "package example.runner;\n@RunWith(Cucumber.class)\npublic class RunCucumberTest {}\n")
	// The step definitions use the lambda backend, which the module inherits
	// from its parent: its own POM declares no Cucumber dependency.
	writeJavaSource(t, filepath.Join(root, "src", "test", "java", "example", "steps", "Steps.java"), "package example.steps;\npublic class Steps implements io.cucumber.java8.En {}\n")
	writeJavaSource(t, filepath.Join(root, "pom.xml"), "<project><parent><artifactId>parent</artifactId></parent><artifactId>service</artifactId></project>")
	probe, reason, err := javaCucumberProbe(root, ".", []string{"-B", "-Dcucumber.glue=example.steps", "test"})
	require.NoError(t, err)
	require.Empty(t, reason)
	require.Equal(t, filepath.Join("src", "test", "java", "example", "steps", "DdtestValidationSteps.java"), probe.files[1].path, "the command's glue wins over the runner's package")
	require.Contains(t, probe.files[1].source, "implements io.cucumber.java8.En", "the glue sources show the lambda backend")
	// Annotated glue elsewhere keeps the annotated probe.
	writeJavaSource(t, filepath.Join(root, "src", "test", "java", "example", "steps", "More.java"), "package example.steps;\nimport io.cucumber.java.en.When;\npublic class More {}\n")
	probe, _, err = javaCucumberProbe(root, ".", nil)
	require.NoError(t, err)
	require.Contains(t, probe.files[1].source, "@io.cucumber.java.en.When")
}
