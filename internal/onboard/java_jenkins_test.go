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
	require.Equal(t, javaHandWrittenExample, ci.Jobs[0].TracerRequested)
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

	// A value that may select tests still requires review.
	root = javaJenkinsRepository(t, "pipeline {\n  agent { label 'java' }\n  stages {\n    stage('Test') { steps { sh \"mvn verify -Dgroups=$GROUPS\" } }\n  }\n}\n")
	scope, err = DiscoverValidationScope(root, "junit5")
	require.NoError(t, err)
	require.NotEmpty(t, scope.Unresolved)
}
