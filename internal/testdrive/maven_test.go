package testdrive

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/maven"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestCompareMavenRequiresNativeAndTelemetryAgreement(t *testing.T) {
	baseline := validationRun{ExitCode: 1, Tests: []jestTest{{File: ". / example.Test", Name: "fails()", Status: "failed", Failure: "AssertionError: expected"}}}
	reporting := baseline
	reporting.mavenModules = map[string]string{"": "."}
	reporting.Facts = intake.Facts{Tests: []intake.Test{{Suite: "example.Test", Name: "fails", Attempts: []intake.TestRun{{Status: "fail"}}}}, TestEventCount: 1}
	require.Equal(t, "compatible", compareMaven(baseline, reporting).Status)
	reporting.Tests = []jestTest{{File: ". / example.Test", Name: "other", Status: "failed"}}
	require.Equal(t, "suspected regression", compareMaven(baseline, reporting).Status)
	reporting = baseline
	require.Equal(t, "inconclusive", compareMaven(baseline, reporting).Status)
	baseline.ResultError = "no native reports"
	require.Equal(t, "inconclusive", compareMaven(baseline, reporting).Status)
}

func TestMavenTelemetryDoesNotHideDuplicatesOrRetries(t *testing.T) {
	run := validationRun{mavenModules: map[string]string{"": "."}, Tests: []jestTest{{File: ". / A", Name: "test", Status: "passed"}, {File: ". / A", Name: "test", Status: "passed"}}, Facts: intake.Facts{Tests: []intake.Test{{Suite: "A", Name: "test", Attempts: []intake.TestRun{{Status: "pass"}}}}}}
	require.Equal(t, "inconclusive", verifyMavenTelemetry(run).Status)
	run.Facts.Tests[0].Attempts = append(run.Facts.Tests[0].Attempts, intake.TestRun{Status: "pass"})
	require.Equal(t, "compatible", verifyMavenTelemetry(run).Status)
	run.Facts.Tests[0].Attempts[1].Retry = true
	require.Equal(t, "inconclusive", verifyMavenTelemetry(run).Status)
}

func TestMavenTelemetryCannotSwapReactorModules(t *testing.T) {
	run := validationRun{mavenModules: map[string]string{"one": "one", "two": "two"}, Tests: []jestTest{{File: "one / SharedTest", Name: "test", Status: "passed"}, {File: "two / SharedTest", Name: "test", Status: "failed"}}, Facts: intake.Facts{Tests: []intake.Test{
		{Module: "one maven-surefire-plugin default-test", Suite: "SharedTest", Name: "test", Attempts: []intake.TestRun{{Status: "fail"}}},
		{Module: "two maven-surefire-plugin default-test", Suite: "SharedTest", Name: "test", Attempts: []intake.TestRun{{Status: "pass"}}},
	}}}
	require.Equal(t, "inconclusive", verifyMavenTelemetry(run).Status)
}

func TestMavenTelemetryUsesProjectDisplayNames(t *testing.T) {
	for _, name := range []string{"JavaPoet", "jsoup Java HTML Parser"} {
		run := validationRun{mavenModules: mavenModuleDirectories([]maven.Project{{Name: name, Artifact: "artifact", Directory: "."}}), Tests: []jestTest{{File: ". / ExampleTest", Name: "test", Status: "passed"}}, Facts: intake.Facts{Tests: []intake.Test{
			{Module: name + " maven-surefire-plugin default-test", Suite: "ExampleTest", Name: "test", Attempts: []intake.TestRun{{Status: "pass"}}},
		}}}
		require.Equal(t, "compatible", verifyMavenTelemetry(run).Status, name)
		run.mavenModules = mavenModuleDirectories([]maven.Project{{Name: name, Artifact: "artifact", Directory: "."}, {Name: name, Artifact: "other", Directory: "child"}})
		require.Equal(t, "inconclusive", verifyMavenTelemetry(run).Status, "duplicate names must not conceal module ambiguity")
	}
}

func TestMavenTelemetryReconcilesJUnit5ParameterizedInvocations(t *testing.T) {
	run := validationRun{mavenModules: map[string]string{"module": "."}, Tests: []jestTest{
		{File: ". / ExampleTest", Name: "parameters(String, String)[1]", Status: "passed"},
		{File: ". / ExampleTest", Name: "parameters(String, String)[2]", Status: "passed"},
	}, Facts: intake.Facts{Tests: []intake.Test{
		{Module: "module", Suite: "ExampleTest", Name: "parameters", Parameters: `{"metadata":{"test_name":"[1] first"}}`, Attempts: []intake.TestRun{{Status: "pass"}}},
		{Module: "module", Suite: "ExampleTest", Name: "parameters", Parameters: `{"metadata":{"test_name":"[2] second"}}`, Attempts: []intake.TestRun{{Status: "pass"}}},
	}}}
	require.Equal(t, "compatible", verifyMavenTelemetry(run).Status)
	run.Facts.Tests[1].Parameters = run.Facts.Tests[0].Parameters
	require.Equal(t, "inconclusive", verifyMavenTelemetry(run).Status, "equal total counts cannot hide a missing invocation")
	run.Facts.Tests[1].Parameters = `{"metadata":{"test_name":"custom name without an index"}}`
	require.Equal(t, "inconclusive", verifyMavenTelemetry(run).Status, "ambiguous display names require review")
}

