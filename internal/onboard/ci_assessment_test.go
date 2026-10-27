// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNativeAssessmentCucumberCoverage(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	for _, esm := range []bool{false, true} {
		t.Run(strconv.FormatBool(esm), func(t *testing.T) {
			manifest := `{"scripts":{"test":"` + cucumberScopedOptions + ` nyc -- cucumber-js"},"devDependencies":{"@cucumber/cucumber":"13.2.1","nyc":"17.1.0"}}`
			// The scoped prefix contains quotes; encode the script as JSON.
			manifest = strings.Replace(manifest, cucumberScopedOptions, strings.ReplaceAll(cucumberScopedOptions, `"`, `\"`), 1)
			coverage := ""
			if esm {
				manifest = strings.Replace(manifest, "{", `{"type":"module",`, 1)
				coverage = "--experimental-loader @istanbuljs/esm-loader-hook"
			}
			setup := nativeSetup{Framework: "cucumber", Version: "6.20.1", Site: "datadoghq.com", ESM: true, Coverage: coverage}
			root := providerFixture(t, map[string]string{"package.json": manifest, ".gitlab-ci.yml": "test:\n image: node:24.1.0\n script: " + strconv.Quote(wrapNativeCommand(setup, "npm test", "gitlab")) + "\n"})
			require.Equal(t, StatusCompatible, nativeFollowupCheck(t, root, "cucumber").Status)
			if esm {
				setup.Coverage = ""
				require.NoError(t, os.WriteFile(filepath.Join(root, ".gitlab-ci.yml"), []byte("test:\n image: node:24.1.0\n script: "+strconv.Quote(wrapNativeCommand(setup, "npm test", "gitlab"))+"\n"), 0600))
			} else {
				require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(strings.Replace(manifest, "nyc -- ", "", 1)), 0600))
			}
			check := nativeFollowupCheck(t, root, "cucumber")
			require.NotEqual(t, StatusCompatible, check.Status)
			require.Equal(t, CodeUnverifiedInitialization, check.Jobs[0].Code)
		})
	}
}

func TestNativeAssessmentJenkinsWorkspace(t *testing.T) {
	selectProviderForTest(t, "jenkins")
	for _, separate := range []bool{false, true} {
		pipelineAgent, stageAgent := "agent { docker { image 'node:24.1.0' } }", ""
		if separate {
			pipelineAgent, stageAgent = "agent none", "agent { docker { image 'node:24.1.0' } }"
		}
		root := providerFixture(t, map[string]string{
			"package.json": `{"scripts":{"test":"jest"},"devDependencies":{"jest":"30.5.1","dd-trace":"6.20.1"}}`,
			"Jenkinsfile":  "pipeline { " + pipelineAgent + "; stages { stage('Install') { " + stageAgent + "; steps { sh 'npm ci' } }; stage('Test') { " + stageAgent + "; steps { withEnv(['NODE_OPTIONS=-r dd-trace/ci/init']) { sh 'npm test' } } } } }",
		})
		w, err := readWorkflows(root)
		require.NoError(t, err)
		steps := w[0].Jobs["pipeline"].Steps
		require.Len(t, steps, 2)
		if separate {
			require.Empty(t, steps[1].Native.Tracer, "installation in a different agent cannot prove the test workspace")
		} else {
			require.Equal(t, "6.20.1", steps[1].Native.Tracer)
		}
	}
}

func TestNativeAssessmentCircleCIDefaultShell(t *testing.T) {
	selectProviderForTest(t, "circleci")
	for _, owner := range []string{"job", "executor"} {
		for _, override := range []bool{false, true} {
			text := "jobs:\n test:\n  docker: [{image: 'cimg/node:24.1.0'}]\n  shell: /usr/bin/fish\n  steps:\n   - run:\n      command: npm test\n"
			if owner == "executor" {
				text = "executors:\n node:\n  docker: [{image: 'cimg/node:24.1.0'}]\n  shell: /usr/bin/fish\njobs:\n test:\n  executor: node\n  steps:\n   - run:\n      command: npm test\n"
			}
			if override {
				text += "      shell: '/bin/bash -eo pipefail'\n"
			}
			root := providerFixture(t, map[string]string{".circleci/config.yml": text})
			_, err := PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
			if override {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				scope, err := DiscoverValidationScope(root, "jest")
				require.NoError(t, err)
				require.Contains(t, strings.Join(scope.Unresolved, ";"), "shell")
			}
		}
	}
}

