// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
	"github.com/stretchr/testify/require"
)

// javaProbeSourceFile is where runJavaFeatures writes the probe in a
// single-module reactor, relative to the checkout.
const javaProbeSourceFile = "src/test/java/ddtest/DdtestValidationTest.java"

// simulatedMaven stands in for Maven with dd-java-agent: it writes the
// Surefire reports and the probe state the real build would, and the telemetry
// the agent would send for the scenario the intake serves.
type simulatedMaven struct {
	root     string
	scenario intake.Scenario
	facts    intake.Facts
	// customerFailure makes the customer test fail.
	customerFailure bool
	// brokenBuild writes no reports and exits 1, as a compilation failure does.
	brokenBuild bool
	// unreportedSkips writes no report for a probe that never starts, as
	// Surefire 3.0.0-M5 does for a skipped Spock specification.
	unreportedSkips bool
	calls       []map[string]string
}

type simulatedIntake struct{ maven *simulatedMaven }

func (i simulatedIntake) URL() string                  { return "http://127.0.0.1:1" }
func (i simulatedIntake) Facts() (intake.Facts, error) { return i.maven.facts, nil }
func (i simulatedIntake) Close() error                 { return nil }

func (m *simulatedMaven) startIntake(_ string, scenario intake.Scenario) (localIntake, error) {
	m.scenario, m.facts = scenario, intake.Facts{}
	return simulatedIntake{m}, nil
}

func exitStatus(t *testing.T, code int) error {
	t.Helper()
	err := exec.CommandContext(t.Context(), "sh", "-c", "exit "+strconv.Itoa(code)).Run()
	require.Error(t, err)
	return err
}

func (m *simulatedMaven) report(t *testing.T, class string, cases ...string) {
	t.Helper()
	directory := filepath.Join(m.root, "target", "surefire-reports")
	require.NoError(t, os.MkdirAll(directory, 0755))
	requireWriteFile(t, filepath.Join(directory, "TEST-"+class+".xml"), `<testsuite name="`+class+`">`+strings.Join(cases, "")+`</testsuite>`)
}

