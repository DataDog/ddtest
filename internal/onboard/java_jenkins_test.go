// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
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
	root := javaJenkinsRepository(t, javaJenkinsfile(", additionalVariables: ["+javaPluginTracerURL+": '"+javaHandWrittenDownload()+"']"))
	confirmJavaJenkins(t, "17")
	ci := JavaCIConfiguration(root, "junit5", "")
	require.Len(t, ci.Jobs, 1, ci.Reason)
	require.Equal(t, "not checked", ci.Jobs[0].Status, ci.Jobs[0].Reason)
	require.Equal(t, javaSetupRelease(), ci.Jobs[0].TracerRequested)
	require.False(t, ci.Jobs[0].TracerFloating)

	for _, url := range []string{
		"https://dtdg.co/latest-java-tracer",
		// A custom host may serve another agent under a release's path.
		"https://mirror.example/com/datadoghq/dd-java-agent/1.67.1/dd-java-agent-1.67.1.jar",
		"https://github.com/example/fork/releases/download/v1.67.1/dd-java-agent-1.67.1.jar",
		// The path and the file name name different releases.
		"https://repo1.maven.org/maven2/com/datadoghq/dd-java-agent/1.67.1/dd-java-agent-1.68.0.jar",
	} {
		root = javaJenkinsRepository(t, javaJenkinsfile(", additionalVariables: ["+javaPluginTracerURL+": '"+url+"']"))
		ci = JavaCIConfiguration(root, "junit5", "")
		require.Equal(t, CodeIncompleteCISetup, ci.Jobs[0].Code, url)
		require.Contains(t, ci.Jobs[0].Reason, "which is not an exact tracer release", url)
	}
	release, _ := (javaDiscovery{}).ciPluginTracerRelease(map[string]string{javaPluginTracerURL: "https://github.com/DataDog/dd-trace-java/releases/download/v1.68.0/dd-java-agent-1.68.0.jar"})
	require.Equal(t, "1.68.0", release)
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
		javaPluginTracerURL + ": '" + javaHandWrittenDownload() + "'",
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
	require.Contains(t, string(data), `DD_SET_TRACER_VERSION_JAVA="`+javaSetupRelease()+`"`)

	instructions := providerSetupInstructions(root, jenkins{}, "java", "junit5", "", true)
	require.Contains(t, instructions, "1. Bind DD_API_KEY in those stages from a Jenkins credential")
	require.Contains(t, instructions, "make this edit without waiting for the credential")
	require.Contains(t, instructions, "before the Jenkinsfile change is merged: Jenkins fails a stage whose binding names a credential that does not exist")
	action := jenkinsContainer{}.apiKeyAction(true)
	require.Contains(t, action, "before the Jenkinsfile change is merged")
	require.NotContains(t, action, "Until it exists", "a binding to a missing credential fails the stage")
}

