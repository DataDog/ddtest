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

func TestNativeConfigBuildkiteAliasedStepList(t *testing.T) {
	selectProviderForTest(t, "")
	source := "image: node:24.1.0\nsteps:\n - group: disabled\n   if: 'false'\n   steps: &shared\n    - key: test\n      command: npm test\n - group: active\n   key: active\n   steps: *shared\n"
	root := providerFixture(t, map[string]string{".buildkite/pipeline.yml": source, ".github/workflows/test.yml": "jobs:\n test:\n  steps: [{run: npm test}]\n"})
	_, err := SelectCIProvider(root, "")
	var choice *CIProviderChoice
	require.ErrorAs(t, err, &choice)
	require.ElementsMatch(t, []string{"github", "buildkite"}, choice.Providers)
	selectProviderForTest(t, "buildkite")
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 1)
	require.Contains(t, strings.Join(scope.Commands[0].Locations, ";"), "active")
	// A shared scalar cannot be changed for just the active consumer.
	_, err = PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
	require.ErrorContains(t, err, "shared")
}

func TestNativeConfigBuildkiteSkip(t *testing.T) {
	for _, skip := range []string{"true", "'maintenance'", "'false'", "false", "''"} {
		t.Run(skip, func(t *testing.T) {
			selectProviderForTest(t, "buildkite")
			disabled := skip != "false" && skip != "''"
			image := "node:24.1.0"
			if disabled {
				image = "node:18.20.8"
			}
			source := "steps:\n - key: skipped\n   skip: " + skip + "\n   plugins:\n    - docker#v5.13.0: {image: '" + image + "'}\n   command: npm test\n - key: active\n   plugins:\n    - docker#v5.13.0: {image: 'node:24.1.0'}\n   command: npm test\n"
			root := providerFixture(t, map[string]string{".buildkite/pipeline.yml": source})
			w, err := readWorkflows(root)
			require.NoError(t, err)
			require.Equal(t, disabled, w[0].Jobs["skipped"].If == "false")
			plan, err := PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
			require.NoError(t, err)
			_, err = plan.Apply(true)
			require.NoError(t, err)
			w, err = readWorkflows(root)
			require.NoError(t, err)
			require.Equal(t, !disabled, w[0].Jobs["skipped"].Steps[0].Native.Tracer != "")
			require.NotEmpty(t, w[0].Jobs["active"].Steps[0].Native.Tracer)
			require.Equal(t, StatusCompatible, nativeFollowupCheck(t, root, "jest").Status)
		})
	}
}

func TestNativeConfigComposeShell(t *testing.T) {
	selectProviderForTest(t, "buildkite")
	source := "steps:\n - key: test\n   command: npm test\n   plugins:\n    - docker-compose#v5.14.0:\n       run: app\n       shell: [pwsh, '-c']\n"
	root := providerFixture(t, map[string]string{".buildkite/pipeline.yml": source, "docker-compose.yml": "services:\n app: {image: 'node:24.1.0'}\n"})
	_, err := PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
	require.ErrorContains(t, err, "shell")
	require.NotEqual(t, StatusCompatible, nativeFollowupCheck(t, root, "jest").Status)
	actual, err := os.ReadFile(filepath.Join(root, ".buildkite/pipeline.yml"))
	require.NoError(t, err)
	require.Equal(t, source, string(actual))
}

func TestNativeConfigGitLabDeepInheritance(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	for _, inherits := range []string{"extends: .base\n inherit: {default: false}", "extends: [.base, .other]", "extends: .reset"} {
		t.Run(inherits, func(t *testing.T) {
			source := "variables: {DD_CIVISIBILITY_ENABLED: 'false', GLOBAL: value}\n.base:\n inherit: {variables: false}\n image: {name: 'node:24.1.0', entrypoint: ['']}\n.other:\n inherit: {default: false}\n image: {entrypoint: ['']}\n.reset:\n extends: .base\n inherit: null\ntest:\n " + inherits + "\n script: npm test\n"
			root := providerFixture(t, map[string]string{".gitlab-ci.yml": source})
			w, err := readWorkflows(root)
			require.NoError(t, err)
			env := w[0].Jobs["test"].Env
			if inherits == "extends: .reset" {
				require.Equal(t, "false", env["DD_CIVISIBILITY_ENABLED"])
				return
			}
			require.NotContains(t, env, "DD_CIVISIBILITY_ENABLED")
			require.Equal(t, "24.1.0", w[0].Jobs["test"].Node)
			plan, err := PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
			require.NoError(t, err)
			_, err = plan.Apply(true)
			require.NoError(t, err)
			require.Equal(t, StatusCompatible, nativeFollowupCheck(t, root, "jest").Status)
		})
	}
}

