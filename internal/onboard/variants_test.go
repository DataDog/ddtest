package onboard

import (
	"os"
	"path/filepath"
	"strings"
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
          pnpm add -D react@$REACT_VER react-dom@$REACT_VER && pnpm test
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
	for _, command := range scope.Commands {
		require.Equal(t, "jest run", command.Command)
		switch {
		case command.Environment["REACT_VER"] != "":
			version := command.Environment["REACT_VER"]
			require.Equal(t, []BuildCommand{{Directory: ".", Command: "pnpm add -D react@" + version + " react-dom@" + version, Kind: "dependencies", Environment: map[string]string{"REACT_VER": version}}}, command.Prerequisites)
		case command.Environment["NODE_ENV"] == "development":
			patch := `sed -i~ s/src/dist/ jest.config.mts`
			if command.locations[0].Matrix["build"] == "esm" {
				patch = `sed -i~ s/src/dist\\/esm/ jest.config.mts`
			}
			require.Equal(t, []BuildCommand{
				{Directory: ".", Command: "rollup -c"},
				{Directory: ".", Command: patch, Kind: "patch"},
			}, command.Prerequisites)
		default:
			require.Empty(t, command.Prerequisites)
			require.Empty(t, command.Environment)
		}
	}
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

func TestDiscoveryPreservesExplicitEmptyNodeOptions(t *testing.T) {
	for _, value := range []string{"", "-r ${{ env.DD_TRACE_PACKAGE }}"} {
		root := newJestRepository(t, "jobs:\n  test:\n    steps:\n      - run: tsc\n        env: {NODE_OPTIONS: '"+value+"'}\n      - run: jest\n        env: {NODE_OPTIONS: '"+value+"'}\n")
		scope, err := DiscoverValidationScope(root, "jest")
		require.NoError(t, err)
		require.Len(t, scope.Commands, 1)
		require.Contains(t, scope.Commands[0].Environment, "NODE_OPTIONS")
		require.Empty(t, scope.Commands[0].Environment["NODE_OPTIONS"])
		require.Len(t, scope.Commands[0].Prerequisites, 1)
		require.Contains(t, scope.Commands[0].Prerequisites[0].Environment, "NODE_OPTIONS")
		require.Empty(t, scope.Commands[0].Prerequisites[0].Environment["NODE_OPTIONS"])
	}
}

func TestNonTestSecretEnvironmentDoesNotBlockUnrelatedConfiguration(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"test":"jest"}}`), 0600))
	path := filepath.Join(root, ".github/workflows/ci.yml")
	require.NoError(t, os.WriteFile(path, []byte("jobs:\n  test:\n    steps:\n      - run: npm ci\n        env: {NPM_TOKEN: '${{ secrets.NPM_TOKEN }}'}\n      - run: npm test\n"), 0600))
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 1)
	require.Empty(t, scope.Commands[0].UnvalidatedSetup)
	require.Empty(t, scope.Unresolved)
	require.NotEmpty(t, scope.Review)
	require.NoError(t, os.WriteFile(path, []byte("jobs:\n  test:\n    env: {TOKEN: '${{ secrets.ACCESS_TOKEN }}'}\n    steps:\n      - run: npm ci\n      - run: npm test\n"), 0600))
	scope, err = DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 1)
	require.Contains(t, strings.Join(scope.Commands[0].UnvalidatedSetup, "\n"), "CI environment variable TOKEN")
}