// In an image without Bash, which cannot parse Datadog's installer, a Java
// container stage runs the setup without Bash or curl: onboard --apply writes
// it with the release ddtest recommends and keeps its folders untracked,
// ddtest recognizes and certifies it, and the final report says a
// Bash-capable image would be simpler.
func TestJavaJenkinsShellSetupWithoutBash(t *testing.T) {
	root := javaJenkinsRepository(t, "pipeline {\n  agent none\n  stages {\n    stage('Test') {\n      agent { docker { image 'maven:3-alpine' } }\n      steps {\n        sh 'mvn -B verify'\n      }\n    }\n  }\n}\n")
	require.Empty(t, setupImageNote(root, "java", "junit5"), "no image advice")
	instructions := providerSetupInstructions(root, jenkins{}, "java", "junit5", "", true)
	require.Contains(t, instructions, "Set up Datadog Test Optimization without Bash or curl")
	require.Contains(t, instructions, "run in maven:3-alpine, which has no Bash")
	require.Contains(t, instructions, "which is simpler and more reliable")
	plan, err := PlanProviderSetup(root, "junit5", "")
	require.NoError(t, err)
	_, err = plan.Apply(true)
	require.NoError(t, err, "the image's missing Bash does not block the setup")
	data, err := os.ReadFile(filepath.Join(root, "Jenkinsfile"))
	require.NoError(t, err)
	require.Contains(t, string(data), `DD_TRACER_VERSION_JAVA="`+javaSetupRelease()+`"`)
	require.Contains(t, string(data), javaFallbackURL)
	require.Contains(t, string(data), fallbackComment[0])
	require.NotContains(t, string(data), "install_test_visibility")
	ignore, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	require.NoError(t, err)
	require.Contains(t, string(ignore), jenkinsTracerIgnore, "the setup's folders stay untracked")

	confirmJavaJenkins(t, "8")
	ci := JavaCIConfiguration(root, "junit5", "datadoghq.com")
	require.Len(t, ci.Jobs, 1, ci.Reason)
	job := ci.Jobs[0]
	require.Empty(t, job.Code, job.Reason)
	require.Equal(t, fallbackSetupName, job.Action)
	require.Equal(t, javaSetupRelease(), job.TracerRequested)
	caveats := strings.Join(CISetupCaveats(&ci), "\n")
	require.Contains(t, caveats, "Jenkinsfile / Test")
	require.Contains(t, caveats, "With a Bash-capable image, the installer would set Test Optimization up instead, which is simpler and more reliable")
	scope, err := DiscoverValidationScope(root, "junit5")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Equal(t, "mvn -B verify", scope.Commands[0].Command)
	require.Empty(t, scope.Commands[0].Environment["MAVEN_OPTS"], "the CI agent is local validation's")
}

// The Java form of the setup without Bash or curl installs an exact release
// and checks its SHA256; ddtest recognizes it as written, with its release
// and site, and never an edited copy.
func TestJavaShellSetupIsRecognizedAsWritten(t *testing.T) {
	for _, tc := range []struct{ site, release string }{{"datadoghq.com", javaSetupRelease()}, {"datadoghq.eu", "1.68.0"}} {
		text := fallbackSnippet{form: jenkinsSnippetForm, language: "java", version: `"` + tc.release + `"`, site: `"` + tc.site + `"`}.shell()
		file, parsed := parseCIShell(text)
		require.True(t, parsed)
		setup, consumed, ok := fallbackSetupAt(text, file.Stmts, 0, nil, jenkinsSnippetForm, jenkinsPluginResets())
		require.True(t, ok)
		require.Equal(t, 1, consumed)
		require.Empty(t, setup.Problem)
		require.True(t, setup.fallback && setup.Supported)
		require.Equal(t, map[string]string{"languages": "java", "api_key": "$DD_API_KEY", "site": tc.site, "java-tracer-version": tc.release}, setup.Inputs)
		require.Empty(t, fallbackSetupProblem(setup, "java-tracer-version", exactJavaRelease.MatchString, javaSetupRelease(), tc.site))
	}
	// It checks the agent's SHA256 whichever way it downloads it, runs under
	// set -u, where either option list may be unset, and the agent's path has
	// no whitespace, which the option lists split on.
	written := fallbackSnippet{form: jenkinsSnippetForm, language: "java", version: `"` + javaSetupRelease() + `"`, site: `"datadoghq.com"`}.shell()
	require.Contains(t, written, `sha256sum -c -`)
	require.Contains(t, written, `"$DD_TRACER_FOLDER/dd-java-agent.jar.sha256" `+javaFallbackURL+`.sha256`)
	require.Contains(t, written, `does not match its SHA256`)
	require.Contains(t, written, `${MAVEN_OPTS:-}`)
	require.Contains(t, written, `${GRADLE_OPTS:-}`)
	require.Contains(t, written, `case "$DD_TRACER_BASE" in *[[:space:]]*) DD_TRACER_BASE="${TMPDIR:-/tmp}" ;; esac`)
	require.Contains(t, written, `mktemp -d "$DD_TRACER_BASE/.datadog-ci-container.XXXXXX"`)
	require.NotContains(t, written, `"$PWD/`, "the folder's base is chosen first")
	edited := strings.Replace(written, "GRADLE_OPTS=", "JAVA_OPTS=", 1)
	file, parsed := parseCIShell(edited)
	require.True(t, parsed)
	setup, _, ok := fallbackSetupAt(edited, file.Stmts, 0, nil, jenkinsSnippetForm, jenkinsPluginResets())
	require.True(t, ok)
	require.Contains(t, setup.Problem, "a form ddtest does not recognize", "an edited setup is never certified")
}

