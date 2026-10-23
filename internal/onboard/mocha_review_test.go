package onboard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMochaRetainedAliasKeepsCallerScope(t *testing.T) {
	for _, script := range []string{"MODE=unit nyc -- mocha test.cjs", "cd packages/unit && nyc -- mocha test.cjs"} {
		t.Run(script, func(t *testing.T) {
			root := t.TempDir()
			writeScripts(t, root, map[string]string{"pretest": "node hook.cjs", "test": script, "posttest": "node hook.cjs"})
			requireWrite := func(name, text string) {
				require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(text), 0600))
			}
			requireWrite("hook.cjs", `require('node:assert').equal(process.env.MODE,undefined)`)
			require.NoError(t, os.MkdirAll(filepath.Join(root, "packages/unit"), 0700))
			commands := ValidationCommands(root, "npm test", "mocha")
			require.Len(t, commands, 1)
			require.Equal(t, "npm test", commands[0].Command)
			require.Equal(t, ".", commands[0].Directory)
			require.Empty(t, commands[0].Environment)
			plan := MochaProbePlan(root, "npm test")
			require.Equal(t, "nyc --check-coverage=false -- mocha test.cjs", plan.Command)
			if strings.HasPrefix(script, "cd ") {
				require.Equal(t, "packages/unit", plan.Directory)
				require.Equal(t, filepath.Join(root, "packages/unit"), MochaRunnerDirectory(root, "npm test"))
			} else {
				require.Equal(t, map[string]string{"MODE": "unit"}, plan.Environment)
			}
			commands = ValidationCommands(root, "OUTER=yes npm test", "mocha")
			require.Len(t, commands, 1)
			require.Equal(t, map[string]string{"OUTER": "yes"}, commands[0].Environment)
		})
	}
}

func TestMochaMultipleInvocationsRemainOrderedAndUnvalidated(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"pretest": "true", "test": "mocha unit.cjs && mocha integration.cjs", "posttest": "true"})
	_, err := MochaValidationCommand(root, "npm test")
	require.ErrorContains(t, err, "Multiple Mocha invocations")
	require.Empty(t, ValidationCommands(root, "npm test", "mocha"), "never turn an ordered package invocation into independent configurations")
}

func TestMochaESMCIRequiresActionImport(t *testing.T) {
	for _, input := range []string{"mocha test/one.mjs", "mocha --extension=mjs", "mocha --config custom.json", "mocha", "node runner.mjs"} {
		t.Run(input, func(t *testing.T) {
			root := newJestRepository(t, `jobs:
  test:
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '24'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: js, js-tracer-version: '6.20.1'}
      - run: npm test
        env: {NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }}'}
`)
			writeScripts(t, root, map[string]string{"test": "nyc -- " + input})
			if input == "mocha --config custom.json" {
				require.NoError(t, os.WriteFile(filepath.Join(root, "custom.json"), []byte(`{"extension":["mjs"]}`), 0600))
			}
			if input == "mocha" {
				require.NoError(t, os.WriteFile(filepath.Join(root, ".mocharc.json"), []byte(`{"spec":"test/*.mjs"}`), 0600))
			}
			if input == "node runner.mjs" {
				require.NoError(t, os.WriteFile(filepath.Join(root, "runner.mjs"), []byte(`import Mocha from 'mocha';new Mocha().run()`), 0600))
			}
			check := checkCIRuntimes(t.Context(), root, nil, func(_ context.Context, _, _ string) (tracerRequirement, error) {
				return tracerRequirement{Version: "6.20.1", Node: ">=22"}, nil
			}, "mocha")
			require.NotEqual(t, "compatible", check.Status)
			require.Contains(t, fmt.Sprint(check), "Mocha ESM")
			workflow := filepath.Join(root, ".github/workflows/test.yml")
			data, err := os.ReadFile(workflow)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(workflow, []byte(strings.ReplaceAll(string(data), "-r ${{ env.DD_TRACE_PACKAGE }}'", "-r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}'")), 0600))
			check = checkCIRuntimes(t.Context(), root, nil, func(_ context.Context, _, _ string) (tracerRequirement, error) {
				return tracerRequirement{Version: "6.20.1", Node: ">=22"}, nil
			}, "mocha")
			require.Equal(t, "compatible", check.Status, check)
		})
	}
}
