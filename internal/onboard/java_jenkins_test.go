// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// javaJenkinsfile is a declarative pipeline on a host agent whose Test stage
// runs Maven, with the Datadog Jenkins plugin enabled for Java and options
// its datadog option adds.
func javaJenkinsfile(options string) string {
	return "pipeline {\n  agent { label 'java' }\n  options {\n    datadog(testOptimization: [enabled: true, serviceName: 'app', languages: ['JAVA']" + options + "])\n  }\n  stages {\n    stage('Test') { steps { sh 'mvn -B verify' } }\n  }\n}\n"
}

func javaJenkinsRepository(t *testing.T, jenkinsfile string) string {
	t.Helper()
	root := jenkinsRepository(t, jenkinsfile)
	writeFile(t, root, "pom.xml", javaJUnit5POM)
	return root
}

// confirmJavaJenkins states the agents' JDK and the plugin's facts.
func confirmJavaJenkins(t *testing.T, java string) {
	t.Helper()
	statement, err := ParseCIRuntime("java=" + java)
	require.NoError(t, err)
	require.NoError(t, UseCIStatements([]CIRuntimeStatement{statement}, []CIFact{{Key: jenkinsPluginFact}, {Key: jenkinsModeFact, Value: "agentless:datadoghq.com"}}))
	t.Cleanup(func() { require.NoError(t, UseCIStatements(nil, nil)) })
}

// The Datadog Jenkins plugin sets Java up on a host agent: ddtest asks for
// the plugin's facts and the agents' JDK, which no Jenkinsfile shows, and
// validates the latest dd-java-agent, which the plugin downloads.
func TestJavaJenkinsPlugin(t *testing.T) {
	root := javaJenkinsRepository(t, javaJenkinsfile(""))
	require.NoError(t, CheckCIProviderSupport(root, "java", "junit5"))
	scope, err := DiscoverValidationScope(root, "junit5")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	require.Equal(t, "mvn -B verify", scope.Commands[0].Command)
	require.Empty(t, scope.Commands[0].Environment["MAVEN_OPTS"], "the plugin's agent is local validation's")

	ci := JavaCIConfiguration(root, "junit5", "datadoghq.com")
	require.Len(t, ci.Jobs, 1, ci.Reason)
	require.Equal(t, "ci_setup_confirmation_required", ci.Jobs[0].Code)
	for _, asked := range []string{"--ci-confirm jenkins-plugin", "jenkins-plugin-mode=agentless:<site>|agent", "--ci-runtime java=<version>"} {
		require.Contains(t, ci.Jobs[0].Reason, asked)
	}

	confirmJavaJenkins(t, "21")
	ci = JavaCIConfiguration(root, "junit5", "datadoghq.com")
	require.Len(t, ci.Jobs, 1, ci.Reason)
	job := ci.Jobs[0]
	require.Equal(t, "not checked", job.Status, job.Reason)
	require.Equal(t, "21", job.Runtime)
	require.True(t, job.UserStated)
	require.Equal(t, "latest", job.TracerRequested)
	require.True(t, job.TracerFloating)
	require.Contains(t, strings.Join(job.SetupCaveats, "\n"), "downloads the latest dd-java-agent when each build runs")

	// The plugin reports to the confirmed Agentless site.
	ci = JavaCIConfiguration(root, "junit5", "datadoghq.eu")
	require.Contains(t, ci.Jobs[0].Reason, "reports to datadoghq.com")
}

// additionalVariables pin dd-java-agent by replacing the plugin's download
// with an exact release's.
func TestJavaJenkinsPluginPinnedRelease(t *testing.T) {
	root := javaJenkinsRepository(t, javaJenkinsfile(", additionalVariables: ["+javaPluginTracerURL+": '"+javaHandWrittenDownload+"']"))
	confirmJavaJenkins(t, "17")
	ci := JavaCIConfiguration(root, "junit5", "")
	require.Len(t, ci.Jobs, 1, ci.Reason)
	require.Equal(t, "not checked", ci.Jobs[0].Status, ci.Jobs[0].Reason)
	require.Equal(t, javaRecommendedTracer, ci.Jobs[0].TracerRequested)
	require.False(t, ci.Jobs[0].TracerFloating)

	root = javaJenkinsRepository(t, javaJenkinsfile(", additionalVariables: ["+javaPluginTracerURL+": 'https://dtdg.co/latest-java-tracer']"))
	ci = JavaCIConfiguration(root, "junit5", "")
	require.Equal(t, CodeIncompleteCISetup, ci.Jobs[0].Code)
	require.Contains(t, ci.Jobs[0].Reason, "which is not an exact tracer release")
}

