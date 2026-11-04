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

const handWrittenPOM = `<project><artifactId>service</artifactId><dependencies><dependency><groupId>org.junit.jupiter</groupId><artifactId>junit-jupiter</artifactId></dependency></dependencies></project>`

const handWrittenAgentDownload = "curl -Lo dd-java-agent.jar https://repo1.maven.org/maven2/com/datadoghq/dd-java-agent/1.67.1/dd-java-agent-1.67.1.jar"

// javaHandWrittenRepository is a repository whose provider configuration
// runs command after before with env, in a JDK 21 image.
func javaHandWrittenRepository(t *testing.T, provider handWrittenProvider, env map[string]string, before []string, command string, files map[string]string) string {
	t.Helper()
	config := strings.NewReplacer("cimg/node:22.11", "cimg/openjdk:21.0", "node:22", "maven:3.9-eclipse-temurin-21").Replace(provider.config(env, before, command))
	return unvalidatedRepository(t, provider, config, files)
}

// javaHandWrittenFinding is the Java CI check's finding of the job that
// sets dd-java-agent up by hand, which onboarding no longer stops at.
func javaHandWrittenFinding(t *testing.T, root string) RuntimeFinding {
	t.Helper()
	require.NoError(t, CheckCIProviderSupport(root, "java", "junit5"), "a hand-written Java setup is checked, not refused")
	ci := JavaCIConfiguration(root, "junit5", "datadoghq.com")
	require.Len(t, ci.Jobs, 1, ci.Reason)
	require.True(t, HandWrittenSetupAction(ci.Jobs[0].Action), ci.Jobs[0].Action)
	return ci.Jobs[0]
}

