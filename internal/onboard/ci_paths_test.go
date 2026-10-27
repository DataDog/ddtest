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

func TestNativePathsExpandedGitLabControls(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	root := providerFixture(t, map[string]string{".gitlab-ci.yml": "disabled:\n image: node:24.1.0\n variables: {FLAG: 'false', DD_CIVISIBILITY_ENABLED: {value: '$FLAG', expand: true}}\n script: npm test\nenabled:\n image: node:24.1.0\n script: npm test\n"})
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 2)
	require.True(t, scope.Commands[0].CIInstrumentationExcluded || scope.Commands[1].CIInstrumentationExcluded)
	plan, err := PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
	require.NoError(t, err)
	_, err = plan.Apply(true)
	require.NoError(t, err)
	check := nativeFollowupCheck(t, root, "jest")
	require.Len(t, check.Jobs, 2)
	for _, finding := range check.Jobs {
		if finding.Job == "disabled" {
			require.Equal(t, StatusExcluded, finding.Status)
		} else {
			require.Equal(t, StatusCompatible, finding.Status)
		}
	}
	shared := providerFixture(t, map[string]string{".gitlab-ci.yml": "test:\n image: node:24.1.0\n variables: {DD_CIVISIBILITY_ENABLED: {value: '$FLAG', expand: true}}\n parallel: {matrix: [{FLAG: ['false', 'true']}]}\n script: npm test\n"})
	_, err = PlanJavaScriptSetup(shared, "jest", "6.20.1", "datadoghq.com", ">=22")
	require.Error(t, err, "the expanded disabled consumer protects a shared source")
}

func TestNativePathsInlineControlsAreVisible(t *testing.T) {
	for _, provider := range []string{"gitlab", "buildkite", "circleci", "jenkins"} {
		for _, command := range []string{"DD_CIVISIBILITY_ENABLED=false npm test", "cross-env DD_CIVISIBILITY_ENABLED=false npm test"} {
			step := runtimeStep{Run: command}
			resolved, r, err := resolveNativeTestStep(providerFixture(t, map[string]string{}), ciWorkflow{Provider: provider}, runtimeJob{}, step, "jest")
			require.NoError(t, err, command)
			require.True(t, r.Matched, command)
			require.Empty(t, r.Reason)
			require.Equal(t, "false", resolved.Env["DD_CIVISIBILITY_ENABLED"])
			require.NotContains(t, resolved.Run, "DD_CIVISIBILITY_ENABLED")
		}
	}
	root := providerFixture(t, map[string]string{})
	_, _, err := resolveNativeTestStep(root, ciWorkflow{Provider: "gitlab"}, runtimeJob{}, runtimeStep{Run: "DD_CIVISIBILITY_ENABLED=false npm test && npm test"}, "jest")
	require.Error(t, err, "compound controls must not be normalized into one global exclusion")
}

func TestNativePathsGitLabAliasedRules(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	for _, rules := range []string{" rules: *rules\n", ""} {
		workflow := ""
		if rules == "" {
			workflow = "workflow: {rules: *rules}\n"
		}
		root := providerFixture(t, map[string]string{".gitlab-ci.yml": ".rules: &rules\n - if: '$CI_COMMIT_BRANCH == \"legacy\"'\n   variables: {NODE_VERSION: '16.20.2'}\nvariables: {NODE_VERSION: '24.1.0'}\n" + workflow + "test:\n image: node:$NODE_VERSION\n" + rules + " script: npm test\n"})
		_, err := PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
		require.ErrorContains(t, err, "rules")
		require.NotEqual(t, StatusCompatible, nativeFollowupCheck(t, root, "jest").Status)
	}
}

func TestNativePathsAfterScriptAndPostConsumers(t *testing.T) {
	for _, provider := range []string{"gitlab", "jenkins"} {
		t.Run(provider, func(t *testing.T) {
			selectProviderForTest(t, provider)
			path, source := ".gitlab-ci.yml", ".base:\n after_script: [npm run integration]\ntest:\n extends: .base\n image: node:24.1.0\n before_script:\n  - export APP_ENV=main\n  - nvm install 18.20.8\n script: npm run unit\n"
			if provider == "jenkins" {
				path, source = "Jenkinsfile", "pipeline { tools { nodejs '24.1.0' }; stages { stage('Test') { steps { sh 'npm run unit' } } }; post { always { sh 'npm run integration' } } }"
			}
			root := providerFixture(t, map[string]string{path: source, "package.json": `{"scripts":{"unit":"jest unit.test.js","integration":"jest integration.test.js"},"devDependencies":{"jest":"30.5.1"}}`})
			w, err := readWorkflows(root)
			require.NoError(t, err)
			seen := false
			for _, job := range w[0].Jobs {
				for _, step := range job.Steps {
					if step.Run == "npm run integration" {
						seen = true
						if provider == "gitlab" {
							require.NotContains(t, step.Env, "APP_ENV")
						}
					}
				}
			}
			require.True(t, seen)
			scope, err := DiscoverValidationScope(root, "jest")
			require.NoError(t, err)
			if provider == "gitlab" {
				require.Contains(t, strings.Join(scope.Unresolved, ";"), "after_script")
			} else {
				require.Contains(t, strings.Join(scope.Unresolved, ";")+strings.Join(scope.Commands[0].UnvalidatedSetup, ";"), "post")
			}
			_, err = PlanJavaScriptSetup(root, "jest", "5.99.0", "datadoghq.com", ">=18")
			require.Error(t, err)
			actual, err := os.ReadFile(filepath.Join(root, path))
			require.NoError(t, err)
			require.Equal(t, source, string(actual))
		})
	}
}

