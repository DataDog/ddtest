package onboard

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func immerScopeFixture(t *testing.T) string {
	t.Helper()
	root := newJestRepository(t, `jobs:
  test:
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '24'}
      - run: yarn --frozen-lockfile
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: js, js-tracer-version: '6.18.0'}
      - run: yarn test
        env: {NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}'}
      - run: yarn coverage
        env: {NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}'}
      - run: yarn test:perf
  release:
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '24'}
      - run: yarn test
  docs:
    steps:
      - run: cd website && yarn && yarn build
`)
	writeScripts(t, root, map[string]string{"test": "vitest run && yarn test:build && yarn test:flow", "test:build": "yarn build && vitest run --config vitest.config.build.ts", "test:flow": "yarn flow check tests/flow", "build": "tsup", "coverage": "vitest run --coverage", "test:perf": "cd performance && node bench.mjs"})
	writeScripts(t, filepath.Join(root, "website"), map[string]string{"build": "docusaurus build"})
	require.NoError(t, os.Mkdir(filepath.Join(root, "performance"), 0755))
	return root
}

func TestScopeRetainsEveryImmerConfigurationAndConsumer(t *testing.T) {
	root := immerScopeFixture(t)
	scope, err := DiscoverValidationScope(root, "vitest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 3)
	require.Equal(t, "vitest run", scope.Commands[0].Command)
	require.Equal(t, "vitest run --config vitest.config.build.ts", scope.Commands[1].Command)
	require.Equal(t, "vitest run --coverage", scope.Commands[2].Command)
	require.Empty(t, scope.Commands[0].Prerequisites)
	require.Equal(t, []BuildCommand{{Directory: ".", Command: "tsup"}}, scope.Commands[1].Prerequisites)
	require.Empty(t, scope.Commands[2].Prerequisites)
	require.Len(t, scope.Commands[0].Locations, 2)
	require.Len(t, scope.Commands[1].Locations, 2)
	require.Contains(t, strings.Join(scope.Review, "\n"), "node bench.mjs")
	result := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "6.18.0", Node: ">=22"}, nil
	}, "vitest")
	require.Equal(t, "incompatible", result.Status)
	var missing []RuntimeFinding
	for _, finding := range result.Jobs {
		if finding.Code == "missing_instrumentation" {
			missing = append(missing, finding)
		}
	}
	require.Len(t, missing, 1)
	require.Equal(t, "release", missing[0].Job)
}

func TestBuildPrerequisitesKeepOrderAndDistinctConfigurations(t *testing.T) {
	for _, framework := range []string{"jest", "vitest"} {
		t.Run(framework, func(t *testing.T) {
			root := newJestRepository(t, `jobs:
  test:
    steps:
      - run: npm run test:a
      - run: npm run test:b
      - run: npm run test:a
`)
			writeScripts(t, root, map[string]string{
				"test:a":  "yarn build:a && " + framework + " run && yarn build:after",
				"test:b":  "yarn build:b && " + framework + " run",
				"build:a": "tsc && tsup --config a.ts", "build:b": "tsup --config b.ts", "build:after": "rollup",
			})
			scope, err := DiscoverValidationScope(root, framework)
			require.NoError(t, err)
			require.Len(t, scope.Commands, 2, "same test command with different builds is a different configuration")
			require.Len(t, scope.Commands[0].Locations, 2)
			require.Equal(t, []BuildCommand{{Directory: ".", Command: "tsc"}, {Directory: ".", Command: "tsup --config a.ts"}}, scope.Commands[0].Prerequisites)
			require.Equal(t, []BuildCommand{{Directory: ".", Command: "tsup --config b.ts"}}, scope.Commands[1].Prerequisites)
			selected := ValidationCommands(root, "MODE=production npm run test:a", framework)
			require.Equal(t, "MODE=production tsc", selected[0].Prerequisites[0].Command, "must not silently discard the alias environment")
		})
	}
}

func TestPartialResolutionDoesNotHideMissingInstrumentation(t *testing.T) {
	root := newJestRepository(t, `jobs:
  test:
    steps:
      - run: npm test
`)
	writeScripts(t, root, map[string]string{"test": "node unknown-setup.js && vitest run && node unknown-after.js"})
	scope, err := DiscoverValidationScope(root, "vitest")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 1)
	require.NotEmpty(t, scope.Unresolved)
	check := checkCIRuntimes(t.Context(), root, nil, nil, "vitest")
	require.Equal(t, "incompatible", check.Status)
	require.Len(t, check.Jobs, 2)
	require.Contains(t, check.Jobs[0].Reason, "unknown-setup.js")
	require.Equal(t, "missing_instrumentation", check.Jobs[1].Code)
}