func (m *simulatedMaven) run(t *testing.T, args []string, env map[string]string) (string, error) {
	t.Helper()
	m.calls = append(m.calls, env)
	instrumented := env["DD_CIVISIBILITY_ENABLED"] != "false"
	if m.brokenBuild {
		return "[ERROR] COMPILATION ERROR\n", exitStatus(t, 1)
	}
	if !slices.Contains(args, "-Dtest="+javaProbeClass) {
		testCase := `<testcase classname="example.CalcTest" name="adds()"/>`
		status := "pass"
		if m.customerFailure {
			testCase = `<testcase classname="example.CalcTest" name="adds()"><failure type="org.opentest4j.AssertionFailedError" message="expected: &lt;5&gt; but was: &lt;4&gt;"/></testcase>`
			status = "fail"
		}
		m.report(t, "example.CalcTest", testCase)
		if instrumented {
			m.facts = intake.Facts{TestEventCount: 1, Tests: []intake.Test{{
				Module: "fixture maven-surefire-plugin default-test", Suite: "example.CalcTest", Name: "adds",
				Attempts: []intake.TestRun{{Status: status, SpanID: 1}}, CoveredFiles: []string{"src/main/java/example/Calc.java"},
			}}}
		}
		if m.customerFailure {
			return "[ERROR] Failed to execute goal org.apache.maven.plugins:maven-surefire-plugin:3.5.3:test (default-test) on project fixture: There are test failures.\n", exitStatus(t, 1)
		}
		return "", nil
	}

	// The probe body appends to its state file and fails as its mode asks.
	attempts := 0
	body := func() string {
		attempts++
		state, err := os.OpenFile(env["DDTEST_PROBE_STATE"], os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		require.NoError(t, err)
		_, err = state.WriteString("run\n")
		require.NoError(t, errors.Join(err, state.Close()))
		if mode := env["DDTEST_PROBE_MODE"]; mode == "fail" || (mode == "fail-once" && attempts == 1) {
			return "fail"
		}
		return "pass"
	}
	var events []intake.Event
	event := func(status string, tags ...string) {
		values := map[string]string{"test.module": m.scenario.Module, "test.suite": javaProbeClass, "test.name": "ddtestProbe", "test.status": status}
		for i := 0; i+1 < len(tags); i += 2 {
			values[tags[i]] = tags[i+1]
		}
		events = append(events, intake.Event{Type: "test", Tags: values})
	}
	retried := func(reason string, tags ...string) string {
		final := body()
		event(final, tags...)
		for range 2 {
			final = body()
			event(final, append([]string{"test.is_retry", "true", "test.retry_reason", reason}, tags...)...)
		}
		return final
	}
	native, exit := "", 0
	switch m.scenario.Feature {
	case "reporting-only":
		status := body()
		native = map[string]string{"pass": "", "fail": "fail"}[status]
		m.facts = intake.Facts{TestEventCount: 1, Tests: []intake.Test{{
			Module: "fixture", Suite: javaProbeClass, Name: "ddtestProbe", SourceFile: javaProbeSourceFile,
			Attempts: []intake.TestRun{{Status: status, SpanID: 1}},
		}}}
		if status == "fail" {
			exit = 1
		}
	case "early-flake-detection":
		retried("early_flake_detection")
	case "attempt-to-fix":
		retried("attempt_to_fix", "test.test_management.is_attempt_to_fix", "true")
	case "auto-retries":
		event(body())
		event(body(), "test.is_retry", "true", "test.retry_reason", "auto_test_retry")
	case "skipping":
		native = "skip"
		event("skip", "test.skipped_by_itr", "true")
	case "quarantine":
		// Surefire reports a quarantined failure as skipped.
		native = "skip"
		event(body(), "test.test_management.is_quarantined", "true")
	case "disabled":
		native = "skip"
		event("skip", "test.test_management.is_test_disabled", "true")
	default:
		return "", fmt.Errorf("unexpected scenario %q", m.scenario.Feature)
	}
	if m.scenario.Feature != "reporting-only" {
		m.facts = intake.Facts{TestEventCount: len(events), Events: events}
	}
	if m.unreportedSkips && (m.scenario.Feature == "skipping" || m.scenario.Feature == "disabled") {
		return "", nil
	}
	m.report(t, javaProbeClass, map[string]string{
		"":     `<testcase classname="ddtest.DdtestValidationTest" name="ddtestProbe()"/>`,
		"fail": `<testcase classname="ddtest.DdtestValidationTest" name="ddtestProbe()"><failure type="java.lang.AssertionError" message="ddtest intentional failure"/></testcase>`,
		"skip": `<testcase classname="ddtest.DdtestValidationTest" name="ddtestProbe()"><skipped/></testcase>`,
	}[native])
	if exit != 0 {
		return "[ERROR] Failed to execute goal org.apache.maven.plugins:maven-surefire-plugin:3.5.3:test (default-test) on project fixture: There are test failures.\n", exitStatus(t, exit)
	}
	return "", nil
}

type simulatedMavenExecutor struct {
	t     *testing.T
	maven *simulatedMaven
}

func (e simulatedMavenExecutor) CombinedOutput(_ context.Context, _ string, args []string, env map[string]string) ([]byte, error) {
	output, err := e.maven.run(e.t, args, env)
	return []byte(output), err
}

func javaSimulation(t *testing.T) (*Testdrive, *simulatedMaven, *workspace.Session) {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0755))
	requireWriteFile(t, filepath.Join(root, "pom.xml"), `<project><artifactId>fixture</artifactId><dependencies><dependency><groupId>org.junit.jupiter</groupId><artifactId>junit-jupiter</artifactId></dependency></dependencies></project>`)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "src/main/java/example"), 0755))
	requireWriteFile(t, filepath.Join(root, "src/main/java/example/Calc.java"), "package example;\npublic class Calc {}\n")
	maven := &simulatedMaven{root: root}
	session, err := workspace.NewSession()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, session.Close()) })
	drive := &Testdrive{
		repositoryRoot: root, isolated: true, language: "java", framework: framework.NewMaven("junit5", nil),
		command: "mvn", args: []string{"-B", "test"}, executor: simulatedMavenExecutor{t, maven}, startIntake: maven.startIntake,
	}
	return drive, maven, session
}