// Where a datadog(…) call ddtest cannot read may reach a container stage
// without Bash, the Java setup without Bash or curl first removes the
// plugin's -javaagent from MAVEN_OPTS and GRADLE_OPTS: the instructions and
// onboard --apply write that reset first, and ddtest accepts its own reset.
func TestJavaJenkinsShellSetupResetsThePlugin(t *testing.T) {
	scripted := func(script string) string {
		return "def options = [testOptimization: [enabled: true]]\ndatadog(options) {\n  node('docker') {\n    stage('Test') {\n      docker.image('maven:3-alpine').inside {\n        withCredentials([string(credentialsId: 'datadog-api-key', variable: 'DD_API_KEY')]) {\n          sh '''\n" + script + "\n          '''\n        }\n      }\n    }\n  }\n}\n"
	}
	reset := javaDiscovery{}.ciPluginReset()
	require.True(t, strings.HasPrefix(reset, "{ ") && strings.HasSuffix(reset, "; }"), "one compound statement")
	request := ciSetupRequest{Language: "java", Site: "datadoghq.com", TracerVersion: javaSetupRelease()}
	fallback := newImageFallback(jenkinsContainer{reset: true}, "maven:3-alpine", "no Bash")
	written := fallback.snippet(request).shell()
	require.Contains(t, written, fallbackComment[1]+"\n"+reset+"\nif [ \"${DD_API_KEY:+set}\" = set ]; then")
	require.Contains(t, fallback.setupInstructions(request), reset)
	require.NotContains(t, newImageFallback(jenkinsContainer{}, "maven:3-alpine", "no Bash").snippet(request).shell(), reset)

	for name, tc := range map[string]struct {
		script  string
		problem string
	}{
		"with the reset":    {written, ""},
		"without the reset": {fallbackSnippet{form: jenkinsSnippetForm, language: "java", version: `"` + javaSetupRelease() + `"`, site: `"datadoghq.com"`}.shell(), reset},
	} {
		t.Run(name, func(t *testing.T) {
			jobs := jenkinsJobs(t, scripted(tc.script+"\nmvn -B verify"))
			inside := jobs["Test (docker.image('maven:3-alpine').inside)"]
			require.NotNil(t, inside.Steps[0].Setup)
			setup := *inside.Steps[0].Setup
			require.True(t, setup.fallback)
			if tc.problem == "" {
				require.Empty(t, setup.Problem)
				require.False(t, jenkinsResetMissing(setup))
				require.Equal(t, []string{"SETUP", "mvn -B verify"}, jenkinsRuns(inside))
				return
			}
			require.True(t, jenkinsResetMissing(setup))
			require.Contains(t, setup.Problem, tc.problem)
		})
	}

	// The reset keeps the job's own options after the plugin's agent.
	for _, key := range []string{"MAVEN_OPTS", "GRADLE_OPTS"} {
		shell := exec.Command("sh", "-u", "-c", reset+`; printf '%s' "$`+key+`"`)
		shell.Env = []string{key + "=-javaagent:/w/.datadog/dd-java-agent.jar -Xmx1g"}
		output, err := shell.CombinedOutput()
		require.NoError(t, err, string(output))
		require.Equal(t, "-Xmx1g", string(output), key)
	}
}

