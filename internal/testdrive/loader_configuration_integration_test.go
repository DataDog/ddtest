package testdrive

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Model PnP's loader-only resolution with real Jest and dd-trace. Neither the
// configuration nor the package has a node_modules bin available to launch.
func TestPublicJestLoaderConfiguration(t *testing.T) {
	modules := os.Getenv("DDTEST_JEST_NODE_MODULES")
	if modules == "" {
		t.Skip("set DDTEST_JEST_NODE_MODULES")
	}
	modules, err := filepath.Abs(modules)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	root = filepath.Join(root, "project with spaces")
	require.NoError(t, os.MkdirAll(root, 0700))
	write := func(name, value string) {
		path := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
		requireWriteFile(t, path, value)
	}
	version := integrationTracerVersion(t, modules)
	write("package.json", `{"scripts":{"test":"jest --runInBand"},"devDependencies":{"jest":"*"}}`)
	write("jest.config.js", `module.exports={testEnvironment:'node',watchman:false}`)
	write("add.js", `module.exports=(a,b)=>a+b`)
	write("one.test.js", `test('adds',()=>expect(require('./add')(1,1)).toBe(2))`)
	write(".pnp.cjs", fmt.Sprintf(`const Module=require('module');const original=Module._resolveFilename;
const entries={'jest/bin/jest':%q,'jest/package.json':%q,'dd-trace/ci/init':%q,'typescript/package.json':%q};
Module._resolveFilename=function(request,...args){return entries[request]||original.call(this,request,...args)};
`, filepath.Join(modules, "jest/bin/jest.js"), filepath.Join(modules, "jest/package.json"), filepath.Join(modules, "dd-trace/ci/init.js"), filepath.Join(modules, "typescript/package.json")))
	workflow := fmt.Sprintf(`jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '24'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: js, js-tracer-version: '%s'}
      - run: npm test
        env: {NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }}'}
`, version)
	write(".github/workflows/ci.yml", workflow)
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
	for _, flags := range [][]string{{"--check-only"}, {}} {
		args := append([]string{"testdrive", "--framework", "jest", "--all", "--yes"}, flags...)
		data, err = command(root, binary, args...)
		require.NoError(t, err, string(data))
		report := readValidationReport(t, root)
		require.True(t, report.ChecksPassed, string(data))
		require.NotNil(t, report.Selection)
		require.Equal(t, "project", report.Selection.Source)
		require.Equal(t, version, report.Selection.Version)
		require.Len(t, report.Configurations, 1)
		require.NoDirExists(t, filepath.Join(root, "node_modules"))
		if report.CheckOnly {
			require.False(t, report.Success)
			require.Empty(t, report.Runs)
		} else {
			require.True(t, report.Success, string(data))
			require.Len(t, report.Features, 6)
			for _, feature := range report.Features {
				require.Equal(t, "passed", feature.Status, feature)
			}
			for _, run := range report.Runs {
				require.Contains(t, run.Command, ".pnp.cjs")
				require.NotContains(t, run.Command, "node_modules/.bin/jest")
			}
		}
	}
	// The same loader-only checkout must also execute installed build tools.
	write("tsconfig.json", `{"compilerOptions":{"module":"CommonJS","outDir":"lib","types":[],"skipLibCheck":true},"files":["src/build.ts"]}`)
	write("src/build.ts", `export const value=1`)
	write("built.test.js", `test('built source',()=>expect(require('./lib/build').value).toBe(1))`)
	write(".github/workflows/ci.yml", strings.Replace(workflow, "      - run: npm test", "      - run: tsc --project tsconfig.json\n      - run: npm test", 1))
	data, err = command(root, binary, "testdrive", "--framework", "jest", "--all", "--yes")
	require.NoError(t, err, string(data))
	built := readValidationReport(t, root)
	require.True(t, built.Success, string(data))
	require.Equal(t, "passed", built.Configurations[0].Preparation[0].Status)
	require.NoDirExists(t, filepath.Join(root, "lib"))
	require.NoDirExists(t, filepath.Join(root, "node_modules"))
}
