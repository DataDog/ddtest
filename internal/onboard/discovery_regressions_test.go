package onboard

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCommonJestInvocations(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"test": "jest"})
	for _, tc := range []struct {
		input, command string
		environment    map[string]string
	}{
		{"cross-env NODE_ENV=test jest", "jest", map[string]string{"NODE_ENV": "test"}},
		{"cross-env-shell NODE_ENV=test jest --ci", "jest --ci", map[string]string{"NODE_ENV": "test"}},
		{"yarn jest --ci", "jest --ci", nil},
		{"node --experimental-vm-modules node_modules/jest/bin/jest.js", "node --experimental-vm-modules node_modules/jest/bin/jest.js", nil},
		{"node --no-warnings node_modules/.bin/jest --ci", "node --no-warnings node_modules/.bin/jest --ci", nil},
		{"npx jest", "jest", nil},
		{"npm test", "jest", nil},
		{"pnpm exec jest", "jest", nil},
		{"npm test -- --shard=1/2", "jest --shard=1/2", nil},
		{"NODE_ENV=test jest --ci", "jest --ci", map[string]string{"NODE_ENV": "test"}},
		{"LABEL='unit tests' npm test", "jest", map[string]string{"LABEL": "unit tests"}},
		{"bun test", "", nil},
	} {
		t.Run(tc.input, func(t *testing.T) {
			result := resolveJestCommand(root, tc.input, nil)
			require.Empty(t, result.Reason)
			if tc.command == "" {
				require.Empty(t, result.Tests)
				require.False(t, result.Matched)
				return
			}
			require.Equal(t, []TestCommand{{Directory: ".", Command: tc.command, Environment: tc.environment}}, result.Tests)
		})
	}
	for _, input := range []string{"cross-env NODE_ENV=$MODE jest", "node --eval 'jest()' node_modules/jest/bin/jest.js", "node $FLAGS node_modules/jest/bin/jest.js"} {
		result := resolveJestCommand(root, input, nil)
		require.NotEmpty(t, result.Reason, input)
		require.Empty(t, result.Tests, input)
	}
	writeScripts(t, root, map[string]string{"jest": "node wrapper.js"})
	require.Empty(t, resolveJestCommand(root, "yarn jest", nil).Tests, "package scripts shadow the binary")
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{}}`), 0600))
	result := resolveJestCommand(root, "yarn jest", nil)
	require.Empty(t, result.Tests)
	require.NotEmpty(t, result.Reason)
	require.NotContains(t, result.Reason, "No package.json script")
}

func TestScopeRetainsSetupBuildsAndInlineEnvironment(t *testing.T) {
	root := newJestRepository(t, `jobs:
  tests:
    env: {LABEL: outer}
    steps:
      - run: cp ci.js jest.config.js
      - run: node $FIRST && node $SECOND && tsc && jest --config src.js
      - run: jest --config dist.js && rollup -c
      - run: LABEL='unit tests' npm --prefix app test
`)
	writeScripts(t, root, map[string]string{"test": "jest"})
	writeScripts(t, filepath.Join(root, "app"), map[string]string{"test": "jest"})
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Equal(t, []string{"jest --config src.js", "jest --config dist.js", "jest"}, commandNames(scope.Commands))
	require.Equal(t, []BuildCommand{{Directory: ".", Command: "tsc", Environment: map[string]string{"LABEL": "outer"}}}, scope.Commands[0].Prerequisites)
	require.Equal(t, []string{"tsc", "rollup -c"}, buildNames(scope.Commands[2].Prerequisites))
	require.Equal(t, "unit tests", scope.Commands[2].Environment["LABEL"])
	for _, entry := range scope.Commands {
		setup := strings.Join(entry.UnvalidatedSetup, "\n")
		require.Contains(t, setup, "cp ci.js jest.config.js")
		require.Contains(t, setup, "$FIRST")
		require.Contains(t, setup, "$SECOND")
	}
	require.NotEmpty(t, scope.Unresolved)
}

func TestMetadataKeepsTestAndBuildRecords(t *testing.T) {
	root := newJestRepository(t, `jobs:
  tests:
    steps:
      - run: echo "dir=$(npm config get cache)" >> "$GITHUB_OUTPUT" && tsc && npm test
`)
	writeScripts(t, root, map[string]string{"test": "jest"})
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Equal(t, []string{"jest"}, commandNames(scope.Commands))
	require.Equal(t, []string{"tsc"}, buildNames(scope.Commands[0].Prerequisites))
}

func TestLiteralEnvironmentAndNodeOptionsSurviveDiscovery(t *testing.T) {
	root := newJestRepository(t, `jobs:
  test:
    env:
      ENABLED: ' '
      PREFIX: 'unit '
      NODE_OPTIONS: '--experimental-vm-modules -r ${{ env.DD_TRACE_PACKAGE }}'
    steps:
      - if: env.ENABLED
        run: jest
