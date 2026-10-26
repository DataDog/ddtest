package testdrive

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/framework"

	"github.com/DataDog/ddtest/internal/maven"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestMavenPreservesConfigurationOnlyValidation(t *testing.T) {
	previous := *settings.Get()
	t.Cleanup(func() { *settings.Get() = previous })
	settings.Get().Platform, settings.Get().Command = "java", ""
	t.Setenv("DD_TRACER_FOLDER", "")
	fixtures, err := filepath.Abs("testdata/maven")
	require.NoError(t, err)
	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "java"), []byte("#!/bin/sh\nexit 0\n"), 0700))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, name := range maven.FrameworkNames {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			pom, err := os.ReadFile(filepath.Join(fixtures, name, "pom.xml"))
			require.NoError(t, err)
			require.NoError(t, os.WriteFile("pom.xml", pom, 0600))
			settings.Get().Framework = name
			drive, err := Prepare(PrepareOptions{TracerVersion: "1.67.1", CheckOnly: true})
			require.NoError(t, err)
			var output bytes.Buffer
			require.Error(t, drive.Run(t.Context(), &output))
			require.Contains(t, output.String(), "Maven module")
			data, err := os.ReadFile(validationPath(root))
			require.NoError(t, err)
			var result validationResult
			require.NoError(t, json.Unmarshal(data, &result))
			require.Equal(t, name, result.Framework)
			require.True(t, result.CheckOnly)
			require.False(t, result.Success)
			require.Empty(t, result.Runs)
			require.NotNil(t, result.Cleanup)
			require.Equal(t, "passed", result.Cleanup.Status)
			require.NoDirExists(t, drive.session.Directory())
			require.NoDirExists(t, filepath.Join(root, "target"))
		})
	}
}

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

func TestMavenProbeConfinesSymlinkedParents(t *testing.T) {
	for _, parent := range []string{"src", "src/test", "src/test/java", "src/test/java/ddtest"} {
		t.Run(parent, func(t *testing.T) {
			root, shared := t.TempDir(), t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(root, filepath.Dir(parent)), 0700))
			require.NoError(t, os.Symlink(shared, filepath.Join(root, parent)))
			probe := newMavenProbe(maven.Project{Directory: "."}, "junit5")
			require.Error(t, probe.write(root))
			entries, err := os.ReadDir(shared)
			require.NoError(t, err)
			require.Empty(t, entries, "probe must not create shared directories or files")
		})
	}
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sources"), 0700))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "src/test"), 0700))
	require.NoError(t, os.Symlink("../../sources", filepath.Join(root, "src/test/java")))
	require.NoError(t, newMavenProbe(maven.Project{Directory: "."}, "junit5").write(root))
	require.FileExists(t, filepath.Join(root, "sources/ddtest/DdtestValidationTest.java"))
}

func TestMavenProbeSelectsOnlySurefire(t *testing.T) {
	probe := newMavenProbe(maven.Project{Directory: "."}, "junit5")
	require.Contains(t, probe.args, "-Dtest=ddtest.DdtestValidationTest")
	require.Contains(t, probe.args, "-DskipITs=true", "verify must not run the probe again through Failsafe")
	require.NotContains(t, probe.args, "-Dit.test=ddtest.DdtestValidationTest")
}

func TestMavenExecutedUnresolvedModuleCannotInheritPassingFeatures(t *testing.T) {
	drive := &Testdrive{framework: framework.NewMaven("junit5", nil)}
	result := validationResult{Framework: "junit5"}
	for _, policy := range featurePolicies("junit5") {
		result.Features = append(result.Features, featureResult{Project: "direct", Name: policy.Name, Status: "passed"})
	}
	projects := []maven.Project{{Directory: "remote"}, {Directory: "not-executed"}}
	reporting := validationRun{Tests: []jestTest{{File: "direct / DirectTest"}, {File: "remote / RemoteTest"}}}
	require.NoError(t, drive.mavenFeatures(t.Context(), io.Discard, "", projects, reporting, &result))
	remote := 0
	for _, feature := range result.Features {
		require.NotEqual(t, "not-executed", feature.Project)
		if feature.Project == "remote" {
			remote++
			require.Equal(t, "inconclusive", feature.Status)
		}
	}
	require.Equal(t, 6, remote, "every executed unresolved module needs its own inconclusive controls")
	require.False(t, featureChecksPassed("junit5", result.Features))
}

func TestMavenRunPreservesResourcesNamedTarget(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "pom.xml"), []byte(`<project><modules><module>child</module></modules></project>`), 0600))
	for _, directory := range []string{"target", "child/target", "src/test/resources/target", "src/test/resources/fixture/target"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, directory), 0700))
		require.NoError(t, os.WriteFile(filepath.Join(root, directory, "sentinel"), []byte("original"), 0600))
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "child/pom.xml"), []byte(`<project><artifactId>child</artifactId></project>`), 0600))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "src/test/resources/target/surefire-reports"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "src/test/resources/target/surefire-reports/TEST-saved.xml"), []byte("<fixture-not-a-live-report"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "mvnw"), []byte(`#!/bin/sh
set -eu
test ! -e target/sentinel
test ! -e child/target/sentinel
test -f src/test/resources/target/sentinel
test -f src/test/resources/fixture/target/sentinel
mkdir -p target/surefire-reports
printf '%s' '<testsuite><testcase classname="ResourceTest" name="resources"/></testsuite>' > target/surefire-reports/TEST-ResourceTest.xml
`), 0700))
	session, err := NewSession()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, session.Close()) })
	drive := &Testdrive{repositoryRoot: root, framework: framework.NewMaven("junit5", nil), command: "./mvnw", args: []string{"test"}, session: session,
		startIntake: func(string, intake.Scenario) (localIntake, error) { return &fakeIntake{url: "http://127.0.0.1:1"}, nil },
	}
	run, err := drive.mavenRun(t.Context(), io.Discard, "baseline", "", intake.Scenario{}, "", nil)
	require.NoError(t, err)
	require.Zero(t, run.ExitCode, run.commandOutput)
	require.Empty(t, run.ResultError)
	require.Len(t, run.Tests, 1)
	require.FileExists(t, filepath.Join(root, "target/sentinel"), "original build output must also stay untouched")
}

