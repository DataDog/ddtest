package onboard

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVitestInstructionsRemainSpecificWhenAlreadyConfigured(t *testing.T) {
	for _, configured := range []bool{false, true} {
		workflow := "jobs:\n  tests:\n    steps:\n"
		if configured {
			workflow += "      - uses: datadog/test-visibility-github-action@v3\n"
		}
		workflow += "      - run: pnpm vitest --coverage\n"
		root := newJestRepository(t, workflow)
		require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"devDependencies":{"vitest":"4.1.6"}}`), 0600))
		t.Chdir(root)
		var output bytes.Buffer
		require.NoError(t, Run(&output))
		text := output.String()
		for _, required := range []string{"--framework vitest --command", "--check-only", "--import ${{ env.DD_TRACE_ESM_IMPORT }}", "js-tracer-version: <resolved-release>", "summary.final_response", "Existing project dependencies take precedence", "Git and published packages", "Known tracer incompatibilities"} {
			require.Contains(t, text, required)
		}
		require.NotContains(t, text, "jest-circus")
		require.NotContains(t, text, "--showConfig")
		require.NotContains(t, text, "For Jest")
		require.Less(t, len(text), 8000)
	}
}

func TestVitestCISeparatesTypecheckAndRequiresESMLoader(t *testing.T) {
	workflow := `jobs:
  tests:
    steps:
      - run: corepack enable
      - uses: actions/setup-node@v6
        with: {node-version: '22'}
      - run: pnpm install
      - run: pnpm lint
      - run: pnpm build
      - run: pnpm test:types
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: js, js-tracer-version: '6.3.0'}
      - run: pnpm vitest --coverage
        env: {NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}'}
`
	for _, loader := range []bool{true, false} {
		t.Run(map[bool]string{true: "with ESM", false: "missing ESM"}[loader], func(t *testing.T) {
			value := workflow
			if !loader {
				value = strings.ReplaceAll(value, " --import ${{ env.DD_TRACE_ESM_IMPORT }}", "")
			}
			root := newJestRepository(t, value)
			require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"test":"pnpm lint && pnpm test:types && pnpm vitest run","lint":"oxlint src && oxfmt --check src test","build":"unbuild","test:types":"tsgo --noEmit"}}`), 0600))
			result := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
				return tracerRequirement{Version: "6.3.0", Node: ">=22"}, nil
			}, "vitest")
			require.Len(t, result.Jobs, 1)
			require.Equal(t, "pnpm vitest --coverage", result.Jobs[0].Command)
			if loader {
				require.Equal(t, "compatible", result.Status)
			} else {
				require.Equal(t, "inconclusive", result.Status)
				require.Equal(t, "unverified_initialization", result.Jobs[0].Code)
			}
		})
	}
}

func TestVitestCIResolvesScriptsWithoutMatchingEchoedText(t *testing.T) {
	root := newJestRepository(t, "")
	writeScripts(t, root, map[string]string{"test": "pnpm test:types && vitest run", "test:types": "tsgo --noEmit", "other": "jest"})
	for command, matched := range map[string]bool{"pnpm test": true, "pnpm test:types": false, "echo vitest": false, "pnpm other": false} {
		remaining := 256
		r := resolveJestSequence(root, root, command, nil, &remaining, "vitest")
		require.Empty(t, r.Reason, command)
		require.Equal(t, matched, r.Matched, command)
	}
}

func TestVitestCIReviewsAutomdWithoutBlockingTestValidation(t *testing.T) {
	workflow := `jobs:
  autofix:
    steps:
      - run: pnpm automd
  tests:
    steps:
      - uses: actions/setup-node@v6
        with: {node-version: '20'}
      - run: pnpm build
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: js, js-tracer-version: '5.128.0'}
      - run: pnpm vitest --coverage
        env: {NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}'}
`
	root := newJestRepository(t, workflow)
	writeScripts(t, root, map[string]string{"automd": "automd", "build": "automd && unbuild"})
	result := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "5.128.0", Node: ">=18"}, nil
	}, "vitest")
	require.Equal(t, "compatible", result.Status)
	require.Len(t, result.Jobs, 1)
	require.Equal(t, "pnpm vitest --coverage", result.Jobs[0].Command)
	require.Len(t, result.Review, 2)
}

func TestAutomdScriptStillResolvesTestsAndUnknownCommands(t *testing.T) {
	for _, tc := range []struct {
		name, script, hook string
		matched            bool
	}{
		{name: "test alias", script: "vitest run", matched: true},
		{name: "documentation then tests", script: "automd && vitest run", matched: true},
		{name: "unknown wrapper", script: "node test-wrapper.js"},
		{name: "documentation then unknown wrapper", script: "automd && node test-wrapper.js"},
		{name: "lifecycle hook", script: "automd", hook: "vitest run"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			scripts := map[string]string{"automd": tc.script}
			if tc.hook != "" {
				scripts["preautomd"] = tc.hook
			}
			writeScripts(t, root, scripts)
			result := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: "pnpm automd"}, "javascript", "vitest")
			require.False(t, result.Review, result)
			require.Equal(t, tc.matched, result.Matched, result)
			if !tc.matched {
				require.NotEmpty(t, result.Reason, result)
			}
		})
	}
}
