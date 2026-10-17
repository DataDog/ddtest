package onboard

import (
	"context"
	"testing"

	"github.com/kballard/go-shellquote"
	"github.com/stretchr/testify/require"
)

func TestVitestRunnerCommandPreservesOptionsAndRejectsUnreproducibleModes(t *testing.T) {
	args, err := shellquote.Split("--framework vitest run --vitest-config='config/ci tests.ts' --min-parallelism 2 --max-parallelism=2 -- src/**/*.test.ts")
	require.NoError(t, err)
	parsed, err := ParseVitestRunnerCommand(args)
	require.NoError(t, err)
	require.Equal(t, "config/ci tests.ts", parsed.Config)
	require.Contains(t, parsed.Options, "--max-parallelism=2")
	require.Equal(t, []string{"src/**/*.test.ts"}, parsed.Files)
	for _, command := range []string{"plan", "run --command vitest", "run --ci-node 1", "run --worker-env FOO=bar", "run --framework jest", "run --vitest-config", "run --vitest-config ../outside.ts", "run -- /outside.test.ts"} {
		words, err := shellquote.Split(command)
		require.NoError(t, err)
		_, err = ParseVitestRunnerCommand(words)
		require.Error(t, err, command)
	}
}

func TestVitestScopeKeepsCLIAndRunnerDistinct(t *testing.T) {
	root := newJestRepository(t, `jobs:
  test:
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '24'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: js, js-tracer-version: '6.20.1'}
      - run: npm run test:runner
      - run: pnpm vitest run --config ci.ts
        env: {NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}'}
`)
	writeScripts(t, root, map[string]string{"test:runner": "ddtest run --framework vitest --vitest-config ci.ts --max-parallelism 2"})
	scope, err := DiscoverValidationScope(root, "vitest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 2)
	require.Equal(t, "ddtest run --framework vitest --vitest-config ci.ts --max-parallelism 2", scope.Commands[0].Command)
	require.Equal(t, "vitest run --config ci.ts", scope.Commands[1].Command)
	result := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "6.20.1", Node: ">=22"}, nil
	}, "vitest")
	require.Equal(t, "compatible", result.Status, "%+v", result.Jobs)
}