func TestMavenRunNormalizesOnlyOwnedWorkspacePaths(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "pom.xml"), []byte(`<project><artifactId>suite</artifactId></project>`), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "mvnw"), []byte(`#!/bin/sh
mkdir -p target/surefire-reports
printf '<testsuite><testcase classname="PathTest" name="fails"><failure type="AssertionError" message="missing %s/src/test/data"/></testcase></testsuite>' "$PWD" > target/surefire-reports/TEST-PathTest.xml
exit 1
`), 0700))
	session, err := NewSession()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, session.Close()) })
	drive := &Testdrive{repositoryRoot: root, framework: framework.NewMaven("junit5", nil), command: "./mvnw", args: []string{"test"}, session: session,
		startIntake: func(string, intake.Scenario) (localIntake, error) { return &fakeIntake{url: "http://127.0.0.1:1"}, nil },
	}
	baseline, err := drive.mavenRun(t.Context(), io.Discard, "baseline", "", intake.Scenario{}, "", nil)
	require.NoError(t, err)
	reporting, err := drive.mavenRun(t.Context(), io.Discard, "reporting-only", "", intake.Scenario{}, "", nil)
	require.NoError(t, err)
	require.NotEqual(t, baseline.Tests[0].Failure, reporting.Tests[0].Failure)
	require.Equal(t, "compatible", compareMavenNative(baseline, reporting).Status)
	reporting.Tests[0].Failure += " /customer/other-path"
	require.Equal(t, "suspected regression", compareMavenNative(baseline, reporting).Status)
}

func TestMavenPassesTimeoutToTracerInstallation(t *testing.T) {
	root, bin := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "pom.xml"), []byte(`<project><artifactId>suite</artifactId></project>`), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(bin, "java"), []byte("#!/bin/sh\nexit 0\n"), 0700))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	tracer := &fakeTracer{Platform: platform.NewJava(), err: errors.New("installation stopped")}
	session := planSession()
	drive := &Testdrive{repositoryRoot: root, platform: tracer, framework: framework.NewMaven("junit5", nil), command: "mvn", args: []string{"test"}, session: session, commandTimeout: 200 * time.Millisecond}
	require.ErrorContains(t, drive.runMaven(t.Context(), io.Discard), "installation stopped")
	require.Equal(t, 200*time.Millisecond, tracer.options.Timeout)
	require.NoDirExists(t, session.Directory())
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
		(validationRun{Role: roleBaseline, Name: "baseline", Command: "mvn test"}).summary(),
		(validationRun{Role: roleReporting, Name: "reporting-only", Command: "mvn test", Instrumented: true}).summary(),
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

func TestMavenTimeoutPreservesExecutionRoleAndDiagnostic(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "pom.xml"), []byte(`<project><groupId>example</groupId><artifactId>suite</artifactId><version>1</version></project>`), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "mvnw"), []byte("#!/bin/sh\nsleep 30\n"), 0700))
	session, err := NewSession()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, session.Close()) })
	drive := &Testdrive{repositoryRoot: root, framework: framework.NewMaven("junit5", nil), command: "./mvnw", args: []string{"test"}, session: session, commandTimeout: 30 * time.Millisecond,
		startIntake: func(string, intake.Scenario) (localIntake, error) { return &fakeIntake{url: "http://127.0.0.1:1"}, nil },
	}
	run, err := drive.mavenRun(t.Context(), &bytes.Buffer{}, "baseline", "", intake.Scenario{}, "", nil)
	require.NoError(t, err)
	require.Equal(t, roleBaseline, run.Role)
	require.Contains(t, run.Timeout, "--timeout")
	require.Contains(t, run.ResultError, "--timeout")
	require.Equal(t, run.Command, run.NativeCommand)
	require.NoDirExists(t, filepath.Join(root, "target"))
}

func TestMavenFeaturePoliciesRequireEveryObservedControl(t *testing.T) {
	for _, name := range maven.FrameworkNames {
		t.Run(name, func(t *testing.T) {
			var features []featureResult
			for _, policy := range featurePolicies(name) {
				features = append(features, featureResult{Project: "module", Name: policy.Name, Status: "passed"})
			}
			require.Len(t, features, 6)
			require.True(t, featureChecksPassed(name, features))
			require.False(t, featureChecksPassed(name, features[:5]))
			features[2].Status = "not supported"
			require.True(t, featureChecksPassed(name, features), "tracer capability limits must stay explicit")
			features[0].Status = "inconclusive"
			require.False(t, featureChecksPassed(name, features))
		})
	}
}