// The Java setup without Bash or curl downloads dd-java-agent and its
// SHA256 with curl, or else wget, checks it, and attaches it only when it
// matches; the agent's path has no whitespace even where the workspace's
// has, since MAVEN_OPTS and GRADLE_OPTS split on it.
func TestJavaShellSetupDownloadsAndChecksTheAgent(t *testing.T) {
	agent := "dd-java-agent " + javaSetupRelease()
	sum := sha256.Sum256([]byte(agent))
	download := "#!/bin/sh\nout= url=\nwhile [ $# -gt 0 ]; do case \"$1\" in -o|-qO) out=\"$2\"; shift 2 ;; -*) shift ;; *) url=\"$1\"; shift ;; esac; done\n" +
		"case \"$url\" in https://repo1.maven.org/maven2/com/datadoghq/dd-java-agent/" + javaSetupRelease() + "/dd-java-agent-" + javaSetupRelease() + ".jar) printf '%s' '" + agent + "' >\"$out\" ;; *.jar.sha256) printf '%s' \"$SUM\" >\"$out\" ;; *) exit 22 ;; esac\n"
	missing := "#!/bin/sh\nexit 127\n"
	for name, tc := range map[string]struct {
		curl, wget, sum string
		attached        bool
	}{
		"curl":             {download, missing, hex.EncodeToString(sum[:]), true},
		"wget":             {missing, download, hex.EncodeToString(sum[:]), true},
		"checksum differs": {download, missing, strings.Repeat("0", 64), false},
	} {
		for _, form := range []snippetForm{jenkinsSnippetForm, gitLabSnippetForm} {
			t.Run(name+" "+form.provider, func(t *testing.T) {
				tools, temporary := t.TempDir(), t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(tools, "curl"), []byte(tc.curl), 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(tools, "wget"), []byte(tc.wget), 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(tools, "java"), []byte(missing), 0o755))
				workspace := filepath.Join(t.TempDir(), "my workspace")
				require.NoError(t, os.Mkdir(workspace, 0o755))
				text := fallbackSnippet{form: form, language: "java", version: `"` + javaSetupRelease() + `"`, site: `"datadoghq.com"`}.shell()
				shell := exec.Command("sh", "-u", "-c", text+"\nprintf '%s' \"${MAVEN_OPTS:-}|${GRADLE_OPTS:-}\"")
				shell.Dir = workspace
				shell.Env = []string{"PATH=" + tools + ":/usr/bin:/bin", "DD_API_KEY=key", "SUM=" + tc.sum, "TMPDIR=" + temporary, "MAVEN_OPTS=-Xmx1g"}
				output, err := shell.Output()
				require.NoError(t, err, string(output))
				if !tc.attached {
					require.Equal(t, "-Xmx1g|", string(output), "an agent that does not match is never attached")
					return
				}
				maven, gradle, _ := strings.Cut(string(output), "|")
				path, ok := strings.CutPrefix(maven, "-javaagent:")
				require.True(t, ok, maven)
				path, ok = strings.CutSuffix(path, " -Xmx1g")
				require.True(t, ok, maven)
				require.NotContains(t, path, " ", "the agent's path has no whitespace")
				require.True(t, strings.HasPrefix(path, temporary+"/"), path)
				require.Equal(t, "-javaagent:"+path+" ", gradle)
				data, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, agent, string(data))
			})
		}
	}
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
		{map[string]string{"DD_CIVISIBILITY_AGENTLESS_URL": "https://ci.example/intake"}, javaMaven, "DD_CIVISIBILITY_AGENTLESS_URL=https://ci.example/intake sends this step's reports to a custom URL"},
	} {
		require.Equal(t, tc.reason == "", javaReportingOverride(tc.env, tc.tool, "datadoghq.com") == "", "%v", tc.env)
		require.Contains(t, javaReportingOverride(tc.env, tc.tool, "datadoghq.com"), tc.reason, "%v", tc.env)
	}
	require.Empty(t, javaReportingOverride(map[string]string{"MAVEN_OPTS": "-Ddd.site=datadoghq.eu"}, javaMaven, ""), "any site when none is selected")

	// A matrix row's value is the one CI passes.
	workflow := ciWorkflow{}
	job := runtimeJob{Env: map[string]string{"JAVA_TOOL_OPTIONS": "-Ddd.site=${{ matrix.site }}"}}
	env := javaRowEnvironment(workflow, job, runtimeStep{}, map[string]any{"site": "datadoghq.com"})
	require.Equal(t, "-Ddd.site=datadoghq.com", env["JAVA_TOOL_OPTIONS"])
	require.Empty(t, javaReportingOverride(env, javaMaven, "datadoghq.com"))
	env = javaRowEnvironment(workflow, job, runtimeStep{}, map[string]any{"site": "datadoghq.eu"})
	require.Contains(t, javaReportingOverride(env, javaMaven, "datadoghq.com"), "-Ddd.site=datadoghq.eu in JAVA_TOOL_OPTIONS")

	// Agentless reporting turned off reports through an Agent, which only the
	// user can confirm; a later list may turn it back on.
	agent := map[string]string{"JAVA_TOOL_OPTIONS": "-Ddd.civisibility.agentless.enabled=false"}
	require.Contains(t, javaReportingOverride(agent, javaMaven, "datadoghq.com"), "turns agentless reporting off")
	require.NotContains(t, javaReportingOverride(agent, javaMaven, "datadoghq.com"), "--ci-confirm", "GitHub Actions defines no Agent fact")
	require.Empty(t, javaReportingOverride(map[string]string{"JAVA_TOOL_OPTIONS": "-Ddd.civisibility.agentless.enabled=false", "MAVEN_OPTS": "-Ddd.civisibility.agentless.enabled=true"}, javaMaven, "datadoghq.com"))
	useCIProvider(t, gitLabCIProvider)
	require.Contains(t, javaReportingOverride(agent, javaMaven, "datadoghq.com"), "--ci-confirm datadog-agent")
	require.NoError(t, UseCIStatements(nil, []CIFact{{Key: handWrittenAgent}}))
	t.Cleanup(func() { require.NoError(t, UseCIStatements(nil, nil)) })
	require.Empty(t, javaReportingOverride(agent, javaMaven, "datadoghq.com"), "a confirmed Agent")
}