// The plugin computes MAVEN_OPTS once per agent from the first step it
// configures, so a stage's own value is not the one its steps get.
func TestJavaJenkinsPluginComposesMavenOpts(t *testing.T) {
	require.Contains(t, (javaDiscovery{}).ciPluginComposes("MAVEN_OPTS"), "its -javaagent option followed by a space and the MAVEN_OPTS of the first step it configures there")
	require.Empty(t, (javaDiscovery{}).ciPluginComposes("JAVA_OPTS"))
	exported, references := (javaDiscovery{}).ciPluginExports(map[string]string{"MAVEN_OPTS": "-Xmx1g"})
	require.Nil(t, references)
	require.Equal(t, map[string]string{"MAVEN_OPTS": javaInstallerAgent + " -Xmx1g", "GRADLE_OPTS": javaInstallerAgent}, exported)
}

// The plugin's setup instructions for Java say how it attaches the agent
// and how to pin it.
func TestJavaJenkinsInstructions(t *testing.T) {
	root := javaJenkinsRepository(t, "pipeline {\n  agent { label 'java' }\n  stages {\n    stage('Test') { steps { sh 'mvn -B verify' } }\n  }\n}\n")
	text := providerSetupInstructions(root, jenkins{}, "java", "junit5", "datadoghq.com", true)
	for _, phrase := range []string{
		"languages: ['JAVA']",
		javaPluginTracerURL + ": '" + javaHandWrittenDownload + "'",
		"puts -javaagent before the MAVEN_OPTS and GRADLE_OPTS of every step",
		"--ci-runtime java=<version>",
	} {
		require.Contains(t, text, phrase)
	}
}

// A credential or Sonar setting a JVM option passes, with a value only the
// build knows, signs, publishes or reports, and selects no tests: validation
// runs with it empty and says so, as kfgodel/bean2bean's pipeline does.
func TestJavaJenkinsValidatesWithoutCIOnlyOptions(t *testing.T) {
	root := javaJenkinsRepository(t, `pipeline {
  agent { label 'java' }
  options {
    datadog(testOptimization: [enabled: true, serviceName: 'app', languages: ['JAVA']])
  }
  stages {
    stage('Build') { steps { sh "mvn verify -Dgpg.passphrase=$GPG_PASSPHRASE -e" } }
    stage('Sonar') {
      steps {
        withSonarQubeEnv(installationName: 'sonar') {
          sh "mvn $SONAR_MAVEN_GOAL -Dsonar.host.url=$SONAR_HOST_URL -e"
        }
      }
    }
  }
}
`)
	scope, err := DiscoverValidationScope(root, "junit5")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	require.Equal(t, "mvn verify -Dgpg.passphrase= -e", scope.Commands[0].Command)
	require.Equal(t, []string{"-Dgpg.passphrase"}, scope.Commands[0].WithoutCIValues)
	require.Contains(t, strings.Join(scope.Caveats, "\n"), "validated without the CI values of -Dgpg.passphrase")
	require.Contains(t, strings.Join(scope.Review, "\n"), "(mvn sonar:sonar -Dsonar.host.url= -e): Maven goals outside the test lifecycle", "withSonarQubeEnv sets the scanner's goal")

	// A value that may configure the tests still requires review, even a
	// credential.
	for _, option := range []string{"-Dgroups=$GROUPS", "-Ddb.password=$DB_PASSWORD"} {
		root = javaJenkinsRepository(t, "pipeline {\n  agent { label 'java' }\n  stages {\n    stage('Test') { steps { sh \"mvn verify "+option+"\" } }\n  }\n}\n")
		scope, err = DiscoverValidationScope(root, "junit5")
		require.NoError(t, err)
		require.NotEmpty(t, scope.Unresolved, option)
	}
}

