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

func TestMochaCoverageCommandsPreserveCIEntryPoints(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"test":"c8 --reporter=lcov --reporter=text mocha test/test.js","test-esm":"mocha test/test.mjs","opaque":"node test.js"}}`), 0600))
	for _, command := range []string{"npm test", "npm test -- --bail", "npm run test-esm", "npx --no-install nyc -- npm test", "nyc --reporter lcov c8 --check-coverage mocha test/test.js"} {
		t.Run(command, func(t *testing.T) {
			commands := ValidationCommands(root, command, "mocha")
			require.Len(t, commands, 1)
			require.Equal(t, strings.TrimPrefix(command, "npx --no-install "), commands[0].Command)
			actual, err := MochaValidationCommand(root, command)
			require.NoError(t, err)
			require.Equal(t, command, actual)
		})
	}
	for _, command := range []string{"npm test && npm run test-esm", "c8 --unknown value mocha", "npm run opaque", "c8 --reporter"} {
		_, err := MochaValidationCommand(root, command)
		require.Error(t, err, command)
	}
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".github/workflows/ci.yml"), []byte("jobs:\n  test:\n    steps:\n      - run: npm test\n      - run: npm run test-esm\n"), 0600))
	scope, err := DiscoverValidationScope(root, "mocha")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 2)
	_, err = MochaValidationCommand(root, "")
	require.ErrorContains(t, err, "--all")
}

func TestMochaCoverageFollowsPackageAliases(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"test":"npm run covered","covered":"npx --no-install nyc -- c8 --reporter=text mocha test.js","plain":"c8 mocha test.js","test-esm":"npx --no-install nyc -- mocha test.mjs"}}`), 0600))
	for _, command := range []string{"npm test", "npm run test-esm", "c8 -- npm test"} {
		require.True(t, MochaUsesNYC(root, command), command)
	}
	require.False(t, MochaUsesNYC(root, "npm run plain"))
	require.False(t, MochaUsesNYC(root, "npm test && npm run plain"))
}

func TestMochaProgrammaticCommandPreservesCompanionChecks(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"test": "npm run lint && npm run unit && npm run unit-esm", "lint": "standard", "unit": "nyc node test.js", "unit-esm": "node test.mjs"})
	require.NoError(t, os.WriteFile(filepath.Join(root, "test.js"), []byte("'use strict'\nconst Mocha = require('mocha')\nnew Mocha().run()"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "test.mjs"), []byte("import assert from 'node:assert'; assert.ok(true)"), 0600))
	for _, command := range []string{"npm test", "npm run unit", "nyc node test.js"} {
		commands := ValidationCommands(root, command, "mocha")
		require.Len(t, commands, 1, command)
		require.Equal(t, command, commands[0].Command)
		require.True(t, MochaUsesNYC(root, command))
		_, err := MochaValidationCommand(root, command)
		require.NoError(t, err)
	}
	for _, command := range []string{"node test.mjs", "node missing.js", "node -e code", "node ../outside.js"} {
		_, err := MochaValidationCommand(root, command)
		require.Error(t, err, command)
	}
	require.Empty(t, ValidationCommands(root, "npm run unit-esm", "mocha"))
	for _, command := range []string{"node test.mjs", "npm run unit-esm", "node test.mjs && mocha", "mocha && node test.mjs && codecov"} {
		resolution := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: command}, "javascript", "mocha")
		require.NotEmpty(t, resolution.Reason, command)
		require.False(t, resolution.Review, command)
	}
	resolution := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: "npm test"}, "javascript", "mocha")
	require.Empty(t, resolution.Reason)
	require.False(t, resolution.MochaAuxiliary)

	require.Equal(t, "npm run unit", MochaProbeCommand(root, "npm test"))
	require.Equal(t, "nyc -- npm run unit", MochaProbeCommand(root, "nyc -- npm test"))
	// Unknown shell fragments must not disappear when keeping a package alias.
	writeScripts(t, root, map[string]string{"test": "node test.js && opaque-command"})
	_, err := MochaValidationCommand(root, "npm test")
	require.ErrorContains(t, err, "opaque-command")
}

func TestMochaLegacyTestsBeforeActionAreExplicitlyExcluded(t *testing.T) {
	root := newJestRepository(t, `jobs:
  test:
    strategy:
      matrix: {node: [14, 18, 24]}
    steps:
      - uses: actions/setup-node@v3
        with: {node-version: '${{ matrix.node }}'}
      - run: npm test
        if: matrix.node < 18
        env: {DD_CIVISIBILITY_ENABLED: 'false'}
      - uses: datadog/test-visibility-github-action@v3
        if: matrix.node >= 18
        with: {languages: js, js-tracer-version: '5.131.1'}
      - run: npm test
        if: matrix.node >= 18
        env: {NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }}'}
`)
	writeScripts(t, root, map[string]string{"test": "mocha"})
	resolve := func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "5.131.1", Node: ">=18"}, nil
	}
	result := checkCIRuntimes(t.Context(), root, nil, resolve, "mocha")
	require.Equal(t, "compatible", result.Status, result.Jobs)
	require.Len(t, result.Jobs, 3)
	require.Equal(t, "excluded", result.Jobs[0].Status)
	require.Contains(t, result.Jobs[0].Reason, "not validated")
	for _, value := range []string{"true", "", "${{ inputs.disabled }}"} {
		require.False(t, mochaInstrumentationDisabled(ciWorkflow{Env: map[string]string{"DD_CIVISIBILITY_ENABLED": "false"}}, runtimeJob{}, runtimeStep{Env: map[string]string{"DD_CIVISIBILITY_ENABLED": value}}))
	}
}
