// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/maven"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
	"github.com/stretchr/testify/require"
)

func TestJavaSelectsOnlyLocalMavenTestCommands(t *testing.T) {
	runner := framework.NewMaven("junit5", nil)
	command, args, handled, err := javaLanguage{}.selectCommand(commandRequest{runner: runner, override: "./mvnw -B -f service/pom.xml verify"})
	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, "./mvnw", command)
	require.Equal(t, []string{"-B", "-f", "service/pom.xml", "verify"}, args)

	_, _, handled, err = javaLanguage{}.selectCommand(commandRequest{runner: runner})
	require.NoError(t, err)
	require.False(t, handled, "without --command the framework's default Maven command runs")

	for override, message := range map[string]string{
		"gradle test":                    "literal mvn or mvnw command",
		"sh -c 'mvn test'":               "literal mvn or mvnw command",
		"mvn compile":                    "literal mvn or mvnw command",
		"mvn test deploy:deploy@publish": "never installs or publishes artifacts",
		"mvn -f ../other/pom.xml test":   "must stay inside the repository",
		"mvn test 'unterminated":         "invalid Maven command",
	} {
		_, _, _, err := javaLanguage{}.selectCommand(commandRequest{runner: runner, override: override})
		require.ErrorContains(t, err, message, override)
	}
}

func TestJavaAgentEnvironmentAttachesTheAgentOnce(t *testing.T) {
	t.Setenv("MAVEN_OPTS", "-Xmx1g")
	t.Setenv("JAVA_TOOL_OPTIONS", "-Dfile.encoding=UTF-8")
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "pom.xml"), `<project><dependencies><dependency><groupId>org.junit.jupiter</groupId><artifactId>junit-jupiter</artifactId></dependency></dependencies></project>`)
	drive := &Testdrive{repositoryRoot: root, configurationRoot: root, framework: framework.NewMaven("junit5", nil), command: "mvn", args: []string{"verify"}}
	env := javaLanguage{}.runEnvironment(drive, "/tmp/session/dd-java-agent.jar")
	require.Equal(t, "-Xmx1g -javaagent:/tmp/session/dd-java-agent.jar", env["MAVEN_OPTS"], "the Maven JVM configures the forked test JVMs")
	require.NotContains(t, env, "JAVA_TOOL_OPTIONS")
	require.Equal(t, "false", env["DD_CIVISIBILITY_COMPILER_PLUGIN_AUTO_CONFIGURATION_ENABLED"])

	// A configuration's own options are kept, as the baseline keeps them.
	drive.configurationEnvironment = map[string]string{"MAVEN_OPTS": "-Xmx4g -Dhttps.proxyHost=proxy"}
	env = javaLanguage{}.runEnvironment(drive, "/tmp/session/dd-java-agent.jar")
	require.Equal(t, "-Xmx4g -Dhttps.proxyHost=proxy -javaagent:/tmp/session/dd-java-agent.jar", env["MAVEN_OPTS"])

	// ScalaTest's plugin fork inherits only JAVA_TOOL_OPTIONS, in any module.
	requireWriteFile(t, filepath.Join(root, "pom.xml"), `<project><modules><module>scala</module></modules><dependencies><dependency><groupId>junit</groupId><artifactId>junit</artifactId></dependency></dependencies></project>`)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "scala"), 0755))
	requireWriteFile(t, filepath.Join(root, "scala", "pom.xml"), `<project><dependencies><dependency><groupId>org.scalatest</groupId><artifactId>scalatest_2.13</artifactId></dependency></dependencies></project>`)
	drive.framework, drive.configurationEnvironment = framework.NewMaven("junit4", nil), nil
	env = javaLanguage{}.runEnvironment(drive, "/tmp/session/dd-java-agent.jar")
	require.Equal(t, "-Dfile.encoding=UTF-8 -javaagent:/tmp/session/dd-java-agent.jar", env["JAVA_TOOL_OPTIONS"])
	require.NotContains(t, env, "MAVEN_OPTS")

	require.NotContains(t, javaLanguage{}.runEnvironment(drive, ""), "JAVA_TOOL_OPTIONS", "the baseline never attaches the agent")
}

func TestJavaRunsRemoveAnInheritedDatadogAgent(t *testing.T) {
	// The GitHub action attaches dd-java-agent to MAVEN_OPTS, or with
	// java-instrumented-build-system: all to JAVA_TOOL_OPTIONS.
	ciAgent := filepath.Join(t.TempDir(), "agent.jar")
	writeTestAgentJAR(t, ciAgent)
	t.Setenv("MAVEN_OPTS", "-Xmx1g -javaagent:/opt/datadog/dd-java-agent.jar -javaagent:/opt/jacoco.jar=destfile=jacoco.exec")
	t.Setenv("JAVA_TOOL_OPTIONS", "-javaagent:"+ciAgent+" -Dfile.encoding=UTF-8")
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "pom.xml"), `<project><artifactId>fixture</artifactId></project>`)
	drive := &Testdrive{repositoryRoot: root, configurationRoot: root, framework: framework.NewMaven("junit5", nil), command: "mvn", args: []string{"verify"}}

	baseline := javaLanguage{}.runEnvironment(drive, "")
	require.Equal(t, "-Xmx1g -javaagent:/opt/jacoco.jar=destfile=jacoco.exec", baseline["MAVEN_OPTS"], "other agents stay")
	require.Equal(t, "-Dfile.encoding=UTF-8", baseline["JAVA_TOOL_OPTIONS"], "an agent found by its manifest is removed too")
	instrumented := javaLanguage{}.runEnvironment(drive, "/tmp/session/dd-java-agent.jar")
	require.Equal(t, "-Xmx1g -javaagent:/opt/jacoco.jar=destfile=jacoco.exec -javaagent:/tmp/session/dd-java-agent.jar", instrumented["MAVEN_OPTS"], "only the selected agent runs")
	require.Equal(t, "-Dfile.encoding=UTF-8", instrumented["JAVA_TOOL_OPTIONS"])

	drive.configurationEnvironment = map[string]string{"MAVEN_OPTS": "-Xmx2g", "JAVA_TOOL_OPTIONS": ""}
	baseline = javaLanguage{}.runEnvironment(drive, "")
	require.NotContains(t, baseline, "MAVEN_OPTS", "a configuration's own options already reach Maven")
	require.NotContains(t, baseline, "JAVA_TOOL_OPTIONS")
}