func TestJavaValidationRunsPairedBuildsAndEveryFeatureProbe(t *testing.T) {
	drive, maven, session := javaSimulation(t)
	var output bytes.Buffer
	result := &validationResult{Framework: "junit5"}
	require.NoError(t, drive.runFrameworkValidation(t.Context(), &output, session, "/agent/dd-java-agent.jar", result), output.String())

	require.Equal(t, "compatible", result.Compatibility.Status, "%+v", result.Compatibility)
	status := map[string]string{}
	for _, feature := range result.Features {
		status[feature.Name] = feature.Status + ": " + feature.Reason
	}
	require.Len(t, status, 6)
	for name, verdict := range status {
		require.True(t, strings.HasPrefix(verdict, "passed: "), "%s %s", name, verdict)
	}
	names := []string{}
	for _, run := range result.Runs {
		names = append(names, run.Name)
	}
	require.Equal(t, []string{"baseline", "reporting-only", "probe-pass-control", "probe-fail-control", "auto-retries", "early-flake-detection", "skipping", "quarantine", "disabled", "attempt-to-fix"}, names)
	require.Equal(t, "observed", result.Runs[1].Coverage.Status, "the instrumented run covers repository application code")

	baseline, instrumented := maven.calls[0], maven.calls[1]
	require.Equal(t, "false", baseline["DD_CIVISIBILITY_ENABLED"])
	require.NotContains(t, baseline["MAVEN_OPTS"], "-javaagent")
	require.Contains(t, instrumented["MAVEN_OPTS"], "-javaagent:/agent/dd-java-agent.jar")
	require.Equal(t, "true", instrumented["MAVEN_SKIP_RC"], "~/.mavenrc cannot change the build between runs")
	require.Equal(t, instrumented["DD_TRACE_AGENT_URL"], "http://127.0.0.1:1")
	require.NoFileExists(t, filepath.Join(drive.repositoryRoot, javaProbeSourceFile), "the probe is removed from the copy")
}

func TestJavaValidationSkipsProbesWithoutUsableBaseline(t *testing.T) {
	t.Run("broken build", func(t *testing.T) {
		drive, maven, session := javaSimulation(t)
		maven.brokenBuild = true
		result := &validationResult{Framework: "junit5"}
		require.NoError(t, drive.runFrameworkValidation(t.Context(), &bytes.Buffer{}, session, "/agent/dd-java-agent.jar", result))
		require.Len(t, maven.calls, 2, "no probe runs after the build fails")
		require.Equal(t, "native_startup_failed", result.Compatibility.Code)
	})
	t.Run("stale reports", func(t *testing.T) {
		drive, maven, session := javaSimulation(t)
		maven.report(t, "example.StaleTest", `<testcase classname="example.StaleTest" name="old()"/>`)
		maven.brokenBuild = true
		result := &validationResult{Framework: "junit5"}
		require.NoError(t, drive.runFrameworkValidation(t.Context(), &bytes.Buffer{}, session, "/agent/dd-java-agent.jar", result))
		require.Equal(t, "native_startup_failed", result.Compatibility.Code, "an earlier build's reports are not this run's results")
	})
	t.Run("customer failure", func(t *testing.T) {
		drive, maven, session := javaSimulation(t)
		maven.customerFailure = true
		result := &validationResult{Framework: "junit5"}
		require.NoError(t, drive.runFrameworkValidation(t.Context(), &bytes.Buffer{}, session, "/agent/dd-java-agent.jar", result))
		require.Equal(t, "compatible", result.Compatibility.Status, "matching native failures: %+v", result.Compatibility)
		require.Len(t, result.Features, 6, "an existing test failure does not stop the feature probes")
	})
}

func TestJavaValidationNeverRunsPublishingGoalsFromMavenArgs(t *testing.T) {
	drive, maven, session := javaSimulation(t)
	t.Setenv("MAVEN_ARGS", "install")
	result := &validationResult{Framework: "junit5"}
	require.ErrorContains(t, drive.runFrameworkValidation(t.Context(), &bytes.Buffer{}, session, "/agent/dd-java-agent.jar", result), "MAVEN_ARGS adds install")
	require.Empty(t, maven.calls, "Maven never starts")

	drive.configurationEnvironment = map[string]string{"JAVA_HOME": writeJDK(t, "17.0.20")}
	result = &validationResult{}
	require.ErrorContains(t, drive.checkJavaPreflight(t.Context(), &bytes.Buffer{}, result), "inspect Maven reactor")
	require.Contains(t, result.Preflight.Verdict.Reason, "MAVEN_ARGS adds install", "the preflight names the inherited goal")
}

