// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"github.com/stretchr/testify/require"
	"testing"
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