func writeTestAgentJAR(t *testing.T, path string) {
	t.Helper()
	file, err := os.Create(path)
	require.NoError(t, err)
	writer := zip.NewWriter(file)
	entry, err := writer.Create("META-INF/MANIFEST.MF")
	require.NoError(t, err)
	_, err = entry.Write([]byte("Manifest-Version: 1.0\r\nImplementation-Title: dd-java-agent\r\nImplementation-Version: 1.67.1\r\nPremain-Class: datadog.trace.bootstrap.AgentPreCheck\r\n\r\n"))
	require.NoError(t, err)
	require.NoError(t, errors.Join(writer.Close(), file.Close()))
}

func TestJavaReactorIncludesMavenConfig(t *testing.T) {
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "pom.xml"), `<project><artifactId>root</artifactId></project>`)
	for _, directory := range []string{".mvn", "service"} {
		require.NoError(t, os.Mkdir(filepath.Join(root, directory), 0755))
	}
	requireWriteFile(t, filepath.Join(root, "service", "pom.xml"), `<project><artifactId>service</artifactId></project>`)
	drive := &Testdrive{repositoryRoot: root, configurationRoot: root}

	requireWriteFile(t, filepath.Join(root, ".mvn", "maven.config"), "-B\n--file=service/pom.xml\n")
	_, projects, err := drive.javaReactor([]string{"test"})
	require.NoError(t, err)
	require.Equal(t, "service", projects[0].Artifact, "Maven reads the POM that maven.config selects")
	requireWriteFile(t, filepath.Join(root, ".mvn", "maven.config"), "--file\n/elsewhere/pom.xml\n")
	_, _, err = drive.javaReactor([]string{"test"})
	require.ErrorContains(t, err, "must be a repository path", "the build cannot leave the disposable copy")
	requireWriteFile(t, filepath.Join(root, ".mvn", "maven.config"), "-ntp install\n")
	_, _, err = drive.javaReactor([]string{"test"})
	require.ErrorContains(t, err, ".mvn/maven.config adds install")

	// Maven 3.9 skips # comments.
	requireWriteFile(t, filepath.Join(root, ".mvn", "maven.config"), "# shared build options\n-ntp\n\n")
	_, projects, err = drive.javaReactor([]string{"test"})
	require.NoError(t, err)
	require.Equal(t, "root", projects[0].Artifact)

	// The launcher reads the nearest .mvn directory above a POM that a
	// separate -f names; compact forms keep the working directory's.
	requireWriteFile(t, filepath.Join(root, ".mvn", "maven.config"), "install\n")
	require.NoError(t, os.Mkdir(filepath.Join(root, "service", ".mvn"), 0755))
	_, projects, err = drive.javaReactor([]string{"-f", "service/pom.xml", "test"})
	require.NoError(t, err)
	require.Equal(t, "service", projects[0].Artifact)
	_, projects, err = drive.javaReactor([]string{"-f", "service", "test"})
	require.NoError(t, err, "a selected directory's own .mvn applies")
	require.Equal(t, "service", projects[0].Artifact)
	for _, args := range [][]string{{"--file=service/pom.xml", "test"}, {"-fservice/pom.xml", "test"}} {
		_, _, err = drive.javaReactor(args)
		require.ErrorContains(t, err, ".mvn/maven.config adds install", "%v", args)
	}
	requireWriteFile(t, filepath.Join(root, "service", ".mvn", "jvm.config"), "-javaagent:dd-java-agent.jar\n")
	_, _, err = drive.javaReactor([]string{"-f", "service", "test"})
	require.ErrorContains(t, err, ".mvn/jvm.config attaches dd-java-agent")
}

func TestJavaRefusesADatadogAgentInJVMConfig(t *testing.T) {
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "pom.xml"), `<project><artifactId>root</artifactId></project>`)
	require.NoError(t, os.Mkdir(filepath.Join(root, ".mvn"), 0755))
	drive := &Testdrive{repositoryRoot: root, configurationRoot: root}
	requireWriteFile(t, filepath.Join(root, ".mvn", "jvm.config"), "-Xmx2g -javaagent:/opt/jacoco.jar\n")
	_, _, err := drive.javaReactor([]string{"test"})
	require.NoError(t, err, "other agents and options are the project's own")
	requireWriteFile(t, filepath.Join(root, ".mvn", "jvm.config"), "-Xmx2g\n-javaagent:tools/dd-java-agent.jar\n")
	_, _, err = drive.javaReactor([]string{"test"})
	require.ErrorContains(t, err, ".mvn/jvm.config attaches dd-java-agent", "the baseline could not run without the agent")
}

