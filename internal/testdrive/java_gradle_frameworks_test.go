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

func TestProbePropertiesReachEveryJVM(t *testing.T) {
	drive := &Testdrive{}
	t.Setenv("JAVA_TOOL_OPTIONS", "-Dfile.encoding=UTF-8")
	env := map[string]string{}
	drive.addJavaToolOptions(env, nil)
	require.NotContains(t, env, "JAVA_TOOL_OPTIONS", "a probe without properties leaves the variable alone")
	drive.addJavaToolOptions(env, []string{"-Dcucumber.features=classpath:ddtest/DdtestValidation.feature", "-Dkarate.options=--tags @ddtest file:/w/a.feature"})
	require.Equal(t, `-Dfile.encoding=UTF-8 -Dcucumber.features=classpath:ddtest/DdtestValidation.feature "-Dkarate.options=--tags @ddtest file:/w/a.feature"`, env["JAVA_TOOL_OPTIONS"], "the inherited options stay; a property with spaces is quoted, as the JVM reads the variable")
	drive.configurationEnvironment = map[string]string{"JAVA_TOOL_OPTIONS": "-Xss1m"}
	env = map[string]string{}
	drive.addJavaToolOptions(env, []string{"-Da=b"})
	require.Equal(t, "-Xss1m -Da=b", env["JAVA_TOOL_OPTIONS"], "a configuration's own options come first")
	env = map[string]string{"JAVA_TOOL_OPTIONS": "-Dset=by-run"}
	drive.addJavaToolOptions(env, []string{"-Da=b"})
	require.Equal(t, "-Dset=by-run -Da=b", env["JAVA_TOOL_OPTIONS"], "options the run already set are extended")
}

func TestGradleNativeResultsOfRunnerProbes(t *testing.T) {
	results := []javaResult{
		{module: ":test", Test: maven.Test{Suite: "example.KarateTest", Name: "[1:4] ddtestProbe", Status: "passed"}},
		{module: ":test", Test: maven.Test{Suite: "example.KarateTest", Name: "[2:9] adds", Status: "passed"}},
	}
	tests := javaNativeTests(results, true)
	require.Equal(t, "ddtestProbe", tests[0].Name, "Karate's [index:line] is not part of the scenario's name")
	require.True(t, tests[0].probe, "Gradle files the probe scenario under the runner, so its name identifies it")
	require.Equal(t, "ddtestProbe", *tests[0].telemetryName)
	require.False(t, tests[1].probe)
	require.Equal(t, "adds", tests[1].Name)
	require.False(t, javaNativeTests(results, false)[0].probe, "only a probe run has a probe")

	// The runner is no test of its own, but Gradle files the probe under it;
	// scenarios a tag filter excluded never ran.
	probe := javaProbe{harness: "example.KarateTest"}
	kept, facts := probe.selectedResults(append(results, javaResult{module: ":test", Test: maven.Test{Suite: "example.KarateTest", Name: "testParallel()", Status: "passed"}}), intake.Facts{})
	require.Len(t, kept, 1)
	require.Equal(t, "[1:4] ddtestProbe", kept[0].Name)
	require.Empty(t, facts.Tests)
	filtered := javaProbe{filtered: true}
	reportedTests := []intake.Test{
		{Suite: "ddtest.DdtestValidationTest", Name: "ddtestProbe", Status: "skip", Attempts: []intake.TestRun{{Status: "skip"}}},
		{Suite: "Calculator", Name: "adding", Status: "skip", Attempts: []intake.TestRun{{Status: "skip"}}},
	}
	kept, facts = filtered.selectedResults([]javaResult{
		{module: ":test", Test: maven.Test{Suite: "example.RunCucumberTest", Name: "ddtestProbe", Status: "skipped"}},
		{module: ":test", Test: maven.Test{Suite: "example.RunCucumberTest", Name: "adding", Status: "skipped"}},
		{module: ":test", Test: maven.Test{Suite: "example.RunCucumberTest", Name: "adding more", Status: "passed"}},
	}, intake.Facts{TestEventCount: 2, Tests: reportedTests})
	require.Equal(t, []string{"ddtestProbe", "adding more"}, []string{kept[0].Name, kept[1].Name}, "the probe is kept even when skipped; a test that ran is never dropped")
	require.Equal(t, 1, facts.TestEventCount)
	require.Equal(t, "ddtestProbe", facts.Tests[0].Name)
}