func TestJavaPreflightInspectsTheConfigurationJDKAndReactor(t *testing.T) {
	drive, _, _ := javaSimulation(t)
	drive.configurationEnvironment = map[string]string{"JAVA_HOME": t.TempDir()}
	result := &validationResult{}
	require.ErrorContains(t, drive.checkJavaPreflight(t.Context(), &bytes.Buffer{}, result), "No JDK was found")
	require.Equal(t, "inconclusive", result.Preflight.Verdict.Status)

	drive.configurationEnvironment = map[string]string{"JAVA_HOME": writeJDK(t, "17.0.20")}
	requireWriteFile(t, filepath.Join(drive.repositoryRoot, "pom.xml"), `<project><artifactId>fixture</artifactId><modules><module>legacy</module></modules><dependencies><dependency><groupId>org.junit.jupiter</groupId><artifactId>junit-jupiter</artifactId></dependency></dependencies></project>`)
	require.NoError(t, os.Mkdir(filepath.Join(drive.repositoryRoot, "legacy"), 0755))
	requireWriteFile(t, filepath.Join(drive.repositoryRoot, "legacy/pom.xml"), `<project><artifactId>legacy</artifactId><dependencies><dependency><groupId>org.testng</groupId><artifactId>testng</artifactId></dependency></dependencies></project>`)
	var output bytes.Buffer
	result = &validationResult{}
	require.NoError(t, drive.checkJavaPreflight(t.Context(), &output, result))
	require.Equal(t, "compatible", result.Preflight.Verdict.Status)
	require.Equal(t, "17.0.20", result.Preflight.Runtime, "the configuration's JAVA_HOME selects the JDK")
	require.Contains(t, result.Preflight.Verdict.Reason, "builds 2 Maven projects")
	require.Contains(t, result.Preflight.Verdict.Reason, "module(s) legacy of other frameworks", "modules of other frameworks are reported")
	require.Contains(t, output.String(), "JUnit 5 preflight: compatible")

	drive.framework = framework.NewMaven("junit4", nil)
	result = &validationResult{}
	require.NoError(t, drive.checkJavaPreflight(t.Context(), &bytes.Buffer{}, result))
	require.Contains(t, result.Preflight.Verdict.Reason, "No POM in the repository declares JUnit 4", "an inherited framework is named")

	drive.args = []string{"-f", "missing/pom.xml", "test"}
	result = &validationResult{}
	require.ErrorContains(t, drive.checkJavaPreflight(t.Context(), &bytes.Buffer{}, result), "inspect Maven reactor")
	require.Contains(t, result.Preflight.Verdict.Reason, "reactor cannot be read")
}

func TestJavaValidationReportsTheCIPinnedAgent(t *testing.T) {
	drive, _, _ := javaSimulation(t)
	require.NoError(t, os.MkdirAll(filepath.Join(drive.repositoryRoot, ".github/workflows"), 0755))
	requireWriteFile(t, filepath.Join(drive.repositoryRoot, ".github/workflows/ci.yml"), "jobs:\n  test:\n    steps:\n      - uses: actions/setup-java@v4\n        with: {java-version: '17'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: java, api_key: '${{ secrets.DD_API_KEY }}', java-tracer-version: '1.67.1'}\n      - run: mvn -B test\n")
	drive.configurationEnvironment = map[string]string{"JAVA_HOME": writeJDK(t, "17.0.20")}
	drive.platform = &fakeTracer{preloadPath: "/agent/dd-java-agent.jar"}
	drive.tracerVersion, drive.tracerLabel, drive.tracerVersionDefaulted = "latest", "dd-java-agent@latest", true
	drive.preflight = drive.frameworkPreflight()
	var output bytes.Buffer
	result, err := drive.run(t.Context(), &output)
	require.NoError(t, err, output.String())
	require.Equal(t, "dd-java-agent@1.67.1", result.Tracer, "the report names the release CI pins, which this run validated")
	require.Equal(t, "1.67.1", drive.platform.(*fakeTracer).options.Version)
	require.Equal(t, "compatible", result.CIRuntime.Status, "%+v", result.CIRuntime)
}

func TestJavaFeaturesPassWhenSurefireReportsNoSkippedProbe(t *testing.T) {
	drive, maven, session := javaSimulation(t)
	maven.unreportedSkips = true
	result := &validationResult{Framework: "junit5"}
	require.NoError(t, drive.runFrameworkValidation(t.Context(), &bytes.Buffer{}, session, "/agent/dd-java-agent.jar", result))
	status := map[string]string{}
	for _, feature := range result.Features {
		status[feature.Name] = feature.Status + ": " + feature.Reason
	}
	require.True(t, strings.HasPrefix(status["skipping"], "passed"), status["skipping"])
	require.True(t, strings.HasPrefix(status["disabled"], "passed"), "the probe's zero executions prove the body did not run: %s", status["disabled"])
}