func TestJavaConfigurationRefusesImplicitPublishingGoalsForEveryFramework(t *testing.T) {
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "pom.xml"), `<project><artifactId>root</artifactId></project>`)
	drive := &Testdrive{repositoryRoot: root, configurationRoot: root, framework: framework.NewMaven("testng", nil), command: "mvn", args: []string{"test"}}
	require.NoError(t, javaLanguage{}.prepareConfiguration(drive, nil, root, root))
	drive.configurationEnvironment = map[string]string{"MAVEN_ARGS": "deploy"}
	require.ErrorContains(t, javaLanguage{}.prepareConfiguration(drive, nil, root, root), "MAVEN_ARGS adds deploy", "reporting-only frameworks never reach runJava")

	// A configuration's command runs from its working directory.
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	requireWriteFile(t, filepath.Join(root, "pom.xml"), `<project><artifactId>root</artifactId></project>`)
	require.NoError(t, os.Mkdir(filepath.Join(root, "build"), 0755))
	drive = &Testdrive{repositoryRoot: root, configurationRoot: root, framework: framework.NewMaven("junit5", nil), command: "mvn", args: []string{"-f", "../pom.xml", "verify"}}
	item := &configurationResult{TestCommand: onboard.TestCommand{Directory: "build"}}
	require.NoError(t, javaLanguage{}.prepareConfiguration(drive, item, root, root))
	item.Directory = "."
	require.ErrorContains(t, javaLanguage{}.prepareConfiguration(drive, item, root, root), "must stay inside the repository")
}

func TestJavaReactorIsBoundedByTheCheckout(t *testing.T) {
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "pom.xml"), `<project><modules><module>core</module></modules></project>`)
	for _, directory := range []string{"core", "build"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, directory), 0755))
	}
	requireWriteFile(t, filepath.Join(root, "core", "pom.xml"), `<project><artifactId>core</artifactId></project>`)
	requireWriteFile(t, filepath.Join(root, "build", "pom.xml"), `<project><modules><module>../core</module></modules></project>`)
	drive := &Testdrive{repositoryRoot: filepath.Join(root, "build"), configurationRoot: root}
	for _, args := range [][]string{{"verify"}, {"-f", "../pom.xml", "verify"}} {
		checkout, projects, err := drive.javaReactor(args)
		require.NoError(t, err, "%v", args)
		require.Equal(t, root, checkout)
		require.Equal(t, "core", projects[len(projects)-1].Directory, "%v", args)
	}
	_, _, err := drive.javaReactor([]string{"-f", "../../pom.xml", "verify"})
	require.ErrorContains(t, err, "must stay inside the repository")
}

func TestJavaReactorIncludesMavenArgs(t *testing.T) {
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "pom.xml"), `<project><artifactId>root</artifactId></project>`)
	require.NoError(t, os.Mkdir(filepath.Join(root, "service"), 0755))
	requireWriteFile(t, filepath.Join(root, "service", "pom.xml"), `<project><artifactId>service</artifactId></project>`)
	drive := &Testdrive{repositoryRoot: root}

	// Maven 3.9+ puts MAVEN_ARGS before every command's own arguments.
	for _, value := range []string{"install", "-B deploy", "clean javadoc:jar"} {
		t.Setenv("MAVEN_ARGS", value)
		_, _, err := drive.javaReactor([]string{"test"})
		require.ErrorContains(t, err, "MAVEN_ARGS adds", value)
	}
	t.Setenv("MAVEN_ARGS", "-B -ntp -f service/pom.xml")
	_, projects, err := drive.javaReactor([]string{"test"})
	require.NoError(t, err)
	require.Equal(t, "service", projects[0].Artifact, "a POM that MAVEN_ARGS selects is the reactor")
	_, _, err = drive.javaReactor([]string{"-f", "pom.xml", "test"})
	require.ErrorContains(t, err, "more than one POM")

	// A configuration's own value replaces the inherited one.
	drive.configurationEnvironment = map[string]string{"MAVEN_ARGS": "deploy"}
	_, _, err = drive.javaReactor([]string{"test"})
	require.ErrorContains(t, err, "remove deploy")
	drive.configurationEnvironment = map[string]string{"MAVEN_ARGS": ""}
	_, projects, err = drive.javaReactor([]string{"test"})
	require.NoError(t, err)
	require.Equal(t, "root", projects[0].Artifact)
}

func TestJavaTemporaryAgentPathCannotContainWhitespace(t *testing.T) {
	_, _, err := javaLanguage{}.installDirectory("/tmp/with space/session", "")
	require.ErrorContains(t, err, "MAVEN_OPTS cannot carry")
	directory, cleanup, err := javaLanguage{}.installDirectory("/tmp/session", "")
	require.NoError(t, err)
	require.Nil(t, cleanup)
	require.Equal(t, "/tmp/session", directory)
}

func TestJavaRunsMavenOnlyInADisposableCopy(t *testing.T) {
	drive := &Testdrive{repositoryRoot: t.TempDir(), framework: framework.NewMaven("junit5", nil)}
	session := workspace.PlanSession()
	_, err := drive.runJava(context.Background(), io.Discard, session, "", runSpec{Name: "baseline"}, javaProbe{})
	require.ErrorContains(t, err, "disposable copy")
}

func javaFixtureProjects() []maven.Project {
	return []maven.Project{
		{Directory: ".", Artifact: "parent"},
		{Directory: "api", Name: "Customer API", Artifact: "api"},
		{Directory: "core", Artifact: "core", Dependencies: []maven.Dependency{{Group: "org.junit.jupiter", Artifact: "junit-jupiter"}}},
		{Directory: "a/util", Artifact: "util"},
		{Directory: "b/util", Artifact: "util"},
	}
}

