// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestShellSetupRejectsOpaqueCommands(t *testing.T) {
	root := t.TempDir()
	for _, shell := range []string{"bash -c {0}", "bash -o custom {0}", "bash pipefail {0}", "python {0}"} {
		require.False(t, supportedCIShell(shell))
	}
	for _, command := range []string{`echo "$(npm test)"`, `nvm install 22; nvm use 18`, `if npm -ps ls nyc | grep -q nyc; then npm test; else node hidden-tests.js; fi`} {
		result := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: command}, "javascript", "jest")
		require.True(t, result.Matched || result.Reason != "")
		require.False(t, result.Review)
	}
	_, err := interpolateMatrix(`npm install ${{ matrix.package }}`, map[string]any{"package": "x; npm test"})
	require.Error(t, err)
}

func TestMatrixInterpolationResolvesStaticExpressions(t *testing.T) {
	row := map[string]any{"node": 20, "version": "20", "shard": "1", "os": "windows-latest", "unsafe": "x; npm test"}
	for _, tc := range []struct{ command, expected, err string }{
		{`npm test -- --shard=${{ matrix.shard }}/${{ matrix.node }}`, "npm test -- --shard=1/20", ""},
		{`npm run ${{ matrix.script || format('test:node{0}', matrix.version) }}`, "npm run test:node20", ""},
		{`npm run ${{ matrix.os == 'windows-latest' && 'test:win' || 'test' }}`, "npm run test:win", ""},
		{`npm test${{ matrix.missing }}`, "npm test", ""},
		// Unresolvable expressions stay in place for the caller to report.
		{`npm test --token ${{ secrets.TOKEN }} --node ${{ matrix.node }}`, "npm test --token ${{ secrets.TOKEN }} --node 20", ""},
		{`cd ${{ github.workspace }}/app && npm test`, "cd ${{ github.workspace }}/app && npm test", ""},
		{`npm test ${{ matrix.node`, "npm test ${{ matrix.node", ""},
		{`npm install ${{ matrix.unsafe }}`, "", "shell quoting review: ${{ matrix.unsafe }}"},
		{`npm install ${{ matrix.unsafe || 'jest' }}`, "", "shell quoting review"},
		{`npm test ${{ format('{{{0}}}', matrix.version) }}`, "", "shell quoting review"},
		{`npm test ${{ fromJSON('["a"]') }}`, "", "must resolve to a scalar"},
		{"npm test " + strings.Repeat("x", maxInterpolatedCommand), "", "exceeds"},
	} {
		t.Run(tc.command[:min(len(tc.command), 80)], func(t *testing.T) {
			actual, err := interpolateMatrix(tc.command, row)
			if tc.err != "" {
				require.ErrorContains(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.expected, actual)
		})
	}
	// A runtime-computed axis holds a placeholder; it never selects a branch.
	computed := map[string]any{"suite": computedAxisValue("suite"), "node": 20}
	for _, command := range []string{
		`npm test ${{ matrix.suite }}`,
		`npm run ${{ matrix.suite == 'unit' && 'test:unit' || 'test:integration' }}`,
		`npm run ${{ format('test:{0}', matrix.suite) }}`,
	} {
		_, err := interpolateMatrix(command, computed)
		require.ErrorContains(t, err, "computed at runtime", command)
	}
	actual, err := interpolateMatrix(`npm test --node=${{ matrix.node }}`, computed)
	require.NoError(t, err)
	require.Equal(t, "npm test --node=20", actual)
	// Without a matrix row, commands are left for the caller to report.
	actual, err = interpolateMatrix(`npm test ${{ matrix.node }}`, nil)
	require.NoError(t, err)
	require.Equal(t, `npm test ${{ matrix.node }}`, actual)
}

func TestMatrixDiagnosticsRetainCausesAndScope(t *testing.T) {
	result := groupMatrixDiagnostics([]scopeDiagnostic{
		{commandLocation{Workflow: "ci.yml", Job: "test", Matrix: map[string]any{"node": 18}, Step: 2, Command: "opaque"}, "custom setup needs review"},
		{commandLocation{Workflow: "ci.yml", Job: "test", Matrix: map[string]any{"node": 20}, Step: 2, Command: "opaque"}, "custom setup needs review"},
		{commandLocation{Workflow: "ci.yml", Job: "test", Matrix: map[string]any{"node": 22}, Step: 2, Command: "opaque"}, "custom setup needs review"},
		{commandLocation{Workflow: "ci.yml", Job: "test", Matrix: map[string]any{"node": 22}, Step: 3, Command: "other"}, "different cause"},
	})
	require.Equal(t, []string{
		"ci.yml / test / step 2 (opaque): custom setup needs review [affects 3 matrix entries; e.g. node: 18; node: 20]",
		"ci.yml / test / step 3 (other): different cause [affects 1 matrix entries; e.g. node: 22]",
	}, result)
}