`)
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Equal(t, []string{"jest"}, commandNames(scope.Commands))
	require.Equal(t, map[string]string{"ENABLED": " ", "PREFIX": "unit ", "NODE_OPTIONS": "--experimental-vm-modules"}, scope.Commands[0].Environment)
}

func TestWorkingDirectoryCannotEscapeViaSymlink(t *testing.T) {
	root := newJestRepository(t, "jobs:\n  test:\n    steps:\n      - run: npm test\n        working-directory: linked\n")
	outside := t.TempDir()
	writeScripts(t, outside, map[string]string{"test": "jest"})
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "linked")))
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Empty(t, scope.Commands)
	require.NotEmpty(t, scope.Unresolved)
}

func TestGlobsFailClosedButQuotedPatternsStayLiteral(t *testing.T) {
	root := t.TempDir()
	for _, input := range []string{"jest tests/*.test.js", "jest --runTestsByPath tests/?.js", "jest tests/[ab].js"} {
		result := resolveJestCommand(root, input, nil)
		require.Empty(t, result.Tests)
		require.NotEmpty(t, result.Reason)
	}
	result := resolveJestCommand(root, `jest 'tests/*.test.js'`, nil)
	require.Empty(t, result.Reason)
	require.Equal(t, []string{`jest tests/\*.test.js`}, commandNames(result.Tests))
	_, _, err := resolveVariantStep(ciWorkflow{}, runtimeJob{}, runtimeStep{Run: "jest $DIR/*.js", Env: map[string]string{"DIR": "tests"}}, nil)
	require.Error(t, err)
}

func TestAssignmentsProduceRunnableShellSyntax(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("requires POSIX shell")
	}
	command := shellCommand([]string{"LABEL=unit tests", "sh", "-c", `test "$LABEL" = 'unit tests'`})
	require.NoError(t, exec.Command("sh", "-c", command).Run())
}

func commandNames(commands []TestCommand) []string {
	var result []string
	for _, command := range commands {
		result = append(result, command.Command)
	}
	return result
}
func buildNames(commands []BuildCommand) []string {
	var result []string
	for _, command := range commands {
		result = append(result, command.Command)
	}
	return result
}

func TestMatrixGroupingDoesNotParseDisplayDelimiters(t *testing.T) {
	job := "test map[odd] / step name"
	root := newJestRepository(t, `jobs:
  'test map[odd] / step name':
    strategy:
      matrix: {mode: ['x[y] / step z', plain]}
    steps:
      - run: jest
      - run: node unknown.js
`)
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Equal(t, []string{"jest"}, commandNames(scope.Commands))
	require.Equal(t, []string{
		`.github/workflows/test.yml / ` + job + ` (mode: "x[y] / step z") / step 1 (jest)`,
		`.github/workflows/test.yml / ` + job + ` (mode: "plain") / step 1 (jest)`,
	}, scope.Commands[0].Locations)
	require.Equal(t, []string{`.github/workflows/test.yml / ` + job + ` / step 2 (node unknown.js): Cannot statically resolve command: node unknown.js [affects 2 matrix entries; e.g. mode: "x[y] / step z"; mode: "plain"]`}, scope.Unresolved)
}

func TestCycleAndMissingScriptDiagnosticsAreRepositoryRelative(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, filepath.Join(root, "app"), map[string]string{"test": "npm run other", "other": "npm test"})
	for _, command := range []string{"npm --prefix app test", "npm --prefix app run absent", "npm --prefix missing test"} {
		result := resolveJestCommand(root, command, nil)
		require.NotEmpty(t, result.Reason)
		require.NotContains(t, result.Reason, root)
		canonical, err := filepath.EvalSymlinks(root)
		require.NoError(t, err)
		require.NotContains(t, result.Reason, canonical)
	}
	result := resolveJestCommand(root, "npm --prefix app test", nil)
	require.Contains(t, result.Reason, "app/package.json:test")
}

func TestUnknownToolsAndDownloadsStayUnresolved(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"build": "node unknown.js"})
	for _, command := range []string{"knip", "automd", "ls-engines", "bunchee", "clean-publish", "pkg-pr-new publish", "npm run build", `bash <(curl -s https://codecov.io/bash)`} {
		result := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: command}, "javascript", "jest")
		require.False(t, result.Review, command)
		require.NotEmpty(t, result.Reason, command)
		require.Empty(t, result.Tests, command)
	}
	for _, command := range []string{`jq '.' input.json > output.json`, `jq 'del(.scripts)' package.json > build/package.json`} {
		result := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: command}, "javascript", "jest")
		require.True(t, result.Review, command)
		require.NotEmpty(t, result.Setup, command)
	}
}

func TestConditionalNodeOptionsKeepOrdinaryRuntimeFlags(t *testing.T) {
	root := newJestRepository(t, `jobs:
  test:
    strategy:
      matrix: {node: [20,22]}
    steps:
      - run: jest
        env:
          NODE_OPTIONS: "--experimental-vm-modules ${{ matrix.node >= 22 && format('-r {0}', env.DD_TRACE_PACKAGE) || '' }}"
`)
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Equal(t, []string{"jest"}, commandNames(scope.Commands))
	require.Equal(t, map[string]string{"NODE_OPTIONS": "--experimental-vm-modules"}, scope.Commands[0].Environment)
	require.Equal(t, 2, len(scope.Commands[0].Locations), "both matrix consumers must survive expression resolution")
}