func TestJavaNativeTestsUseTheAgentsModuleNames(t *testing.T) {
	tests := javaNativeTests(javaFixtureProjects(), []maven.Test{
		{Module: "api", Suite: "example.ApiTest", Name: "works()", Status: "passed"},
		{Module: "core", Suite: "example.CoreTest", Name: "fails", Status: "failed", Failure: "AssertionError: no\n"},
		{Module: "a/util", Suite: "example.UtilTest", Name: "works", Status: "passed"},
		{Module: "core", Suite: javaProbeClass, Name: "ddtestProbe()", Status: "passed"},
	}, true)
	require.Equal(t, "Customer API / example.ApiTest", tests[0].File, "the agent reports a project's <name>")
	require.Equal(t, "core / example.CoreTest", tests[1].File, "and otherwise its artifactId")
	require.Equal(t, "a/util / example.UtilTest", tests[2].File, "a name two projects share cannot identify either")
	probe := tests[3]
	require.True(t, probe.probe)
	require.Equal(t, "core / "+javaProbeClass, probe.File, "the probe is matched by module, class and name, as other tests")
	require.Equal(t, "ddtestProbe", *probe.telemetryName)
	karate := javaNativeTests(javaFixtureProjects(), []maven.Test{
		{Module: "core", Suite: "[src/test/resources/ddtest/DdtestValidation] " + javaProbeClass, Name: "ddtestProbe", Status: "passed"},
		{Module: "core", Suite: "[example/calc] Calculator", Name: "adds", Status: "passed"},
	}, true)
	require.True(t, karate[0].probe, "a Karate probe is its feature's path and name")
	require.False(t, karate[1].probe)
}

func javaTelemetryRun(native []nativeTest, reported ...intake.Test) validationRun {
	return validationRun{framework: "junit5", Tests: native, Facts: intake.Facts{TestEventCount: len(reported), Tests: reported}}
}

func reported(module, suite, name, parameters string, attempts ...intake.TestRun) intake.Test {
	return intake.Test{Module: module, Suite: suite, Name: name, Parameters: parameters, Attempts: attempts}
}

func TestJavaTelemetryMatchesEveryNativeResult(t *testing.T) {
	native := []nativeTest{
		{File: "core / example.CoreTest", Name: "works()", Status: "passed"},
		{File: "core / example.CoreTest", Name: "existingFailure", Status: "failed"},
		{File: "core / example.CoreTest", Name: "ignored", Status: "skipped"},
		{File: "core / example.CoreTest", Name: "positive(int)[1]", Status: "passed"},
		{File: "core / example.CoreTest", Name: "positive(int)[2]", Status: "passed"},
	}
	module := "core maven-surefire-plugin default-test"
	run := javaTelemetryRun(native,
		reported(module, "example.CoreTest", "works", "", intake.TestRun{Status: "pass", SpanID: 1}),
		reported(module, "example.CoreTest", "existingFailure", "", intake.TestRun{Status: "fail", SpanID: 2}, intake.TestRun{Status: "fail", SpanID: 2}),
		reported(module, "example.CoreTest", "ignored", "", intake.TestRun{Status: "skip", SpanID: 3}),
		reported(module, "example.CoreTest", "positive", `{"metadata":{"test_name":"[1] 1"}}`, intake.TestRun{Status: "pass", SpanID: 4}),
		reported(module, "example.CoreTest", "positive", `{"metadata":{"test_name":"[2] 2"}}`, intake.TestRun{Status: "pass", SpanID: 5}),
	)
	require.Equal(t, verdict{}, verifyJavaTelemetry(run), "a span sent twice is one execution")

	// One invocation reported twice cannot stand in for a missing one.
	run.Facts.Tests[4].Parameters = `{"metadata":{"test_name":"[1] 1"}}`
	check := verifyJavaTelemetry(run)
	require.Equal(t, "inconclusive", check.Status)
	require.Equal(t, []string{
		`Unexpected telemetry: module "core", class "example.CoreTest", test "positive[1]", status "pass" (count 1)`,
		`Missing telemetry: module "core", class "example.CoreTest", test "positive[2]", status "pass" (count 1)`,
	}, check.Differences)
}

func TestJavaTelemetryAttributesScalaTestSuitesToTheirModule(t *testing.T) {
	native := []nativeTest{{File: "core / example.CalcTest", Name: "adds", Status: "passed"}}
	run := javaTelemetryRun(native, reported(javaScalaTestRunner, "example.CalcTest", "adds", "", intake.TestRun{Status: "pass", SpanID: 1}))
	require.Equal(t, verdict{}, verifyJavaTelemetry(run), "the plugin's runner stands for the module that ran the suite")
	native = append(native, nativeTest{File: "api / example.CalcTest", Name: "adds", Status: "passed"})
	run = javaTelemetryRun(native, reported(javaScalaTestRunner, "example.CalcTest", "adds", "", intake.TestRun{Status: "pass", SpanID: 1}), reported(javaScalaTestRunner, "example.CalcTest", "adds", "", intake.TestRun{Status: "pass", SpanID: 2}))
	require.Equal(t, "inconclusive", verifyJavaTelemetry(run).Status, "a suite two modules ran cannot be attributed")
}

