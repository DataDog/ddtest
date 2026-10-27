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

func TestNativeExecutionComposePluginCommand(t *testing.T) {
	root := providerFixture(t, map[string]string{
		".buildkite/pipeline.yml":    "steps:\n - plugins:\n    - docker-compose#v5.14.0:\n       run: app\n       command: [npm, test]\n",
		"docker-compose.yml":         "services:\n app: {image: 'node:24.1.0'}\n",
		".github/workflows/test.yml": "jobs:\n test:\n  steps: [{run: npm test}]\n",
	})
	_, err := SelectCIProvider(root, "")
	var choice *CIProviderChoice
	require.ErrorAs(t, err, &choice)
	require.ElementsMatch(t, []string{"github", "buildkite"}, choice.Providers)
	selectProviderForTest(t, "buildkite")
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 1)
	_, err = PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
	require.ErrorContains(t, err, "plugin command")
}

func TestNativeExecutionStandaloneAssignments(t *testing.T) {
	for _, provider := range []string{"gitlab", "buildkite"} {
		t.Run(provider, func(t *testing.T) {
			selectProviderForTest(t, provider)
			path, text := ".gitlab-ci.yml", "test:\n image: node:24.1.0\n before_script: [TEST_SCRIPT=test]\n script: npm run $TEST_SCRIPT\n"
			if provider == "buildkite" {
				path, text = ".buildkite/pipeline.yml", "image: node:24.1.0\nsteps:\n - commands:\n    - TEST_SCRIPT=test\n    - npm run $TEST_SCRIPT\n"
			}
			root := providerFixture(t, map[string]string{path: text})
			scope, err := DiscoverValidationScope(root, "jest")
			require.NoError(t, err)
			require.Empty(t, scope.Unresolved)
			require.Len(t, scope.Commands, 1)
			require.NotContains(t, scope.Commands[0].Environment, "TEST_SCRIPT", "a shell variable is not implicitly exported")
			plan, err := PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
			require.NoError(t, err)
			_, err = plan.Apply(true)
			require.NoError(t, err)
			require.Equal(t, StatusCompatible, nativeFollowupCheck(t, root, "jest").Status)
		})
	}
}

func TestNativeExecutionComposeConflictingCommands(t *testing.T) {
	selectProviderForTest(t, "buildkite")
	root := providerFixture(t, map[string]string{
		".buildkite/pipeline.yml": "steps:\n - command: npm test\n   plugins:\n    - docker-compose#v5.14.0:\n       run: app\n       command: [npm, test]\n",
		"docker-compose.yml":      "services:\n app: {image: 'node:24.1.0'}\n",
	})
	_, err := PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
	require.ErrorContains(t, err, "conflicts")
}

func TestNativeExecutionComposeConfigArray(t *testing.T) {
	selectProviderForTest(t, "buildkite")
	root := providerFixture(t, map[string]string{
		".buildkite/pipeline.yml": "steps:\n - command: npm test\n   plugins:\n    - docker-compose#v5.14.0:\n       run: app\n       config: [docker-compose.yml, override.yml]\n",
		"docker-compose.yml":      "services:\n app: {image: 'node:24.1.0'}\n",
		"override.yml":            "services:\n app: {image: 'node:16.20.2'}\n",
	})
	_, err := PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
	require.ErrorContains(t, err, "config")
	require.NotEqual(t, StatusCompatible, nativeFollowupCheck(t, root, "jest").Status)
}

func TestNativeExecutionGitLabInputsHeader(t *testing.T) {
	for _, included := range []bool{false, true} {
		files := map[string]string{".github/workflows/test.yml": "jobs:\n test:\n  steps: [{run: npm test}]\n"}
		header := "spec:\n inputs:\n  script: {default: test}\n---\ntest:\n image: node:24.1.0\n script: npm test\n"
		files[".gitlab-ci.yml"] = header
		if included {
			files[".gitlab-ci.yml"], files[".tests.yml"] = "include: {local: .tests.yml}\n", header
		}
		root := providerFixture(t, files)
		_, err := SelectCIProvider(root, "")
		var choice *CIProviderChoice
		require.ErrorAs(t, err, &choice)
		require.ElementsMatch(t, []string{"github", "gitlab"}, choice.Providers)
		t.Run(map[bool]string{true: "include", false: "root"}[included], func(t *testing.T) {
			selectProviderForTest(t, "gitlab")
			scope, err := DiscoverValidationScope(root, "jest")
			require.NoError(t, err)
			require.Contains(t, strings.Join(scope.Unresolved, ";"), "spec")
			_, err = PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
			require.ErrorContains(t, err, "spec")
		})
	}
}

