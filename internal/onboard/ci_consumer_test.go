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

func TestNativeConsumerChoiceDespiteEnvironmentGap(t *testing.T) {
	root := providerFixture(t, map[string]string{
		".github/workflows/test.yml": "jobs:\n test:\n  steps: [{run: npm test}]\n",
		".gitlab-ci.yml":             "test:\n image: node:24.1.0\n variables: {TEST_SCRIPT: test, UNRELATED: '$TOKEN'}\n script: npm run $TEST_SCRIPT\n",
	})
	_, err := SelectCIProvider(root, "")
	var choice *CIProviderChoice
	require.ErrorAs(t, err, &choice)
	require.ElementsMatch(t, []string{"github", "gitlab"}, choice.Providers)
	selectProviderForTest(t, "gitlab")
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Contains(t, strings.Join(scope.Unresolved, ";"), "UNRELATED")
}

func TestNativeConsumerReferencedScriptsBlockCompletion(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	text := ".integration:\n script: npm run integration\nintegration:\n image: node:24.1.0\n script: !reference [.integration, script]\nunit:\n image: node:24.1.0\n script: npm run unit\n"
	root := providerFixture(t, map[string]string{".gitlab-ci.yml": text, "package.json": `{"scripts":{"unit":"jest unit.test.js","integration":"jest integration.test.js"},"devDependencies":{"jest":"30.5.1"}}`})
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Contains(t, strings.Join(scope.Unresolved, ";"), "reference")
	_, err = PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
	require.ErrorContains(t, err, "reference")
	require.NotEqual(t, StatusCompatible, nativeFollowupCheck(t, root, "jest").Status)
	actual, err := os.ReadFile(filepath.Join(root, ".gitlab-ci.yml"))
	require.NoError(t, err)
	require.Equal(t, text, string(actual))
}

func TestNativeConsumerEmptyGitLabServices(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	for _, services := range []string{"[]", "*empty"} {
		root := providerFixture(t, map[string]string{".gitlab-ci.yml": ".empty: &empty []\nservices: [postgres:16]\ntest:\n image: node:24.1.0\n services: " + services + "\n script: npm test\n"})
		plan, err := PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
		require.NoError(t, err)
		_, err = plan.Apply(true)
		require.NoError(t, err)
		require.Equal(t, StatusCompatible, nativeFollowupCheck(t, root, "jest").Status)
	}
}

func TestNativeConsumerAliasedCircleCICheckout(t *testing.T) {
	selectProviderForTest(t, "circleci")
	root := providerFixture(t, map[string]string{".circleci/config.yml": ".tests: &tests\n - checkout\n - run: npm test\njobs:\n test:\n  working_directory: ~/project\n  docker: [{image: 'cimg/node:24.1.0'}]\n  steps: *tests\n"})
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	require.Equal(t, ".", scope.Commands[0].Directory)
	_, err = PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
	require.NoError(t, err)
}

func TestNativeConsumerWorkspaceInstallEvidence(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	for _, install := range []string{"npm ci --workspace packages/foo", "npm ci --workspace=packages/foo", "npm ci -w packages/foo", "npm ci --workspaces", "pnpm install --filter foo", "pnpm install --filter=foo", "pnpm install -F foo"} {
		root := providerFixture(t, map[string]string{"package.json": `{"scripts":{"test":"jest"},"devDependencies":{"jest":"30.5.1","dd-trace":"6.20.1"}}`, ".gitlab-ci.yml": "test:\n image: node:24.1.0\n before_script:\n  - " + install + "\n  - export NODE_OPTIONS='-r dd-trace/ci/init'\n script: npm test\n"})
		w, err := readWorkflows(root)
		require.NoError(t, err)
		require.Empty(t, w[0].Jobs["test"].Steps[2].Native.Tracer, install)
	}
}

func TestNativeConsumerNpmInstalledNode(t *testing.T) {
	selectProviderForTest(t, "buildkite")
	root := providerFixture(t, map[string]string{
		".buildkite/pipeline.yml": "steps:\n - command: npm test\n   plugins:\n    - docker-compose#v5.14.0: {run: app}\n",
		"docker-compose.yml":      "services:\n app:\n  build: .\n",
		"Dockerfile":              "FROM node:24.1.0\nRUN npm install -g --force node@16.20.2\n",
	})
	_, err := PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
	require.ErrorContains(t, err, "runtime")
	w, err := readWorkflows(root)
	require.NoError(t, err)
	for _, job := range w[0].Jobs {
		require.Empty(t, job.Node)
	}
}

func TestNativeConsumerDockerCommandOverridesStep(t *testing.T) {
	selectProviderForTest(t, "buildkite")
	root := providerFixture(t, map[string]string{
		".buildkite/pipeline.yml": "steps:\n - command: npm run unit\n   plugins:\n    - docker#v5.14.0:\n       image: node:24.1.0\n       command: [npm, run, integration]\n",
		"package.json":            `{"scripts":{"unit":"jest unit.test.js","integration":"jest integration.test.js"},"devDependencies":{"jest":"30.5.1"}}`,
	})
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 1)
	require.Contains(t, scope.Commands[0].Command, "integration.test.js")
	_, err = PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
	require.ErrorContains(t, err, "plugin command")
}

func TestNativeConsumerGitLabRunSteps(t *testing.T) {
	root := providerFixture(t, map[string]string{
		".github/workflows/test.yml": "jobs:\n test:\n  steps: [{run: npm test}]\n",
		".gitlab-ci.yml":             "test:\n image: node:24.1.0\n run:\n  - name: tests\n    script: npm test\n",
	})
	_, err := SelectCIProvider(root, "")
	var choice *CIProviderChoice
	require.ErrorAs(t, err, &choice)
	require.ElementsMatch(t, []string{"github", "gitlab"}, choice.Providers)
	selectProviderForTest(t, "gitlab")
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 1)
	require.Contains(t, strings.Join(scope.Commands[0].UnvalidatedSetup, ";"), "GitLab run")
	_, err = PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
	require.ErrorContains(t, err, "GitLab run")
}

func TestNativeConsumerDockerShellSemantics(t *testing.T) {
	selectProviderForTest(t, "buildkite")
	for _, option := range []string{"shell: false", "shell: [pwsh, '-c']", "entrypoint: /entrypoint.sh"} {
		root := providerFixture(t, map[string]string{".buildkite/pipeline.yml": "steps:\n - command: npm test\n   plugins:\n    - docker#v5.14.0:\n       image: node:24.1.0\n       " + option + "\n"})
		_, err := PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
		require.ErrorContains(t, err, "shell/entrypoint")
		require.NotEqual(t, StatusCompatible, nativeFollowupCheck(t, root, "jest").Status)
	}
}