func TestJavaTelemetryRejectsUnusableEvidence(t *testing.T) {
	native := []nativeTest{{File: "core / example.CoreTest", Name: "works", Status: "passed"}}
	require.Contains(t, verifyJavaTelemetry(javaTelemetryRun(native)).Reason, "sent no test events")
	retried := javaTelemetryRun(native, reported("core", "example.CoreTest", "works", "", intake.TestRun{Status: "fail", SpanID: 1}, intake.TestRun{Status: "pass", Retry: true, SpanID: 2}))
	require.Equal(t, "unexpected_retry", verifyJavaTelemetry(retried).Code)
	wrongModule := javaTelemetryRun(native, reported("api", "example.CoreTest", "works", "", intake.TestRun{Status: "pass", SpanID: 1}))
	require.Equal(t, "inconclusive", verifyJavaTelemetry(wrongModule).Status, "a test cannot move between reactor modules")
	misconfigured := javaTelemetryRun(native, reported("core", "example.CoreTest", "works", "", intake.TestRun{Status: "pass", SpanID: 1}))
	misconfigured.Facts.ConfigurationErrors = []string{"invalid DD_SERVICE"}
	require.Contains(t, verifyJavaTelemetry(misconfigured).Reason, "configuration errors")
}

func TestJavaProbeRunsInAModuleThatRanTests(t *testing.T) {
	projects := javaFixtureProjects()
	module, ok := javaProbeModule(projects, []nativeTest{{File: "Customer API / example.ApiTest"}, {File: "core / example.CoreTest"}}, "junit5")
	require.True(t, ok)
	require.Equal(t, "core", module, "a module declaring the selected framework is preferred")
	module, ok = javaProbeModule(projects, []nativeTest{{File: "Customer API / example.ApiTest"}}, "junit5")
	require.True(t, ok)
	require.Equal(t, "api", module, "an inherited framework dependency still runs the probe")
	_, ok = javaProbeModule(projects, nil, "junit5")
	require.False(t, ok)
}

func TestJavaTestName(t *testing.T) {
	for native, telemetry := range map[string]string{
		"works":               "works",
		"works()":             "works",
		"check(TestInfo)":     "check",
		"add(int, int)[2]":    "add[2]",
		"[1] custom display":  "[1] custom display",
		"parse[a, 1](2)":      "parse",
		"parse[[x], (y)](10)": "parse",
	} {
		require.Equal(t, telemetry, javaTestName(native), native)
	}
}

func TestJavaTelemetryNormalizesOnlyNamesTheAgentRenamed(t *testing.T) {
	pass := []intake.TestRun{{Status: "pass", SpanID: 1}}
	// A Spock feature keeps a name that looks like a TestNG invocation.
	run := javaTelemetryRun([]nativeTest{{File: "service / example.ParseSpec", Name: "parse[a](1)", Status: "passed"}},
		intake.Test{Module: "service", Suite: "example.ParseSpec", Name: "parse[a](1)", Attempts: pass})
	run.framework = "testng"
	require.Empty(t, verifyJavaTelemetry(run).Status, "a name the agent reports as written is compared as written")
	// JUnit 5 methods in a build whose selected framework is another.
	run = javaTelemetryRun([]nativeTest{{File: "service / example.ApiTest", Name: "convert(String)[1]", Status: "passed"}, {File: "service / example.ApiTest", Name: "convert(String)[2]", Status: "passed"}},
		intake.Test{Module: "service", Suite: "example.ApiTest", Name: "convert", Parameters: `{"metadata":{"test_name":"[1] a"}}`, Attempts: pass},
		intake.Test{Module: "service", Suite: "example.ApiTest", Name: "convert", Parameters: `{"metadata":{"test_name":"[2] b"}}`, Attempts: []intake.TestRun{{Status: "pass", SpanID: 2}}})
	run.framework = "cucumber-java"
	require.Empty(t, verifyJavaTelemetry(run).Status, "JUnit signatures are normalized whatever framework was selected")
	// A custom @ParameterizedTest name keeps the invocation index out of telemetry.
	custom := func(span uint64) intake.Test {
		return intake.Test{Module: "service", Suite: "example.ApiTest", Name: "convert", Parameters: `{"metadata":{"test_name":"Missing parameter a"}}`, Attempts: []intake.TestRun{{Status: "pass", SpanID: span}}}
	}
	run = javaTelemetryRun([]nativeTest{{File: "service / example.ApiTest", Name: "convert(String)[1]", Status: "passed"}, {File: "service / example.ApiTest", Name: "convert(String)[2]", Status: "passed"}}, custom(1), custom(2))
	require.Empty(t, verifyJavaTelemetry(run).Status, "invocations still match by count")
	run = javaTelemetryRun([]nativeTest{{File: "service / example.ApiTest", Name: "convert(String)[1]", Status: "passed"}, {File: "service / example.ApiTest", Name: "convert(String)[2]", Status: "passed"}}, custom(1))
	require.Equal(t, "inconclusive", verifyJavaTelemetry(run).Status, "a missing invocation is still missing")
}

