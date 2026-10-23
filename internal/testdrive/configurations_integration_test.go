package testdrive

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Use only the public --all/check-only commands available at this stack step.
func TestPublicJestConfigurations(t *testing.T) {
	modules := os.Getenv("DDTEST_JEST_NODE_MODULES")
	if modules == "" {
		t.Skip("set DDTEST_JEST_NODE_MODULES to Jest, dd-trace and TypeScript dependencies")
	}
	modules, err := filepath.Abs(modules)
	require.NoError(t, err)
	if _, err := os.Stat(filepath.Join(modules, "typescript", "package.json")); os.IsNotExist(err) {
		t.Skip("TypeScript is required for the build-prerequisite fixture")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Minute)
	defer cancel()
	environment := []string{}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "DD_") && !strings.HasPrefix(entry, "DATADOG_") {
			environment = append(environment, entry)
		}
	}
	environment = append(environment, "NODE_OPTIONS=--max-old-space-size=4096")
	command := func(directory, name string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Dir = directory
		cmd.Env = environment
		return cmd.CombinedOutput()
	}
	binary := filepath.Join(t.TempDir(), "ddtest")
	data, err := command("../..", "go", "build", "-o", binary, ".")
	require.NoError(t, err, string(data))
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, copyConfiguration(ctx, filepath.Dir(modules), root))
	tracerVersion := integrationTracerVersion(t, modules)
	data, err = command(root, "git", "init", "-q")
	require.NoError(t, err, string(data))
	write := func(name, value string) {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0755))
		requireWriteFile(t, filepath.Join(root, name), value)
	}
	write("package.json", `{"scripts":{"build":"tsc --project tsconfig.json","test":"jest --runInBand"},"devDependencies":{"jest":"30.5.1","typescript":"5.9.3"}}`)
	write("tsconfig.json", `{"compilerOptions":{"module":"CommonJS","outDir":"lib","types":[],"skipLibCheck":true},"files":["src/add.ts"]}`)
	write("src/add.ts", `export const add=(a:number,b:number)=>a+b;`)
	write("one.test.js", `const {add}=require('./lib/add'); test('adds',()=>{expect(add(1,1)).toBe(2);expect(['source','built']).toContain(process.env.MODE);expect(process.env.NODE_OPTIONS).toContain('--max-old-space-size='+process.env.HEAP);expect(process.env.NODE_OPTIONS).not.toContain('4096');});`)
	write("source.config.js", `module.exports={displayName:'Source',testMatch:['**/*.test.js'],testEnvironment:'node',watchman:false};`)
	write("built.config.js", `module.exports={displayName:'Built',testMatch:['**/*.test.js'],testEnvironment:'node',watchman:false};`)
	write(".github/workflows/ci.yml", strings.ReplaceAll(`jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '24'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: js, js-tracer-version: 'TRACER_VERSION'}
      - run: npm run build
      - run: npm test -- --config source.config.js
        env:
          MODE: source
          HEAP: '1024'
          NODE_OPTIONS: "--max-old-space-size=1024 -r ${{ env.DD_TRACE_PACKAGE }}"
      - run: npm test -- --config built.config.js
        env:
          MODE: built
          HEAP: '2048'
          NODE_OPTIONS: "--max-old-space-size=2048 -r ${{ env.DD_TRACE_PACKAGE }}"
`, "TRACER_VERSION", tracerVersion))
	original := map[string][]byte{}
	for _, name := range []string{"package.json", "src/add.ts", "tsconfig.json", "one.test.js", "source.config.js", "built.config.js"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		require.NoError(t, err)
		original[name] = data
	}
	data, err = command(root, binary, "testdrive", "--framework", "jest", "--all", "--yes")
	require.NoError(t, err, string(data))
	require.Contains(t, string(data), "Open report:")
	report := readValidationReport(t, root)
	require.True(t, report.Success, string(data))
	require.Equal(t, "PASSED", report.Summary.Status)
	require.Len(t, report.Configurations, 2)
	for _, item := range report.Configurations {
		require.Equal(t, "passed", item.Status)
		require.True(t, item.PairedExecution)
		require.Len(t, item.Preparation, 1)
		require.Equal(t, "passed", item.Preparation[0].Status)
	}
	require.Len(t, report.Features, 12)
	for _, feature := range report.Features {
		require.Equal(t, "passed", feature.Status, feature)
	}
	require.NotNil(t, report.ReportingCounts)
	require.Equal(t, 2, report.ReportingCounts.TestExecutions)
	require.Equal(t, 1, *report.ReportingCounts.SourceTestIdentities)
	for name, before := range original {
		after, err := os.ReadFile(filepath.Join(root, name))
		require.NoError(t, err)
		require.Equal(t, before, after, name)
	}
	require.NoDirExists(t, filepath.Join(root, "lib"), "builds must write only to disposable copies")
	html, err := os.ReadFile(htmlReportPath(root))
	require.NoError(t, err)
	require.Contains(t, string(html), "source.config.js")
	require.Contains(t, string(html), "built.config.js")
	// A metadata-only rerun retains the pair without executing either build.
	data, err = command(root, binary, "testdrive", "--framework", "jest", "--all", "--check-only", "--yes")
	require.NoError(t, err, string(data))
	check := readValidationReport(t, root)
	require.True(t, check.ChecksPassed)
	require.False(t, check.Success)
	require.Empty(t, check.Runs)
	require.NotNil(t, check.Retained)
	currentHTML, err := os.ReadFile(htmlReportPath(root))
	require.NoError(t, err)
	require.Equal(t, html, currentHTML)
	require.NoDirExists(t, filepath.Join(root, "lib"))
	// Missing setup is a failed preparation, not a matching test-suite failure.
	require.NoError(t, os.Remove(filepath.Join(root, "tsconfig.json")))
	data, err = command(root, binary, "testdrive", "--framework", "jest", "--all", "--yes")
	require.Error(t, err, string(data))
	failed := readValidationReport(t, root)
	require.False(t, failed.Success)
	for _, item := range failed.Configurations {
		require.Equal(t, "blocked", item.Status)
		require.False(t, item.ExecutionAttempted)
		require.Equal(t, "failed", item.Preparation[0].Status)
		require.NotEmpty(t, item.Preparation[0].Diagnostic)
	}
	require.Empty(t, failed.Runs)
	require.Nil(t, failed.Retained)
	entries, err := os.ReadDir(filepath.Join(root, ".testoptimization"))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.NoFileExists(t, htmlReportPath(root))
	encoded, err := json.Marshal(failed)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "raw-event-stack")
}
