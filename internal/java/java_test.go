// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package java

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

const junit5POM = `<project><artifactId>service</artifactId><dependencies><dependency><groupId>org.junit.jupiter</groupId><artifactId>junit-jupiter</artifactId></dependency></dependencies></project>`

const mavenWorkflow = "jobs:\n  test:\n    steps:\n      - uses: actions/checkout@v4\n      - run: ./mvnw -B verify\n"

const configuredWorkflow = "jobs:\n  test:\n    steps:\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: java}\n      - run: mvn test\n"

func useSettings(t *testing.T, values map[string]string) {
	t.Helper()
	viper.Reset()
	for key, value := range values {
		viper.Set(key, value)
	}
	settings.Init()
	t.Cleanup(func() { viper.Reset(); settings.Init() })
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	require.NoError(t, os.WriteFile(path, []byte(contents), 0600))
}

func TestSelected(t *testing.T) {
	for _, tc := range []struct {
		name     string
		files    []string
		settings map[string]string
		want     bool
	}{
		{"Maven project alone", []string{"pom.xml"}, nil, true},
		{"no Maven project", []string{"Gemfile"}, nil, false},
		{"JavaScript project keeps its default", []string{"pom.xml", "package.json"}, nil, false},
		{"explicit platform", []string{"pom.xml", "package.json"}, map[string]string{"platform": "java"}, true},
		{"explicit Maven framework", []string{"package.json"}, map[string]string{"framework": "junit5"}, true},
		{"explicit JavaScript framework", []string{"pom.xml"}, map[string]string{"framework": "jest"}, false},
		{"explicit other platform", []string{"pom.xml"}, map[string]string{"platform": "javascript"}, false},
		{"Cucumber means Cucumber.js", []string{"pom.xml"}, map[string]string{"framework": "cucumber"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			useSettings(t, tc.settings)
			for _, file := range tc.files {
				contents := "<project/>"
				if file == "package.json" {
					contents = `{"devDependencies":{"jest":"30"}}`
				}
				writeFile(t, file, contents)
			}
			selected, err := Selected()
			require.NoError(t, err)
			require.Equal(t, tc.want, selected)
		})
	}
}

func TestSelectedRejectsNonMavenFrameworkForJava(t *testing.T) {
	t.Chdir(t.TempDir())
	useSettings(t, map[string]string{"platform": "java", "framework": "jest"})
	_, err := Selected()
	require.ErrorContains(t, err, "framework 'jest' is not supported by platform 'java'")
}

func TestInstructionsListFrameworksAndWorkflows(t *testing.T) {
	root := t.TempDir()
	useSettings(t, nil)
	writeFile(t, filepath.Join(root, "pom.xml"), `<project><modules><module>service</module></modules></project>`)
	writeFile(t, filepath.Join(root, "service/pom.xml"), junit5POM)
	writeFile(t, filepath.Join(root, ".github/workflows/ci.yml"), mavenWorkflow)
	writeFile(t, filepath.Join(root, ".github/workflows/lint.yml"), "jobs:\n  lint:\n    steps:\n      - run: mvn spotless:check\n")

	var output bytes.Buffer
	require.NoError(t, Instructions(&output, root))
	text := output.String()
	require.Contains(t, text, "DDTest found a Maven project with JUnit 5.")
	require.Contains(t, text, "Maven test workflow(s):\n  - .github/workflows/ci.yml\n")
	require.NotContains(t, text, "lint.yml")
	require.Contains(t, text, "languages: java")
	require.Contains(t, text, "uses: datadog/test-visibility-github-action@7ce545f228901e4ac4ef0b45d946120f1c4124e0 # v3.3.0", "manual guidance installs the verified action commit")
	require.Contains(t, text, "secret named `DD_API_KEY`")
	require.NotContains(t, text, "__")
	require.NotContains(t, text, "ddtest testdrive", "Java has no local validation to recommend")
}

func TestInstructionsStopWhenEveryWorkflowIsConfigured(t *testing.T) {
	root := t.TempDir()
	useSettings(t, nil)
	writeFile(t, filepath.Join(root, "pom.xml"), junit5POM)
	writeFile(t, filepath.Join(root, ".github/workflows/ci.yml"), configuredWorkflow)

	var output bytes.Buffer
	require.NoError(t, Instructions(&output, root))
	require.Contains(t, output.String(), "Datadog Test Optimization already appears in every detected Maven test workflow:\n  - .github/workflows/ci.yml\n")
	require.NotContains(t, output.String(), "## 1.")
}

func TestInstructionsStayManualWhenDiscoveryIsIncomplete(t *testing.T) {
	for _, tc := range []struct{ name, pom, framework, summary string }{
		{"no workflow", junit5POM, "", "DDTest found a Maven project with JUnit 5."},
		{"no framework dependency", "<project/>", "", "without a supported test framework dependency"},
		{"unreadable reactor", "<project><modules><module>${module}</module></modules></project>", "", "could not read its reactor"},
		{"selected framework", "<project/>", "testng", "DDTest found a Maven project with TestNG."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			useSettings(t, map[string]string{"framework": tc.framework})
			writeFile(t, filepath.Join(root, "pom.xml"), tc.pom)

			var output bytes.Buffer
			require.NoError(t, Instructions(&output, root))
			require.Contains(t, output.String(), tc.summary)
			require.Contains(t, output.String(), "CI discovery is incomplete: no GitHub Actions workflow runs a Maven test phase. The guidance below is manual.")
			require.Contains(t, output.String(), "## 1. Instrument the test job")
		})
	}
}

func TestStartReturnsManualSetupHandoff(t *testing.T) {
	root := t.TempDir()
	useSettings(t, nil)
	writeFile(t, filepath.Join(root, "pom.xml"), junit5POM)
	writeFile(t, filepath.Join(root, ".github/workflows/ci.yml"), mavenWorkflow)

	var output bytes.Buffer
	require.NoError(t, Start(&output, root))
	var result onboard.AgentResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	require.Equal(t, "ready", result.Status)
	require.Equal(t, "manual_setup_required", result.Code)
	require.Equal(t, "run_command", result.NextAction)
	require.Equal(t, []string{"onboard", "--instructions", "--platform", "java"}, result.NextCommand[len(result.NextCommand)-4:])
	require.Contains(t, result.Reason, "JUnit 5")
	require.Contains(t, result.Reason, ".github/workflows/ci.yml")
	require.Equal(t, []onboard.WorkflowCheck{
		{Name: "automatic_setup", Status: "not supported", Reason: "ddtest does not edit Maven workflows."},
		{Name: "local_validation", Status: "not supported", Reason: "ddtest testdrive does not run Java tests."},
	}, result.Checks)
}

func TestStartReportsExistingSetup(t *testing.T) {
	root := t.TempDir()
	useSettings(t, nil)
	writeFile(t, filepath.Join(root, "pom.xml"), junit5POM)
	writeFile(t, filepath.Join(root, ".github/workflows/ci.yml"), configuredWorkflow)

	var output bytes.Buffer
	require.NoError(t, Start(&output, root))
	var result onboard.AgentResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	require.Equal(t, "setup_present", result.Code)
	require.Equal(t, "report_result", result.NextAction)
	require.Empty(t, result.NextCommand)
}
