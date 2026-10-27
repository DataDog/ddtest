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

func TestNativeBoundariesGitLabReferencedMatrix(t *testing.T) {
	selectProviderForTest(t, "gitlab")
	for _, parallel := range []string{"!reference [.matrix, parallel]", "{matrix: !reference [.matrix, parallel, matrix]}"} {
		text := ".matrix:\n parallel:\n  matrix:\n   - DD_CIVISIBILITY_ENABLED: ['false', 'true']\ntest:\n image: node:24.1.0\n parallel: " + parallel + "\n script: npm test\n"
		root := providerFixture(t, map[string]string{".gitlab-ci.yml": text})
		scope, err := DiscoverValidationScope(root, "jest")
		require.NoError(t, err)
		require.Len(t, scope.Commands, 1, "keep a visible unresolved consumer rather than claiming expanded rows")
		require.Contains(t, strings.Join(scope.Unresolved, ";")+strings.Join(scope.Commands[0].UnvalidatedSetup, ";"), "reference")
		_, err = PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
		require.ErrorContains(t, err, "reference")
		require.NotEqual(t, StatusCompatible, nativeFollowupCheck(t, root, "jest").Status)
		actual, err := os.ReadFile(filepath.Join(root, ".gitlab-ci.yml"))
		require.NoError(t, err)
		require.Equal(t, text, string(actual))
	}
}

func TestNativeBoundariesBuildkiteCheckoutMount(t *testing.T) {
	selectProviderForTest(t, "buildkite")
	for _, mount := range []string{"false", "'$MOUNT_CHECKOUT'", "true", ""} {
		t.Run(mount, func(t *testing.T) {
			option := ""
			if mount != "" {
				option = "\n       mount-checkout: " + mount
			}
			text := "steps:\n - command: npm test\n   plugins:\n    - docker#v5.14.0:\n       image: node:24.1.0" + option + "\n"
			root := providerFixture(t, map[string]string{".buildkite/pipeline.yml": text})
			plan, err := PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
			if mount == "false" || strings.Contains(mount, "$") {
				require.ErrorContains(t, err, "checkout")
				require.NotEqual(t, StatusCompatible, nativeFollowupCheck(t, root, "jest").Status)
				actual, err := os.ReadFile(filepath.Join(root, ".buildkite/pipeline.yml"))
				require.NoError(t, err)
				require.Equal(t, text, string(actual))
			} else {
				require.NoError(t, err)
				_, err = plan.Apply(true)
				require.NoError(t, err)
				require.Equal(t, StatusCompatible, nativeFollowupCheck(t, root, "jest").Status)
			}
		})
	}
}

func TestNativeBoundariesBuildkitePluginCommand(t *testing.T) {
	for _, command := range []string{"['npm', 'run', 'test']", "['npx', 'jest', '--testPathPattern=unit tests']"} {
		root := providerFixture(t, map[string]string{
			".buildkite/pipeline.yml":    "steps:\n - plugins:\n    - docker#v5.14.0:\n       image: node:24.1.0\n       command: " + command + "\n",
			".github/workflows/test.yml": "jobs:\n test:\n  steps: [{run: npm test}]\n",
		})
		_, err := SelectCIProvider(root, "")
		var choice *CIProviderChoice
		require.ErrorAs(t, err, &choice)
		require.ElementsMatch(t, []string{"github", "buildkite"}, choice.Providers)
		t.Run(command, func(t *testing.T) {
			selectProviderForTest(t, "buildkite")
			scope, err := DiscoverValidationScope(root, "jest")
			require.NoError(t, err)
			require.Len(t, scope.Commands, 1)
			require.Contains(t, strings.Join(scope.Unresolved, ";")+strings.Join(scope.Commands[0].UnvalidatedSetup, ";"), "plugin command")
			_, err = PlanJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
			require.ErrorContains(t, err, "plugin command")
		})
	}
}

func TestNativeBoundariesProviderLanguageParity(t *testing.T) {
	for _, command := range []string{"npm test", "python -m pytest", "bundle exec rspec", "bundle exec rake test"} {
		root := providerFixture(t, map[string]string{
			".gitlab-ci.yml":             "test:\n script: " + command + "\n",
			".github/workflows/test.yml": "jobs:\n test:\n  steps:\n   - run: " + command + "\n",
		})
		_, err := SelectCIProvider(root, "")
		var choice *CIProviderChoice
		require.ErrorAs(t, err, &choice, command)
		require.ElementsMatch(t, []string{"github", "gitlab"}, choice.Providers, command)
	}
}
