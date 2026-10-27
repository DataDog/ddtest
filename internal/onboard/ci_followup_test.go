// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func nativeFollowupCheck(t *testing.T, root, framework string) RuntimeCheck {
	t.Helper()
	return checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "6.20.1", Node: ">=22"}, nil
	}, framework)
}

func TestNativeFollowupGitLabWorkflowRulesBlockEdits(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	text := "variables: {NODE_VERSION: '24.1.0'}\nworkflow:\n rules:\n  - if: '$CI_COMMIT_BRANCH == \"legacy\"'\n    variables: {NODE_VERSION: '16.20.2'}\ntest:\n image: node:$NODE_VERSION\n script: npm test\n"
	root := providerFixture(t, map[string]string{".gitlab-ci.yml": text})
	_, err := PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
	require.ErrorContains(t, err, "rules")
	check := nativeFollowupCheck(t, root, "jest")
	require.Equal(t, StatusInconclusive, check.Status)
	require.Contains(t, check.Jobs[0].Reason, "rules")
	actual, err := os.ReadFile(filepath.Join(root, ".gitlab-ci.yml"))
	require.NoError(t, err)
	require.Equal(t, text, string(actual))
}

func TestNativeFollowupJenkinsComputedEnvironment(t *testing.T) {
	selectProviderForTest(t, "jenkins")
	for _, expression := range []string{`'test' + ':unit'`, "'test'\n + ':unit'", `'test'.trim()`, `"${params.SCRIPT}"`} {
		t.Run(expression, func(t *testing.T) {
			text := "pipeline { tools { nodejs '24.1.0' }; environment { TEST_SCRIPT = " + expression + ` }; stages { stage('Test') { steps { sh "npm run ${env.TEST_SCRIPT}" } } } }`
			root := providerFixture(t, map[string]string{"Jenkinsfile": text, "package.json": `{"scripts":{"test":"jest","test:unit":"jest --runInBand"},"devDependencies":{"jest":"30.5.1"}}`})
			w, err := readWorkflows(root)
			require.NoError(t, err)
			require.NotEmpty(t, w[0].Jobs["pipeline"].Gaps)
			require.NotContains(t, w[0].Jobs["pipeline"].Steps[0].Env, "TEST_SCRIPT")
			_, err = PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
			require.Error(t, err)
			actual, err := os.ReadFile(filepath.Join(root, "Jenkinsfile"))
			require.NoError(t, err)
			require.Equal(t, text, string(actual))
		})
	}
}

func TestNativeFollowupJenkinsShebangNeedsExplicitSetup(t *testing.T) {
	selectProviderForTest(t, "jenkins")
	for _, script := range []string{"npm test", "false\nnpm test"} {
		t.Run(script, func(t *testing.T) {
			text := "pipeline { tools { nodejs '24.1.0' }; stages { stage('Test') { steps { sh '''#!/bin/bash\n" + script + "\n''' } } } }"
			root := providerFixture(t, map[string]string{"Jenkinsfile": text})
			_, err := PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
			require.ErrorContains(t, err, "shebang")
			actual, err := os.ReadFile(filepath.Join(root, "Jenkinsfile"))
			require.NoError(t, err)
			require.Equal(t, text, string(actual))
		})
	}
}

func TestNativeFollowupCircleCIParameterEnvironment(t *testing.T) {
	selectProviderForTest(t, "circleci")
	root := providerFixture(t, map[string]string{".circleci/config.yml": `version: 2.1
jobs:
 test:
  parameters:
   mode: {type: string, default: development}
  docker: [{image: 'cimg/node:24.1.0'}]
  environment: {MODE: '<< parameters.mode >>'}
  steps:
   - run:
      command: npm test
      environment: {STEP_MODE: '<< parameters.mode >>'}
workflows:
 test:
  jobs:
   - test: {name: production, mode: production}
   - test: {name: development}
`})
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 2)
	seen := map[string]bool{}
	for _, command := range scope.Commands {
		require.NotContains(t, command.Environment["MODE"], "<<")
		require.Equal(t, command.Environment["MODE"], command.Environment["STEP_MODE"])
		seen[command.Environment["MODE"]] = true
	}
	require.Equal(t, map[string]bool{"development": true, "production": true}, seen)
}