func TestScopeDirectoryChangesAndUnsafeInputs(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"test": "vitest"})
	writeScripts(t, filepath.Join(root, "app"), map[string]string{"test": "vitest run --coverage"})
	for _, tc := range []struct {
		command   string
		count     int
		directory string
	}{
		{"cd app && npm test", 1, "app"},
		{"cd app && cd .. && npm test", 1, "."},
		{"cd missing && npm test", 0, ""},
		{"cd /tmp && npm test", 0, ""},
		{"cd ../ && npm test", 0, ""},
		{"cd $(node setup.js) && npm test", 0, ""},
	} {
		t.Run(tc.command, func(t *testing.T) {
			remaining := 256
			result := resolveJestSequence(root, root, tc.command, nil, &remaining, "vitest")
			require.Len(t, result.Tests, tc.count)
			if tc.count > 0 {
				require.Empty(t, result.Reason)
				require.Equal(t, tc.directory, result.Tests[0].Directory)
			} else {
				require.NotEmpty(t, result.Reason)
			}
		})
	}
	for _, command := range []string{"yarn --frozen-lockfile", "yarn --non-interactive --frozen-lockfile"} {
		require.Empty(t, resolveJestCommand(root, command, nil).Reason)
	}
	require.NotEmpty(t, resolveJestCommand(root, "yarn --unknown-option", nil).Reason)
	require.Equal(t, "CI=true vitest run", ValidationCommands(root, "CI=true npx vitest run", "vitest")[0].Command, "do not discard shell environment for batch execution")
	require.Equal(t, "CI=true vitest", ValidationCommands(root, "CI=true npm test", "vitest")[0].Command, "preserve environment through package aliases")
}

func TestDynamicSetupRetainsTestsButDoesNotValidateVariants(t *testing.T) {
	root := newJestRepository(t, `jobs:
  source:
    steps:
      - run: pnpm run test:types
      - run: pnpm test
  react:
    steps:
      - run: |
          pnpm add -D react@$REACT_VER react-dom@$REACT_VER
          pnpm test
  built:
    steps:
      - run: sed -i~ 's/src/dist/' vitest.config.mts
      - run: pnpm test
`)
	writeScripts(t, root, map[string]string{"test": "vitest run", "test:types": "tsc --noEmit"})
	scope, err := DiscoverValidationScope(root, "vitest")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 3, "source, built and alternate dependency tests must not collapse")
	for _, entry := range scope.Commands {
		require.Equal(t, "vitest run", entry.Command)
		if !strings.Contains(entry.Locations[0], " / react /") {
			require.Empty(t, entry.UnvalidatedSetup)
		} else {
			require.NotEmpty(t, entry.UnvalidatedSetup)
		}
	}
	require.Contains(t, strings.Join(scope.Unresolved, "\n"), "react@$REACT_VER")
	check := checkCIRuntimes(t.Context(), root, nil, nil, "vitest")
	var missing []string
	for _, job := range check.Jobs {
		if job.Code == "missing_instrumentation" {
			missing = append(missing, job.Job)
		}
	}
	require.ElementsMatch(t, []string{"source", "react", "built"}, missing)
}

func TestPartialShellDiscoveryPreservesStateBoundaries(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"test": "vitest run"})
	for _, command := range []string{
		"pnpm add react@$VERSION && pnpm test",
		"pnpm add react@$(node version.js)\npnpm test",
		"node setup.js > setup.log && pnpm test",
	} {
		remaining := 256
		result := resolveJestSequence(root, root, command, nil, &remaining, "vitest")
		require.Len(t, result.Tests, 1, command)
		require.NotEmpty(t, result.Tests[0].UnvalidatedSetup, command)
		require.NotEmpty(t, result.Reason, command)
		require.False(t, result.SingleJest, command)
	}
	for _, command := range []string{
		"cd $DIR && pnpm test", "source $SETUP && pnpm test",
		"export NODE_OPTIONS=$OPTIONS; pnpm test", "eval $SETUP && pnpm test",
		"if true; then cd app; fi; pnpm test", "pnpm add $DEP | pnpm test",
		"pnpm add $DEP & pnpm test", "$COMMAND && pnpm test",
	} {
		remaining := 256
		result := resolveJestSequence(root, root, command, nil, &remaining, "vitest")
		require.Empty(t, result.Tests, command)
		require.NotEmpty(t, result.Reason, command)
	}
}

func TestPnpmNativeCommandsAreNotPackageScriptAliases(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"test": "vitest run"})
	for _, command := range []string{"pnpm publish --no-git-checks", "pnpm dlx pkg-pr-new publish './dist' --compact"} {
		result := resolveJestCommand(root, command, nil)
		require.Empty(t, result.Reason, command)
		require.Contains(t, result.ReviewReason, "review separately", command)
	}
	remaining := 256
	result := resolveJestSequence(root, root, "pnpm add -D react@18 && pnpm test", nil, &remaining, "vitest")
	require.Len(t, result.Tests, 1)
	require.Empty(t, result.Reason)
	require.Empty(t, result.Tests[0].UnvalidatedSetup)
	require.Equal(t, "dependencies", result.Tests[0].Prerequisites[0].Kind)
	writeScripts(t, root, map[string]string{"prepublishOnly": "vitest run"})
	require.Contains(t, resolveJestCommand(root, "pnpm publish --no-git-checks", nil).Reason, "may run tests")
	writeScripts(t, root, map[string]string{"prepublish": "vitest run"})
	require.Contains(t, resolveJestCommand(root, "pnpm publish", nil).Reason, "prepublish")
}
