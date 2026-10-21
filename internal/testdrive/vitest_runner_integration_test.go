// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPublicVitestRunnerTestdrive(t *testing.T) {
	if os.Getenv("DDTEST_RUN_FRAMEWORK_INTEGRATION_TEST") == "" {
		t.Skip("set DDTEST_RUN_FRAMEWORK_INTEGRATION_TEST=1")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "ddtest-under-test")
	integrationCommand(t, ctx, "../..", nil, "go", "build", "-o", binary, ".")
	root := t.TempDir()
	integrationCommand(t, ctx, root, nil, "git", "init", "-q")
	vitest, tracer := os.Getenv("DDTEST_VITEST_VERSION"), os.Getenv("DDTEST_VITEST_TRACER_VERSION")
	if vitest == "" {
		vitest = "4.1.11"
	}
	if tracer == "" {
		tracer = "6.21.0"
	}
	modules := os.Getenv("DDTEST_VITEST_NODE_MODULES")
	if modules != "" {
		data, err := os.ReadFile(filepath.Join(modules, "vitest/package.json"))
		require.NoError(t, err)
		var pkg struct{ Version string }
		require.NoError(t, json.Unmarshal(data, &pkg))
		vitest = pkg.Version
		data, err = os.ReadFile(filepath.Join(modules, "dd-trace/package.json"))
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(data, &pkg))
		tracer = pkg.Version
	}
	integrationFile(t, root, "package.json", fmt.Sprintf(`{"type":"module","scripts":{"test":"vitest run --config vitest.config.js"},"devDependencies":{"vitest":%q,"@vitest/coverage-v8":%q,"dd-trace":%q}}`, vitest, vitest, tracer))
	integrationFile(t, root, "vitest.config.js", `export default { test: { dir: 'src', pool: 'forks', maxWorkers: 1, coverage: { enabled:true, include:['src/value.js'], thresholds:{ autoUpdate:true, lines:90, 'src/**':{lines:90} } } } }`)
	// CLI substring matching historically selected both of these for one
	// assignment. The Node API must run each file on exactly one worker.
	for _, file := range []string{"src/endOfYear/example.test.ts", "src/eachWeekendOfYear/example.test.ts"} {
		integrationFile(t, root, file, `import { test, expect } from 'vitest'; import {add} from '../value.js'; test('adds', () => expect(add(1,1)).toBe(2))`)
	}
	integrationFile(t, root, "src/value.js", `export const add=(a,b)=>a+b`)
	workflow := func(packageOnly bool) string {
		text := fmt.Sprintf(`jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '24.14.1'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: js, js-tracer-version: '%s'}
      - run: ddtest run --framework vitest --vitest-config vitest.config.js --min-parallelism 2 --max-parallelism 2
`, tracer)
		if packageOnly {
			return strings.ReplaceAll(text, "run: ddtest run", "run: ../../bin/ddtest run") + "        working-directory: packages/app\n        env: {DD_TEST_OPTIMIZATION_RUNNER_MAX_PARALLELISM: '2'}\n"
		}
		return text + `      - run: vitest run --config vitest.config.js
        env: {NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}'}
`
	}
	integrationFile(t, root, ".github/workflows/test.yml", workflow(false))
	integrationFile(t, root, ".testoptimization/runner/parallel-runners.txt", "customer's existing plan")
	integrationFile(t, root, "customer.json", "existing customer report")
	if modules != "" {
		require.NoError(t, os.Symlink(modules, filepath.Join(root, "node_modules")))
	} else {
		integrationCommand(t, ctx, root, nil, "npm", "install", "--no-audit", "--no-fund")
	}
	originalConfig, err := os.ReadFile(filepath.Join(root, "vitest.config.js"))
	require.NoError(t, err)
	for _, mode := range []struct {
		name                   string
		args                   []string
		features, paths, tests int
	}{
		{"mixed CI paths", []string{"--all"}, 12, 2, 2},
		{"explicit CLI", []string{"--command", "npx vitest run --config vitest.config.js"}, 6, 1, 2},
		{"CLI positional", []string{"--command", "npx vitest run --config vitest.config.js src/endOfYear --testNamePattern adds --reporter=junit --outputFile customer.json --coverage.reportsDirectory customer-coverage"}, 6, 1, 2},
		{"CLI shard and reporter map", []string{"--command", "npx vitest run --config vitest.config.js --shard=1/2 --reporter=junit --outputFile.junit=customer.json"}, 6, 1, 1},
		{"CLI terminator", []string{"--command", "npx vitest run --config vitest.config.js -- src"}, 6, 1, 2},
		{"explicit runner", []string{"--command", "ddtest run --framework vitest --vitest-config vitest.config.js --min-parallelism 2 --max-parallelism 2"}, 6, 1, 2},
		{"package runner", []string{"--all"}, 6, 1, 2},
	} {
		t.Run(mode.name, func(t *testing.T) {
			if mode.name == "package runner" {
				require.NoError(t, os.MkdirAll(filepath.Join(root, "bin"), 0700))
				require.NoError(t, os.Link(binary, filepath.Join(root, "bin/ddtest")))
				pkg := filepath.Join(root, "packages/app")
				require.NoError(t, os.MkdirAll(pkg, 0700))
				for _, name := range []string{"node_modules", "src", "vitest.config.js", "package.json"} {
					require.NoError(t, os.Rename(filepath.Join(root, name), filepath.Join(pkg, name)))
				}
				integrationFile(t, root, "package.json", `{}`)
				integrationFile(t, root, "vitest.config.js", `throw new Error('root config must not be loaded')`)
				integrationFile(t, root, ".github/workflows/test.yml", workflow(true))
			}

			if mode.name == "package runner" {
				check := exec.CommandContext(ctx, binary, "testdrive", "--framework", "vitest", "--all", "--check-only", "--yes")
				check.Dir = root
				output, err := check.CombinedOutput()
				require.NoError(t, err, string(output))
				data, err := os.ReadFile(filepath.Join(root, ".testoptimization/testdrive.json"))
				require.NoError(t, err)
				var inspection struct {
					Error          string
					Runs           []any
					Configurations []struct {
						Preflight struct {
							Verdict        struct{ Status string }
							VitestProjects []struct{ Root string } `json:"vitest_projects"`
						}
					}
				}
				require.NoError(t, json.Unmarshal(data, &inspection))
				require.Empty(t, inspection.Error)
				require.Empty(t, inspection.Runs)
				require.Len(t, inspection.Configurations, 1)
				preflight := inspection.Configurations[0].Preflight
				require.Equal(t, "compatible", preflight.Verdict.Status)
				require.Len(t, preflight.VitestProjects, 1)
				require.True(t, strings.HasSuffix(filepath.ToSlash(preflight.VitestProjects[0].Root), "/packages/app"))
			}
			command := exec.CommandContext(ctx, binary, append([]string{"testdrive", "--framework", "vitest", "--yes"}, mode.args...)...)
			command.Dir = root
			output, err := command.CombinedOutput()
			// The declared CI selection matches the local tracer; no real CI or
			// Datadog backend is contacted by these controlled local checks.
			require.NoError(t, err, string(output))
			require.Contains(t, string(output), "Open report:")
			t.Log(string(output))
			data, err := os.ReadFile(filepath.Join(root, ".testoptimization/testdrive.json"))
			require.NoError(t, err)
			var report struct {
				Error   string
				Summary struct {
					ExecutedValidation struct{ Status string } `json:"executed_validation"`
				}
				Cleanup       struct{ Status string }
				Compatibility struct{ Status string }
				Runs          []struct {
					Name              string
					ExecutionPath     string   `json:"execution_path"`
					ReplacedReporters []string `json:"replaced_reporters"`
					Workers           int
					TestCounts        struct{ Passed int } `json:"test_counts"`
				}
				Features []struct{ Name, Status, Reason string }
			}
			require.NoError(t, json.Unmarshal(data, &report))
			if report.Compatibility.Status != "compatible" {
				t.Log(string(data))
			}
			require.Equal(t, "compatible", report.Compatibility.Status, string(output))
			require.Empty(t, report.Error)
			require.Equal(t, "passed", report.Cleanup.Status)
			require.Equal(t, "PASSED", report.Summary.ExecutedValidation.Status, string(data))
			require.GreaterOrEqual(t, len(report.Runs), 2)
			paths := map[string]bool{}
			for _, run := range report.Runs {
				paths[run.ExecutionPath] = true
			}
			require.Equal(t, mode.paths, len(paths))
			for _, run := range report.Runs[:2] {
				if strings.Contains(mode.name, "CLI") {
					require.Equal(t, "vitest-cli", run.ExecutionPath)
				} else {
					require.Equal(t, "ddtest-run-node-api", run.ExecutionPath)
					require.Equal(t, 2, run.Workers)
				}
				// Native Vitest's substring filter selects both fixture files here;
				// preserve its actual behavior rather than infer a file count.
				require.Equal(t, mode.tests, run.TestCounts.Passed)
				if mode.name == "CLI positional" || mode.name == "CLI shard and reporter map" {
					require.Contains(t, run.ReplacedReporters, "junit")
				}
			}
			require.Len(t, report.Features, mode.features)
			configPath := filepath.Join(root, "vitest.config.js")
			if mode.name == "package runner" {
				configPath = filepath.Join(root, "packages/app/vitest.config.js")
			}
			after, err := os.ReadFile(configPath)
			require.NoError(t, err)
			require.Equal(t, originalConfig, after, "coverage autoUpdate must not change customer config")
			for _, feature := range report.Features {
				if feature.Status != "passed" {
					t.Log(string(data))
				}
				require.Equal(t, "passed", feature.Status, "%s: %s", feature.Name, feature.Reason)
			}
		})
	}
	customerReport, err := os.ReadFile(filepath.Join(root, "customer.json"))
	require.NoError(t, err)
	require.Equal(t, "existing customer report", string(customerReport))
	plan, err := os.ReadFile(filepath.Join(root, ".testoptimization/runner/parallel-runners.txt"))
	require.NoError(t, err)
	require.Equal(t, "customer's existing plan", string(plan))
	require.NoError(t, filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		require.NoError(t, err)
		if entry.IsDir() && entry.Name() == "node_modules" {
			return filepath.SkipDir
		}
		require.False(t, strings.Contains(entry.Name(), "ddtest-validation"), path)
		return nil
	}))
}
