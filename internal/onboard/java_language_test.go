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

func TestJavaOnboardUsesTheSharedGitHubInstructions(t *testing.T) {
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
	require.Contains(t, text, "languages: java\n    java-tracer-version: ''")
	require.Contains(t, text, javaBootstrap)
	require.Contains(t, text, "ddtest testdrive --framework junit5")
}

func TestJavaOnboardIsNotCoordinated(t *testing.T) {
	require.False(t, coordinatedLanguage("java"), "agent onboarding returns manual guidance until Java has automatic CI setup")
	require.Equal(t, "java", discoveryLanguageName("cucumber-java"), "Cucumber JVM is not Cucumber.js")
	require.Equal(t, "javascript", discoveryLanguageName("cucumber"))
}
