package testdrive

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/kballard/go-shellquote"
	"github.com/stretchr/testify/require"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	require.NoError(t, copyConfiguration(ctx, filepath.Dir(modules), root, false))
	tracerVersion := integrationTracerVersion(t, modules)
	pkg := filepath.Join(root, "packages/app")
	require.NoError(t, os.MkdirAll(pkg, 0700))
	require.NoError(t, copyConfiguration(ctx, filepath.Dir(modules), pkg, false))
	// Only the unexecuted root installation's manifest differs; the package uses
	// the original real tracer. This detects a stale cached root preload path.
	manifest := filepath.Join(root, "node_modules/dd-trace/package.json")
	data, err := os.ReadFile(manifest)
	require.NoError(t, err)
	var metadata map[string]any
	require.NoError(t, json.Unmarshal(data, &metadata))
	rootVersion := "6.14.0"
	if strings.HasPrefix(tracerVersion, "5.") {
		rootVersion = "5.131.0"
	}
	metadata["version"] = rootVersion
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
	write(".github/workflows/ci.yml", strings.ReplaceAll(`jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '24'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: js, js-tracer-version: 'TRACER_VERSION'}
      - run: npm test
        working-directory: packages/app
        env: {NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }}'}
`, "TRACER_VERSION", tracerVersion))
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
		require.Equal(t, tracerVersion, report.Selection.Version)
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
	// A literal patch is applied in a copy, relative to the package directory.
	originalWorkflow, err := os.ReadFile(filepath.Join(root, ".github/workflows/ci.yml"))
	require.NoError(t, err)
	configSource := `module.exports={displayName:'Source',testEnvironment:'node',watchman:false}`
	write("packages/app/ci.config.js", configSource)
	require.NoError(t, os.Symlink(t.TempDir(), filepath.Join(root, "unrelated-external")))
	write(".gitignore", "unrelated-external\n")
	require.NoError(t, os.Symlink("missing", filepath.Join(pkg, "node_modules/.bin/unused-dangling")))
	patchedWorkflow := strings.Replace(string(originalWorkflow), "      - run: npm test", "      - run: sed -i 's/Source/Built/g' ci.config.js\n        working-directory: packages/app\n      - run: npm test", 1)
	write(".github/workflows/ci.yml", patchedWorkflow)
	data, err = command(root, binary, "testdrive", "--framework", "jest", "--all", "--yes")
	require.NoError(t, err, string(data))
	patched := readValidationReport(t, root)
	require.True(t, patched.Success, string(data))
	require.Equal(t, "Built", patched.Configurations[0].Preflight.Projects[0].DisplayName.Name)
	require.Equal(t, "passed", patched.Configurations[0].Preparation[0].Status)
	unchanged, err := os.ReadFile(filepath.Join(pkg, "ci.config.js"))
	require.NoError(t, err)
	require.Equal(t, configSource, string(unchanged))
	write(".github/workflows/ci.yml", string(originalWorkflow))
	if runtime.GOOS != "windows" {
		node, err := exec.LookPath("node")
		require.NoError(t, err)
		wrapper := filepath.Join(t.TempDir(), "node")
		used := filepath.Join(t.TempDir(), "requested-runtime-used")
		script := "#!/bin/sh\nprintf 'used\\n' >> " + shellquote.Join(used) + "\nexec " + shellquote.Join(node) + " \"$@\"\n"
		require.NoError(t, os.WriteFile(wrapper, []byte(script), 0700))
		commandText := shellquote.Join(wrapper, "node_modules/jest/bin/jest.js", "--runInBand", "--config", "ci.config.js")
		workflow := fmt.Sprintf("jobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '24'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: js, js-tracer-version: '%s'}\n      - run: %s\n        working-directory: packages/app\n        env: {NODE_OPTIONS: ''}\n", tracerVersion, commandText)
		write(".github/workflows/ci.yml", workflow)
		data, err = command(root, binary, "testdrive", "--framework", "jest", "--all", "--yes")
		require.Error(t, err, "arbitrary explicit paths cannot be certified from setup-node alone")
		explicit := readValidationReport(t, root)
		require.Equal(t, "compatible", explicit.Compatibility.Status, string(data))
		require.Equal(t, "inconclusive", explicit.CIRuntime.Status)
		for _, feature := range explicit.Features {
			require.Equal(t, "passed", feature.Status, feature)
		}
		calls, err := os.ReadFile(used)
		require.NoError(t, err)
		require.Greater(t, strings.Count(string(calls), "used"), 8, "the requested executable must run tests and probes, not only metadata checks")
	}
	requireWriteFile(t, filepath.Join(root, "jest.config.js"), `module.exports={testEnvironment:'node',watchman:false,testMatch:['<rootDir>/*.test.js']}`)
	write("root.test.js", `test('root test',()=>{expect(1+1).toBe(2);expect(process.env.TZ).toBe('UTC')})`)
	write("package.json", `{"scripts":{"test":"jest --runInBand"},"devDependencies":{"jest":"30.5.1"}}`)
	write(".github/workflows/ci.yml", strings.ReplaceAll(`jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '24'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: js, js-tracer-version: 'ROOT_VERSION'}
      - run: npm test
        env: {TZ: UTC, NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }}'}
`, "ROOT_VERSION", rootVersion))
	// Default execution must match CI independently of --showConfig. The fixture
	// does not inherit extra options for this final root comparison.
	t.Setenv("NODE_OPTIONS", "")
	for _, extra := range [][]string{nil, {"--command", "npm test"}, {"--all"}} {
		args := append([]string{"testdrive", "--framework", "jest", "--yes"}, extra...)
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Dir = root
		data, err = cmd.CombinedOutput()
		require.NoError(t, err, string(data))
		report := readValidationReport(t, root)
		require.Equal(t, err == nil, report.Success, string(data))
		require.Equal(t, "passed", report.Configurations[0].Status)
	}
}