func TestNativeAssessmentGitLabScriptAliases(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	root := providerFixture(t, map[string]string{
		".gitlab-ci.yml": ".integration: &integration [npm run integration]\nunit:\n image: node:24.1.0\n script: npm run unit\nintegration:\n image: node:24.1.0\n script: *integration\n",
		"package.json":   `{"scripts":{"unit":"jest unit.test.js","integration":"jest integration.test.js"},"devDependencies":{"jest":"30.5.1"}}`,
	})
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 2)
	_, err = PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
	require.Error(t, err, "aliased source edits must not bypass ownership checks")
}

func TestNativeAssessmentIncompleteSetupRetainsTracerPin(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	setup := nativeSetup{Framework: "mocha", Version: "6.19.0", Site: "datadoghq.com"}
	root := providerFixture(t, map[string]string{"package.json": `{"scripts":{"test":"mocha"},"devDependencies":{"mocha":"11.7.6","nyc":"17.1.0"}}`, ".gitlab-ci.yml": "test:\n image: node:24.1.0\n script: " + strconv.Quote(wrapNativeCommand(setup, "npm test", "gitlab")) + "\n"})
	check := nativeFollowupCheck(t, root, "mocha")
	require.Len(t, check.Jobs, 1)
	require.Equal(t, CodeUnverifiedInitialization, check.Jobs[0].Code)
	require.Equal(t, "dd-trace@6.19.0", check.Jobs[0].Tracer)
	require.Equal(t, "6.19.0", check.Jobs[0].TracerRequested)
	plan, err := PlanJavaScriptSetup(root, "mocha", "6.19.0", "datadoghq.com", ">=22")
	require.NoError(t, err)
	diff, err := plan.Diff()
	require.NoError(t, err)
	require.Contains(t, diff, "npx --no-install nyc -- npm test")
}

func TestNativeAssessmentPnpmProductionAlias(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	for _, dependency := range []string{"dependencies", "devDependencies"} {
		root := providerFixture(t, map[string]string{"package.json": `{"scripts":{"test":"jest"},"` + dependency + `":{"jest":"30.5.1","dd-trace":"6.20.1"}}`, ".gitlab-ci.yml": "test:\n image: node:24.1.0\n before_script:\n  - pnpm install -P\n  - export NODE_OPTIONS='-r dd-trace/ci/init'\n script: npm test\n"})
		w, err := readWorkflows(root)
		require.NoError(t, err)
		tracer := w[0].Jobs["test"].Steps[2].Native.Tracer
		if dependency == "dependencies" {
			require.Equal(t, "6.20.1", tracer)
		} else {
			require.Empty(t, tracer)
		}
	}
}

func TestNativeAssessmentGitLabExpansionPrecedesExports(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	root := providerFixture(t, map[string]string{
		"package.json":   `{"scripts":{"unit":"jest unit.test.js","integration":"jest integration.test.js"},"devDependencies":{"jest":"30.5.1"}}`,
		".gitlab-ci.yml": "test:\n image: node:24.1.0\n variables: {MODE: unit, TEST_SCRIPT: '$MODE'}\n before_script: [export MODE=integration]\n script: npm run $TEST_SCRIPT\n",
	})
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	require.Contains(t, scope.Commands[0].Command, "unit.test.js")
	require.Equal(t, "unit", scope.Commands[0].Environment["TEST_SCRIPT"])
	require.Equal(t, "integration", scope.Commands[0].Environment["MODE"])
}

func TestNativeAssessmentCircleCIAliasedSharedSteps(t *testing.T) {
	selectProviderForTest(t, "circleci")
	text := "jobs:\n modern:\n  docker: [{image: 'cimg/node:24.1.0'}]\n  steps: &tests\n   - run: npm test\n legacy:\n  docker: [{image: 'cimg/node:16.20.2'}]\n  steps: *tests\n"
	root := providerFixture(t, map[string]string{".circleci/config.yml": text})
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 2)
	_, err = PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
	require.ErrorContains(t, err, "shared")
	actual, err := os.ReadFile(filepath.Join(root, ".circleci/config.yml"))
	require.NoError(t, err)
	require.Equal(t, text, string(actual))
}
