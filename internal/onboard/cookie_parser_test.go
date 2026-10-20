// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestCookieParserSetupRejectsOpaqueCommands(t *testing.T) {
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

func TestMatrixDiagnosticsRetainCausesAndScope(t *testing.T) {
	result := groupMatrixDiagnostics([]string{
		"ci.yml / test map[node:18] / step 2 (opaque): custom setup needs review",
		"ci.yml / test map[node:20] / step 2 (opaque): custom setup needs review",
		"ci.yml / test map[node:22] / step 2 (opaque): custom setup needs review",
		"ci.yml / test map[node:22] / step 3 (other): different cause",
	})
	require.Len(t, result, 2)
	require.Contains(t, result[0], "custom setup needs review")
	require.Contains(t, result[0], "affects 3 matrix entries")
	require.Contains(t, result[1], "different cause")
}
