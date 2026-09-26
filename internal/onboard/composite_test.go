// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const compositeSetup = `inputs:
  node: {default: '22'}
runs:
  using: composite
  steps:
    - uses: actions/setup-node@v6
      with: {node-version: '${{ inputs.node }}'}
    - run: pnpm install
      shell: bash
`

func writeComposite(t *testing.T, root, name, data string) {
	t.Helper()
	path := filepath.Join(root, ".github/actions", name, "action.yml")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
	require.NoError(t, os.WriteFile(path, []byte(data), 0644))
}

func TestLocalCompositeRuntimeAndReactHookFormCommands(t *testing.T) {
	workflow := strings.Replace(algorithmsWorkflow, "actions/setup-node@v4\n        with: {node-version: '22.x'}", "./.github/actions/install", 1)
	workflow += "      - run: pnpm build:esm\n      - run: pnpm api-extractor:ci\n      - run: pnpm bundlewatch\n      - run: pnpm e2e\n      - run: npx playwright install --with-deps chromium\n"
	root := newJestRepository(t, workflow)
	writeComposite(t, root, "install", compositeSetup)
	writeScripts(t, root, map[string]string{"coverage": "jest", "lint": "eslint .", "build:esm": "rollup -c scripts/rollup.js", "api-extractor:ci": "node scripts/apiExtractor.js", "bundlewatch": "pnpm build:esm && bundlewatch", "e2e": "playwright test"})
	result := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "6.17.0", Node: ">=22"}, nil
	})
	require.Equal(t, "compatible", result.Status, result)
	require.Len(t, result.Jobs, 1)
	require.Equal(t, 5, result.Jobs[0].Step)
	require.Equal(t, "22", result.Jobs[0].Node)
	require.Equal(t, ".github/actions/install/action.yml / step 1", result.Jobs[0].NodeSource)
	require.Len(t, result.Review, 5)
	discovery, err := findWorkflows(root, "javascript", "jest")
	require.NoError(t, err)
	require.Empty(t, discovery.Unresolved)
	require.Equal(t, discovery.Workflows, discovery.Configured)
}

func TestCompositeInputsConditionsOrderAndUnknowns(t *testing.T) {
	base := strings.Replace(algorithmsWorkflow, "actions/setup-node@v4\n        with: {node-version: '22.x'}", "./.github/actions/install", 1)
	for _, tc := range []struct{ name, workflow, action, status string }{
		{"default input", base, compositeSetup, "compatible"},
		{"old input", strings.Replace(base, "uses: ./.github/actions/install", "uses: ./.github/actions/install\n        with: {node: '20'}", 1), compositeSetup, "incompatible"},
		{"dynamic input", strings.Replace(base, "uses: ./.github/actions/install", "uses: ./.github/actions/install\n        with: {node: '${{ inputs.runtime }}'}", 1), compositeSetup, "inconclusive"},
		{"excluded parent", strings.Replace(base, "uses: ./.github/actions/install", "uses: ./.github/actions/install\n        if: false", 1), compositeSetup, "inconclusive"},
		{"unknown parent condition", strings.Replace(base, "uses: ./.github/actions/install", "uses: ./.github/actions/install\n        if: github.event_name == 'push'", 1), compositeSetup, "inconclusive"},
		{"excluded child", base, strings.Replace(compositeSetup, "uses: actions/setup-node@v6", "uses: actions/setup-node@v6\n      if: false", 1), "inconclusive"},
		{"later setup wins", strings.Replace(base, "      - run: npm i", "      - uses: actions/setup-node@v6\n        with: {node-version: '20'}\n      - run: npm i", 1), compositeSetup, "incompatible"},
		{"cycle", base, "runs:\n  using: composite\n  steps:\n    - uses: ./.github/actions/install\n", "inconclusive"},
		{"local JS action", base, "runs: {using: node24, main: index.js}", "inconclusive"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := newJestRepository(t, tc.workflow)
			writeScripts(t, root, map[string]string{"coverage": "jest", "lint": "eslint ."})
			writeComposite(t, root, "install", tc.action)
			result := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
				return tracerRequirement{Version: "6.17.0", Node: ">=22"}, nil
			})
			require.Equal(t, tc.status, result.Status, result)
		})
	}
}

func TestCompositeNestingAndPathBoundary(t *testing.T) {
	root := t.TempDir()
	writeComposite(t, root, "outer", "runs:\n  using: composite\n  steps:\n    - uses: ./.github/actions/inner\n      with: {node: '24'}\n")
	writeComposite(t, root, "inner", compositeSetup)
	steps := expandCompositeSteps(root, "workflow", []runtimeStep{{Uses: "./.github/actions/outer"}})
	require.Len(t, steps, 2)
	require.Equal(t, "24", steps[0].With["node-version"])
	require.Equal(t, ".github/actions/inner/action.yml / step 1", steps[0].Source)
	out := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(out, "action.yml"), []byte(compositeSetup), 0644))
	require.NoError(t, os.Symlink(out, filepath.Join(root, "outside")))
	_, err := localActionPath(root, "./outside")
	require.ErrorContains(t, err, "escapes")
	_, err = localActionPath(root, "./../outside")
	require.Error(t, err)
}
