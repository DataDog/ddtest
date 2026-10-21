// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"context"
	"os"
	"path/filepath"
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

func TestVitestRunnerCIRejectsUnreproducedInputsAndPreloads(t *testing.T) {
	commands := []TestCommand{{Command: "ddtest run --framework vitest"}}
	for key, value := range map[string]string{"DD_TEST_OPTIMIZATION_RUNNER_CI_NODE": "1", "DD_TEST_OPTIMIZATION_RUNNER_WORKER_ENV": "FOO=bar"} {
		step := runtimeStep{Env: map[string]string{key: value}}
		require.NotEmpty(t, checkTestBootstrap(ciWorkflow{}, runtimeJob{}, step, nil, commands, "vitest"), key)
		_, _, err := resolveVariantStep(ciWorkflow{}, runtimeJob{}, step, nil, "vitest")
		require.ErrorContains(t, err, key)
	}
	for _, options := range []string{"-r dd-trace/ci/init", "--require=dd-trace/ci/init", "-rdd-trace/ci/init", "--import dd-trace/register.js"} {
		reason := checkTestBootstrap(ciWorkflow{}, runtimeJob{}, runtimeStep{Env: map[string]string{"NODE_OPTIONS": options}}, nil, commands, "vitest")
		require.Contains(t, reason, "local replacement does not prove")
	}
	for _, options := range []string{"", "--conditions=development", "-r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}"} {
		require.Empty(t, checkTestBootstrap(ciWorkflow{}, runtimeJob{}, runtimeStep{Env: map[string]string{"NODE_OPTIONS": options}}, nil, commands, "vitest"))
	}
}

func TestVitestSupportedRunnerEnvironmentIsRecordedAndPreserved(t *testing.T) {
	env := map[string]string{"DD_TEST_OPTIMIZATION_RUNNER_VITEST_CONFIG": "ci.ts", "DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM": "2", "DD_TEST_OPTIMIZATION_RUNNER_TESTS_LOCATION": "subset/*.test.ts"}
	args, err := VitestRunnerEnvironmentArguments([]string{"run", "--framework=vitest", "--max-parallelism=3", "--", "src/one.test.ts"}, env)
	require.NoError(t, err)
	require.Contains(t, args, "--max-parallelism=3")
	require.NotContains(t, args, "--max-parallelism=2")
	parsed, err := ParseVitestRunnerCommand(args)
	require.NoError(t, err)
	require.Equal(t, "ci.ts", parsed.Config)
	require.Contains(t, parsed.Options, "--tests-location=subset/*.test.ts")
	commands := []TestCommand{{Command: "ddtest run --framework vitest"}}
	require.Empty(t, checkTestBootstrap(ciWorkflow{}, runtimeJob{}, runtimeStep{Env: env}, nil, commands, "vitest"))
	_, actual, err := resolveVariantStep(ciWorkflow{}, runtimeJob{}, runtimeStep{Run: "ddtest run --framework vitest", Env: env}, nil, "vitest")
	require.NoError(t, err)
	require.Equal(t, env, actual)
}

func TestVitestDefaultUsesCIThenPackageScripts(t *testing.T) {
	root := newJestRepository(t, "jobs:\n  test:\n    steps:\n      - run: npm test\n")
	writeScripts(t, root, map[string]string{"test": "vitest run --config ci.ts"})
	selected, err := VitestValidationCommand(root, "")
	require.NoError(t, err)
	require.Equal(t, "vitest run --config ci.ts", selected.Command)
	require.NoError(t, os.Remove(filepath.Join(root, ".github/workflows/test.yml")))
	selected, err = VitestValidationCommand(root, "")
	require.NoError(t, err)
	require.Equal(t, "vitest run --config ci.ts", selected.Command)
	for _, manager := range []string{"npm test", "pnpm test", "yarn test"} {
		selected, err = VitestValidationCommand(root, manager)
		require.NoError(t, err)
		require.Equal(t, "vitest run --config ci.ts", selected.Command)
	}
	writeScripts(t, root, map[string]string{"test": "tsc && vitest run --config ci.ts"})
	_, err = VitestValidationCommand(root, "")
	require.ErrorContains(t, err, "setup")
}
