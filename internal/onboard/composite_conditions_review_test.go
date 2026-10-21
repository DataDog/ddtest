// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCompositeConditionsKeepInputStringsAndCallerEnvironment(t *testing.T) {
	for _, enabled := range []string{"false", "0", "true", ""} {
		t.Run("input="+enabled, func(t *testing.T) {
			root := newJestRepository(t, `env: {MODE: workflow}
defaults:
  run: {working-directory: app}
jobs:
  tests:
    env: {MODE: ci}
    steps:
      - uses: ./.github/actions/outer
        if: env.MODE == 'ci'
`)
			writeScripts(t, root, map[string]string{"test": "jest --config root.js"})
			writeScripts(t, filepath.Join(root, "app"), map[string]string{"test": "jest --config wrong.js"})
			writeComposite(t, root, "outer", fmt.Sprintf(`inputs:
  enabled: {default: '%s'}
runs:
  using: composite
  steps:
    - uses: ./.github/actions/inner
      if: ${{ inputs.enabled }}
      env: {MODE: unit}
`, enabled))
			writeComposite(t, root, "inner", `runs:
  using: composite
  steps:
    - run: npm test
      shell: bash
      if: env.MODE == 'unit'
      env: {CHILD: yes}
`)
			scope, err := DiscoverValidationScope(root, "jest")
			require.NoError(t, err)
			require.Empty(t, scope.Unresolved)
			if enabled == "" {
				require.Empty(t, scope.Commands)
				return
			}
			require.Equal(t, []string{"jest --config root.js"}, commandNames(scope.Commands))
			require.Equal(t, ".", scope.Commands[0].Directory)
			require.Equal(t, map[string]string{"MODE": "unit", "CHILD": "yes"}, scope.Commands[0].Environment)
		})
	}
}

func TestCompositeChildOverrideDoesNotChangeParentCondition(t *testing.T) {
	for _, caller := range []string{"ci", "local"} {
		root := newJestRepository(t, fmt.Sprintf(`jobs:
  tests:
    env: {MODE: '%s'}
    steps:
      - uses: ./.github/actions/test
        if: env.MODE == 'ci'
`, caller))
		writeComposite(t, root, "test", `runs:
  using: composite
  steps:
    - run: jest
      shell: bash
      env: {MODE: unit}
`)
		scope, err := DiscoverValidationScope(root, "jest")
		require.NoError(t, err)
		require.Empty(t, scope.Unresolved)
		if caller == "ci" {
			require.Equal(t, []string{"jest"}, commandNames(scope.Commands))
		} else {
			require.Empty(t, scope.Commands)
		}
	}
}