func TestNativeFollowupCircleCIShell(t *testing.T) {
	selectProviderForTest(t, "circleci")
	for _, shell := range []string{"/bin/bash -eo pipefail", "/bin/sh -e", "/usr/bin/bash --noprofile --norc -eo pipefail"} {
		t.Run(shell, func(t *testing.T) {
			root := providerFixture(t, map[string]string{".circleci/config.yml": "version: 2.1\njobs:\n test:\n  docker: [{image: 'cimg/node:24.1.0'}]\n  steps:\n   - run:\n      command: npm test\n      shell: " + shell + "\n"})
			scope, err := DiscoverValidationScope(root, "jest")
			require.NoError(t, err)
			require.Empty(t, scope.Unresolved)
			require.Len(t, scope.Commands, 1)
			_, err = PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
			require.NoError(t, err)
		})
	}
}

func TestNativeFollowupProductionInstallDoesNotVerifyDevTracer(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	for _, tc := range []struct {
		install  string
		verified bool
	}{
		{"pnpm install --prod", false},
		{"pnpm install --prod=true", false},
		{"npm ci --production=true", false},
		{"npm ci --omit dev", false},
		{"npm ci --omit optional", true},
		{"pnpm install --prod=false", true},
	} {
		t.Run(tc.install, func(t *testing.T) {
			root := providerFixture(t, map[string]string{
				"package.json":   `{"scripts":{"test":"jest"},"devDependencies":{"jest":"30.5.1","dd-trace":"6.20.1"}}`,
				".gitlab-ci.yml": "test:\n image: node:24.1.0\n before_script:\n  - " + tc.install + "\n  - export NODE_OPTIONS='-r dd-trace/ci/init'\n script: npm test\n",
			})
			w, err := readWorkflows(root)
			require.NoError(t, err)
			check := nativeFollowupCheck(t, root, "jest")
			if tc.verified {
				require.Equal(t, "6.20.1", w[0].Jobs["test"].Steps[2].Native.Tracer)
				require.Equal(t, StatusCompatible, check.Status)
			} else {
				require.Empty(t, w[0].Jobs["test"].Steps[2].Native.Tracer)
				require.NotEqual(t, StatusCompatible, check.Status)
			}
		})
	}
}

func TestNativeFollowupExplicitNodeCannotUseImageRuntime(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	for _, executable := range []string{"/opt/node16/bin/node", "./runtime/node"} {
		t.Run(executable, func(t *testing.T) {
			root := providerFixture(t, map[string]string{
				"package.json":   `{"scripts":{"test":"` + executable + ` node_modules/jest/bin/jest.js"},"devDependencies":{"jest":"30.5.1","dd-trace":"6.20.1"}}`,
				".gitlab-ci.yml": "test:\n image: node:24.1.0\n before_script:\n  - npm ci\n  - export NODE_OPTIONS='-r dd-trace/ci/init'\n script: npm test\n",
			})
			_, err := PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
			require.ErrorContains(t, err, "Node")
			check := nativeFollowupCheck(t, root, "jest")
			require.Equal(t, StatusInconclusive, check.Status)
			require.Empty(t, check.Jobs[0].Node)
			require.Equal(t, CodeRuntimeUnknown, check.Jobs[0].Code)
		})
	}
}

