// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeScripts(t *testing.T, root string, scripts map[string]string) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"scripts": scripts, "devDependencies": map[string]string{"jest": "30.2.0"}})
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(root, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), data, 0644))
}

func TestStaticJestScriptResolution(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{
		"test": "jest --config 'config/jest unit.js'", "coverage": "npm run test -- --coverage",
		"lint": "eslint ./src/**", "t": "eslint .", "hooked": "jest", "prehooked": "node setup.js", "ci": "npm run lint && npm run coverage",
		"loop": "npm run loop", "first": "npm run second", "second": "npm run first",
		"custom": "node scripts/test.js", "dynamic": "npm run \"$SUITE\"",
		"reset": "NODE_OPTIONS='' jest", "compound": "echo 'jest && npm test'; npm run coverage",
	})
	for _, tc := range []struct {
		command             string
		matched, unresolved bool
		evidence            string
	}{
		{"npm run coverage", true, false, "npm run coverage -> npm run test -- --coverage -> jest --config 'config/jest unit.js' --coverage"},
		{"npm run ci", true, false, "--coverage"},
		{"npm test -- --runInBand", true, false, "--runInBand"},
		{"yarn run coverage", true, false, "--coverage"},
		{"pnpm coverage", true, false, "--coverage"},
		{"bun run coverage", true, false, "--coverage"},
		{"bun test", false, false, ""},
		{"npm run test --workspaces", false, true, ""},
		{"npm run compound", true, false, "--coverage"},
		{"npx jest --config 'jest unit.js'", true, false, "'jest unit.js'"},
		{"npm exec -- jest", true, false, "jest"},
		{"CI=true ./node_modules/.bin/jest", true, false, "jest"},
		{"# jest\necho 'npm test' # npx jest", false, false, ""},
		{"npm run lint", false, false, ""},
		{"npm run t", false, false, ""},
		{"npm t", true, false, "jest"},
		{"npm run hooked", false, true, ""},
		{"npm ci --no-audit", false, false, ""},
		{"npm run loop", false, true, ""},
		{"npm run first", false, true, ""},
		{"npm run missing", false, true, ""},
		{"npm run custom", false, true, ""},
		{"npm run dynamic", false, true, ""},
		{"npm run reset", false, true, ""},
		{"npm --workspace app test", false, true, ""},
		{"node wrapper.js && npx jest", false, true, ""},
		{"cd app && npm test", false, true, ""},
		{"echo $(touch sentinel)", false, true, ""},
		{"jest || true", false, true, ""},
		{"jest | tee output", false, true, ""},
		{"jest &", false, true, ""},
		{"jest --config 'unterminated", false, true, ""},
	} {
		t.Run(tc.command, func(t *testing.T) {
			result := resolveJestCommand(root, tc.command, nil)
			require.Equal(t, tc.matched, result.Matched, result)
			require.Equal(t, tc.unresolved, result.Reason != "", result)
			if tc.evidence != "" {
				require.Contains(t, result.Evidence, tc.evidence)
			}
		})
	}
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	require.Len(t, entries, 1, "discovery must not execute any command or generate files")
}

func TestJestDiscoveryWorkingDirectoryPrecedence(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"test": "eslint ."})
	writeScripts(t, filepath.Join(root, "packages", "app"), map[string]string{"test": "jest"})
	for _, scope := range []string{"workflow", "job", "step"} {
		t.Run(scope, func(t *testing.T) {
			workflow := ciWorkflow{}
			job := runtimeJob{}
			step := runtimeStep{Run: "npm test"}
			switch scope {
			case "workflow":
				workflow.Defaults.Run.WorkingDirectory = "packages/app"
			case "job":
				workflow.Defaults.Run.WorkingDirectory = "missing"
				job.Defaults.Run.WorkingDirectory = "packages/app"
			case "step":
				job.Defaults.Run.WorkingDirectory = "missing"
				step.WorkingDirectory = "packages/app"
			}
			result := resolveTestStep(root, workflow, job, step, "javascript", "jest")
			require.True(t, result.Matched, result)
			require.Empty(t, result.Reason)
		})
	}
	for _, directory := range []string{"${{ matrix.package }}", "../outside", "missing"} {
		result := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: "npm test", WorkingDirectory: directory}, "javascript", "jest")
		require.NotEmpty(t, result.Reason)
	}
	result := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: "npm test", Shell: "pwsh"}, "javascript", "jest")
	require.Contains(t, result.Reason, "shell")
}

const algorithmsWorkflow = `name: Node CI
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '22.x'}
      - run: npm i
      - run: npm run lint
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: js, js-tracer-version: '6.17.0'}
      - run: npm run coverage
        env:
          NODE_OPTIONS: -r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}
`