func TestJavaValidatesSurefireFrameworksWithPairedFeatureControls(t *testing.T) {
	for _, name := range javaProbeFrameworks {
		require.True(t, supportsValidationFramework(name), name)
		require.Len(t, featurePolicies(name), 6)
		require.True(t, frameworkFeaturePolicies[name].TestLevelSkipping, "dd-java-agent skips a test by its suite and name")
		for _, feature := range featurePolicies(name) {
			unsupported := name == "munit" && (feature.Name == "skipping" || feature.Name == "disabled")
			require.Equal(t, unsupported, feature.Status == "not supported", "%s %s", name, feature.Name)
		}
		require.Equal(t, "java", languageForFramework(name).name())
	}
	for name, source := range javaProbeSources {
		require.Contains(t, source.source, "ddtest intentional failure", name)
		require.Contains(t, source.source, "class DdtestValidationTest", "every probe is javaProbeClass")
	}
	require.Contains(t, javaProbeSources["testng"].source, "@org.testng.annotations.Test")
	require.True(t, strings.HasSuffix(javaProbeSources["spock"].path, ".groovy"), "Spock specifications compile from src/test/groovy")
	require.Contains(t, javaProbeSources["munit"].source, "extends munit.FunSuite")
	require.True(t, strings.HasSuffix(javaProbeSources["scalatest"].path, ".scala"), "Scala suites compile from src/test/scala")
	probe, reason, err := javaProbeFor(t.TempDir(), "core", "scalatest", nil)
	require.NoError(t, err)
	require.Empty(t, reason)
	require.Contains(t, probe.args, "-Dsuites="+javaProbeClass, "the scalatest-maven-plugin selects suites by its own property")
	require.Contains(t, probe.args, "-Dtest="+javaProbeClass, "suites Surefire runs through JUnit match -Dtest")
	require.Equal(t, "java", languageForFramework("testng").name())
	require.Equal(t, "JUnit 5", displayName("junit5"))
	require.Equal(t, "Java", displayName("java"))
}

func writeJDK(t *testing.T, version string) string {
	t.Helper()
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, "bin"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(home, "bin", "java"), []byte("#!/bin/sh\n"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(home, "release"), []byte("IMPLEMENTOR=\"Eclipse Adoptium\"\nJAVA_VERSION=\""+version+"\"\n"), 0644))
	return home
}

func TestJavaEnvironmentReadsTheJDKReleaseFile(t *testing.T) {
	home := writeJDK(t, "21.0.5")
	t.Setenv("JAVA_HOME", home)
	info := javaEnvironmentInfo(nil)
	require.Equal(t, "21.0.5", info.Version)
	require.Equal(t, "Java from JAVA_HOME", info.Source)

	// Without JAVA_HOME, Maven runs the java on PATH, often a symlink.
	other := writeJDK(t, "1.8.0_442")
	path := t.TempDir()
	require.NoError(t, os.Symlink(filepath.Join(other, "bin", "java"), filepath.Join(path, "java")))
	t.Setenv("JAVA_HOME", "")
	t.Setenv("PATH", path)
	info = javaEnvironmentInfo(nil)
	require.Equal(t, "1.8.0_442", info.Version)
	require.Equal(t, "8", javaMajor(info.Version))

	t.Setenv("PATH", t.TempDir())
	require.Empty(t, javaEnvironmentInfo(nil).Version, "no JDK, no version")

	// A configuration's own JAVA_HOME or PATH selects the JDK Maven runs.
	require.Equal(t, "21.0.5", javaEnvironmentInfo(map[string]string{"JAVA_HOME": home}).Version)
	require.Equal(t, "1.8.0_442", javaEnvironmentInfo(map[string]string{"PATH": path}).Version)
}

func javaCIFixture(t *testing.T, matrix string) string {
	t.Helper()
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "pom.xml"), `<project><artifactId>fixture</artifactId><dependencies><dependency><groupId>org.junit.jupiter</groupId><artifactId>junit-jupiter</artifactId></dependency></dependencies></project>`)
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0755))
	requireWriteFile(t, filepath.Join(root, ".github/workflows/ci.yml"), "jobs:\n  test:\n    strategy:\n      matrix:\n        java: "+matrix+"\n    steps:\n      - uses: actions/setup-java@v4\n        with: {java-version: '${{ matrix.java }}'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: java, api_key: '${{ secrets.DD_API_KEY }}', java-tracer-version: '1.67.1'}\n      - run: mvn -B clean install\n")
	return root
}

