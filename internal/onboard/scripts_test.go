// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"encoding/json"
	"os"
	"path/filepath"

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
		"lint": "eslint ./src", "t": "eslint .", "hooked": "jest", "prehooked": "node setup.js", "ci": "npm run lint && npm run coverage",
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
		{"mkdir -p pages\ncp -R storybook-static/. pages/", false, false, ""},
		{"npm run t", false, false, ""},
		{"npm t", true, false, "jest"},
		{"npm run hooked", true, true, ""},
		{"npm ci --no-audit", false, false, ""},
		{"npm run loop", false, true, ""},
		{"npm run first", false, true, ""},
		{"npm run missing", false, true, ""},
		{"npm run custom", false, true, ""},
		{"npm run dynamic", false, true, ""},
		{"npm run reset", false, true, ""},
		{"npm --workspace app test", false, true, ""},
		{"node wrapper.js && npx jest", true, true, ""},
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
	discovery, err := findWorkflows(root, "javascript", "jest")
	require.NoError(t, err)
	require.Empty(t, discovery.Workflows)
}

func TestScriptExpansionIsBounded(t *testing.T) {
	root := t.TempDir()
	scripts := map[string]string{"a": "npm run b && npm run b && npm run b && npm run b", "b": "npm run c && npm run c && npm run c && npm run c", "c": "npm run d && npm run d && npm run d && npm run d", "d": "npm run e && npm run e && npm run e && npm run e", "e": "jest"}
	writeScripts(t, root, scripts)
	require.Contains(t, resolveJestCommand(root, "npm run a", nil).Reason, "exceeds 256")
}

func TestCIScopeNeverHidesIdentifiedOrInstrumentedTestCommands(t *testing.T) {
	for _, tc := range []struct{ command, build, options string }{
		{"yarn build", "jest", ""},
		{"yarn build", "node wrapper.js && jest", ""},
		{"yarn build", "jest && node wrapper.js", ""},
		{"yarn build", "node custom-build.js", "-r dd-trace/ci/init"},
		{"yarn test", "node custom-build.js", ""},
		{"node scripts/run-ci.js", "node custom-build.js", ""},
		{"yarn build && node tests.js", "node custom-build.js", ""},
	} {
		t.Run(tc.command+tc.build+tc.options, func(t *testing.T) {
			root := t.TempDir()
			writeScripts(t, root, map[string]string{"test": "node tests.js", "build": tc.build})
			result := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: tc.command, Env: map[string]string{"NODE_OPTIONS": tc.options}}, "javascript", "jest")
			require.False(t, result.Review, result)
			require.True(t, result.Matched || result.Reason != "", result)
		})
	}
}

func TestBuildEntryPointWithUnresolvedSetupStillBlocks(t *testing.T) {
	for _, scripts := range []map[string]string{
		{"build": "jest", "prebuild": "node setup.js"},
		{"build": "zx build.mjs && npm run missing"},
		{"build": "npm run \"$TARGET\""},
		{"build": "NODE_OPTIONS='' node build.js"},
	} {
		root := t.TempDir()
		writeScripts(t, root, scripts)
		result := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: "npm run build"}, "javascript", "jest")
		require.NotEmpty(t, result.Reason)
		require.False(t, result.Review, result)
	}
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
