// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestJavaOnboardPrintsJavaInstructions(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "pom.xml"), []byte(`<project><dependencies><dependency><groupId>org.junit.jupiter</groupId><artifactId>junit-jupiter</artifactId></dependency></dependencies></project>`), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github", "workflows"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".github", "workflows", "ci.yml"), []byte("jobs:\n  test:\n    steps:\n      - uses: actions/setup-java@v4\n      - run: ./mvnw -B verify\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".github", "workflows", "lint.yml"), []byte("jobs:\n  lint:\n    steps:\n      - run: mvn spotless:check\n"), 0644))
	t.Chdir(root)

	var output bytes.Buffer
	require.NoError(t, Run(&output))
	text := output.String()
	require.True(t, strings.HasPrefix(text, "DDTest found java, junit5, and GitHub Actions.\n\nTest workflow(s):\n  - .github/workflows/ci.yml\n"), text)
	require.Contains(t, text, "  - [.] ./mvnw -B verify\n", "the discovered CI command is listed for validation")
	require.Contains(t, text, "lint.yml / lint: no junit5 invocation identified")
	require.Contains(t, text, "an exact `java-tracer-version`", "local validation can validate only an exact release")
	require.NotContains(t, text, "java-tracer-version: ''", "a floating release cannot be validated")
	require.Contains(t, text, "${{ env.MAVEN_OPTS }}")
	require.Contains(t, text, "ddtest testdrive --framework junit5 --all --yes")
	require.NotContains(t, text, "__FRAMEWORK__")
}

func TestJavaOnboardIsCoordinated(t *testing.T) {
	require.True(t, coordinatedLanguage("java"), "agents apply the CI setup, then the coordinator validates")
	require.False(t, discoveryLanguageAutomaticSetup("java"))
	require.Equal(t, "java", discoveryLanguageName("cucumber-java"), "Cucumber JVM is not Cucumber.js")
	require.Equal(t, "javascript", discoveryLanguageName("cucumber"))
}

func TestJavaInstructionsDescribeGradleBuilds(t *testing.T) {
	for name, text := range map[string]string{
		"agent":  javaDiscovery{}.agentInstructions("junit5", "datadoghq.com"),
		"manual": javaDiscovery{}.instructions("junit5"),
	} {
		for _, phrase := range []string{"Gradle settings and build scripts", "Maven goals or Gradle tasks", "One Maven or Gradle command runs every framework", "GRADLE_OPTS"} {
			require.Contains(t, text, phrase, name)
		}
		require.NotContains(t, text, "One Maven command", name)
	}
	require.Contains(t, javaDiscovery{}.instructions("junit5"), "or only Gradle test tasks, and never installs or publishes artifacts")
	require.Contains(t, javaDiscovery{}.agentInstructions("junit5", ""), "Maven test lifecycle or Gradle test tasks and never installs or publishes")
}
