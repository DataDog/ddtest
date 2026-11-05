// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// useJavaRelease makes release the latest dd-java-agent release for one
// test, counting how often ddtest reads it.
func useJavaRelease(t *testing.T, release string, err error) *int {
	t.Helper()
	reads := 0
	t.Cleanup(SetJavaReleaseResolver(func(context.Context) (string, error) {
		reads++
		return release, err
	}))
	return &reads
}

// Maven Central's metadata names the latest release in its release element,
// or else in its latest one; anything else is not an exact release.
func TestReadJavaReleaseFromMavenMetadata(t *testing.T) {
	for metadata, want := range map[string]string{
		"<metadata><groupId>com.datadoghq</groupId><versioning><latest>1.68.1</latest><release>1.68.1</release><versions><version>1.67.1</version><version>1.68.1</version></versions></versioning></metadata>": "1.68.1",
		"<metadata><versioning><latest>1.69.0</latest></versioning></metadata>":            "1.69.0",
		"<metadata><versioning><release>1.70.0-SNAPSHOT</release></versioning></metadata>": "",
		"<metadata><versioning></versioning></metadata>":                                   "",
		"<html>": "",
	} {
		release, err := readJavaRelease(strings.NewReader(metadata))
		require.Equal(t, want, release, metadata)
		require.Equal(t, want == "", err != nil, metadata)
	}
}

// The latest release is read once per run; an exact --tracer-version wins
// without reading it; a floating selector is refused; and an unreadable
// Maven Central falls back to the release ddtest has validated.
func TestSelectJavaRelease(t *testing.T) {
	reads := useJavaRelease(t, "1.70.2", nil)
	require.Equal(t, "1.70.2", javaSetupRelease())
	release, err := SelectJavaRelease("latest")
	require.NoError(t, err)
	require.Equal(t, "1.70.2", release)
	require.Equal(t, 1, *reads)

	reads = useJavaRelease(t, "1.70.2", nil)
	release, err = SelectJavaRelease("1.66.0")
	require.NoError(t, err)
	require.Equal(t, "1.66.0", release)
	require.Equal(t, "1.66.0", javaSetupRelease())
	require.Zero(t, *reads)

	for _, floating := range []string{"1.66", "1.x", "git:main", "1.68.1-SNAPSHOT"} {
		_, err = SelectJavaRelease(floating)
		require.ErrorContains(t, err, "exact dd-java-agent release", floating)
	}

	useJavaRelease(t, "", errors.New("offline"))
	require.Equal(t, javaRecommendedTracer, javaSetupRelease())
	useJavaRelease(t, "latest", nil)
	require.Equal(t, javaRecommendedTracer, javaSetupRelease())
}

// Every Java setup ddtest prescribes names the release it selected, never a
// placeholder or one the agent picks: the GitLab script, the CircleCI orb,
// the CircleCI setup without Bash, the Buildkite snippet, the GitHub action
// instructions and the hand-written download it suggests.
func TestJavaSetupsNameTheSelectedRelease(t *testing.T) {
	useJavaRelease(t, "1.70.2", nil)
	for name, tc := range map[string]struct {
		provider ciProvider
		file     string
		config   string
		want     []string
	}{
		"gitlab":            {gitLabCI{}, gitLabCIFile, "test:\n  image: maven:3.9-eclipse-temurin-21\n  script:\n    - mvn -B verify\n", []string{`JAVA_TRACER_VERSION="1.70.2"`}},
		"circleci":          {circleCI{}, circleCIFile, "version: 2.1\njobs:\n  test:\n    docker:\n      - image: cimg/openjdk:17.0\n    steps:\n      - checkout\n      - run: mvn -B verify\n" + circleWorkflow("      - test\n"), []string{"java_tracer_version: 1.70.2"}},
		"circleci, no bash": {circleCI{}, circleCIFile, "version: 2.1\njobs:\n  test:\n    docker:\n      - image: eclipse-temurin:17-jdk-alpine\n    steps:\n      - checkout\n      - run: mvn -B verify\n" + circleWorkflow("      - test\n"), []string{`DD_TRACER_VERSION_JAVA="1.70.2"`}},
		"buildkite":         {buildkite{}, buildkiteFile, "steps:\n  - label: test\n    command: mvn -B verify\n    plugins:\n      - docker#v5.12.0:\n          image: maven:3.9-eclipse-temurin-17\n", []string{`DD_SET_TRACER_VERSION_JAVA="1.70.2"`}},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, root, "pom.xml", javaJUnit5POM)
			writeFile(t, root, tc.file, tc.config)
			t.Chdir(root)
			useCIProvider(t, tc.provider.name())
			text := providerSetupInstructions(root, tc.provider, "java", "junit5", "", true)
			for _, want := range append(tc.want, "The setup installs dd-java-agent 1.70.2, the exact release ddtest selected") {
				require.Contains(t, text, want)
			}
			require.NotContains(t, text, "<exact dd-java-agent release>")
			require.NotContains(t, text, javaRecommendedTracer)
		})
	}
	github := javaDiscovery{}.agentInstructions("junit5", "")
	require.Contains(t, github, "`java-tracer-version: 1.70.2`")
	require.Contains(t, javaDiscovery{}.instructions("junit5"), "`java-tracer-version: 1.70.2`")
	require.Equal(t, "https://repo1.maven.org/maven2/com/datadoghq/dd-java-agent/1.70.2/dd-java-agent-1.70.2.jar", javaHandWrittenDownload())
}