func TestJavaScriptAlgorithmsAliasesReachOnboardingAndRuntimeChecks(t *testing.T) {
	for _, node := range []string{"22.x", "20.x"} {
		t.Run(node, func(t *testing.T) {
			workflow := strings.Replace(algorithmsWorkflow, "22.x", node, 1)
			root := newJestRepository(t, workflow)
			writeScripts(t, root, map[string]string{"test": "jest", "coverage": "npm run test -- --coverage", "lint": "eslint ./src/**"})
			before, err := os.ReadFile(filepath.Join(root, "package.json"))
			require.NoError(t, err)
			discovery, err := findWorkflows(root, "javascript", "jest")
			require.NoError(t, err)
			require.Equal(t, []string{".github/workflows/test.yml"}, discovery.Workflows)
			require.Equal(t, discovery.Workflows, discovery.Configured)
			require.Empty(t, discovery.Unresolved)
			t.Chdir(root)
			var output bytes.Buffer
			require.NoError(t, Run(&output))
			require.Contains(t, output.String(), "ddtest testdrive")
			result := checkCIRuntimes(t.Context(), root, func(_ context.Context, action, version string) (tracerRequirement, error) {
				require.Equal(t, githubAction+"@v3", action)
				require.Equal(t, "6.17.0", version)
				return tracerRequirement{Version: version, Node: ">=22"}, nil
			})
			expected := "compatible"
			if node == "20.x" {
				expected = "incompatible"
			}
			require.Equal(t, expected, result.Status, result)
			require.Len(t, result.Jobs, 1)
			require.Equal(t, 5, result.Jobs[0].Step)
			require.Equal(t, "npm run coverage", result.Jobs[0].Command)
			require.Equal(t, "npm run coverage -> npm run test -- --coverage -> jest --coverage", result.Jobs[0].Resolution)
			after, err := os.ReadFile(filepath.Join(root, "package.json"))
			require.NoError(t, err)
			require.Equal(t, before, after)
			after, err = os.ReadFile(filepath.Join(root, ".github/workflows/test.yml"))
			require.NoError(t, err)
			require.Equal(t, workflow, string(after))
		})
	}
}

func TestUnresolvedCIStillPrintsInstructionsButCannotPass(t *testing.T) {
	root := newJestRepository(t, strings.Replace(algorithmsWorkflow, "npm run coverage", "node scripts/run-ci.js", 1))
	writeScripts(t, root, map[string]string{"test": "jest", "lint": "eslint ."})
	t.Chdir(root)
	var output bytes.Buffer
	require.NoError(t, Run(&output))
	require.Contains(t, output.String(), "CI command discovery is inconclusive")
	require.Contains(t, output.String(), "step 5 (node scripts/run-ci.js)")
	require.Contains(t, output.String(), "datadog/test-visibility-github-action@v3")
	require.Contains(t, output.String(), "ddtest testdrive")
	require.NotContains(t, output.String(), "already appears")
	result := checkCIRuntimes(t.Context(), root, nil)
	require.Equal(t, "inconclusive", result.Status)
	require.Contains(t, result.Jobs[0].Reason, "Could not resolve CI test command")
}

func TestUnrelatedWorkflowCannotBeMatchedByCommentsOrStepNames(t *testing.T) {
	root := newJestRepository(t, `name: jest
# npm test
jobs:
  test:
    steps:
      - name: jest
        run: echo 'npm test'
      - run: npm run lint
`)
	writeScripts(t, root, map[string]string{"test": "jest", "lint": "eslint ."})
	result := checkCIRuntimes(t.Context(), root, nil)
	require.Equal(t, "not applicable", result.Status)
	discovery, err := findWorkflows(root, "javascript", "jest")
	require.NoError(t, err)
	require.Empty(t, discovery.Workflows)
}

func TestAliasedTestsRequireActionBeforeTestAndEffectivePreload(t *testing.T) {
	for _, tc := range []struct{ name, old, new, reason string }{
		{"missing preload", "NODE_OPTIONS: -r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}", "NODE_OPTIONS: --max-old-space-size=4096", "NODE_OPTIONS"},
		{"action too late", "      - run: npm run coverage\n        env:\n          NODE_OPTIONS: -r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}\n", "", "No Datadog JavaScript action"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workflow := strings.Replace(algorithmsWorkflow, tc.old, tc.new, 1)
			if tc.name == "action too late" {
				workflow = strings.Replace(workflow, "      - uses: datadog/", tc.old+"      - uses: datadog/", 1)
			}
			root := newJestRepository(t, workflow)
			writeScripts(t, root, map[string]string{"test": "jest", "coverage": "npm test -- --coverage", "lint": "eslint ."})
			result := checkCIRuntimes(t.Context(), root, func(context.Context, string, string) (tracerRequirement, error) {
				return tracerRequirement{Version: "6.17.0", Node: ">=22"}, nil
			})
			require.Equal(t, "inconclusive", result.Status)
			require.Contains(t, result.Jobs[0].Reason, tc.reason)
		})
	}
	workflow := ciWorkflow{Env: map[string]string{"NODE_OPTIONS": "-r ${{ env.DD_TRACE_PACKAGE }}"}}
	require.Empty(t, checkJestBootstrap(workflow, runtimeJob{}, runtimeStep{}))
	require.NotEmpty(t, checkJestBootstrap(workflow, runtimeJob{Env: map[string]string{"NODE_OPTIONS": ""}}, runtimeStep{}))
	require.Empty(t, checkJestBootstrap(workflow, runtimeJob{Env: map[string]string{"NODE_OPTIONS": ""}}, runtimeStep{Env: map[string]string{"NODE_OPTIONS": "--require=${{ env.DD_TRACE_PACKAGE }}"}}))
}

func TestScriptExpansionIsBounded(t *testing.T) {
	root := t.TempDir()
	scripts := map[string]string{"a": "npm run b && npm run b && npm run b && npm run b", "b": "npm run c && npm run c && npm run c && npm run c", "c": "npm run d && npm run d && npm run d && npm run d", "d": "npm run e && npm run e && npm run e && npm run e", "e": "jest"}
	writeScripts(t, root, scripts)
	require.Contains(t, resolveJestCommand(root, "npm run a", nil).Reason, "exceeds 256")
}