// A Maven or Gradle job that attaches dd-java-agent by hand is checked on
// every provider without the shared syntax: certified when it pins an exact
// release and configures reporting, with what it lacks otherwise.
func TestHandWrittenJavaSetups(t *testing.T) {
	agentless := map[string]string{"MAVEN_OPTS": "-javaagent:dd-java-agent.jar", "DD_CIVISIBILITY_ENABLED": "true", "DD_CIVISIBILITY_AGENTLESS_ENABLED": "true", "DD_SITE": "datadoghq.com"}
	for _, provider := range handWrittenProviders {
		t.Run(provider.name, func(t *testing.T) {
			root := javaHandWrittenRepository(t, provider, agentless, []string{handWrittenAgentDownload}, "mvn -B test", map[string]string{"pom.xml": handWrittenPOM})
			setup := firstJobHandWrittenSetup(t, root, "java", "junit5", "datadoghq.com")
			require.NotNil(t, setup)
			require.True(t, setup.complete(), setup.problem())
			finding := javaHandWrittenFinding(t, root)
			require.Equal(t, "hand-written dd-java-agent 1.67.1 setup (MAVEN_OPTS)", finding.Action)
			require.Equal(t, "1.67.1", finding.TracerRequested, "validation runs the release the job attaches")
			require.Equal(t, "not checked", finding.Status, finding.Reason)
			require.Contains(t, finding.Reason, "Validate dd-java-agent 1.67.1 on Java 21")
		})
	}
	provider := handWrittenProviders[0]
	gradleBuild := map[string]string{"settings.gradle": "rootProject.name = 'service'\n", "build.gradle": "plugins { id 'java' }\ndependencies { testImplementation 'org.junit.jupiter:junit-jupiter:5.11.0' }\ntest { useJUnitPlatform() }\n", "gradlew": "#!/bin/sh\n"}
	for _, tc := range []struct {
		name    string
		env     map[string]string
		before  []string
		command string
		files   map[string]string
		// reference is the setup's; problem and unresolved, one of what it
		// lacks and of what ddtest cannot read, each keeping it uncertified.
		reference, problem, unresolved string
	}{
		{name: "tool options and properties", env: map[string]string{"JAVA_TOOL_OPTIONS": "-javaagent:/opt/dd-java-agent-1.67.1.jar -Ddd.civisibility.enabled=true -Ddd.civisibility.agentless.enabled=true -Ddd.site=datadoghq.com"}, before: []string{"curl -Lo /opt/dd-java-agent-1.67.1.jar https://repo1.maven.org/maven2/com/datadoghq/dd-java-agent/1.67.1/dd-java-agent-1.67.1.jar"}, command: "mvn test", reference: "hand-written dd-java-agent 1.67.1 setup (JAVA_TOOL_OPTIONS)"},
		// The download writes exactly the path the agent is attached from.
		{name: "download elsewhere", env: with(agentless, "MAVEN_OPTS", "-javaagent:/opt/dd-java-agent.jar"), command: "mvn test", unresolved: "writes dd-java-agent.jar, not the /opt/dd-java-agent.jar the tests attach"},
		{name: "other absolute path", env: with(agentless, "MAVEN_OPTS", "-javaagent:/opt/dd-java-agent.jar"), before: []string{"curl -Lo /tmp/dd-java-agent.jar https://repo1.maven.org/maven2/com/datadoghq/dd-java-agent/1.67.1/dd-java-agent-1.67.1.jar"}, command: "mvn test", unresolved: "writes /tmp/dd-java-agent.jar, not the /opt/dd-java-agent.jar"},
		{name: "other spelling of the path", env: with(agentless, "MAVEN_OPTS", "-javaagent:./dd-java-agent.jar"), command: "mvn test", unresolved: "writes dd-java-agent.jar, not the ./dd-java-agent.jar"},
		{name: "same absolute path", env: with(agentless, "MAVEN_OPTS", "-javaagent:/opt/dd-java-agent.jar"), before: []string{"curl -Lo /opt/dd-java-agent.jar https://repo1.maven.org/maven2/com/datadoghq/dd-java-agent/1.67.1/dd-java-agent-1.67.1.jar"}, command: "mvn test", reference: "hand-written dd-java-agent 1.67.1 setup (MAVEN_OPTS)"},
		{name: "Maven Central's other host", env: agentless, before: []string{"curl -Lo dd-java-agent.jar https://repo.maven.apache.org/maven2/com/datadoghq/dd-java-agent/1.67.1/dd-java-agent-1.67.1.jar"}, command: "mvn test", reference: "hand-written dd-java-agent 1.67.1 setup (MAVEN_OPTS)"},
		// A release in the attached file's name says nothing of its bytes.
		{name: "versioned file, no download", env: with(agentless, "MAVEN_OPTS", "-javaagent:/opt/dd-java-agent-1.67.1.jar"), before: []string{}, command: "mvn test", problem: "No command of the job downloads /opt/dd-java-agent-1.67.1.jar with an exact release"},
		{name: "versioned file, latest download", env: with(agentless, "MAVEN_OPTS", "-javaagent:dd-java-agent-1.67.1.jar"), before: []string{"curl -Lo dd-java-agent-1.67.1.jar https://dtdg.co/latest-java-tracer"}, command: "mvn test", problem: "The job downloads the latest dd-java-agent"},
		{name: "custom mirror", before: []string{"curl -Lo dd-java-agent.jar https://mirror.example/com/datadoghq/dd-java-agent/1.67.1/dd-java-agent-1.67.1.jar"}, command: "mvn test", problem: "which is not an exact release's artifact on Maven Central or the DataDog/dd-trace-java GitHub releases"},
		{name: "GitHub release", before: []string{"wget -O dd-java-agent.jar https://github.com/DataDog/dd-trace-java/releases/download/v1.68.0/dd-java-agent-1.68.0.jar"}, command: "mvn test", reference: "hand-written dd-java-agent 1.68.0 setup (MAVEN_OPTS)"},
		{name: "GitHub release's unversioned asset", before: []string{"wget -O dd-java-agent.jar https://github.com/DataDog/dd-trace-java/releases/download/v1.68.0/dd-java-agent.jar"}, command: "mvn test", reference: "hand-written dd-java-agent 1.68.0 setup (MAVEN_OPTS)"},
		// A download that may not run does not tell which jar the tests attach.
		{name: "download in an if", before: []string{"if [ -n \"$PIN\" ]; then " + handWrittenAgentDownload + "; fi"}, command: "mvn test", unresolved: "inside conditional shell control flow"},
		{name: "download after &&", before: []string{"echo pinning && " + handWrittenAgentDownload}, command: "mvn test", unresolved: "inside conditional shell control flow"},
		// Any other command naming the agent may replace the downloaded bytes.
		{name: "conditional overwrite", before: []string{handWrittenAgentDownload, "if [ -n \"$MIRROR\" ]; then curl -Lo dd-java-agent.jar https://mirror.example/dd-java-agent.jar; fi"}, command: "mvn test", unresolved: "names the agent JAR in more than one command"},
		{name: "copied over", before: []string{handWrittenAgentDownload, "cp /cache/agent.jar dd-java-agent.jar"}, command: "mvn test", unresolved: "names the agent JAR in more than one command"},
		{name: "copied into its directory", before: []string{handWrittenAgentDownload, "cp /cache/dd-java-agent.jar ."}, command: "mvn test", unresolved: "names the agent JAR in more than one command"},
		{name: "attached wget output", before: []string{handWrittenAgentDownload, "wget -qOdd-java-agent.jar https://mirror.example/custom.bin"}, command: "mvn test", unresolved: "names the agent JAR in more than one command"},
		{name: "copied in", before: []string{"cp /cache/agent.jar dd-java-agent.jar"}, command: "mvn test", unresolved: "gets it with cp, not curl -o or wget -O"},
		{name: "output directory", before: []string{"curl --output-dir tools -Lo dd-java-agent.jar https://repo1.maven.org/maven2/com/datadoghq/dd-java-agent/1.67.1/dd-java-agent-1.67.1.jar"}, command: "mvn test", unresolved: "downloads with --output-dir"},
		{name: "cd before the tests", before: []string{"cd . && " + handWrittenAgentDownload}, command: "mvn test", unresolved: "changes directory with cd or pushd"},
		{name: "agent attached twice", env: with(agentless, "JAVA_TOOL_OPTIONS", "-javaagent:old/dd-java-agent.jar"), command: "mvn test", unresolved: "attaches dd-java-agent more than once (-javaagent:old/dd-java-agent.jar in JAVA_TOOL_OPTIONS; -javaagent:dd-java-agent.jar in MAVEN_OPTS)"},
		{name: "property enables over the variable", env: map[string]string{"MAVEN_OPTS": "-javaagent:dd-java-agent.jar -Ddd.civisibility.enabled=true", "DD_CIVISIBILITY_ENABLED": "false", "DD_CIVISIBILITY_AGENTLESS_ENABLED": "true", "DD_SITE": "datadoghq.com"}, command: "mvn test", reference: "hand-written dd-java-agent 1.67.1 setup (MAVEN_OPTS)"},
		{name: "signed in place", before: []string{handWrittenAgentDownload, "jarsigner dd-java-agent.jar release"}, command: "mvn test", unresolved: "names the agent JAR in more than one command"},
		// The simple form names the agent once; even a read leaves it uncertified.
		{name: "checksum", before: []string{handWrittenAgentDownload, "sha256sum dd-java-agent.jar"}, command: "mvn test", unresolved: "names the agent JAR in more than one command"},
		{name: "release from the shell's state there", before: []string{"TRACER_VERSION=1.67.1; curl -Lo dd-java-agent.jar https://repo1.maven.org/maven2/com/datadoghq/dd-java-agent/${TRACER_VERSION}/dd-java-agent-${TRACER_VERSION}.jar; TRACER_VERSION=1.68.0"}, command: "mvn test", reference: "hand-written dd-java-agent 1.67.1 setup (MAVEN_OPTS)"},
		{name: "several URLs", before: []string{"curl -o dd-java-agent.jar https://mirror.example/custom.jar https://repo1.maven.org/maven2/com/datadoghq/dd-java-agent/1.67.1/dd-java-agent-1.67.1.jar"}, command: "mvn test", unresolved: "downloads 2 URLs in one command"},
		{name: "latest", before: []string{"curl -Lo dd-java-agent.jar https://dtdg.co/latest-java-tracer"}, command: "mvn test", reference: "hand-written dd-java-agent setup (MAVEN_OPTS)", problem: "The job downloads the latest dd-java-agent"},
		{name: "no download", before: []string{}, command: "mvn test", problem: "No command of the job downloads dd-java-agent.jar with an exact release"},
		{name: "another tool's release", before: []string{"curl -Lo java-helper.zip https://github.com/example/tools/releases/download/v1.67.1/java-helper.zip"}, command: "mvn test", problem: "No command of the job downloads dd-java-agent.jar with an exact release"},
		{name: "not enabled", env: with(agentless, "DD_CIVISIBILITY_ENABLED", ""), command: "mvn test", problem: "set DD_CIVISIBILITY_ENABLED=true"},
		{name: "literal key", env: with(agentless, "DD_API_KEY", "0123456789abcdef"), command: "mvn test", problem: "never commit the key"},
		{name: "Gradle reads no MAVEN_OPTS", command: "./gradlew test", files: gradleBuild, problem: "this job runs Gradle, which does not read it"},
		{name: "Gradle JVM arguments", env: with(agentless, "MAVEN_OPTS", ""), command: "./gradlew test -Dorg.gradle.jvmargs=-javaagent:dd-java-agent.jar", files: gradleBuild, reference: "hand-written dd-java-agent 1.67.1 setup (org.gradle.jvmargs)", unresolved: "which local validation cannot remove from the build"},
		// Maven passes MAVEN_OPTS's quotes on, so "true" does not enable it.
		{name: "quoted enablement in MAVEN_OPTS", env: map[string]string{"MAVEN_OPTS": `-javaagent:dd-java-agent.jar -Ddd.civisibility.enabled="true" -Ddd.civisibility.agentless.enabled=true -Ddd.site=datadoghq.com`}, command: "mvn test", problem: `DD_CIVISIBILITY_ENABLED="true" leaves Test Optimization off`},
		{name: "quoted enablement in JAVA_TOOL_OPTIONS", env: map[string]string{"JAVA_TOOL_OPTIONS": `-javaagent:dd-java-agent.jar -Ddd.civisibility.enabled="true" -Ddd.civisibility.agentless.enabled=true -Ddd.site=datadoghq.com`}, command: "mvn test", reference: "hand-written dd-java-agent 1.67.1 setup (JAVA_TOOL_OPTIONS)"},
		{name: "APM tracing off", env: with(agentless, "DD_TRACE_ENABLED", "false"), command: "mvn test", reference: "hand-written dd-java-agent 1.67.1 setup (MAVEN_OPTS)"},
		{name: "curl --output=", before: []string{"curl -L --output=dd-java-agent.jar https://repo1.maven.org/maven2/com/datadoghq/dd-java-agent/1.67.1/dd-java-agent-1.67.1.jar"}, command: "mvn test", unresolved: "downloads it without --output <path> as a separate word"},
		{name: "downloader path", before: []string{"/usr/bin/wget -O dd-java-agent.jar https://repo1.maven.org/maven2/com/datadoghq/dd-java-agent/1.67.1/dd-java-agent-1.67.1.jar"}, command: "mvn test", reference: "hand-written dd-java-agent 1.67.1 setup (MAVEN_OPTS)"},
		{name: "Agent mode in another option list", env: with(agentless, "JAVA_TOOL_OPTIONS", "-Ddd.civisibility.agentless.enabled=false"), command: "mvn test", unresolved: "--ci-confirm datadog-agent"},
		{name: "test JVMs only", env: with(agentless, "MAVEN_OPTS", ""), command: "mvn test -DargLine=-javaagent:dd-java-agent.jar", unresolved: "-DargLine attaches dd-java-agent to the forked test JVMs only"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := tc.env
			if env == nil {
				env = agentless
			}
			files := map[string]string{"pom.xml": handWrittenPOM}
			if tc.files != nil {
				files = tc.files
			}
			before := tc.before
			if before == nil {
				before = []string{handWrittenAgentDownload}
			}
			root := javaHandWrittenRepository(t, provider, env, before, tc.command, files)
			finding := javaHandWrittenFinding(t, root)
			if tc.reference != "" {
				require.Equal(t, tc.reference, finding.Action)
			}
			switch {
			case tc.problem != "":
				require.Equal(t, CodeIncompleteCISetup, finding.Code)
				require.Contains(t, finding.Reason, "keep it, without adding a second setup")
				require.Contains(t, finding.Reason, tc.problem)
			case tc.unresolved != "":
				require.Equal(t, CodeUnverifiedInitialization, finding.Code, finding.Reason)
				require.Contains(t, finding.Reason, tc.unresolved)
			default:
				require.Equal(t, "not checked", finding.Status, finding.Reason)
			}
		})
	}
	// The command's own MAVEN_OPTS replaces the job's, so the job's agent
	// never reaches the build.
	root := t.TempDir()
	writeFile(t, root, "pom.xml", handWrittenPOM)
	job := runtimeJob{Env: agentless, Steps: []runtimeStep{{Run: handWrittenAgentDownload}, {Run: "MAVEN_OPTS=-Xmx1g mvn test"}}}
	require.Nil(t, javaHandWrittenSetup(root, gitLabCI{}, ciWorkflow{}, job, 1, nil, "junit5", ""), "an inline MAVEN_OPTS replaces the inherited carrier")
	job.Steps[1].Run = "mvn test"
	require.NotNil(t, javaHandWrittenSetup(root, gitLabCI{}, ciWorkflow{}, job, 1, nil, "junit5", ""))

	// A download step that does not run for the row lends no release; one
	// whose condition ddtest cannot decide may not run.
	job = runtimeJob{Env: agentless, Steps: []runtimeStep{{If: "${{ matrix.pinned == 'yes' }}", Run: handWrittenAgentDownload}, {Run: "mvn test"}}}
	setup := javaHandWrittenSetup(root, gitLabCI{}, ciWorkflow{}, job, 1, map[string]any{"pinned": "no"}, "junit5", "")
	require.Empty(t, setup.Tracer, "the download does not run for this row")
	require.Contains(t, setup.problem(), "No command of the job downloads dd-java-agent.jar")
	require.Equal(t, "1.67.1", javaHandWrittenSetup(root, gitLabCI{}, ciWorkflow{}, job, 1, map[string]any{"pinned": "yes"}, "junit5", "").Tracer)
	job.Steps[0].If = "${{ secrets.PINNED == 'yes' }}"
	setup = javaHandWrittenSetup(root, gitLabCI{}, ciWorkflow{}, job, 1, map[string]any{}, "junit5", "")
	require.Empty(t, setup.Tracer)
	require.Contains(t, setup.problem(), "inside conditional shell control flow")
	// Each step reads its own environment, not the test step's.
	job = runtimeJob{Env: agentless, Steps: []runtimeStep{{Env: map[string]string{"URL": "https://mirror.example/agent.jar"}, Run: "curl -Lo dd-java-agent.jar \"$URL\""}, {Env: map[string]string{"URL": "https://repo1.maven.org/maven2/com/datadoghq/dd-java-agent/1.67.1/dd-java-agent-1.67.1.jar"}, Run: "mvn test"}}}
	require.Empty(t, javaHandWrittenSetup(root, gitLabCI{}, ciWorkflow{}, job, 1, nil, "junit5", "").Tracer, "the download's own URL is the mirror's")
	// Without the agent there is no hand-written setup, and the job is
	// reported as uninstrumented.
	root = javaHandWrittenRepository(t, provider, map[string]string{"MAVEN_OPTS": "-Xmx1g"}, nil, "mvn test", map[string]string{"pom.xml": handWrittenPOM})
	require.Nil(t, firstJobHandWrittenSetup(t, root, "java", "junit5", ""))
	ci := JavaCIConfiguration(root, "junit5", "")
	require.Len(t, ci.Jobs, 1)
	require.Contains(t, ci.Jobs[0].Reason, "so its tests are not instrumented")
}