// Java's plugin reset is one statement, which the snippet check reads, so a
// container stage an unread datadog(…) may reach accepts ddtest's own reset.
func TestJavaJenkinsPluginResetIsOneStatement(t *testing.T) {
	reset := (javaDiscovery{}).ciPluginReset()
	parsed, ok := parseCIShell(reset)
	require.True(t, ok)
	require.Len(t, parsed.Stmts, 1)
	setup := ciSetup{Inputs: map[string]string{"languages": (javaDiscovery{}).actionLanguage()}, before: []string{shellText(parsed.Stmts[0])}}
	require.False(t, jenkinsResetMissing(setup), "ddtest accepts its own reset")
	require.True(t, jenkinsResetMissing(ciSetup{Inputs: setup.Inputs}), "a snippet without it still lacks it")
}

// A pipeline whose host stages get the plugin, which downloads the latest
// release, and whose container stages get the snippet, which installs the
// recommended one, cannot be certified by one validation, so apply asks to
// pin the plugin first.
// mixedJenkinsfile runs Maven on a host-agent stage and in a container stage.
const mixedJenkinsfile = "pipeline {\n  agent none\n  stages {\n    stage('Host') {\n      agent { label 'java' }\n      steps {\n        sh 'mvn -B verify'\n      }\n    }\n    stage('Container') {\n      agent { docker { image 'maven:3.9-eclipse-temurin-17' } }\n      steps {\n        sh 'mvn -B verify'\n      }\n    }\n  }\n}\n"