func TestNativeExecutionComputedJenkinsChoice(t *testing.T) {
	root := providerFixture(t, map[string]string{
		"Jenkinsfile":                "pipeline { tools { nodejs '24.1.0' }; stages { stage('Test') { steps { sh 'npm ci'; sh testCommand } } } }",
		".github/workflows/test.yml": "jobs:\n test:\n  steps: [{run: npm test}]\n",
	})
	_, err := SelectCIProvider(root, "")
	var choice *CIProviderChoice
	require.ErrorAs(t, err, &choice)
	require.ElementsMatch(t, []string{"github", "jenkins"}, choice.Providers)
}

func TestNativeExecutionCommandlessPluginChoice(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		condition := ""
		if disabled {
			condition = "   if: 'false'\n"
		}
		root := providerFixture(t, map[string]string{
			".buildkite/pipeline.yml":    "steps:\n - plugins:\n    - docker-compose#v5.14.0: {run: app}\n" + condition,
			"docker-compose.yml":         "services:\n app: {image: 'node:24.1.0', command: 'npm test'}\n",
			".github/workflows/test.yml": "jobs:\n test:\n  steps: [{run: npm test}]\n",
		})
		provider, err := SelectCIProvider(root, "")
		if disabled {
			require.NoError(t, err)
			require.Equal(t, "github", provider)
		} else {
			var choice *CIProviderChoice
			require.ErrorAs(t, err, &choice)
			require.ElementsMatch(t, []string{"github", "buildkite"}, choice.Providers)
		}
	}
}

func TestNativeExecutionInlineInstallEnvironment(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	for _, tc := range []struct {
		inherited, command string
		verified           bool
	}{
		{"test", "NODE_ENV=production npm ci", false},
		{"test", "cross-env NODE_ENV=production npm ci", false},
		{"production", "NODE_ENV=test npm ci", true},
	} {
		root := providerFixture(t, map[string]string{"package.json": `{"scripts":{"test":"jest"},"devDependencies":{"jest":"30.5.1","dd-trace":"6.20.1"}}`, ".gitlab-ci.yml": "test:\n image: node:24.1.0\n variables: {NODE_ENV: '" + tc.inherited + "'}\n before_script:\n  - " + tc.command + "\n  - export NODE_OPTIONS='-r dd-trace/ci/init'\n script: npm test\n"})
		w, err := readWorkflows(root)
		require.NoError(t, err)
		tracer := w[0].Jobs["test"].Steps[2].Native.Tracer
		if tc.verified {
			require.Equal(t, "6.20.1", tracer)
		} else {
			require.Empty(t, tracer, tc.command)
		}
	}
}

func TestNativeExecutionInstallDirectoryChange(t *testing.T) {
	for _, provider := range []string{"circleci", "jenkins"} {
		t.Run(provider, func(t *testing.T) {
			selectProviderForTest(t, provider)
			path, text := ".circleci/config.yml", "jobs:\n test:\n  docker: [{image: 'cimg/node:24.1.0'}]\n  steps:\n   - run: cd tools && npm ci\n   - run:\n      command: npm test\n      environment: {NODE_OPTIONS: '-r dd-trace/ci/init'}\n"
			if provider == "jenkins" {
				path, text = "Jenkinsfile", "pipeline { tools { nodejs '24.1.0' }; stages { stage('Test') { steps { sh 'cd tools && npm ci'; withEnv(['NODE_OPTIONS=-r dd-trace/ci/init']) { sh 'npm test' } } } } }"
			}
			root := providerFixture(t, map[string]string{path: text, "tools/package.json": "{}", "package.json": `{"scripts":{"test":"jest"},"devDependencies":{"jest":"30.5.1","dd-trace":"6.20.1"}}`})
			w, err := readWorkflows(root)
			require.NoError(t, err)
			for _, job := range w[0].Jobs {
				require.Empty(t, job.Steps[len(job.Steps)-1].Native.Tracer)
			}
		})
	}
}