// Java's provider setups and its hand-written setups never handle the same
// job: a job the Datadog setup script sets up is checked as the script's,
// whatever its JVM options name, and one that attaches dd-java-agent by hand
// beside it is checked as that setup, without the script prescribed on top.
func TestHandWrittenJavaBesideTheProviderSetup(t *testing.T) {
	handWrittenJob := "manual:\n  image: maven:3.9-eclipse-temurin-21\n  variables:\n    MAVEN_OPTS: -javaagent:dd-java-agent.jar\n    DD_CIVISIBILITY_ENABLED: \"true\"\n    DD_CIVISIBILITY_AGENTLESS_ENABLED: \"true\"\n  script:\n    - " + handWrittenAgentDownload + "\n    - mvn -B verify\n"
	root := gitLabJavaRepository(t, gitLabJavaJob("maven:3.9-eclipse-temurin-21", gitLabJavaSetup, "", "    - export MAVEN_OPTS=\"$MAVEN_OPTS -javaagent:dd-java-agent.jar\"\n"))
	require.Nil(t, firstJobHandWrittenSetup(t, root, "java", "junit5", ""))
	require.NoError(t, CheckCIProviderSupport(root, "java", "junit5"))
	ci := JavaCIConfiguration(root, "junit5", "")
	require.Len(t, ci.Jobs, 1, ci.Reason)
	require.False(t, HandWrittenSetupAction(ci.Jobs[0].Action), ci.Jobs[0].Action)

	root = gitLabJavaRepository(t, gitLabJavaJob("maven:3.9-eclipse-temurin-21", gitLabJavaSetup, "", "")+handWrittenJob)
	require.NoError(t, CheckCIProviderSupport(root, "java", "junit5"))
	ci = JavaCIConfiguration(root, "junit5", "")
	require.Len(t, ci.Jobs, 2, ci.Reason)
	actions := map[string]string{}
	for _, job := range ci.Jobs {
		actions[job.Job] = job.Action
	}
	require.Equal(t, "hand-written dd-java-agent 1.67.1 setup (MAVEN_OPTS)", actions["manual"])
	require.False(t, HandWrittenSetupAction(actions["test"]), actions["test"])
}

