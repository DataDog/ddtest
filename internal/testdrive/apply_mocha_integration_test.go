package testdrive

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/testdrive/workspace"
	"github.com/stretchr/testify/require"
)

func TestPublicMochaApply(t *testing.T) {
	modules := os.Getenv("DDTEST_MOCHA_NODE_MODULES")
	if modules == "" {
		t.Skip("set DDTEST_MOCHA_NODE_MODULES to Mocha, NYC and dd-trace dependencies")
	}
	modules, err := filepath.Abs(modules)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
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
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, workspace.Copy(ctx, filepath.Dir(modules), root, skipIgnoredOutput))
	write := func(name, value string) {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0700))
		requireWriteFile(t, filepath.Join(root, name), value)
	}
	write("package.json", `{"private":true,"scripts":{"test":"mocha test/one.test.cjs"},"devDependencies":{"mocha":"11.7.6","nyc":"17.1.0","dd-trace":"6.20.1"}}`)
	write("lib/add.cjs", `module.exports=(a,b)=>a+b`)
	write("test/one.test.cjs", `const assert=require('node:assert/strict');describe('math',()=>it('adds',()=>{assert.equal(process.env.CI,'true');assert.equal(require('../lib/add.cjs')(1,1),2)}))`)
	write(".nycrc.json", `{"include":["lib/**/*.cjs"],"extension":[".cjs"],"reporter":"none"}`)
	write(".github/workflows/test.yml", "name: CI\non: [push, pull_request]\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '24.14.1'}\n      - run: CI=true npm test\n")
	output, err = command(root, "git", "init", "-q")
	require.NoError(t, err, string(output))
	output, err = command(root, binary, "onboard", "--framework", "mocha", "--apply", "--yes", "--allow-dirty")
	require.NoError(t, err, string(output))
	assertAppliedWorkflowActionlint(t, filepath.Join(root, ".github/workflows/test.yml"))
	report := readValidationReport(t, root)
	require.True(t, report.Success, string(output))
	require.NotEmpty(t, report.Features)
	for _, feature := range report.Features {
		require.Equal(t, "passed", feature.Status, feature)
	}
	require.Equal(t, "passed", report.Cleanup.Status)
	data, err := os.ReadFile(filepath.Join(root, ".github/workflows/test.yml"))
	require.NoError(t, err)
	require.Contains(t, string(data), "- run: CI=true npx --no-install nyc -- npm test")
	require.FileExists(t, htmlReportPath(root))
	require.NoDirExists(t, filepath.Join(root, ".nyc_output"))
}