func TestJavaCIRuntimeCertifiesTheValidatedJDKOrAnAcceptedSubstitute(t *testing.T) {
	root := javaCIFixture(t, "['17', '21', '21.0.4']")
	drive := &Testdrive{repositoryRoot: root, framework: framework.NewMaven("junit5", nil), command: "mvn", args: []string{"-B", "clean", "verify"}, tracerVersion: "1.67.1"}
	ci, substitutions, err := drive.javaCIRuntime("17.0.20")
	require.NoError(t, err)
	require.Len(t, ci.Jobs, 3, "the CI step validated as mvn -B clean verify matches every row")
	status := map[string]string{}
	for _, job := range ci.Jobs {
		status[job.Runtime] = job.Status + ": " + job.Reason
	}
	require.Contains(t, status["17"], "compatible: dd-java-agent 1.67.1 is validated on this host's Java 17.0.20.")
	require.Contains(t, status["21"], "compatible: dd-java-agent 1.67.1 is validated on this host's Java 17.0.20 as a substitute for Java 21.")
	require.Equal(t, "compatible", ci.Status)
	require.Len(t, substitutions, 1, "one substitution per other CI JDK major")
	substitution := substitutions[0]
	require.Equal(t, []string{"21", "17.0.20", "Java"}, []string{substitution.CI, substitution.Local, substitution.Runtime})
	require.False(t, substitution.Accepted, "only the user accepts a substitute")
	require.Contains(t, substitution.blocker(), "validated on substitute Java 17.0.20")
	require.Contains(t, substitution.blocker(), "Accept this evidence with --accept-substitute-runtime, or set JAVA_HOME to an existing Java 21 JDK and rerun")
	drive.acceptSubstituteRuntime = true
	_, substitutions, err = drive.javaCIRuntime("17.0.20")
	require.NoError(t, err)
	require.True(t, substitutions[0].Accepted)
	require.Contains(t, substitutions[0].caveat(), "validated on substitute Java 17.0.20")

	drive.tracerVersion, drive.acceptSubstituteRuntime = "1.70.0", false
	ci, substitutions, err = drive.javaCIRuntime("17.0.20")
	require.NoError(t, err)
	require.Contains(t, ci.Jobs[0].Reason, "CI pins dd-java-agent 1.67.1, but this run validates 1.70.0")
	require.Empty(t, substitutions, "another agent release validates no row, on any JDK")

	drive.tracerVersion, drive.checkOnly = "1.67.1", true
	ci, substitutions, err = drive.javaCIRuntime("17.0.20")
	require.NoError(t, err)
	require.Equal(t, "inconclusive", ci.Jobs[0].Status, "configuration-only checks run nothing")
	require.Empty(t, substitutions)

	single := javaCIFixture(t, "['17']")
	drive = &Testdrive{repositoryRoot: single, framework: framework.NewMaven("junit5", nil), command: "mvn", args: []string{"-B", "clean", "verify"}, tracerVersion: "1.67.1"}
	ci, substitutions, err = drive.javaCIRuntime("17.0.20")
	require.NoError(t, err)
	require.Equal(t, "compatible", ci.Status)
	require.Empty(t, substitutions)
	drive.args = []string{"test"}
	ci, _, err = drive.javaCIRuntime("17.0.20")
	require.NoError(t, err)
	require.Contains(t, ci.Reason, "No CI consumer matches this Maven command", "a local command that CI does not run certifies no CI row")
}

func TestJavaRuntimeChecksMergeAcrossConfigurations(t *testing.T) {
	first := &onboard.RuntimeCheck{Jobs: []onboard.RuntimeFinding{{Location: "a", Status: "compatible"}, {Location: "b", Status: "not checked"}}}
	second := &onboard.RuntimeCheck{Jobs: []onboard.RuntimeFinding{{Location: "b", Status: "compatible"}}}
	merged := mergeJavaRuntimeChecks(first, second)
	require.Equal(t, "compatible", merged.Status)
	require.Len(t, merged.Jobs, 2)
	excluded := &onboard.RuntimeCheck{Jobs: []onboard.RuntimeFinding{{Location: "c", Status: "excluded"}}}
	summarizeJavaRuntime(excluded)
	require.Equal(t, "inconclusive", excluded.Status, "an instrumented consumer is required")
}

func TestJavaCoverageCountsOnlyApplicationSources(t *testing.T) {
	root := t.TempDir()
	for _, file := range []string{"src/main/java/example/Calc.java", "src/test/java/example/CalcTest.java", "core/src/main/java/example/Core.java", "src/integrationTest/java/example/Fixture.java", "core/src/testFixtures/java/example/Data.java", "src/it/simple/Verify.java"} {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, file)), 0755))
		requireWriteFile(t, filepath.Join(root, file), "class X {}")
	}
	facts := intake.Facts{TestCount: 2, Tests: []intake.Test{
		{Name: "adds", CoveredFiles: []string{"src/main/java/example/Calc.java", "src/test/java/example/CalcTest.java"}},
		{Name: "testOnly", CoveredFiles: []string{"src/test/java/example/CalcTest.java"}},
	}}
	coverage := javaSourceCoverage(root, facts, nil)
	require.Equal(t, "observed", coverage.Status)
	require.Equal(t, 1, coverage.CoveredTests, "test-source coverage alone does not qualify")
	require.Equal(t, "not observed", javaSourceCoverage(root, intake.Facts{TestCount: 1, Tests: []intake.Test{{Name: "testOnly", CoveredFiles: []string{"src/test/java/example/CalcTest.java"}}}}, nil).Status)
	require.Equal(t, "invalid", javaSourceCoverage(root, intake.Facts{TestCount: 1, Tests: []intake.Test{{Name: "x", CoveredFiles: []string{"src/main/java/example/Missing.java"}}}}, nil).Status)
	for _, file := range []string{"src/integrationTest/java/example/Fixture.java", "core/src/testFixtures/java/example/Data.java", "src/it/simple/Verify.java"} {
		facts := intake.Facts{TestCount: 1, Tests: []intake.Test{{Name: "fixtureOnly", CoveredFiles: []string{file}}}}
		require.Equal(t, "not observed", javaSourceCoverage(root, facts, nil).Status, "every test source tree is test code: %s", file)
	}
}

func TestJavaPinsTheCITracer(t *testing.T) {
	require.Equal(t, "1.67.1", javaLanguage{}.ciPinnedTracer(onboard.RuntimeFinding{TracerRequested: "1.67.1"}))
	require.Empty(t, javaLanguage{}.ciPinnedTracer(onboard.RuntimeFinding{TracerRequested: "latest"}))
	drive := &Testdrive{language: "java", tracerVersionDefaulted: true, tracerVersion: "latest"}
	drive.defaultToCITracer(onboard.RuntimeCheck{Jobs: []onboard.RuntimeFinding{{TracerRequested: "1.67.1", Status: "not checked"}, {TracerRequested: "1.70.0", Status: "excluded"}}})
	require.Equal(t, "1.67.1", drive.tracerVersion)
	require.Equal(t, "dd-java-agent@1.67.1", drive.tracerLabel)
	drive = &Testdrive{language: "java", tracerVersionDefaulted: true, tracerVersion: "latest"}
	drive.defaultToCITracer(onboard.RuntimeCheck{Jobs: []onboard.RuntimeFinding{{TracerRequested: "1.67.1"}, {TracerRequested: "1.70.0"}}})
	require.Equal(t, "latest", drive.tracerVersion, "different pins select no default")
}