// Local replay drops the -Ddd.* properties a hand-written setup configures
// CI's agent with, as it drops those DD_* variables, and keeps tracer
// plugin settings, which change test behavior.
func TestJavaReplayDropsAgentProperties(t *testing.T) {
	value, handled, err := (javaDiscovery{}).variantValue(ciWorkflow{}, runtimeJob{}, runtimeStep{}, nil, "MAVEN_OPTS", "-Xmx1g -Ddd.civisibility.agentless.enabled=true -Ddd.site=datadoghq.com -Ddd.trace.jdbc.enabled=false")
	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, "-Xmx1g -Ddd.trace.jdbc.enabled=false", value)
	key, setting, ok := javaAgentProperty("JAVA_TOOL_OPTIONS", "-Ddd.civisibility.enabled=true")
	require.True(t, ok)
	require.Equal(t, "DD_CIVISIBILITY_ENABLED", key)
	require.Equal(t, "true", setting)
	_, _, ok = javaAgentProperty("JAVA_TOOL_OPTIONS", "-Dmaven.repo.local=/cache")
	require.False(t, ok)
	// Maven's launcher keeps quotes in MAVEN_OPTS; the JVM removes them.
	_, setting, _ = javaAgentProperty("MAVEN_OPTS", `-Ddd.civisibility.enabled="true"`)
	require.Equal(t, `"true"`, setting)
	_, setting, _ = javaAgentProperty("JAVA_TOOL_OPTIONS", `-Ddd.civisibility.enabled="true"`)
	require.Equal(t, "true", setting)
}