// dd-java-agent is no project dependency, so a container stage's snippet
// installs the release ddtest recommends, and the Jenkins credential is a
// human action after the edit, not a question before it.
func TestJavaJenkinsContainerStageApply(t *testing.T) {
	root := javaJenkinsRepository(t, "pipeline {\n  agent none\n  stages {\n    stage('Test') {\n      agent { docker { image 'maven:3.9-eclipse-temurin-17' } }\n      steps {\n        sh 'mvn -B verify'\n      }\n    }\n  }\n}\n")
	plan, err := PlanProviderSetup(root, "junit5", "")
	require.NoError(t, err)
	_, err = plan.Apply(true)
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(root, "Jenkinsfile"))
	require.NoError(t, err)
	require.Contains(t, string(data), `DD_SET_TRACER_VERSION_JAVA="`+javaRecommendedTracer+`"`)

	instructions := providerSetupInstructions(root, jenkins{}, "java", "junit5", "", true)
	require.Contains(t, instructions, "1. Bind DD_API_KEY in those stages from a Jenkins credential")
	require.Contains(t, instructions, "make this edit without waiting for the credential")
}

// In an image without Bash, which cannot parse Datadog's installer, a Java
// container stage runs ddtest's sh setup: onboard --apply writes it, ddtest
// recognizes and certifies it, and says a Bash-capable image would be simpler.
func TestJavaJenkinsShellSetupWithoutBash(t *testing.T) {
	root := javaJenkinsRepository(t, "pipeline {\n  agent none\n  stages {\n    stage('Test') {\n      agent { docker { image 'maven:3-alpine' } }\n      steps {\n        sh 'mvn -B verify'\n      }\n    }\n  }\n}\n")
	require.Contains(t, setupImageNote(root, "java", "junit5", "datadoghq.com"), "In the sh script that runs their tests, put this setup, which a POSIX shell runs")
	plan, err := PlanProviderSetup(root, "junit5", "")
	require.NoError(t, err)
	_, err = plan.Apply(true)
	require.NoError(t, err, "the image's missing Bash no longer blocks the setup")
	data, err := os.ReadFile(filepath.Join(root, "Jenkinsfile"))
	require.NoError(t, err)
	require.Contains(t, string(data), "dd-java-agent/"+javaRecommendedTracer+"/dd-java-agent-"+javaRecommendedTracer+".jar")
	require.NotContains(t, string(data), "install_test_visibility")

	confirmJavaJenkins(t, "8")
	ci := JavaCIConfiguration(root, "junit5", "datadoghq.com")
	require.Len(t, ci.Jobs, 1, ci.Reason)
	job := ci.Jobs[0]
	require.Empty(t, job.Code, job.Reason)
	require.Equal(t, javaRecommendedTracer, job.TracerRequested)
	require.Contains(t, job.SetupCaveats, javaShellSetupCaveat)
	scope, err := DiscoverValidationScope(root, "junit5")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Equal(t, "mvn -B verify", scope.Commands[0].Command)
	require.Empty(t, scope.Commands[0].Environment["MAVEN_OPTS"], "the CI agent is local validation's")
}

func TestJavaShellSetupIsRecognizedAsWritten(t *testing.T) {
	java := javaDiscovery{}
	for _, tc := range []struct{ site, release string }{{"datadoghq.com", "1.67.1"}, {"datadoghq.eu", "1.68.0"}} {
		file, parsed := parseCIShell(java.shellSetup(tc.site, tc.release, jenkinsSnippetForm))
		require.True(t, parsed)
		setup, consumed, ok := java.shellSetupAt(file.Stmts, 0, jenkinsSnippetForm)
		require.True(t, ok)
		require.Equal(t, 1, consumed)
		require.Empty(t, setup.Problem)
		require.True(t, setup.withoutBash && setup.Supported)
		require.Equal(t, map[string]string{"languages": "java", "api_key": "$DD_API_KEY", "site": tc.site, "java-tracer-version": tc.release}, setup.Inputs)
	}
	edited := strings.Replace(java.shellSetup("datadoghq.com", "1.67.1", jenkinsSnippetForm), "GRADLE_OPTS=", "JAVA_OPTS=", 1)
	file, parsed := parseCIShell(edited)
	require.True(t, parsed)
	setup, _, ok := java.shellSetupAt(file.Stmts, 0, jenkinsSnippetForm)
	require.True(t, ok)
	require.Contains(t, setup.Problem, "a form ddtest does not recognize", "an edited setup is never certified")
}

