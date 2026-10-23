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
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMochaNpmLifecyclePreservesCommandsAndHiddenConsumers(t *testing.T) {
	root := newJestRepository(t, `jobs:
  unit:
    steps:
      - run: npm run test-node
  esm:
    if: ${{ github.ref == 'refs/heads/main' }}
    steps:
      - run: npm run build
`)
	writeScripts(t, root, map[string]string{
		"pretest-node": "npm run build-artifacts", "test-node": "mocha test.js",
		"build-artifacts": "node build.cjs", "build": "npm run build-artifacts",
		"prebuild":  `node -e "require('node:fs').rmSync('pkg', {recursive:true,force:true})"`,
		"postbuild": "run-s test-esm check", "test-esm": "mocha test.mjs", "check": "node check.mjs && eslint .",
	})
	for _, name := range []string{"build.cjs", "check.mjs"} {
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte("console.log('helper')"), 0600))
	}
	scope, err := DiscoverValidationScope(root, "mocha")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 2)
	for _, entry := range scope.Commands {
		require.Contains(t, []string{"npm run build", "npm run test-node"}, entry.Command)
		require.Contains(t, entry.Resolution, "mocha test")
		require.Equal(t, "npx --no-install nyc -- "+entry.Command, entry.CICoverageCommand)
		require.Empty(t, entry.Prerequisites)
		require.Empty(t, entry.UnvalidatedSetup)
		_, err := MochaValidationCommand(root, entry.Command)
		require.NoError(t, err)
	}
	var output bytes.Buffer
	WriteValidationScope(&output, scope)
	require.Contains(t, output.String(), "CI coverage command: npx --no-install nyc -- npm run build")
	require.Contains(t, output.String(), "Real source coverage is a separate check")
	require.Equal(t, "mocha test.js", MochaProbeCommand(root, "npm run test-node"))
	require.Equal(t, "mocha test.mjs", MochaProbeCommand(root, "npm run build"))
	for _, command := range []string{"npm run test-node -- --bail", "nyc -- npm run test-node"} {
		_, err := MochaValidationCommand(root, command)
		require.NoError(t, err)
	}

}

func TestMochaLifecycleUnknownHooksAndOrchestrationStayBlocked(t *testing.T) {
	for _, hook := range []string{"unknown --build", "npm test", "run-s test-*", "run-p unit check"} {
		t.Run(hook, func(t *testing.T) {
			root := t.TempDir()
			writeScripts(t, root, map[string]string{"pretest": hook, "test": "mocha", "unit": "mocha", "check": "true"})
			_, err := MochaValidationCommand(root, "npm test")
			require.Error(t, err)
		})
	}
}

func TestMochaBranchGuardDoesNotRelaxConditionalInstallation(t *testing.T) {
	for _, guard := range []string{"", "        if: github.ref == 'refs/heads/main'\n"} {
		root := newJestRepository(t, `jobs:
  test:
    if: ${{ github.ref == 'refs/heads/main' }}
    steps:
      - uses: actions/setup-node@v6
        with: {node-version: '24'}
      - uses: datadog/test-visibility-github-action@v3
`+guard+`        with: {languages: js, js-tracer-version: '6.21.0'}
      - run: npm test
        env: {NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }}'}
`)
		writeScripts(t, root, map[string]string{"test": "nyc -- mocha"})
		result := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
			return tracerRequirement{Version: "6.21.0", Node: ">=22"}, nil
		}, "mocha")
		if guard == "" {
			require.Equal(t, "compatible", result.Status, result.Jobs)
		} else {
			require.Equal(t, "inconclusive", result.Status, result.Jobs)
		}
	}
}

func TestMochaOtherFrameworkAndDocumentationHooksAreReviewOnly(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"docs": "vitepress build", "predocs": "node sidebar.mjs", "test": "tap 'tests/*.js'"})
	require.NoError(t, os.WriteFile(filepath.Join(root, "sidebar.mjs"), []byte("console.log('docs')"), 0600))
	for _, command := range []string{"npm run docs", "npm test", "nyc -- mochify --driver puppeteer"} {
		result := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: command}, "javascript", "mocha")
		require.True(t, result.Review, result)
		require.False(t, result.Matched, result)
	}
}

func TestMochaBrowserClassificationDoesNotHideNodeConsumers(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"browser": "node browser.mjs", "unit": "mocha", "build": "mocha esm.mjs"})
	require.NoError(t, os.WriteFile(filepath.Join(root, "browser.mjs"), []byte("import puppeteer from 'puppeteer'; puppeteer.launch()"), 0600))
	for _, command := range []string{
		"export CHROME=$(which chrome)\nnpm run browser -- --chrome $CHROME",
		"nyc -- mochify --driver puppeteer",
	} {
		result := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: command}, "javascript", "mocha")
		require.True(t, result.Review, result)
	}
	for _, command := range []string{
		"export HIDDEN=$(npm run unit)\nnpm run browser",
		"npm run build\nnpm run browser",
		"npm run unknown\nnpm run browser",
		"npm run browser && mocha",
		"bash <(curl -s https://codecov.io/bash) -F unit; npm run unit",
	} {
		result := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: command}, "javascript", "mocha")
		require.False(t, result.Review, result)
	}
}

func TestMochaCIRequiresCoverageForEveryConsumer(t *testing.T) {
	for _, tc := range []struct {
		name, command, postbuild, status string
	}{
		{"plain alias", "npm test", "mocha test.mjs", "inconclusive"},
		{"hidden consumer", "npm run build", "mocha test.mjs", "inconclusive"},
		{"wrapped lifecycle", "npx --no-install nyc -- npm run build", "mocha test.mjs", "compatible"},
		{"wrapped hidden consumer", "npm run build", "nyc -- mocha test.mjs", "compatible"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := newJestRepository(t, `jobs:
  test:
    steps:
      - uses: actions/setup-node@v6
        with: {node-version: '24'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: js, js-tracer-version: '6.21.0'}
      - run: `+tc.command+`
        env: {NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}'}
`)
			writeScripts(t, root, map[string]string{"test": "mocha", "build": "echo build", "postbuild": tc.postbuild})
			result := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
				return tracerRequirement{Version: "6.21.0", Node: ">=22"}, nil
			}, "mocha")
			require.Equal(t, tc.status, result.Status, result.Jobs)
			scope, err := DiscoverValidationScope(root, "mocha")
			require.NoError(t, err)
			require.Len(t, scope.Commands, 1)
			if tc.status == "compatible" {
				require.Empty(t, scope.Commands[0].CICoverageCommand, "do not suggest a duplicate NYC wrapper")
			} else {
				require.Equal(t, "npx --no-install nyc -- "+tc.command, scope.Commands[0].CICoverageCommand)
			}
			if tc.status == "inconclusive" {
				require.Contains(t, result.Jobs[0].Reason, "NYC")
				require.Contains(t, result.Jobs[0].Reason, tc.command)
			}
		})
	}
}
