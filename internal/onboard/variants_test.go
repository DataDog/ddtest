package onboard

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVariantRuntimeIgnoresResolvedTypecheckOnlyMatrix(t *testing.T) {
	root := newJestRepository(t, `jobs:
  types:
    strategy:
      matrix: {typescript: [5.9.3, 5.8.3]}
    steps:
      - uses: actions/setup-node@v6
        with: {node-version: '24'}
      - run: pnpm add -D typescript@$TS_VERSION
        env: {TS_VERSION: '${{ matrix.typescript }}'}
      - run: pnpm test:types
  tests:
    steps:
      - uses: actions/setup-node@v6
        with: {node-version: '24'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: js, js-tracer-version: '6.18.0'}
      - run: pnpm test
        env: {NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}'}
`)
	writeScripts(t, root, map[string]string{"test": "vitest run", "test:types": "tsc --noEmit"})
	result := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "6.18.0", Node: ">=22"}, nil
	}, "vitest")
	require.Equal(t, "compatible", result.Status)
	require.Len(t, result.Jobs, 1)
	require.Equal(t, "tests", result.Jobs[0].Job)
	require.NotEmpty(t, result.Review, "type checking remains visible for separate review")
}

func TestVariantPreparationPreservesMatrixAndConditions(t *testing.T) {
	root := newJestRepository(t, `jobs:
  built:
    strategy:
      matrix: {build: [cjs, esm]}
    steps:
      - run: pnpm run build
      - if: matrix.build == 'cjs'
        run: sed -i~ 's/src/dist/' vitest.config.mts
      - if: matrix.build == 'esm'
        run: sed -i~ 's/src/dist\/esm/' vitest.config.mts
      - run: pnpm test
        env: {NODE_ENV: development}
  react:
    strategy:
      matrix: {react: [18.0.0, 19.0.0]}
    steps:
      - run: |
          pnpm add -D react@$REACT_VER react-dom@$REACT_VER
          pnpm test
        env: {REACT_VER: '${{ matrix.react }}'}
  source:
    steps:
      - run: pnpm test
`)
	writeScripts(t, root, map[string]string{"test": "vitest run", "build": "rollup -c"})
	scope, err := DiscoverValidationScope(root, "vitest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 5)
	for i := 0; i < 2; i++ {
		require.Len(t, scope.Commands[i].Prerequisites, 2)
		require.Equal(t, "rollup -c", scope.Commands[i].Prerequisites[0].Command)
		require.Equal(t, "patch", scope.Commands[i].Prerequisites[1].Kind)
		require.Equal(t, "development", scope.Commands[i].Environment["NODE_ENV"])
	}
	require.NotEqual(t, scope.Commands[0].Prerequisites[1].Command, scope.Commands[1].Prerequisites[1].Command)
	for i, version := range []string{"18.0.0", "19.0.0"} {
		entry := scope.Commands[i+2]
		require.Len(t, entry.Prerequisites, 1)
		require.Equal(t, "pnpm add -D react@"+version+" react-dom@"+version, entry.Prerequisites[0].Command)
		require.Equal(t, version, entry.Environment["REACT_VER"])
	}
	require.Empty(t, scope.Commands[4].Prerequisites)
	require.Empty(t, scope.Commands[4].Environment)
}

func TestVariantExpansionDoesNotExecuteShellOrUseAmbientVariables(t *testing.T) {
	for _, command := range []string{"pnpm add -D react@$UNDECLARED && pnpm test", "pnpm add -D react@$(touch marker) && pnpm test"} {
		_, _, err := resolveVariantStep(ciWorkflow{}, runtimeJob{}, runtimeStep{Run: command, Env: map[string]string{"REACT_VER": "18"}}, nil)
		require.Error(t, err)
	}
	step, env, err := resolveVariantStep(ciWorkflow{}, runtimeJob{}, runtimeStep{Run: `sed -i~ "1s/^/import.meta.env.MODE='${NODE_ENV}';/" tests/*.tsx`, Env: map[string]string{"NODE_ENV": "development"}}, nil)
	require.NoError(t, err)
	require.Equal(t, "development", env["NODE_ENV"])
	require.Contains(t, step.Run, "development")
	require.NotContains(t, step.Run, "${NODE_ENV}")
	for _, command := range []string{"sed -i 's/x/y/e' config.js", "sed -i 's/x/y/' ../config.js", "pnpm add -D pkg@https://example.com/pkg.tgz"} {
		words, err := staticCommands(command)
		require.NoError(t, err)
		_, ok := localPreparation("/repo", "/repo", words[0])
		require.False(t, ok, command)
	}
}

func TestPnpmBuildOrchestrationRetainsWholeCommand(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"build": `pnpm run prebuild && pnpm run "/^build:.*/" && pnpm run postbuild`, "prebuild": "shx rm -rf dist", "postbuild": "node transform.js", "build:cjs": "rollup -c", "build:esm": "rollup -c --esm"})
	result := resolveJestCommand(root, "pnpm run build", nil)
	require.Empty(t, result.Reason)
	require.Len(t, result.Builds, 1)
	require.Equal(t, "package-build", result.Builds[0].Kind)
	require.Equal(t, "pnpm run build", result.Builds[0].Command)
	writeScripts(t, root, map[string]string{"build": `pnpm run "/^build:.*/"`, "build:test": "jest"})
	result = resolveJestCommand(root, "pnpm run build", nil)
	require.True(t, result.Matched || strings.Contains(result.Reason, "script"), "must not silently treat tests as build-only")
}