// Each Jenkins sh step is a shell of its own: a setup snippet in one step
// does not instrument the tests a later step runs.
func TestJavaJenkinsSetupExpiresWithItsShell(t *testing.T) {
	root := javaJenkinsRepository(t, "pipeline {\n  agent none\n  stages {\n    stage('Test') {\n      agent { docker { image 'maven:3.9-eclipse-temurin-17' } }\n      steps {\n        sh 'mvn -B verify'\n      }\n    }\n  }\n}\n")
	plan, err := PlanProviderSetup(root, "junit5", "")
	require.NoError(t, err)
	_, err = plan.Apply(true)
	require.NoError(t, err)
	path := filepath.Join(root, "Jenkinsfile")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	ci := JavaCIConfiguration(root, "junit5", "")
	require.Len(t, ci.Jobs, 1, ci.Reason)
	require.NotContains(t, ci.Jobs[0].Reason, "runs in another shell", "the snippet and the tests share the sh step")

	split := strings.Replace(string(data), "            mvn -B verify\n          '''\n", "          '''\n          sh 'mvn -B verify'\n", 1)
	require.NotEqual(t, string(data), split)
	writeFile(t, root, "Jenkinsfile", split)
	ci = JavaCIConfiguration(root, "junit5", "")
	require.Len(t, ci.Jobs, 1, ci.Reason)
	require.Equal(t, "inconclusive", ci.Jobs[0].Status)
	require.Contains(t, ci.Jobs[0].Reason, "runs in another shell than this test command", "the later sh step runs without the setup")
}

// The Datadog Jenkins plugin's MAVEN_OPTS and GRADLE_OPTS agent is removed
// before a container stage's setup, keeping the step's own options.
func TestJavaJenkinsPluginReset(t *testing.T) {
	reset := javaDiscovery{}.ciPluginReset()
	for _, key := range []string{"MAVEN_OPTS", "GRADLE_OPTS"} {
		require.Contains(t, reset, `case "${`+key+`:-}" in -javaagent:*/.datadog/dd-java-agent.jar*)`)
	}
	require.Contains(t, jenkinsPluginResets("java"), reset)
}

// A -Ddd.* property in the JVM options the build reads that reports
// elsewhere than the selected site is reported before replay removes it.
func TestJavaReportingOverrides(t *testing.T) {
	for _, tc := range []struct {
		env    map[string]string
		tool   string
		reason string
	}{
		{map[string]string{"JAVA_TOOL_OPTIONS": "-Ddd.site=datadoghq.eu"}, javaMaven, "-Ddd.site=datadoghq.eu in JAVA_TOOL_OPTIONS sends this step's reports to datadoghq.eu, not datadoghq.com"},
		{map[string]string{"JAVA_TOOL_OPTIONS": "-Ddd.site=datadoghq.eu", "MAVEN_OPTS": "-Ddd.site=datadoghq.com"}, javaMaven, ""},
		{map[string]string{"JAVA_OPTS": "-Ddd.site=datadoghq.eu"}, javaMaven, ""},
		{map[string]string{"JAVA_OPTS": "-Ddd.site=datadoghq.eu"}, javaGradle, "in JAVA_OPTS"},
		{map[string]string{"GRADLE_OPTS": "-Ddd.civisibility.agentless.url=https://intake.example"}, javaGradle, "custom URL"},
	} {
		require.Equal(t, tc.reason == "", javaReportingOverride(tc.env, tc.tool, "datadoghq.com") == "", "%v", tc.env)
		require.Contains(t, javaReportingOverride(tc.env, tc.tool, "datadoghq.com"), tc.reason, "%v", tc.env)
	}
	require.Empty(t, javaReportingOverride(map[string]string{"MAVEN_OPTS": "-Ddd.site=datadoghq.eu"}, javaMaven, ""), "any site when none is selected")
}
