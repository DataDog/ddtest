package onboard

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTinypoolReleaseAndBenchmarkScope(t *testing.T) {
	root := newJestRepository(t, `jobs:
  tests:
    strategy:
      matrix: {node: [20, 22]}
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '${{ matrix.node }}'}
      - run: pnpm build
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: js, js-tracer-version: '5.129.0'}
      - run: pnpm test
        env: {NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}'}
      - run: |
          git config --global user.name 'github-actions'
          git config --global user.email 'github-actions@example.com'
      - run: pnpm version ${{ github.event.inputs.release-type }}
      - run: git push origin main --follow-tags
      - run: pnpm publish
  benchmark:
    steps:
      - run: pnpm bench
  release:
    if: github.repository == 'tinylibs/tinypool'
    steps:
      - run: pnpx pkg-pr-new publish --compact
`)
	writeScripts(t, root, map[string]string{"test": "vitest", "bench": "vitest bench", "build": "tsdown", "publish": "clean-publish"})
	discovery, err := findWorkflows(root, "javascript", "vitest")
	require.NoError(t, err)
	require.Empty(t, discovery.Unresolved)
	require.Len(t, discovery.Review, 7)
	require.Contains(t, strings.Join(discovery.Review, "\n"), "benchmark mode")
	result := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "5.129.0", Node: ">=18"}, nil
	}, "vitest")
	require.Equal(t, "compatible", result.Status, result)
	require.Len(t, result.Jobs, 2)
	for _, job := range result.Jobs {
		require.Equal(t, "pnpm test", job.Command)
	}
	require.Len(t, result.Review, 7)
}

func TestReleaseScopeDoesNotHideTestHooksOrShellCode(t *testing.T) {
	for _, tc := range []struct{ command, hook string }{
		{"pnpm version ${{ inputs.version }}", "preversion"},
		{"pnpm version patch", "postversion"},
		{"pnpm publish", "prepublishOnly"},
		{"pnpx pkg-pr-new publish --compact", "prepare"},
		{"pnpm version ${{ inputs.version }} && vitest", ""},
		{"pnpm version $(node run-tests.js)", ""},
		{"git config core.hooksPath test-hooks", ""},
		{"pnpm publish && node unknown-tests.js", ""},
	} {
		t.Run(tc.command+tc.hook, func(t *testing.T) {
			root := t.TempDir()
			scripts := map[string]string{"publish": "clean-publish"}
			if tc.hook != "" {
				scripts[tc.hook] = "vitest"
			}
			writeScripts(t, root, scripts)
			result := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: tc.command}, "javascript", "vitest")
			require.False(t, result.Review, result)
			require.NotEmpty(t, result.Reason, result)
		})
	}
	root := t.TempDir()
	writeScripts(t, root, map[string]string{})
	result := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: "git push origin main && vitest"}, "javascript", "vitest")
	require.True(t, result.Matched)
	require.False(t, result.Review)
}

func TestVitestBenchmarksAreNotOrdinaryTestEntries(t *testing.T) {
	for _, command := range []string{"vitest bench", "pnpm vitest bench", "npx vitest bench --run", "pnpm bench"} {
		root := t.TempDir()
		writeScripts(t, root, map[string]string{"bench": "vitest bench"})
		result := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: command}, "javascript", "vitest")
		require.False(t, result.Matched, command)
		require.True(t, result.Review, command)
		require.Contains(t, result.Reason, "benchmark mode")
	}
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"bench": "vitest"})
	result := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: "pnpm bench"}, "javascript", "vitest")
	require.True(t, result.Matched, "script names do not determine runner mode")
	result = resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: "echo $(pnpm bench)"}, "javascript", "vitest")
	require.True(t, result.Matched, "metadata substitutions retain the selected framework")
}

func TestMixedVitestBenchmarkAndTestsRemainUnvalidated(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"test": "vitest bench && vitest run"})
	result := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: "pnpm test"}, "javascript", "vitest")
	require.True(t, result.Matched)
	require.False(t, result.Review)
	require.Contains(t, result.Reason, "combines ordinary tests and Vitest benchmarks")
}
