// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const legacyESLintSetup = `if [[ "$(cut -d. -f1 <<< "${{ matrix.node-version }}")" -lt 10 ]]; then
  node -pe 'Object.keys(require("./package").devDependencies).join("\n")' |
    grep -E '^eslint(-|$)' | sort -r |
    xargs -n1 npm rm --silent --save-dev
fi`

func TestDiscoverySkipsOnlyStaticallyInactiveShellSetup(t *testing.T) {
	for _, node := range []string{"8", "9.11.0", "10", "18", "20.19.0", "22.x", "lts/*"} {
		t.Run(node, func(t *testing.T) {
			root := newJestRepository(t, fmt.Sprintf("jobs:\n  test:\n    strategy:\n      matrix: {node-version: ['%s']}\n    steps:\n      - run: |\n          %s\n      - run: npm test\n", node, strings.ReplaceAll(legacyESLintSetup, "\n", "\n          ")))
			writeScripts(t, root, map[string]string{"test": "mocha test.js"})
			scope, err := DiscoverValidationScope(root, "mocha")
			require.NoError(t, err)
			require.Len(t, scope.Commands, 1)
			if node == "8" || node == "9.11.0" || node == "lts/*" {
				require.NotEmpty(t, scope.Commands[0].UnvalidatedSetup, "active or unknown setup must not be dropped")
			} else {
				require.Empty(t, scope.Unresolved)
				require.Empty(t, scope.Commands[0].UnvalidatedSetup)
			}
		})
	}
}

func TestStaticShellConditionsNeverExecuteCommands(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "must-not-exist")
	for _, command := range []string{
		`if [[ "$(touch ` + marker + `)" -lt 10 ]]; then npm rm eslint; fi`,
		`if [[ "$UNDECLARED_NODE" -lt 10 ]]; then npm rm eslint; fi`,
		`if [[ "$(cut -d. -f1 <<< "22"; touch ` + marker + `)" -lt 10 ]]; then npm rm eslint; fi`,
		`if [[ "$(cut -d. -f1 <<< "22")" -lt 10 ]] > ` + marker + `; then npm rm eslint; fi`,
	} {
		step, _, err := resolveVariantStep(ciWorkflow{}, runtimeJob{}, runtimeStep{Run: command}, nil, "mocha")
		if err == nil {
			require.Equal(t, command, step.Run, "unknown or side-effecting conditions must be preserved")
		}
	}
	_, err := os.Stat(marker)
	require.True(t, os.IsNotExist(err))
}

func TestExcludedCIRowsRetainSetupWithoutBlockingInstrumentedRows(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(fmt.Sprint(unknown), func(t *testing.T) {
			extra := ""
			if unknown {
				extra = "      - if: matrix.datadog == false\n        run: unknown-test-wrapper\n"
			}
			root := newJestRepository(t, `jobs:
  test:
    strategy:
      matrix:
        include:
          - {node-version: '8', datadog: false}
          - {node-version: '20', datadog: true}
    steps:
      - uses: datadog/test-visibility-github-action@v3
        if: matrix.datadog
        with: {languages: js}
      - run: |
          `+strings.ReplaceAll(legacyESLintSetup, "\n", "\n          ")+"\n"+extra+`      - run: npm test
        env:
          NODE_OPTIONS: ${{ matrix.datadog && format('-r {0}', env.DD_TRACE_PACKAGE) || '' }}
`)
			writeScripts(t, root, map[string]string{"test": "mocha test.js"})
			scope, err := DiscoverValidationScope(root, "mocha")
			require.NoError(t, err)
			require.Len(t, scope.Commands, 2)
			if unknown {
				require.NotEmpty(t, scope.Unresolved, "unknown consumers remain unresolved even on an excluded row")
			} else {
				require.Empty(t, scope.Unresolved)
			}
			require.Contains(t, strings.Join(scope.Review, "\n"), "Unvalidated setup for instrumentation-excluded CI tests")
			for _, command := range scope.Commands {
				if command.CIInstrumentationExcluded {
					require.NotEmpty(t, command.UnvalidatedSetup)
				} else {
					require.Empty(t, command.UnvalidatedSetup)
				}
			}
		})
	}
}

func TestStaticShellConditionsPreserveUnknownArithmeticAndOtherShells(t *testing.T) {
	for _, tc := range []struct{ shell, command string }{
		{"sh", `if [[ "22" -lt 10 ]]; then npm rm eslint; fi`},
		{"pwsh", `if [[ "22" -lt 10 ]]; then npm rm eslint; fi`},
		{"bash", `if [[ "+08" -lt 10 ]]; then npm rm eslint; fi`},
		{"bash", `if [[ "08" -lt 10 ]]; then npm rm eslint; fi`},
		{"bash", `if [[ "arithmetic_expression" -lt 10 ]]; then npm rm eslint; fi`},
	} {
		step, _, _ := resolveVariantStep(ciWorkflow{}, runtimeJob{}, runtimeStep{Shell: tc.shell, Run: tc.command}, nil, "mocha")
		require.Equal(t, tc.command, step.Run)
	}
	step, _, err := resolveVariantStep(ciWorkflow{}, runtimeJob{}, runtimeStep{Run: `if [[ "22" -lt 10 ]]; then npm rm eslint; else npm test; fi`}, nil, "mocha")
	require.NoError(t, err)
	require.Equal(t, "npm test", strings.TrimSpace(step.Run))
}
