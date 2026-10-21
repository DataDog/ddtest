package testdrive

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Only public flags available at this stack position; all telemetry goes to
// the local mock. Native Cucumber/NYC runs supply the evidence.
func TestPublicCucumberTestdrive(t *testing.T) {
	modules := os.Getenv("DDTEST_CUCUMBER_NODE_MODULES")
	if modules == "" {
		t.Skip("set DDTEST_CUCUMBER_NODE_MODULES to Cucumber, NYC and dd-trace dependencies")
	}
	modules, err := filepath.Abs(modules)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "ddtest")
	command := func(root, name string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Dir = root
		for _, value := range os.Environ() {
			if !strings.HasPrefix(value, "DD_") && !strings.HasPrefix(value, "DATADOG_") {
				cmd.Env = append(cmd.Env, value)
			}
		}
		return cmd.CombinedOutput()
	}
	output, err := command("../..", "go", "build", "-o", binary, ".")
	require.NoError(t, err, string(output))
	for _, mode := range []string{"passing", "existing failure", "no application coverage", "package directory"} {
		t.Run(mode, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			require.NoError(t, copyConfiguration(ctx, filepath.Dir(modules), root))
			write := func(name, value string) {
				require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0700))
				requireWriteFile(t, filepath.Join(root, name), value)
			}
			appDirectory := root
			appWrite := write
			if mode == "package directory" {
				write("package.json", `{"private":true}`)
				appDirectory = filepath.Join(root, "packages/api")
				appWrite = func(name, value string) { write(filepath.Join("packages/api", name), value) }
			}
			appWrite("package.json", `{"scripts":{"test":"nyc -- cucumber-js"},"devDependencies":{"@cucumber/cucumber":"12.2.0","nyc":"17.1.0","dd-trace":"6.20.1"}}`)
			appWrite("src/add.cjs", "module.exports=(a,b)=>a+b;\n")
			appWrite("features/one.feature", "Feature: Arithmetic\n  Scenario: Add\n    Given addition works\n")
			assertion := "require('node:assert').equal(require('../../src/add.cjs')(1,1),2)"
			if mode == "existing failure" {
				assertion = "require('node:assert').equal(require('../../src/add.cjs')(1,1),3)"
			}
			if mode == "no application coverage" {
				assertion = "require('node:assert').equal(1+1,2)"
			}
			appWrite("features/step_definitions/one.js", `const {Given}=require('@cucumber/cucumber');Given('addition works',()=>{`+assertion+`});`)
			write(".github/workflows/ci.yml", `jobs:
  test:
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '24'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: js, js-tracer-version: '6.20.1'}
      - run: npm test
        env: {NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}'}
`)
			if mode == "package directory" {
				path := filepath.Join(root, ".github/workflows/ci.yml")
				data, err := os.ReadFile(path)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(path, []byte(strings.Replace(string(data), "      - run: npm test", "      - run: npm test\n        working-directory: packages/api", 1)), 0600))
			}
			output, err = command(root, "git", "init", "-q")
			require.NoError(t, err, string(output))
			if mode == "passing" {
				output, err = command(root, binary, "onboard", "--framework", "cucumber")
				require.NoError(t, err, string(output))
				require.Contains(t, string(output), "NYC")
				output, err = command(root, binary, "testdrive", "--framework", "cucumber", "--all", "--check-only", "--yes")
				require.NoError(t, err, string(output))
				check := readValidationReport(t, root)
				require.True(t, check.ChecksPassed, string(output))
				require.Empty(t, check.Runs)
			}
			args := []string{"testdrive", "--framework", "cucumber", "--all", "--yes"}
			if mode == "existing failure" {
				args = []string{"testdrive", "--framework", "cucumber", "--command", "npm test", "--yes"}
			}
			output, err = command(root, binary, args...)
			report := readValidationReport(t, root)
			if mode == "no application coverage" {
				require.Error(t, err, string(output))
				require.False(t, report.Success)
				require.True(t, missingRealCoverage(report))
			} else {
				require.NoError(t, err, string(output))
				require.True(t, report.Success, string(output))
			}
			require.Equal(t, "compatible", report.Compatibility.Status, string(output))
			require.Len(t, report.Features, 6)
			for _, feature := range report.Features {
				require.Equal(t, "passed", feature.Status, feature)
			}
			require.FileExists(t, htmlReportPath(root))
			require.NotNil(t, report.Cleanup)
			require.Equal(t, "passed", report.Cleanup.Status)
			require.NoDirExists(t, filepath.Join(root, ".nyc_output"))
			require.NoDirExists(t, filepath.Join(root, "coverage"))
			entries, err := os.ReadDir(filepath.Join(appDirectory, "features"))
			require.NoError(t, err)
			require.Len(t, entries, 2, "feature and step definitions only; no owned probes")
		})
	}
}