func TestJavaAllConfigurationsComeFromCIDiscovery(t *testing.T) {
	_, _, handled, err := javaLanguage{}.selectCommand(commandRequest{runner: framework.NewMaven("junit5", nil), override: "", all: true})
	require.NoError(t, err)
	require.False(t, handled)
	_, _, _, err = javaLanguage{}.selectCommand(commandRequest{runner: framework.NewMaven("junit5", nil), override: "mvn -B clean install"})
	require.ErrorContains(t, err, "remove install, for example: mvn -B clean verify", "install would overwrite ~/.m2 artifacts")
}

func TestJavaPackageCheckFindsResourcesThatPackageTheReports(t *testing.T) {
	for _, tc := range []struct{ name, resources, status, reason string }{
		{"standard resources", `<resource><directory>src/main/resources</directory></resource>`, "passed", "none packages the reports"},
		{"no resources", ``, "passed", "Checked 0 Maven resource declarations"},
		{"root license only", `<resource><directory>${project.basedir}</directory><includes><include>LICENSE</include><include>*.txt</include></includes></resource>`, "passed", "none packages"},
		{"whole root", `<resource><directory>.</directory></resource>`, "failed", "add <exclude>.testoptimization/**</exclude>"},
		{"root json files", `<resource><directory>${basedir}</directory><includes><include>**/*.json</include></includes></resource>`, "failed", "including .testoptimization/"},
		{"root with reports excluded", `<resource><directory>.</directory><excludes><exclude>.testoptimization/</exclude></excludes></resource>`, "passed", "none packages"},
		{"unresolved directory", `<resource><directory>${shared}</directory></resource>`, "failed", "cannot resolve"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			requireWriteFile(t, filepath.Join(root, "pom.xml"), `<project><build><resources>`+tc.resources+`</resources></build></project>`)
			check := javaPackageExclusions(root, nil)
			require.Equal(t, tc.status, check.Status, check.Reason)
			require.Contains(t, check.Reason, tc.reason)
		})
	}
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "service"), 0755))
	requireWriteFile(t, filepath.Join(root, "service", "pom.xml"), `<project><build><resources><resource><directory>..</directory></resource></resources></build></project>`)
	require.Equal(t, "failed", javaPackageExclusions(root, nil).Status, "a module can package its parent directory")
}

func TestJavaWorkflowIsBoundToTheJDK(t *testing.T) {
	t.Setenv("JAVA_HOME", writeJDK(t, "17.0.20"))
	runtime, err := javaLanguage{}.workflowRuntime(&Testdrive{})
	require.NoError(t, err)
	require.Equal(t, "17.0.20", runtime)
	require.True(t, javaLanguage{}.reportRuntimeCurrent("", WorkflowEvidence{Runtime: "17.0.20"}))
	require.False(t, javaLanguage{}.reportRuntimeCurrent("", WorkflowEvidence{Runtime: "21.0.5"}), "a report from another JDK is stale")
	require.False(t, javaLanguage{}.reportRuntimeCurrent("", WorkflowEvidence{Node: currentNodeVersion()}), "Node does not stand in for the JDK")
	t.Setenv("JAVA_HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	_, err = javaLanguage{}.workflowRuntime(&Testdrive{})
	require.ErrorContains(t, err, "no JDK was found")
	require.Equal(t, "maven", javaLanguage{}.packageManager(""))
	require.Contains(t, javaLanguage{}.initialization("junit5"), "${{ env.MAVEN_OPTS }}")
	require.True(t, Coordinated("java"))
}

func TestJavaPackageCheckInspectsTheReactorsCommandsSelect(t *testing.T) {
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "pom.xml"), `<project><artifactId>plain</artifactId></project>`)
	requireWriteFile(t, filepath.Join(root, "pom-ci.xml"), `<project><artifactId>ci</artifactId><modules><module>core</module></modules></project>`)
	require.NoError(t, os.Mkdir(filepath.Join(root, "core"), 0755))
	requireWriteFile(t, filepath.Join(root, "core", "pom.xml"), `<project><artifactId>core</artifactId></project>`)
	drive := &Testdrive{repositoryRoot: root, workflowScope: &onboard.ValidationScope{Commands: []onboard.TestCommand{{Directory: ".", Command: "mvn -B -f pom-ci.xml verify"}}}}
	check, handled := javaLanguage{}.packageExclusions(drive, t.Context())
	require.True(t, handled)
	require.Equal(t, "passed", check.Status, check.Reason)
	requireWriteFile(t, filepath.Join(root, "pom-ci.xml"), `<project><artifactId>ci</artifactId><build><resources><resource><directory>.</directory></resource></resources></build></project>`)
	check, _ = javaLanguage{}.packageExclusions(drive, t.Context())
	require.Equal(t, "failed", check.Status, "the POM a command selects is inspected whatever its name")
	require.Contains(t, check.Reason, "pom-ci.xml packages")
	drive.workflowScope.Commands[0].Command = "mvn -f missing.xml verify"
	check, _ = javaLanguage{}.packageExclusions(drive, t.Context())
	require.Equal(t, "failed", check.Status, "an unreadable reactor cannot be ruled out")
}