func TestJavaJenkinsMixedStagesPinThePluginFirst(t *testing.T) {
	root := javaJenkinsRepository(t, mixedJenkinsfile)
	_, err := PlanProviderSetup(root, "junit5", "")
	var gap *EditThenApplyGap
	require.ErrorAs(t, err, &gap)
	require.Contains(t, gap.Error(), "downloads the latest tracer, but this container stage's snippet installs "+javaSetupRelease())
	require.Contains(t, gap.Error(), "Pin the plugin to that release")

	// Once the host stage's plugin is pinned, the container snippet installs
	// the same release, and the rerun applies.
	pinned := strings.Replace(mixedJenkinsfile, "      agent { label 'java' }\n", "      agent { label 'java' }\n      options {\n        datadog(testOptimization: [enabled: true, serviceName: 'app', languages: ['JAVA'], additionalVariables: ["+javaPluginTracerURL+": '"+javaHandWrittenDownload()+"']])\n      }\n", 1)
	writeFile(t, root, "Jenkinsfile", pinned)
	plan, err := PlanProviderSetup(root, "junit5", "")
	require.NoError(t, err)
	_, err = plan.Apply(true)
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(root, "Jenkinsfile"))
	require.NoError(t, err)
	require.Contains(t, string(data), `DD_SET_TRACER_VERSION_JAVA="`+javaSetupRelease()+`"`, "the snippet installs the plugin's release")
}

// javaJenkinsHandWrittenStage is a Jenkins stage that downloads an exact
// dd-java-agent and attaches it by hand, with the key from a credential.
func javaJenkinsHandWrittenStage(name, agent string) string {
	return "    stage('" + name + "') {\n" + agent +
		"      environment {\n        MAVEN_OPTS = '-javaagent:dd-java-agent.jar'\n        DD_CIVISIBILITY_ENABLED = 'true'\n        DD_CIVISIBILITY_AGENTLESS_ENABLED = 'true'\n        DD_SITE = 'datadoghq.com'\n        DD_API_KEY = credentials('datadog-api-key')\n      }\n" +
		"      steps {\n        sh 'curl -Lo dd-java-agent.jar https://repo1.maven.org/maven2/com/datadoghq/dd-java-agent/1.67.1/dd-java-agent-1.67.1.jar'\n        sh 'mvn -B verify'\n      }\n    }\n"
}

// onboard --apply leaves a stage that sets dd-java-agent up by hand to that
// setup: no plugin option over it, no snippet inside it, and the option
// refused where it could only reach it too.
func TestJavaJenkinsApplyKeepsHandWrittenStages(t *testing.T) {
	// A host stage of its own: nothing to add.
	root := javaJenkinsRepository(t, "pipeline {\n  agent { label 'java' }\n  stages {\n"+javaJenkinsHandWrittenStage("Manual", "")+"  }\n}\n")
	confirmJavaJenkins(t, "21")
	plan, err := PlanProviderSetup(root, "junit5", "datadoghq.com")
	require.NoError(t, err)
	require.True(t, plan.Configured(), "the hand-written stage keeps its setup")
	// A container stage of its own: no snippet inside it.
	root = javaJenkinsRepository(t, "pipeline {\n  agent none\n  stages {\n"+javaJenkinsHandWrittenStage("Manual", "      agent { docker { image 'maven:3.9-eclipse-temurin-21' } }\n")+"  }\n}\n")
	plan, err = PlanProviderSetup(root, "junit5", "datadoghq.com")
	require.NoError(t, err)
	require.True(t, plan.Configured())
	// Beside another host stage under one pipeline-wide option: refused.
	root = javaJenkinsRepository(t, "pipeline {\n  agent { label 'java' }\n  stages {\n"+javaJenkinsHandWrittenStage("Manual", "")+"    stage('Test') {\n      steps {\n        sh 'mvn -B verify'\n      }\n    }\n  }\n}\n")
	_, err = PlanProviderSetup(root, "junit5", "datadoghq.com")
	require.ErrorContains(t, err, "its option would also apply to Jenkinsfile / Manual, which set up the tracer by hand")
}
