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

func TestNativeResolutionCircleApproval(t *testing.T) {
	selectProviderForTest(t, "circleci")
	root := providerFixture(t, map[string]string{".circleci/config.yml": "jobs:\n test:\n  docker: [{image: 'cimg/node:24.1.0'}]\n  steps:\n   - checkout\n   - run: npm test\nworkflows:\n main:\n  jobs:\n   - hold: {type: approval}\n   - test: {requires: [hold]}\n"})
	w, err := readWorkflows(root)
	require.NoError(t, err)
	require.Empty(t, w[0].Unresolved)
	plan, err := PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
	require.NoError(t, err)
	_, err = plan.Apply(true)
	require.NoError(t, err)
	require.Equal(t, StatusCompatible, nativeFollowupCheck(t, root, "jest").Status)
}

func TestNativeResolutionBuildkiteUploadVariables(t *testing.T) {
	for _, reference := range []string{"$TEST_SCRIPT", "${TEST_SCRIPT}", "$$TEST_SCRIPT", "$${TEST_SCRIPT}"} {
		t.Run(reference, func(t *testing.T) {
			selectProviderForTest(t, "buildkite")
			source := "image: node:24.1.0\nsteps:\n - key: test\n   env: {TEST_SCRIPT: test}\n   command: npm run " + reference + "\n"
			root := providerFixture(t, map[string]string{".buildkite/pipeline.yml": source})
			plan, err := PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
			if !strings.HasPrefix(reference, "$$") {
				require.ErrorContains(t, err, "upload-time")
				scope, err := DiscoverValidationScope(root, "jest")
				require.NoError(t, err)
				require.Contains(t, strings.Join(scope.Unresolved, ";"), "upload-time")
				require.NotEqual(t, StatusCompatible, nativeFollowupCheck(t, root, "jest").Status)
				actual, err := os.ReadFile(filepath.Join(root, ".buildkite/pipeline.yml"))
				require.NoError(t, err)
				require.Equal(t, source, string(actual))
				return
			}
			require.NoError(t, err)
			_, err = plan.Apply(true)
			require.NoError(t, err)
			actual, err := os.ReadFile(filepath.Join(root, ".buildkite/pipeline.yml"))
			require.NoError(t, err)
			require.Contains(t, string(actual), "npm run "+reference)
			require.Equal(t, StatusCompatible, nativeFollowupCheck(t, root, "jest").Status)
		})
	}
}

func TestNativeResolutionScalarEnvironmentAliases(t *testing.T) {
	selectProviderForTest(t, "circleci")
	root := providerFixture(t, map[string]string{".circleci/config.yml": "jobs:\n test:\n  docker: [{image: 'cimg/node:24.1.0'}]\n  environment: {BASE: &mode integration, MODE: *mode}\n  steps: [checkout, {run: npm test}]\nworkflows:\n main:\n  jobs: [test]\n"})
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	require.Equal(t, "integration", scope.Commands[0].Environment["MODE"])
}

func TestNativeResolutionPrunedTracer(t *testing.T) {
	for _, tc := range []struct {
		name, commands         string
		development, installed bool
	}{
		{"omit", "npm ci\nnpm prune --omit=dev", true, false},
		{"production", "npm ci\nNODE_ENV=production npm prune", true, false},
		{"preloaded", "npm ci\nexport NODE_OPTIONS='-r dd-trace/ci/init'\nnpm prune --omit=dev", true, false},
		{"retain", "npm ci\nnpm prune", true, true},
		{"runtime dependency", "npm ci\nnpm prune --omit=dev", false, true},
		{"prune alone", "npm prune", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selectProviderForTest(t, "gitlab")
			dependencies := `"devDependencies":{"jest":"30.5.1","dd-trace":"6.20.1"}`
			if !tc.development {
				dependencies = `"dependencies":{"dd-trace":"6.20.1"},"devDependencies":{"jest":"30.5.1"}`
			}
			source := "test:\n image: node:24.1.0\n before_script:\n  - " + strings.ReplaceAll(tc.commands, "\n", "\n  - ") + "\n  - export NODE_OPTIONS='-r dd-trace/ci/init'\n script: npm test\n"
			root := providerFixture(t, map[string]string{".gitlab-ci.yml": source, "package.json": `{"scripts":{"test":"jest"},` + dependencies + `}`})
			workflows, err := readWorkflows(root)
			require.NoError(t, err)
			steps := workflows[0].Jobs["test"].Steps
			require.Equal(t, tc.installed, steps[len(steps)-1].Native.Tracer != "")
		})
	}
}