func TestNativeConfigUnknownRuntimeConsumer(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	root := providerFixture(t, map[string]string{
		"package.json":   `{"scripts":{"test":"jest"},"devDependencies":{"jest":"30.5.1","dd-trace":"6.20.1"}}`,
		".gitlab-ci.yml": "default:\n image: node:24.1.0\n before_script:\n  - npm ci\n  - export NODE_OPTIONS='-r dd-trace/ci/init'\nknown:\n script: npm test\nunknown:\n script: npm run $TEST_SCRIPT\n",
	})
	check := nativeFollowupCheck(t, root, "jest")
	require.Equal(t, StatusInconclusive, check.Status)
	require.Len(t, check.Jobs, 2)
	for _, finding := range check.Jobs {
		if finding.Job == "known" {
			require.Equal(t, StatusCompatible, finding.Status)
		} else {
			require.Equal(t, StatusInconclusive, finding.Status)
			require.Equal(t, CodeUnresolvedCommand, finding.Code)
			require.NotEmpty(t, finding.Reason)
		}
	}
}

func TestNativeConfigNpmEnvironment(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	for _, tc := range []struct {
		name, env, command string
		installed          bool
	}{
		{"omit", "NPM_CONFIG_OMIT: dev", "npm ci", false},
		{"lowercase", "npm_config_omit: dev", "npm ci", false},
		{"production", "NPM_CONFIG_PRODUCTION: 'true'", "npm ci", false},
		{"only", "NPM_CONFIG_ONLY: production", "npm ci", false},
		{"inline", "{}", "NPM_CONFIG_OMIT=dev npm ci", false},
		{"inline reset", "NPM_CONFIG_OMIT: dev", "NPM_CONFIG_OMIT='' npm ci", true},
		{"non-dev omit", "NPM_CONFIG_OMIT: optional", "npm ci", true},
		{"lowercase precedence", "NPM_CONFIG_OMIT: dev, npm_config_omit: optional", "npm ci", true},
		{"include", "NPM_CONFIG_OMIT: dev, NPM_CONFIG_INCLUDE: dev", "npm ci", true},
		{"command include", "NPM_CONFIG_OMIT: dev", "npm ci --include=dev", true},
		{"command include pair", "NPM_CONFIG_OMIT: dev", "npm ci --include dev", true},
		{"resolved", "MODE: dev, NPM_CONFIG_OMIT: '$MODE'", "npm ci", false},
		{"unresolved", "NPM_CONFIG_OMIT: '$UNKNOWN'", "npm ci", false},
		{"dry run", "NPM_CONFIG_DRY_RUN: 'true'", "npm ci", false},
		{"global", "NPM_CONFIG_GLOBAL: 'true'", "npm ci", false},
		{"prefix", "NPM_CONFIG_PREFIX: other", "npm ci", false},
		{"workspace", "NPM_CONFIG_WORKSPACE: other", "npm ci", false},
		{"lock only", "NPM_CONFIG_PACKAGE_LOCK_ONLY: 'true'", "npm ci", false},
		{"disabled config", "NPM_CONFIG_PRODUCTION: 'false', NPM_CONFIG_DRY_RUN: 'false'", "npm ci", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := providerFixture(t, map[string]string{
				"package.json":   `{"scripts":{"test":"jest"},"devDependencies":{"jest":"30.5.1","dd-trace":"6.20.1"}}`,
				".gitlab-ci.yml": "test:\n image: node:24.1.0\n variables: {" + strings.Trim(tc.env, "{}") + "}\n before_script:\n  - " + tc.command + "\n  - export NODE_OPTIONS='-r dd-trace/ci/init'\n script: npm test\n",
			})
			w, err := readWorkflows(root)
			require.NoError(t, err)
			steps := w[0].Jobs["test"].Steps
			require.Equal(t, tc.installed, steps[len(steps)-1].Native.Tracer != "")
			check := nativeFollowupCheck(t, root, "jest")
			if tc.installed {
				require.Equal(t, StatusCompatible, check.Status)
			} else {
				require.NotEqual(t, StatusCompatible, check.Status)
			}
		})
	}
}
