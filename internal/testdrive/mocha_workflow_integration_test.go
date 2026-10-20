package testdrive

import (
	"context"
	"github.com/stretchr/testify/require"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPublicMochaTestdrive(t *testing.T) {
	modules := os.Getenv("DDTEST_MOCHA_NODE_MODULES")
	if modules == "" {
		t.Skip("set DDTEST_MOCHA_NODE_MODULES to Mocha, NYC and dd-trace dependencies")
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
	for _, mode := range []string{"passing", "existing failure", "coverage disabled"} {
		t.Run(mode, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			require.NoError(t, copyConfiguration(ctx, filepath.Dir(modules), root))
			write := func(name, value string) {
				require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0700))
				requireWriteFile(t, filepath.Join(root, name), value)
			}
			write("package.json", `{"scripts":{"test":"nyc -- mocha 'test/*.test.cjs'","programmatic":"nyc -- node runner.cjs"},"devDependencies":{"mocha":"11.7.6","nyc":"17.1.0","dd-trace":"6.20.1"}}`)
			write("lib/add.cjs", `module.exports=(a,b)=>a+b`)
			expected := "2"
			if mode == "existing failure" {
				expected = "3"
			}
			write("test/one.test.cjs", `const assert=require('node:assert/strict');describe('math',()=>it('adds',()=>assert.equal(require('../lib/add.cjs')(1,1),`+expected+`)))`)
			write("runner.cjs", `const Mocha=require('mocha');const runner=new Mocha({timeout:2000});runner.addFile('test/one.test.cjs');runner.run(failures=>{process.exitCode=failures?1:0})`)
			write(".nycrc.json", `{"include":["lib/**/*.cjs"],"extension":[".cjs"],"reporter":"none"}`)
			if mode == "coverage disabled" {
				write(".nycrc.json", `{"instrument":false,"reporter":"none"}`)
			}
			write(".github/workflows/ci.yml", `jobs:
  test:
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '24'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: js, js-tracer-version: '6.20.1'}
      - run: npm test
        env: {NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }}'}
      - run: npm run programmatic
        env: {NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }}'}
`)
			output, err = command(root, "git", "init", "-q")
			require.NoError(t, err, string(output))
			if mode == "passing" {
				output, err = command(root, binary, "onboard", "--framework", "mocha")
				require.NoError(t, err, string(output))
				require.Contains(t, string(output), "Istanbul/NYC")
				output, err = command(root, binary, "testdrive", "--framework", "mocha", "--all", "--check-only", "--yes")
				require.NoError(t, err, string(output))
				check := readValidationReport(t, root)
				require.True(t, check.ChecksPassed)
				require.Empty(t, check.Runs)
				require.False(t, check.Success)
			}
			output, err = command(root, binary, "testdrive", "--framework", "mocha", "--all", "--yes")
			if mode == "coverage disabled" {
				require.Error(t, err, string(output))
			} else {
				require.NoError(t, err, string(output))
			}
			report := readValidationReport(t, root)
			require.Equal(t, "compatible", report.Compatibility.Status, string(output))
			require.Len(t, report.Configurations, 2)
			require.Len(t, report.Features, 12)
			for _, feature := range report.Features {
				require.Equal(t, "passed", feature.Status, feature)
			}
			require.Equal(t, mode != "coverage disabled", report.Success, string(output))
			for _, run := range report.Runs {
				if run.Name == "reporting-only" {
					require.NotNil(t, run.Coverage)
					require.Equal(t, mode == "coverage disabled", run.Coverage.Status != "observed")
				}
			}
			require.Equal(t, "passed", report.Cleanup.Status)
			require.FileExists(t, htmlReportPath(root))
			require.NoDirExists(t, filepath.Join(root, ".nyc_output"))
			require.NoError(t, filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
				require.NoError(t, err)
				if entry.IsDir() && entry.Name() == "node_modules" {
					return filepath.SkipDir
				}
				require.False(t, strings.HasPrefix(entry.Name(), "ddtest"), path)
				return nil
			}))
		})
	}
}