func TestNativeResolutionBuildkiteDisabledGroups(t *testing.T) {
	selectProviderForTest(t, "")
	source := "image: node:24.1.0\nsteps:\n - group: disabled\n   if: 'false'\n   steps:\n    - group: nested\n      if: 'true'\n      steps:\n       - key: test\n         if: 'true'\n         command: npm test\n       - command: buildkite-agent pipeline upload generated.yml\n       - trigger: external\n       - plugins: [unknown#v1]\n"
	root := providerFixture(t, map[string]string{".buildkite/pipeline.yml": source, ".github/workflows/test.yml": "jobs:\n test:\n  steps: [{run: npm test}]\n"})
	selected, err := SelectCIProvider(root, "")
	require.NoError(t, err)
	require.Equal(t, "github", selected)
	w, err := readBuildkite(root, ".buildkite/pipeline.yml")
	require.NoError(t, err)
	require.Empty(t, w.Unresolved)
	require.NotEmpty(t, w.Jobs)
	for _, job := range w.Jobs {
		require.Equal(t, "false", job.If)
	}
}

func TestNativeResolutionUnknownLauncherProviderChoice(t *testing.T) {
	selectProviderForTest(t, "")
	root := providerFixture(t, map[string]string{".gitlab-ci.yml": "test:\n image: node:24.1.0\n script: npm run $TEST_SCRIPT\n", ".github/workflows/test.yml": "jobs:\n test:\n  steps: [{run: npm test}]\n"})
	_, err := SelectCIProvider(root, "")
	var choice *CIProviderChoice
	require.ErrorAs(t, err, &choice)
	require.ElementsMatch(t, []string{"github", "gitlab"}, choice.Providers)
	selectProviderForTest(t, "gitlab")
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.NotEmpty(t, scope.Unresolved)
	require.Empty(t, scope.Commands)
}

func TestNativeResolutionJenkinsOpaqueLibraryPath(t *testing.T) {
	selectProviderForTest(t, "jenkins")
	for _, call := range []string{"runIntegrationTests()", "runIntegrationTests 'integration'", "load 'tests.groovy'", "library 'tests'"} {
		t.Run(call, func(t *testing.T) {
			root := providerFixture(t, map[string]string{
				"Jenkinsfile":         "pipeline { tools { nodejs '24.1.0' }; stages { stage('Test') { steps { sh 'npm ci'; " + call + " } } } }",
				"jenkins/Jenkinsfile": "pipeline { tools { nodejs '24.1.0' }; stages { stage('Test') { steps { sh 'npm ci'; withEnv(['NODE_OPTIONS=-r dd-trace/ci/init']) { sh 'npm test' } } } } }",
				"package.json":        `{"scripts":{"test":"jest"},"devDependencies":{"jest":"30.5.1","dd-trace":"6.20.1"}}`,
			})
			scope, err := DiscoverValidationScope(root, "jest")
			require.NoError(t, err)
			require.Contains(t, strings.Join(scope.Unresolved, ";"), "Jenkins")
			require.NotEqual(t, StatusCompatible, nativeFollowupCheck(t, root, "jest").Status)
			_, err = PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
			require.Error(t, err)
		})
	}
}

func TestNativeResolutionGitLabReferencedRules(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	for _, reference := range []string{"!reference [.rules, rules]", "[!reference [.rules, rules]]"} {
		for _, workflow := range []bool{false, true} {
			source := "variables: {NODE_VERSION: '24.1.0'}\n.rules:\n rules:\n  - if: '$CI_COMMIT_BRANCH == \"legacy\"'\n    variables: {NODE_VERSION: '16.20.2'}\n"
			job := "test:\n image: node:$NODE_VERSION\n script: npm test\n"
			if workflow {
				source += "workflow:\n rules: " + reference + "\n" + job
			} else {
				source += job + " rules: " + reference + "\n"
			}
			root := providerFixture(t, map[string]string{".gitlab-ci.yml": source})
			_, err := PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
			require.ErrorContains(t, err, "rules !reference")
			require.Equal(t, StatusInconclusive, nativeFollowupCheck(t, root, "jest").Status)
			actual, err := os.ReadFile(filepath.Join(root, ".gitlab-ci.yml"))
			require.NoError(t, err)
			require.Equal(t, source, string(actual))
		}
	}
}
