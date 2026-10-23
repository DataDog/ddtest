// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

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
			workflow += "      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: js}\n"
		}
		workflow += "      - run: pnpm vitest --coverage\n"
		root := newJestRepository(t, workflow)
		require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"devDependencies":{"vitest":"4.1.6"}}`), 0600))
		t.Chdir(root)
		var output bytes.Buffer
		require.NoError(t, Run(&output))
		text := output.String()
		require.Contains(t, text, "Open report:")
		if configured {
			require.Contains(t, text, "already appears")
			require.NotContains(t, text, "## Validation scope")
			require.NotContains(t, text, "Add or update the action")
		} else {
			require.Contains(t, text, "# Enable Datadog Test Optimization for Vitest")
			require.Contains(t, text, "## Validation scope")
			require.Contains(t, text, "## Cleanup and final response")
			require.Contains(t, text, "js-tracer-version: <resolved-release>")
			require.NotContains(t, text, "--vitest-config ci.ts")
		}
		require.NotContains(t, text, "For Jest")
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
