// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/maven"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestJavaKarateProbeSelectsTheFeatureThroughTheRunner(t *testing.T) {
	for name, tc := range map[string]struct{ source, selection string }{
		// Every runner method would run the probe; the first is selected.
		"JUnit 5 runner":           {"package example;\nimport com.intuit.karate.junit5.Karate;\n@SpringBootTest(webEnvironment = WebEnvironment.DEFINED_PORT)\nclass CalcTest {\n    @Karate.Test\n    Karate calc() { return Karate.run(\"calc\").relativeTo(getClass()); }\n\n    @Karate.Test\n    Karate other() { return Karate.run(\"other\"); }\n}\n", "example.CalcTest#calc"},
		"qualified JUnit 5 runner": {"package example;\nclass CalcTest {\n    @com.intuit.karate.junit5.Karate.Test com.intuit.karate.junit5.Karate calc() { return null; }\n}\n", "example.CalcTest#calc"},
		"JUnit 4 runner":           {"package example;\n@RunWith(Karate.class)\n@KarateOptions(features = \"classpath:example\")\npublic class CalcTest {}\n", "example.CalcTest"},
		"parallel runner":          {"package example;\nclass CalcTest {\n    @Test(timeout = 60000)\n    public void all() { Runner.path(\"classpath:example\").tags(\"@smoke\").parallel(4); }\n}\n", "example.CalcTest#all"},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeJavaSource(t, filepath.Join(root, "api", "src", "test", "java", "example", "Calc.java"), "package example;\npublic class Calc {}\n")
			writeJavaSource(t, filepath.Join(root, "api", "src", "test", "java", "example", "CalcTest.java"), tc.source)
			probe, reason, err := javaProbeFor(root, "api", "karate", nil)
			require.NoError(t, err)
			require.Empty(t, reason)
			require.Equal(t, []javaProbeFile{{filepath.Join("api", javaKarateProbeFeature), javaKarateFeature}}, probe.files)
			require.Contains(t, probe.args, "-Dtest="+tc.selection)
			require.Equal(t, "example.CalcTest", probe.harness)
			require.Contains(t, probe.args, "-Dkarate.options=--tags @ddtest file:"+filepath.ToSlash(filepath.Join(root, "api", javaKarateProbeFeature)), "the tag replaces the runner's tags and the file need not be on the classpath")
		})
	}
	require.Contains(t, javaKarateFeature, "Feature: "+javaProbeClass)
	require.Contains(t, javaKarateFeature, "@ddtest\n  Scenario: ddtestProbe")
	require.Contains(t, javaKarateFeature, "ddtest intentional failure")
}

func TestJavaKarateProbeNeedsARunner(t *testing.T) {
	root := t.TempDir()
	writeJavaSource(t, filepath.Join(root, "src", "test", "java", "example", "CalcTest.java"), "package example;\npublic class CalcTest {}\n")
	// A helper that calls the Runner outside any test cannot be selected.
	writeJavaSource(t, filepath.Join(root, "src", "test", "java", "example", "AHelper.java"), "package example;\nclass AHelper {\n    static Results all() { return Runner.path(\"classpath:example\").parallel(4); }\n}\n")
	// A helper that only imports Karate's types runs no feature.
	writeJavaSource(t, filepath.Join(root, "src", "test", "java", "example", "Helpers.java"), "package example;\nimport com.intuit.karate.Runner;\nimport com.intuit.karate.junit5.Karate;\npublic class Helpers {}\n")
	_, reason, err := javaKarateProbe(root, ".")
	require.NoError(t, err)
	require.Contains(t, reason, "No Karate runner class")
	require.Contains(t, reason, "in the test sources of the root module")
	_, reason, err = javaKarateProbe(t.TempDir(), ".")
	require.NoError(t, err)
	require.Contains(t, reason, "No Karate runner class", "a module without test sources has no runner")
}

func TestJavaKarateProbeGoesToTheModuleWithTheRunner(t *testing.T) {
	root := t.TempDir()
	writeJavaSource(t, filepath.Join(root, "api", "src", "test", "java", "example", "ApiTest.java"), "package example;\npublic class ApiTest {}\n")
	writeJavaSource(t, filepath.Join(root, "e2e", "src", "test", "java", "example", "E2ETest.java"), "package example;\nimport com.intuit.karate.junit5.Karate;\nclass E2ETest {\n    @Karate.Test Karate all() { return Karate.run(\"classpath:example\"); }\n}\n")
	projects := []maven.Project{{Directory: "api", Artifact: "api"}, {Directory: "e2e", Artifact: "e2e"}}
	module, ok := javaRunnerModule(newMavenBuild(root, projects), []nativeTest{{File: "api / example.ApiTest"}, {File: "e2e / [example/calc] Calculator"}}, "karate")
	require.True(t, ok)
	require.Equal(t, "e2e", module)
	_, ok = javaRunnerModule(newMavenBuild(root, projects), []nativeTest{{File: "api / example.ApiTest"}}, "junit5")
	require.False(t, ok, "a class probe goes to any module that ran tests")
}

func TestWithoutSuiteLeavesTheHarnessOut(t *testing.T) {
	facts := intake.Facts{TestEventCount: 3,
		Tests: []intake.Test{
			{Suite: "example.KarateTest", Name: "all", Attempts: []intake.TestRun{{Status: "pass"}}},
			{Suite: "[ddtest/DdtestValidation] " + javaProbeClass, Name: "ddtestProbe", Attempts: []intake.TestRun{{Status: "skip"}, {Status: "skip"}}},
		},
		Events: []intake.Event{{Type: "test", Tags: map[string]string{"test.suite": "example.KarateTest"}}, {Type: "test", Tags: map[string]string{"test.suite": "[ddtest/DdtestValidation] " + javaProbeClass}}},
	}
	left := withoutSuite(facts, "example.KarateTest")
	require.Equal(t, 2, left.TestEventCount, "skipping counts the probe's events against every test event")
	require.Len(t, left.Tests, 1)
	require.Len(t, left.Events, 1)
	require.Equal(t, "ddtestProbe", left.Tests[0].Name)
	require.Len(t, facts.Tests, 2, "the run's own facts are not changed")
}