func TestMavenProbePreservesCustomerFiles(t *testing.T) {
	root := t.TempDir()
	for _, name := range maven.FrameworkNames {
		project := maven.Project{Directory: name, Dependencies: []maven.Dependency{{Group: "io.cucumber", Artifact: "cucumber-junit"}}}
		probe := newMavenProbe(project, name)
		require.NotEmpty(t, probe.files, name)
		require.NoError(t, probe.write(root))
		for path := range probe.files {
			require.FileExists(t, filepath.Join(root, path))
		}
		require.Error(t, probe.write(root), "must never overwrite existing files")
	}
}

func TestMavenFeatureRequiresNativeCounterAndFeatureEvidence(t *testing.T) {
	identity := intake.Test{Module: "m", Suite: "S", Name: "probe"}
	count := 2
	run := validationRun{Tests: []jestTest{{Name: "probe", Status: "passed"}}, ProbeExecutions: &count, Facts: intake.Facts{Events: []intake.Event{
		{Type: "test", Tags: map[string]string{"test.module": "m", "test.suite": "S", "test.name": "probe", "test.status": "fail"}},
		{Type: "test", Tags: map[string]string{"test.module": "m", "test.suite": "S", "test.name": "probe", "test.status": "pass", "test.is_retry": "true"}},
	}}}
	require.Equal(t, "passed", evaluateMavenFeature("auto-retries", run, identity).Status)
	count = 1
	require.Equal(t, "failed", evaluateMavenFeature("auto-retries", run, identity).Status)
	count = 2
	run.ExitCode = 1
	require.Equal(t, "failed", evaluateMavenFeature("auto-retries", run, identity).Status)
	run.ExitCode = 0
	run.ResultError = "broken XML"
	require.Equal(t, "inconclusive", evaluateMavenFeature("auto-retries", run, identity).Status)
}

func TestMavenJVMEnvironmentPreservesOtherOptions(t *testing.T) {
	t.Setenv("MAVEN_OPTS", "-Xmx512m -javaagent:/customer/jacoco.jar")
	for _, key := range []string{"JAVA_TOOL_OPTIONS", "JDK_JAVA_OPTIONS", "_JAVA_OPTIONS"} {
		t.Setenv(key, "")
	}
	env, err := mavenJVMEnvironment("/tmp/dd-java-agent.jar")
	require.NoError(t, err)
	require.Equal(t, "-Xmx512m -javaagent:/customer/jacoco.jar -javaagent:/tmp/dd-java-agent.jar", env["MAVEN_OPTS"])
	require.Equal(t, "-Xmx512m -javaagent:/customer/jacoco.jar", os.Getenv("MAVEN_OPTS"))
	env, err = mavenJVMEnvironment("/tmp/a directory/dd-java-agent.jar")
	require.NoError(t, err)
	require.Contains(t, env["JAVA_TOOL_OPTIONS"], `"-javaagent:/tmp/a directory/dd-java-agent.jar"`)
	t.Setenv("MAVEN_OPTS", `"unfinished`)
	_, err = mavenJVMEnvironment("")
	require.Error(t, err)
}

func TestMavenCheckOnlyRetainsHistoricalPair(t *testing.T) {
	root := t.TempDir()
	previous := validationResult{Framework: "junit5", Session: "executed", Compatibility: verdict{Status: "compatible"}, Runs: []runSummary{
		(validationRun{Name: "baseline", Command: "mvn test"}).summary(),
		(validationRun{Name: "reporting-only", Command: "mvn test", Instrumented: true}).summary(),
	}}
	require.Error(t, finishValidation(&bytes.Buffer{}, root, previous))
	require.Error(t, finishValidation(&bytes.Buffer{}, root, validationResult{Framework: "junit5", Session: "check", CheckOnly: true}))
	data, err := os.ReadFile(validationPath(root))
	require.NoError(t, err)
	var result validationResult
	require.NoError(t, json.Unmarshal(data, &result))
	require.NotNil(t, result.Retained)
	require.NotNil(t, result.LastExecution)
	require.False(t, result.LastExecution.Current)
	require.Equal(t, "executed", result.LastExecution.Session)
	require.Len(t, result.Retained.Result.Runs, 2)
	require.False(t, result.Success)
}
