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

func TestCompositeDynamicConditionInputsRemainUnresolved(t *testing.T) {
	root := newJestRepository(t, `jobs:
  tests:
    steps:
      - uses: ./.github/actions/test
        with: {enabled: '${{ secrets.ENABLED }}'}
`)
	writeComposite(t, root, "test", `inputs:
  enabled: {default: ''}
runs:
  using: composite
  steps:
    - if: inputs.enabled
      run: jest
      shell: bash
`)
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.NotEmpty(t, scope.Unresolved)
	for _, command := range scope.Commands {
		require.NotEmpty(t, command.UnvalidatedSetup)
	}
}

func TestCompositeConditionInputsResolveKnownMatrixAndContext(t *testing.T) {
	for _, tc := range []struct {
		value      string
		row        map[string]any
		env        map[string]string
		want       bool
		unresolved bool
	}{
		{value: "${{ matrix.enabled }}", row: map[string]any{"enabled": ""}, want: false},
		{value: "${{ matrix.enabled }}", row: map[string]any{"enabled": false}, want: true},
		{value: "${{ matrix.enabled }}", row: map[string]any{"enabled": "yes"}, want: true},
		{value: "${{ github.ref }}", env: map[string]string{"\x00github.ref": "refs/heads/main"}, want: true},
		{value: "${{ secrets.ENABLED }}", unresolved: true},
	} {
		step := runtimeStep{If: "inputs.enabled", Inputs: map[string]string{"enabled": tc.value}}
		got, err := stepCondition(step, tc.row, tc.env)
		if tc.unresolved {
			require.Error(t, err)
			continue
		}
		require.NoError(t, err)
		require.Equal(t, tc.want, got)
	}
}

func TestCompositeMatrixConditionSelectsOnlyMatchingInput(t *testing.T) {
	root := newJestRepository(t, `jobs:
  tests:
    strategy:
      matrix: {suite: [unit, integration]}
    steps:
      - uses: ./.github/actions/test
        with: {suite: '${{ matrix.suite }}'}
`)
	writeComposite(t, root, "test", `inputs:
  suite: {default: unit}
runs:
  using: composite
  steps:
    - run: jest
      shell: bash
      if: inputs.suite == 'unit'
`)
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Equal(t, []string{"jest"}, commandNames(scope.Commands))
	require.Equal(t, []commandLocation{{Workflow: ".github/workflows/test.yml", Job: "tests", Step: 1, Matrix: map[string]any{"suite": "unit"}, Command: "jest"}}, scope.Commands[0].locations)
}