func TestNativeFollowupLiteralExportsStayLiteral(t *testing.T) {
	for _, provider := range []string{"gitlab", "buildkite"} {
		t.Run(provider, func(t *testing.T) {
			selectProviderForTest(t, provider)
			path, text := ".gitlab-ci.yml", "test:\n image: node:24.1.0\n before_script:\n  - export TOKEN=blue\n  - export PATTERN='$TOKEN'\n script: npm test\n"
			if provider == "buildkite" {
				path, text = ".buildkite/pipeline.yml", "image: node:24.1.0\nsteps:\n - commands:\n    - export TOKEN=blue\n    - export PATTERN='$TOKEN'\n    - npm test\n"
			}
			root := providerFixture(t, map[string]string{path: text})
			scope, err := DiscoverValidationScope(root, "jest")
			require.NoError(t, err)
			require.Empty(t, scope.Unresolved)
			require.Len(t, scope.Commands, 1)
			require.Equal(t, "$TOKEN", scope.Commands[0].Environment["PATTERN"])
		})
	}
}

func TestNativeFollowupExistingMochaNeedsCoverage(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	manifest := `{"scripts":{"test":"mocha"},"devDependencies":{"mocha":"11.7.6","dd-trace":"6.20.1"}}`
	root := providerFixture(t, map[string]string{
		"package.json":   manifest,
		".gitlab-ci.yml": "test:\n image: node:24.1.0\n before_script:\n  - npm ci\n  - export NODE_OPTIONS='-r dd-trace/ci/init'\n script: npm test\n",
	})
	check := nativeFollowupCheck(t, root, "mocha")
	require.NotEqual(t, StatusCompatible, check.Status)
	require.Contains(t, check.Jobs[0].Reason, "coverage")
	_, err := PlanJavaScriptSetup(root, "mocha", "6.20.1", "datadoghq.com", ">=22")
	var gap *SetupDependencyGap
	require.ErrorAs(t, err, &gap)
	require.Contains(t, gap.Packages, "nyc")
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(strings.Replace(manifest, `"dd-trace":"6.20.1"`, `"dd-trace":"6.20.1","nyc":"17.1.0"`, 1)), 0600))
	plan, err := PlanJavaScriptSetup(root, "mocha", "6.20.1", "datadoghq.com", ">=22")
	require.NoError(t, err)
	diff, err := plan.Diff()
	require.NoError(t, err)
	require.Contains(t, diff, "npx --no-install nyc -- npm test")
	require.NotContains(t, diff, "npm install --prefix", "preserve the existing tracer install")
	_, err = plan.Apply(true)
	require.NoError(t, err)
	require.Equal(t, StatusCompatible, nativeFollowupCheck(t, root, "mocha").Status)
	second, err := PlanJavaScriptSetup(root, "mocha", "6.20.1", "datadoghq.com", ">=22")
	require.NoError(t, err)
	require.True(t, second.Configured())
}

func TestNativeFollowupFloatingNodeIsUnresolved(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	for _, selector := range []string{"18", "20", "24", "22.1", "24.1.0"} {
		t.Run(selector, func(t *testing.T) {
			root := providerFixture(t, map[string]string{
				"package.json":   `{"scripts":{"test":"vitest run"},"devDependencies":{"vitest":"2.1.9","dd-trace":"6.20.1"}}`,
				".gitlab-ci.yml": "test:\n image: node:" + selector + "\n before_script:\n  - npm ci\n  - export NODE_OPTIONS='-r dd-trace/ci/init --import dd-trace/register.js'\n script: npm test\n",
			})
			_, err := PlanJavaScriptSetup(root, "vitest", "6.20.1", "datadoghq.com", ">=22")
			check := nativeFollowupCheck(t, root, "vitest")
			if selector == "24.1.0" {
				require.NoError(t, err)
				require.Equal(t, StatusCompatible, check.Status)
			} else {
				require.ErrorContains(t, err, "unresolved")
				require.Equal(t, StatusInconclusive, check.Status)
				require.Equal(t, CodeRuntimeUnknown, check.Jobs[0].Code)
			}
		})
	}
}