// onboard --agent for Java prints the setup with the release
// --tracer-version names, and refuses a floating one before any setup.
func TestJavaOnboardingHonorsTracerVersion(t *testing.T) {
	useJavaRelease(t, "1.70.2", nil)
	root := gitLabJavaRepository(t, "test:\n  image: maven:3.9-eclipse-temurin-21\n  script:\n    - mvn -B verify\n")
	writeFile(t, root, ".gitignore", "/.testoptimization/\n")
	var output bytes.Buffer
	_ = StartWithOptions(&output, StartOptions{Format: "json", Tracer: "1.66.0"})
	require.Contains(t, output.String(), `JAVA_TRACER_VERSION=\"1.66.0\"`, output.String())
	require.NotContains(t, output.String(), "1.70.2")

	useJavaRelease(t, "1.70.2", nil)
	output.Reset()
	_ = StartWithOptions(&output, StartOptions{Format: "json", Tracer: "1.66"})
	require.Contains(t, output.String(), `"code":"invalid_options"`)
	require.Contains(t, output.String(), "exact dd-java-agent release")
}

// An explicit --tracer-version latest selects the exact latest release, as
// no --tracer-version does: the setup pins it, and every command of the
// handoff carries that release, never latest, so the resumed validation
// compares it with the CI pin.
func TestJavaOnboardingResolvesAnExplicitLatest(t *testing.T) {
	useJavaRelease(t, "1.70.2", nil)
	gitLabJavaRepository(t, "test:\n  image: maven:3.9-eclipse-temurin-21\n  script:\n    - mvn -B verify\n")
	var output bytes.Buffer
	_ = StartWithOptions(&output, StartOptions{Format: "json", Tracer: "latest"})
	var result AgentResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result), output.String())
	require.Contains(t, output.String(), `JAVA_TRACER_VERSION=\"1.70.2\"`, output.String())
	commands := [][]string{result.NextCommand, result.ResumeCommand, result.AcceptResumeCommand}
	require.NotEmpty(t, slices.Concat(commands...), output.String())
	for _, command := range commands {
		if i := slices.Index(command, "--tracer-version"); i >= 0 {
			require.Equal(t, "1.70.2", command[i+1], "%v", command)
		}
	}
	require.Contains(t, slices.Concat(commands...), "1.70.2", "the handoff carries the exact release")
	require.NotContains(t, slices.Concat(commands...), "latest")

	selected, err := SelectTracerRelease("java", "latest")
	require.NoError(t, err)
	require.Equal(t, "1.70.2", selected)
	for _, language := range []string{"javascript", "python", "ruby"} {
		selected, err := SelectTracerRelease(language, "latest")
		require.NoError(t, err)
		require.Equal(t, "latest", selected, "%s installs the release its registry tags latest", language)
	}
}

// The Jenkins container snippet installs the release the host stages' plugin
// pins (see jenkinsPluginRelease), even when ddtest selected another.
func TestJavaJenkinsSnippetFollowsThePluginPin(t *testing.T) {
	useJavaRelease(t, "1.70.2", nil)
	root := javaJenkinsRepository(t, mixedJenkinsfile)
	pin := "https://repo1.maven.org/maven2/com/datadoghq/dd-java-agent/1.66.0/dd-java-agent-1.66.0.jar"
	pinned := strings.Replace(mixedJenkinsfile, "      agent { label 'java' }\n", "      agent { label 'java' }\n      options {\n        datadog(testOptimization: [enabled: true, serviceName: 'app', languages: ['JAVA'], additionalVariables: ["+javaPluginTracerURL+": '"+pin+"']])\n      }\n", 1)
	writeFile(t, root, "Jenkinsfile", pinned)
	plan, err := PlanProviderSetup(root, "junit5", "")
	require.NoError(t, err)
	_, err = plan.Apply(true)
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(root, "Jenkinsfile"))
	require.NoError(t, err)
	require.Contains(t, string(data), `DD_SET_TRACER_VERSION_JAVA="1.66.0"`)
	require.NotContains(t, string(data), "1.70.2")
}
