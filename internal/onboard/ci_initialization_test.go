// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNativeInitializationSharedState(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	for _, both := range []bool{false, true} {
		before := " before_script:\n  - npm ci\n  - export NODE_OPTIONS='-r dd-trace/ci/init'\n"
		other := ""
		if both {
			other = before
		}
		text := ".base:\n image: node:24.1.0\n script: npm test\nconfigured:\n extends: .base\n" + before + "other:\n extends: .base\n" + other
		root := providerFixture(t, map[string]string{".gitlab-ci.yml": text, "package.json": `{"scripts":{"test":"jest"},"devDependencies":{"jest":"30.5.1","dd-trace":"6.20.1"}}`})
		plan, err := PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
		if both {
			require.NoError(t, err)
			diff, err := plan.Diff()
			require.NoError(t, err)
			require.NotContains(t, diff, ".gitlab-ci.yml")
		} else {
			require.ErrorContains(t, err, "shared")
		}
	}
}

func TestNativeInitializationJenkinsCommandCalls(t *testing.T) {
	selectProviderForTest(t, "jenkins")
	for _, call := range []string{"runIntegrationTests 'integration'", "runIntegrationTests mode: 'integration'", "runIntegrationTests('integration')"} {
		root := providerFixture(t, map[string]string{"Jenkinsfile": "pipeline { tools { nodejs '24.1.0' }; stages { stage('Test') { steps { sh 'npm test'; " + call + " } } } }"})
		_, err := PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
		require.ErrorContains(t, err, "runIntegrationTests")
		require.NotEqual(t, StatusCompatible, nativeFollowupCheck(t, root, "jest").Status)
	}
}

func TestNativeInitializationGitHubProviderVariables(t *testing.T) {
	for _, run := range []string{"npm run $TEST_SCRIPT", "npm run ${{ matrix.script }}"} {
		root := providerFixture(t, map[string]string{
			".github/workflows/test.yml": "jobs:\n test:\n  strategy: {matrix: {script: [test]}}\n  env: {TEST_SCRIPT: '${{ matrix.script }}'}\n  steps:\n   - run: " + run + "\n",
			".gitlab-ci.yml":             "test:\n script: npm test\n",
		})
		_, err := SelectCIProvider(root, "")
		var choice *CIProviderChoice
		require.ErrorAs(t, err, &choice)
		require.ElementsMatch(t, []string{"github", "gitlab"}, choice.Providers)
	}
}

func TestNativeInitializationTracerMutations(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	for _, steps := range []string{
		"  - npm install --no-save dd-trace@6.19.0\n",
		"  - npm ci\n  - npm install --no-save dd-trace@6.19.0\n",
		"  - npm ci\n  - npm uninstall dd-trace\n",
		"  - npm ci\n  - npm ci --omit=dev\n",
	} {
		root := providerFixture(t, map[string]string{"package.json": `{"scripts":{"test":"jest"},"devDependencies":{"jest":"30.5.1","dd-trace":"6.20.1"}}`, ".gitlab-ci.yml": "test:\n image: node:24.1.0\n before_script:\n" + steps + "  - export NODE_OPTIONS='-r dd-trace/ci/init'\n script: npm test\n"})
		w, err := readWorkflows(root)
		require.NoError(t, err)
		s := w[0].Jobs["test"].Steps
		require.Empty(t, s[len(s)-1].Native.Tracer, steps)
	}
}

func TestNativeInitializationOpaqueProviderChoice(t *testing.T) {
	for _, provider := range []string{"gitlab", "circleci"} {
		path, source := ".gitlab-ci.yml", "include: {project: external/tests, file: ci.yml}\n"
		if provider == "circleci" {
			path, source = ".circleci/config.yml", "version: 2.1\nsetup: true\n"
		}
		root := providerFixture(t, map[string]string{path: source, ".github/workflows/test.yml": "jobs:\n test:\n  steps: [{run: npm test}]\n"})
		_, err := SelectCIProvider(root, "")
		var choice *CIProviderChoice
		require.ErrorAs(t, err, &choice)
		require.ElementsMatch(t, []string{"github", provider}, choice.Providers)
		selected, err := SelectCIProvider(root, "github")
		require.NoError(t, err)
		require.Equal(t, "github", selected)
	}
}

func TestNativeInitializationInlineOptIn(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("POSIX shell unavailable")
	}
	for _, original := range []string{"DD_CIVISIBILITY_ENABLED=true printf '%s' \"$DD_CIVISIBILITY_ENABLED\"", `cross-env DD_CIVISIBILITY_ENABLED=true sh -c 'printf "%s" "$DD_CIVISIBILITY_ENABLED"'`} {
		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, "npm"), []byte("#!/bin/sh\nprintf installed > \"$INSTALL_MARKER\"\n"), 0700))
		require.NoError(t, os.WriteFile(filepath.Join(root, "cross-env"), []byte("#!/bin/sh\nexec env \"$@\"\n"), 0700))
		marker := filepath.Join(root, "installed")
		cmd := exec.Command("sh", "-c", wrapNativeCommand(nativeSetup{Framework: "jest", Version: "6.20.1", Site: "datadoghq.com"}, original, "gitlab"))
		cmd.Env = append(os.Environ(), "PATH="+root+string(os.PathListSeparator)+os.Getenv("PATH"), "TMPDIR="+root, "DD_API_KEY=synthetic", "DD_CIVISIBILITY_ENABLED=false", "INSTALL_MARKER="+marker)
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, string(output))
		require.Equal(t, "true", string(output))
		require.FileExists(t, marker, "inline opt-in must take effect before installation guard")
	}
}

func TestNativeInitializationWholeTracerControls(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	for _, command := range []string{"npm test", "DD_TRACE_ENABLED=false npm test", "cross-env DD_TRACE_ENABLED=false npm test"} {
		variables := ""
		if command == "npm test" {
			variables = " variables: {DD_TRACE_ENABLED: 'false'}\n"
		}
		root := providerFixture(t, map[string]string{".gitlab-ci.yml": "test:\n image: node:24.1.0\n" + variables + " script: " + command + "\n"})
		_, err := PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
		require.Error(t, err)
		scope, err := DiscoverValidationScope(root, "jest")
		require.NoError(t, err)
		require.Contains(t, strings.Join(scope.Unresolved, ";"), "DD_TRACE_ENABLED")
	}
	setup := nativeSetup{Framework: "jest", Version: "6.20.1", Site: "datadoghq.com"}
	root := providerFixture(t, map[string]string{".gitlab-ci.yml": "test:\n image: node:24.1.0\n variables: {DD_TRACE_ENABLED: 'false'}\n script: " + strconv.Quote(wrapNativeCommand(setup, "npm test", "gitlab")) + "\n"})
	require.NotEqual(t, StatusCompatible, nativeFollowupCheck(t, root, "jest").Status)
}