func TestProbeWhoseBodyNeverRanDidNotPass(t *testing.T) {
	tests := []nativeTest{{Name: "ddtestProbe", Status: "passed", probe: true}, {Name: "other", Status: "passed"}}
	withUnexecutedProbe(tests, 1)
	require.Equal(t, "passed", tests[0].Status, "a body that ran passed")
	withUnexecutedProbe(tests, 0)
	require.Equal(t, "skipped", tests[0].Status, "Gradle reports a Karate scenario the tracer skipped as passed")
	require.Equal(t, "passed", tests[1].Status, "only the probe's own record speaks for it")
}

func TestJavaTelemetryMatchesClassesGradleReportsInPlaceOfFeatures(t *testing.T) {
	pass := func(span uint64) intake.TestRun { return intake.TestRun{Status: "pass", SpanID: span} }
	// Gradle names a Cucumber suite's scenarios after the suite class, and a
	// Karate runner's after the runner; dd-java-agent reports each feature.
	native := []nativeTest{
		{File: ":test / example.RunCucumberTest", Name: "adding", Status: "passed"},
		{File: ":test / example.RunCucumberTest", Name: "adding more", Status: "passed"},
		{File: ":test / example.CalcTest", Name: "works()", Status: "passed"},
	}
	run := javaTelemetryRun(native,
		reported(":test", "classpath:example/calc.feature:Calculator", "adding", "", pass(1)),
		reported(":test", "classpath:example/calc.feature:Calculator", "adding more", "", pass(2)),
		reported(":test", "example.CalcTest", "works", "", pass(3)))
	require.Equal(t, verdict{}, verifyJavaTelemetry(run))
	for name, telemetry := range map[string][]intake.Test{
		"a scenario the tracer did not report": {reported(":test", "Calculator", "adding", "", pass(1)), reported(":test", "example.CalcTest", "works", "", pass(3))},
		"a scenario of another name":           {reported(":test", "Calculator", "adding", "", pass(1)), reported(":test", "Calculator", "subtracting", "", pass(2)), reported(":test", "example.CalcTest", "works", "", pass(3))},
		"another status":                       {reported(":test", "Calculator", "adding", "", pass(1)), reported(":test", "Calculator", "adding more", "", intake.TestRun{Status: "fail", SpanID: 2}), reported(":test", "example.CalcTest", "works", "", pass(3))},
		"another module":                       {reported(":test", "Calculator", "adding", "", pass(1)), reported(":it", "Calculator", "adding more", "", pass(2)), reported(":test", "example.CalcTest", "works", "", pass(3))},
	} {
		require.Equal(t, "inconclusive", verifyJavaTelemetry(javaTelemetryRun(native, telemetry...)).Status, name)
	}
}

func TestMavenRunsUseTheUserHomeValidationReads(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "pom.xml"), `<project><artifactId>fixture</artifactId></project>`)
	options := func(opts, mavenArgs string, args []string, env map[string]string) string {
		t.Helper()
		t.Setenv("MAVEN_OPTS", opts)
		t.Setenv("MAVEN_ARGS", mavenArgs)
		drive := &Testdrive{repositoryRoot: root, configurationRoot: root, command: "mvn", args: args}
		build, err := drive.javaBuild(args)
		require.NoError(t, err)
		build.environment(env)
		return env["MAVEN_OPTS"]
	}
	require.Equal(t, "-Xmx1g -Duser.home="+home, options("-Xmx1g", "", []string{"verify"}, map[string]string{}), "Maven reads ~/.m2 from the home whose files validation reads, not the JVM's user.home")
	require.Equal(t, "-Xmx1g -javaagent:/a.jar -Duser.home="+home, options("-Xmx1g", "", []string{"verify"}, map[string]string{"MAVEN_OPTS": "-Xmx1g -javaagent:/a.jar"}), "the options the run set, as the agent, are extended")
	for _, tc := range []struct {
		name, opts, mavenArgs string
		args                  []string
	}{
		{"MAVEN_OPTS sets the repository", "-Dmaven.repo.local=/cache/m2", "", []string{"verify"}},
		{"MAVEN_ARGS sets the home", "", "-Duser.home=/ci", []string{"verify"}},
		{"the command sets the home", "", "", []string{"verify", "-Duser.home=/ci"}},
	} {
		require.NotContains(t, options(tc.opts, tc.mavenArgs, tc.args, map[string]string{}), "-Duser.home="+home, tc.name)
	}
}