func integrationTracerVersion(t *testing.T, modules string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(modules, "dd-trace/package.json"))
	require.NoError(t, err)
	var manifest struct {
		Version string `json:"version"`
	}
	require.NoError(t, json.Unmarshal(data, &manifest))
	return manifest.Version
}

func TestPublicJestTwoPackageConfiguration(t *testing.T) {
	modules := os.Getenv("DDTEST_JEST_NODE_MODULES")
	if modules == "" {
		t.Skip("set DDTEST_JEST_NODE_MODULES")
	}
	modules, err := filepath.Abs(modules)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	write := func(name, value string) {
		path := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
		requireWriteFile(t, path, value)
	}
	write("package.json", `{"private":true}`)
	version := integrationTracerVersion(t, modules)
	for _, name := range []string{"a", "b"} {
		pkg := filepath.Join(root, "packages", name)
		require.NoError(t, copyConfiguration(ctx, filepath.Dir(modules), pkg, false))
		write("packages/"+name+"/package.json", `{"scripts":{"test":"jest --runInBand"}}`)
		write("packages/"+name+"/one.test.js", `test('same name',()=>{expect(1+1).toBe(2);expect(process.env.TZ).toBe('UTC')});`)
	}
	workflow := "jobs:\n"
	for _, name := range []string{"a", "b"} {
		workflow += "  " + name + ":\n    runs-on: ubuntu-latest\n    strategy:\n      matrix: {node: [20, 22]}\n    defaults:\n      run: {working-directory: packages/" + name + "}\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '${{ matrix.node }}'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: js, js-tracer-version: '" + version + "'}\n      - run: npm test\n        env: {TZ: UTC, NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }}'}\n"
	}
	// dd-trace 6 requires Node >=22, while the 5.x matrix also exercises Node20.
	if strings.HasPrefix(version, "6.") {
		workflow = strings.ReplaceAll(workflow, "[20, 22]", "[22, 24]")
	}
	write(".github/workflows/ci.yml", workflow)
	environment := []string{}
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "DD_") && !strings.HasPrefix(value, "DATADOG_") && !strings.HasPrefix(value, "NODE_OPTIONS=") {
			environment = append(environment, value)
		}
	}
	environment = append(environment, "NODE_OPTIONS=--conditions=development")
	command := func(dir, name string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Dir = dir
		cmd.Env = environment
		return cmd.CombinedOutput()
	}
	data, err := command(root, "git", "init", "-q")
	require.NoError(t, err, string(data))
	binary := filepath.Join(t.TempDir(), "ddtest")
	data, err = command("../..", "go", "build", "-o", binary, ".")
	require.NoError(t, err, string(data))
	for _, args := range [][]string{{"--all", "--check-only"}, {"--all"}} {
		data, err = command(root, binary, append([]string{"testdrive", "--framework", "jest", "--yes"}, args...)...)
		require.NoError(t, err, string(data))
		report := readValidationReport(t, root)
		require.True(t, report.ChecksPassed, string(data))
		require.Len(t, report.Configurations, 2)
		for _, configuration := range report.Configurations {
			require.Len(t, configuration.Locations, 2, "Node matrix rows deduplicate into one command")
		}
		require.NotContains(t, string(data), "Isolating configuration:", "package directories without preparation never copy the checkout")
		if !report.CheckOnly {
			require.True(t, report.Success, string(data))
			require.Equal(t, 2, *report.ReportingCounts.SourceTestIdentities)
			require.Len(t, report.Features, 12)
			for _, feature := range report.Features {
				require.Equal(t, "passed", feature.Status, feature)
			}
			html, err := os.ReadFile(htmlReportPath(root))
			require.NoError(t, err)
			require.NotContains(t, string(html), "Source could not be read")
		}
	}
	// A focused nested invocation still reports correct paths; it cannot certify
	// checkout CI from this package, so its separate CI finding remains incomplete.
	data, err = command(filepath.Join(root, "packages/a"), binary, "testdrive", "--framework", "jest", "--yes")
	require.Error(t, err, string(data))
	nested := readValidationReport(t, filepath.Join(root, "packages/a"))
	require.Equal(t, "compatible", nested.Compatibility.Status, string(data))
	require.Len(t, nested.Features, 6)
	for _, feature := range nested.Features {
		require.Equal(t, "passed", feature.Status, feature)
	}
}
