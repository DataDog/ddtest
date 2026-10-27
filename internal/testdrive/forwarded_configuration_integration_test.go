package testdrive

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/testdrive/workspace"
	"github.com/stretchr/testify/require"
)

func TestPublicJestForwardedConfiguration(t *testing.T) {
	modules := os.Getenv("DDTEST_JEST_NODE_MODULES")
	if modules == "" || runtime.GOOS == "windows" {
		t.Skip("requires real Jest dependencies and POSIX package scripts")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	modules, err := filepath.Abs(modules)
	require.NoError(t, err)
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, workspace.Copy(ctx, filepath.Dir(modules), root, skipIgnoredOutput))
	write := func(name, value string) {
		path := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
		requireWriteFile(t, path, value)
	}
	write("package.json", `{"scripts":{"test":"eslint . && jest --runInBand"},"devDependencies":{"jest":"*"}}`)
	write("jest.config.js", `module.exports={testEnvironment:'node',watchman:false}`)
	write("one.test.js", `test('CI environment',()=>{expect(process.env.TZ).toBe('UTC');expect(process.env.WORKSPACE).toBe('fixtures')})`)
	write("node_modules/.bin/eslint", "#!/bin/sh\nexit 0\n")
	require.NoError(t, os.Chmod(filepath.Join(root, "node_modules/.bin/eslint"), 0700))
	write(".github/workflows/ci.yml", fmt.Sprintf(`jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '24'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: js, js-tracer-version: '%s'}
      - run: npm test
        env: {TZ: UTC, WORKSPACE: fixtures, NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }}'}
`, integrationTracerVersion(t, modules)))
	command := func(directory, name string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Dir = directory
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "DD_") && !strings.HasPrefix(entry, "DATADOG_") && !strings.HasPrefix(entry, "NODE_OPTIONS=") {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		cmd.Env = append(cmd.Env, "NODE_OPTIONS=")
		return cmd.CombinedOutput()
	}
	binary := filepath.Join(t.TempDir(), "ddtest")
	data, err := command("../..", "go", "build", "-o", binary, ".")
	require.NoError(t, err, string(data))
	data, err = command(root, "git", "init", "-q")
	require.NoError(t, err, string(data))
	for _, flags := range [][]string{{}, {"--command", "npm test"}} {
		data, err = command(root, binary, append([]string{"testdrive", "--framework", "jest", "--yes"}, flags...)...)
		require.NoError(t, err, string(data))
		report := readValidationReport(t, root)
		require.True(t, report.Success, string(data))
		require.Len(t, report.Configurations, 1)
		require.Equal(t, "passed", report.Configurations[0].Status)
		require.Empty(t, report.Configurations[0].EnvironmentDifferences)
		require.Len(t, report.Features, 6)
	}
}
