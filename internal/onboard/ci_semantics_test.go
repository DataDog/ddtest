// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNativeSemanticsBootstrapTransportAndOutput(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("POSIX shell unavailable")
	}
	for _, check := range []string{"transport", "stdout"} {
		t.Run(check, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, "npm"), []byte("#!/bin/sh\necho installation-diagnostic\n"), 0700))
			original := `printf '{"result":"original"}'`
			if check == "transport" {
				original = `printf '%s' "$DD_CIVISIBILITY_AGENTLESS_ENABLED"`
			}
			cmd := exec.Command("sh", "-c", wrapNativeCommand(nativeSetup{Framework: "jest", Version: "6.20.1", Site: "datadoghq.com"}, original, "jenkins"))
			cmd.Env = append(os.Environ(), "PATH="+root+string(os.PathListSeparator)+os.Getenv("PATH"), "TMPDIR="+root, "DD_API_KEY=synthetic", "DD_CIVISIBILITY_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=")
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			output, err := cmd.Output()
			require.NoError(t, err, stderr.String())
			if check == "transport" {
				require.True(t, strings.HasSuffix(string(output), "true"), string(output))
			} else {
				require.JSONEq(t, `{"result":"original"}`, string(output))
				require.Contains(t, stderr.String(), "installation-diagnostic")
			}
			entries, err := filepath.Glob(filepath.Join(root, "ddtest-ci.*"))
			require.NoError(t, err)
			require.Empty(t, entries)
		})
	}
}

func TestNativeSemanticsGitLabMatrixAliases(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	root := providerFixture(t, map[string]string{".gitlab-ci.yml": ".matrix: &matrix\n - NODE_VERSION: ['16.20.2', '24.1.0']\ntest:\n image: node:$NODE_VERSION\n parallel: {matrix: *matrix}\n script: npm test\n"})
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 2, "every aliased matrix consumer must survive discovery")
	_, err = PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
	require.ErrorContains(t, err, "shared")
}

func TestNativeSemanticsGitLabLegacyServices(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	root := providerFixture(t, map[string]string{".gitlab-ci.yml": "services: [postgres:16]\ntest:\n image: node:24.1.0\n script: npm test\n"})
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Contains(t, strings.Join(scope.Unresolved, ";"), "service containers")
	require.NotEqual(t, StatusCompatible, nativeFollowupCheck(t, root, "jest").Status)
}

func TestNativeSemanticsCompoundShellExports(t *testing.T) {
	for _, provider := range []string{"gitlab", "buildkite"} {
		t.Run(provider, func(t *testing.T) {
			selectProviderForTest(t, provider)
			path, text := ".gitlab-ci.yml", "test:\n image: node:24.1.0\n before_script:\n  - |\n    export TEST_MODE=integration\n    echo ready\n script: npm test\n"
			if provider == "buildkite" {
				path, text = ".buildkite/pipeline.yml", "image: node:24.1.0\nsteps:\n - commands:\n    - |\n      export TEST_MODE=integration\n      echo ready\n    - npm test\n"
			}
			root := providerFixture(t, map[string]string{path: text})
			_, err := PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
			require.ErrorContains(t, err, "persistent state")
			check := nativeFollowupCheck(t, root, "jest")
			require.NotEqual(t, StatusCompatible, check.Status)
		})
	}
}

func TestNativeSemanticsComposeBuildBoundaries(t *testing.T) {
	selectProviderForTest(t, "buildkite")
	for _, kind := range []string{"mapping", "workdir"} {
		t.Run(kind, func(t *testing.T) {
			build, dockerfile, reason := "{context: ., dockerfile: Dockerfile}", "FROM node:16.20.2\n", "build mapping"
			if kind == "workdir" {
				build, dockerfile, reason = ".", "FROM node:24.1.0\nCOPY . /app\nWORKDIR /app/packages/api\n", "working directory"
			}
			root := providerFixture(t, map[string]string{
				".buildkite/pipeline.yml":   "steps:\n - command: npm test\n   plugins:\n    - docker-compose#v5.12.0: {run: app}\n",
				"docker-compose.yml":        "services:\n app:\n  image: node:24.1.0\n  build: " + build + "\n",
				"Dockerfile":                dockerfile,
				"packages/api/package.json": `{"scripts":{"test":"mocha"},"devDependencies":{"mocha":"11.7.6"}}`,
			})
			_, err := PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
			require.ErrorContains(t, err, reason)
			check := nativeFollowupCheck(t, root, "jest")
			require.NotEqual(t, StatusCompatible, check.Status)
		})
	}
}

func TestNativeSemanticsCircleCIExecutorEnvironment(t *testing.T) {
	selectProviderForTest(t, "circleci")
	root := providerFixture(t, map[string]string{".circleci/config.yml": "executors:\n node:\n  parameters: {mode: {type: string, default: integration}}\n  docker: [{image: 'cimg/node:24.1.0'}]\n  environment: {TEST_MODE: '<< parameters.mode >>', SHARED: executor}\njobs:\n test:\n  parameters: {mode: {type: string, default: unit}}\n  executor: node\n  environment: {NODE_ENV: test, SHARED: job}\n  steps: [{run: npm test}]\n"})
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	require.Equal(t, "integration", scope.Commands[0].Environment["TEST_MODE"])
	require.Equal(t, "test", scope.Commands[0].Environment["NODE_ENV"])
	require.Equal(t, "job", scope.Commands[0].Environment["SHARED"])
}

func TestNativeSemanticsJenkinsDirectiveOrder(t *testing.T) {
	selectProviderForTest(t, "jenkins")
	for _, directive := range []string{"tools { nodejs '16.20.2' }", "agent { docker { image 'node:16.20.2' } }"} {
		root := providerFixture(t, map[string]string{"Jenkinsfile": "pipeline { stages { stage('Legacy') { steps { sh 'npm test' }; " + directive + " }; stage('Modern') { steps { sh 'npm test' } } }; tools { nodejs '24.1.0' } }"})
		w, err := readWorkflows(root)
		require.NoError(t, err)
		steps := w[0].Jobs["pipeline"].Steps
		require.Len(t, steps, 2)
		require.Equal(t, "16.20.2", steps[0].Native.NodeVersion)
		require.Equal(t, "24.1.0", steps[1].Native.NodeVersion)
		plan, err := PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
		require.NoError(t, err)
		_, err = plan.Apply(true)
		require.NoError(t, err)
		check := nativeFollowupCheck(t, root, "jest")
		require.Len(t, check.Jobs, 2)
		require.Equal(t, StatusExcluded, check.Jobs[0].Status)
		require.Equal(t, StatusCompatible, check.Jobs[1].Status)
	}
}

func TestNativeSemanticsJenkinsDockerArguments(t *testing.T) {
	selectProviderForTest(t, "jenkins")
	for _, docker := range []string{"image 'node:24.1.0'; args '-v /node16:/usr/local/bin/node'", "args '-v /node16:/usr/local/bin/node'; image 'node:24.1.0'"} {
		root := providerFixture(t, map[string]string{"Jenkinsfile": "pipeline { agent { docker { " + docker + " } }; stages { stage('Test') { steps { sh 'npm test' } } } }"})
		_, err := PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
		require.ErrorContains(t, err, "Docker arguments")
		require.NotEqual(t, StatusCompatible, nativeFollowupCheck(t, root, "jest").Status)
	}
}