func TestNativePathsComposeDirectoryIsUnresolved(t *testing.T) {
	selectProviderForTest(t, "buildkite")
	root := providerFixture(t, map[string]string{
		".buildkite/pipeline.yml": "steps:\n - command: npm test\n   plugins:\n    - docker-compose#v5.12.0: {run: app}\n",
		"docker-compose.yml":      "services:\n app:\n  image: node:24.1.0\n  working_dir: /workspace/package\n",
		"package/package.json":    `{"scripts":{"test":"mocha"},"devDependencies":{"mocha":"11.7.6"}}`,
	})
	w, err := readWorkflows(root)
	require.NoError(t, err)
	for _, job := range w[0].Jobs {
		require.Contains(t, strings.Join(job.Gaps, ";"), "working directory")
	}
	_, err = PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
	require.Error(t, err)
	require.NotEqual(t, StatusCompatible, nativeFollowupCheck(t, root, "jest").Status)
}

func TestNativePathsWDIOHookConsumption(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	manifest := `{"scripts":{"test":"wdio run wdio.conf.js"},"devDependencies":{"@wdio/cli":"9.20.0"}}`
	root := providerFixture(t, map[string]string{".gitlab-ci.yml": "test:\n image: node:24.1.0\n script: npm test\n", "package.json": manifest, "wdio.conf.js": "exports.config = {\n runner: 'local',\n framework: 'mocha'\n}\n"})
	plan, err := PlanJavaScriptSetup(root, "webdriverio", "6.20.1", "datadoghq.com", ">=22")
	require.NoError(t, err)
	_, err = plan.Apply(true)
	require.NoError(t, err)
	require.Equal(t, StatusCompatible, nativeFollowupCheck(t, root, "webdriverio").Status)
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(manifest), 0600))
	check := nativeFollowupCheck(t, root, "webdriverio")
	require.NotEqual(t, StatusCompatible, check.Status)
	require.Equal(t, CodeUnverifiedInitialization, check.Jobs[0].Code)
}

func TestNativePathsInstallDirectoryFlags(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	for _, install := range []string{"yarn install --cwd tools", "yarn install --cwd=tools", "pnpm install --dir tools", "pnpm install --dir=tools"} {
		root := providerFixture(t, map[string]string{"package.json": `{"scripts":{"test":"jest"},"dependencies":{"jest":"30.5.1","dd-trace":"6.20.1"}}`, ".gitlab-ci.yml": "test:\n image: node:24.1.0\n before_script:\n  - " + install + "\n  - export NODE_OPTIONS='-r dd-trace/ci/init'\n script: npm test\n"})
		w, err := readWorkflows(root)
		require.NoError(t, err)
		require.Empty(t, w[0].Jobs["test"].Steps[2].Native.Tracer, install)
	}
}

func TestNativePathsCircleCIParameterOwnership(t *testing.T) {
	selectProviderForTest(t, "circleci")
	for _, executor := range []string{"node", "{name: node, tag: '<< parameters.tag >>'}"} {
		root := providerFixture(t, map[string]string{".circleci/config.yml": "executors:\n node:\n  parameters:\n   tag: {type: string, default: '16.20.2'}\n  docker: [{image: 'cimg/node:<< parameters.tag >>'}]\njobs:\n test:\n  parameters:\n   tag: {type: string, default: '24.1.0'}\n  executor: " + executor + "\n  steps: [{run: npm test}]\n"})
		w, err := readWorkflows(root)
		require.NoError(t, err)
		expected := "16.20.2"
		if executor != "node" {
			expected = "24.1.0"
		}
		require.Equal(t, expected, w[0].Jobs["test"].Node)
	}
	root := providerFixture(t, map[string]string{".circleci/config.yml": "commands:\n run-tests:\n  parameters:\n   script: {type: string, default: test}\n  steps: [{run: 'npm run << parameters.script >>'}]\njobs:\n test:\n  parameters:\n   script: {type: string, default: lint}\n  docker: [{image: 'cimg/node:24.1.0'}]\n  steps: [run-tests]\n", "package.json": `{"scripts":{"test":"jest","lint":"eslint"},"devDependencies":{"jest":"30.5.1"}}`})
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
}