// Commands that name the agent are read from the script's syntax tree: a
// continued command is one, a here-document's body is data, the build and
// the JVM option lists that attach the agent are not among them, and each
// says whether it may not run.
func TestJavaAgentCommands(t *testing.T) {
	commands, cd, ok := javaAgentCommands("export MAVEN_OPTS=-javaagent:dd-java-agent.jar\ncurl -L -o dd-java-agent.jar \\\n  https://repo1.maven.org/maven2/com/datadoghq/dd-java-agent/1.67.1/dd-java-agent-1.67.1.jar\nmvn test -DargLine=-javaagent:dd-java-agent.jar", nil, "dd-java-agent.jar")
	require.True(t, ok)
	require.False(t, cd)
	require.Len(t, commands, 1)
	address, output, problem := javaSimpleDownload(commands[0].words)
	require.Empty(t, problem)
	require.Equal(t, "dd-java-agent.jar", output)
	require.Equal(t, "https://repo1.maven.org/maven2/com/datadoghq/dd-java-agent/1.67.1/dd-java-agent-1.67.1.jar", address)
	require.False(t, commands[0].conditional)

	commands, _, _ = javaAgentCommands("cat <<EOF\n"+handWrittenAgentDownload+"\nEOF\n", nil, "dd-java-agent.jar")
	require.Empty(t, commands, "a here-document's body is data")

	commands, _, _ = javaAgentCommands("if x; then cp /cache/a.jar dd-java-agent.jar; fi\necho ok > dd-java-agent.jar &\nV=$(curl -Lo dd-java-agent.jar https://x/y.jar)\n./replace-agent.sh dd-java-agent.jar", nil, "dd-java-agent.jar")
	require.Len(t, commands, 4)
	for _, command := range commands[:3] {
		require.True(t, command.conditional, command.command)
	}
	require.False(t, commands[3].conditional, "an unknown command that names the agent counts")

	_, cd, _ = javaAgentCommands("pushd tools && "+handWrittenAgentDownload, nil, "dd-java-agent.jar")
	require.True(t, cd)
	_, _, ok = javaAgentCommands("if then", nil, "dd-java-agent.jar")
	require.False(t, ok)
}
