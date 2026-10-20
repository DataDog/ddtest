package onboard

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVariantPreparationPreservesMatrixAndConditions(t *testing.T) {
	root := newJestRepository(t, `jobs:
  built:
    strategy:
      matrix: {build: [cjs, esm]}
    steps:
      - run: pnpm run build
      - if: matrix.build == 'cjs'
        run: sed -i~ 's/src/dist/' jest.config.mts
      - if: matrix.build == 'esm'
        run: sed -i~ 's/src/dist\/esm/' jest.config.mts
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
	writeScripts(t, root, map[string]string{"test": "jest run", "build": "rollup -c"})
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	var variants []string
	for _, command := range scope.Commands {
		if len(command.Prerequisites) == 0 {
			variants = append(variants, "source")
			continue
		}
		if command.Environment["REACT_VER"] != "" {
			variants = append(variants, "react:"+command.Environment["REACT_VER"])
			continue
		}
		variants = append(variants, command.Prerequisites[1].Command)
	}
	require.ElementsMatch(t, []string{"source", "react:18.0.0", "react:19.0.0", `sed -i~ s/src/dist/ jest.config.mts`, `sed -i~ s/src/dist\\/esm/ jest.config.mts`}, variants)
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
	step, env, err := resolveVariantStep(ciWorkflow{}, runtimeJob{}, runtimeStep{Run: `sed -i~ "1s/^/import.meta.env.MODE='${NODE_ENV}';/" tests/example.tsx`, Env: map[string]string{"NODE_ENV": "development"}}, nil)
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
