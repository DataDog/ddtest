package testdrive

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The root and package install distinct tracer metadata. Both inspection and
// execution must use the package, with checkout-relative telemetry identities.
func TestPublicJestPackageConfiguration(t *testing.T) {
	modules := os.Getenv("DDTEST_JEST_NODE_MODULES")
	if modules == "" {
		t.Skip("set DDTEST_JEST_NODE_MODULES")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	modules, err = filepath.Abs(modules)
	require.NoError(t, err)
	require.NoError(t, copyConfiguration(ctx, filepath.Dir(modules), root))
	pkg := filepath.Join(root, "packages/app")
	require.NoError(t, os.MkdirAll(pkg, 0700))
	require.NoError(t, copyConfiguration(ctx, filepath.Dir(modules), pkg))
	// Only the unexecuted root installation's manifest differs; the package uses
	// the original real tracer. This detects a stale cached root preload path.
	manifest := filepath.Join(root, "node_modules/dd-trace/package.json")
	data, err := os.ReadFile(manifest)
	require.NoError(t, err)
	var metadata map[string]any
	require.NoError(t, json.Unmarshal(data, &metadata))
	metadata["version"] = "6.14.0"
	data, err = json.Marshal(metadata)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(manifest, data, 0600))
	write := func(name, value string) {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0700))
		requireWriteFile(t, filepath.Join(root, name), value)
	}
	write("package.json", `{"devDependencies":{"jest":"30.5.1"}}`)
	write("jest.config.js", `throw new Error('root configuration must not be inspected')`)
	write("packages/app/package.json", `{"scripts":{"test":"jest --runInBand --config ci.config.js"},"devDependencies":{"jest":"30.5.1"}}`)
	write("packages/app/ci.config.js", `module.exports={testEnvironment:'node',watchman:false}`)
	write("packages/app/one.test.js", `test('package test',()=>{expect(1+1).toBe(2);expect(process.env.NODE_OPTIONS).not.toContain('--conditions=development')})`)
	write(".github/workflows/ci.yml", `jobs:
  test:
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '24'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: js, js-tracer-version: '6.15.0'}
      - run: npm test
        working-directory: packages/app
        env: {NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }}'}
`)
	command := func(dir, name string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Dir = dir
		for _, value := range os.Environ() {
			if !strings.HasPrefix(value, "DD_") && !strings.HasPrefix(value, "DATADOG_") && !strings.HasPrefix(value, "NODE_OPTIONS=") {
				cmd.Env = append(cmd.Env, value)
			}
		}
		cmd.Env = append(cmd.Env, "NODE_OPTIONS=--conditions=development")
		return cmd.CombinedOutput()
	}
	binary := filepath.Join(t.TempDir(), "ddtest")
	data, err = command("../..", "go", "build", "-o", binary, ".")
	require.NoError(t, err, string(data))
	data, err = command(root, "git", "init", "-q")
	require.NoError(t, err, string(data))
	for _, args := range [][]string{{"--all", "--check-only"}, {"--all"}} {
		args = append([]string{"testdrive", "--framework", "jest", "--yes"}, args...)
		data, err = command(root, binary, args...)
		require.NoError(t, err, string(data))
		report := readValidationReport(t, root)
		require.True(t, report.ChecksPassed, string(data))
		require.Equal(t, "6.15.0", report.Selection.Version)
		require.Len(t, report.Configurations, 1)
		require.Equal(t, "packages/app", report.Configurations[0].Directory)
		require.Empty(t, report.Configurations[0].Environment["NODE_OPTIONS"])
		if report.CheckOnly {
			require.False(t, report.Success)
			require.Empty(t, report.Runs)
		} else {
			require.True(t, report.Success, string(data))
			require.Len(t, report.Features, 6)
			for _, feature := range report.Features {
				require.Equal(t, "passed", feature.Status, feature)
			}
			require.NotNil(t, report.ReportingCounts)
			require.Equal(t, 1, *report.ReportingCounts.SourceTestIdentities)
			for _, run := range report.Runs {
				if run.Skipping != nil {
					require.True(t, strings.HasPrefix(run.Skipping.RepositoryPath, "packages/app/"))
				}
			}
		}
	}
	requireWriteFile(t, filepath.Join(root, "jest.config.js"), `module.exports={testEnvironment:'node',watchman:false,testMatch:['<rootDir>/*root.test.js']}`)
	write("root.test.js", `test('root test',()=>expect(1+1).toBe(2))`)
	write("package.json", `{"scripts":{"test":"jest --runInBand"},"devDependencies":{"jest":"30.5.1"}}`)
	write(".github/workflows/ci.yml", `jobs:
  test:
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '24'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: js, js-tracer-version: '6.14.0'}
      - run: npm test
        env: {NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }}'}
`)
	// Default execution must match CI independently of --showConfig. The fixture
	// does not inherit extra options for this final root comparison.
	t.Setenv("NODE_OPTIONS", "")
	cmd := exec.CommandContext(ctx, binary, "testdrive", "--framework", "jest", "--yes")
	cmd.Dir = root
	data, err = cmd.CombinedOutput()
	require.NoError(t, err, string(data))
	report := readValidationReport(t, root)
	require.True(t, report.Success, string(data))
	require.Equal(t, "passed", report.Configurations[0].Status)
}
