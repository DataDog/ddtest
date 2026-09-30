package compatibility

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testdrive"
	"github.com/stretchr/testify/require"
)

// These fixtures use real runtimes and package resolution, with no downloaded
// tracer or mocked command output. CI installs all four prerequisite commands.
func requireRuntime(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		if os.Getenv("DDTEST_REQUIRE_PROJECT_RUNTIMES") == "1" {
			t.Fatalf("missing required regression-test runtime %s: %v", name, err)
		}
		t.Skipf("%s is required for the project environment regression test: %v", name, err)
	}
}

func runFixtureCommand(t *testing.T, ctx context.Context, name string, args ...string) {
	t.Helper()
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	require.NoError(t, err, "%s: %s", name, output)
}

func TestJavaScriptProjectEnvironment(t *testing.T) {
	requireRuntime(t, "node")
	requireRuntime(t, "yarn")
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	root := filepath.Join(t.TempDir(), "project with spaces")
	writeFixture(t, root, "package.json", `{"name":"pnp-regression","private":true,"scripts":{"test":"jest"},"dependencies":{"dd-trace":"file:./tracer"}}`)
	writeFixture(t, root, "tracer/package.json", `{"name":"dd-trace","version":"6.18.0","engines":{"node":">=18"},"dependencies":{"pnp-tracer-helper":"file:../helper"}}`)
	writeFixture(t, root, "helper/package.json", `{"name":"pnp-tracer-helper","version":"1.0.0","main":"index.js"}`)
	writeFixture(t, root, "helper/index.js", "module.exports = 'loaded through PnP';\n")
	writeFixture(t, root, "tracer/ci/init.js", "global.ddtestTracer = require('pnp-tracer-helper');\n")
	t.Chdir(root)
	t.Setenv("NODE_OPTIONS", "")
	t.Setenv("NODE_PATH", "")
	runFixtureCommand(t, ctx, "yarn", "install", "--enable-pnp", "--offline", "--ignore-scripts", "--cache-folder", filepath.Join(root, "cache"))
	require.NoDirExists(t, filepath.Join(root, "node_modules"))
	loader := filepath.Join(root, ".pnp.js") // Yarn Classic's PnP loader.
	require.FileExists(t, loader)
	writeFixture(t, root, "noisy-preload.cjs", `process.stdout.write('startup log without newline');
console.error('startup stderr');
process.on('exit', () => {
  process.stdout.write('shutdown log without newline');
  console.error('shutdown stderr');
});
`)
	javascript := platform.NewJavaScript()
	// Prove the fixture cannot pass through ordinary node_modules resolution.
	_, err := javascript.DetectTracer(ctx, platform.TracerOptions{})
	require.ErrorContains(t, err, "Cannot find module 'dd-trace/ci/init'")
	t.Setenv("NODE_OPTIONS", "--require "+strconv.Quote(loader)+" --require "+strconv.Quote(filepath.Join(root, "noisy-preload.cjs"))+" --max-old-space-size=256")
	require.NoError(t, javascript.SanityCheck(ctx))
	path, err := javascript.DetectTracer(ctx, platform.TracerOptions{})
	require.NoError(t, err)
	require.FileExists(t, path)
	// Reusing the project must not fall back to a network installation.
	installation, err := javascript.InstallTestdriveTracer(ctx, platform.TracerOptions{Directory: t.TempDir(), Version: "git:must-not-install"})
	require.NoError(t, err)
	require.True(t, installation.Project)
	require.Equal(t, path, installation.Path)
	// Use the detected result as an actual preload; log-contaminated paths fail.
	runFixtureCommand(t, ctx, "node", "--require", path, "-e", "require('dd-trace/ci/init')")

	// Exercise the actual discovery/run adapters and testdrive worker startup.
	// The tracer's dependency also needs PnP when the tracer path is absolute.
	resetSettingsAfterTest(t)
	writeFixture(t, root, "example.test.js", "// Worker-startup fixture.\n")
	writeFixture(t, root, "worker.cjs", `const assert = require('assert');
const fs = require('fs');
if (process.argv.includes('--showConfig')) {
  assert.strictEqual(global.ddtestTracer, undefined);
  console.log(JSON.stringify({ version: '30.2.0', configs: [{ rootDir: process.cwd(), testRunner: 'jest-circus/runner' }] }));
} else if (process.argv.includes('--listTests')) {
  assert.strictEqual(global.ddtestTracer, undefined);
  assert(process.argv.includes('--json'));
  console.log(JSON.stringify([require('path').resolve('example.test.js')]));
} else if (process.argv.includes('--outputFile')) {
  const instrumented = process.env.DD_CIVISIBILITY_ENABLED === 'true';
  assert.strictEqual(global.ddtestTracer, instrumented ? 'loaded through PnP' : undefined);
  fs.appendFileSync('worker-ran', instrumented ? 'instrumented\n' : 'baseline\n');
  fs.writeFileSync(process.argv[process.argv.indexOf('--outputFile') + 1], JSON.stringify({
    testResults: [{ name: require('path').resolve('example.test.js'), assertionResults: [{ fullName: 'works', status: 'passed' }] }]
  }));
} else {
  assert.strictEqual(global.ddtestTracer, 'loaded through PnP');
  fs.writeFileSync('worker-ran', 'instrumented');
}
`)
	configureFramework(shellCommand("node", filepath.Join(root, "worker.cjs")), "")
	t.Run("discovery and execution", func(t *testing.T) {
		fw, err := javascript.DetectFramework()
		require.NoError(t, err)
		files, err := fw.DiscoverTestFiles(ctx, discovery.TestFileSet{Pattern: "**/*.test.js"})
		require.NoError(t, err)
		require.Len(t, files, 1)
		require.NoError(t, fw.RunTests(ctx, files, nil))
		require.FileExists(t, filepath.Join(root, "worker-ran"))
		require.NoError(t, os.Remove(filepath.Join(root, "worker-ran")))
	})
	t.Run("testdrive", func(t *testing.T) {
		drive, err := testdrive.Prepare("git:must-not-install")
		require.NoError(t, err)
		var output bytes.Buffer
		err = drive.Run(ctx, &output)
		// The fixture exercises both startup modes but emits no telemetry, so
		// successful worker startup must not produce a passing validation verdict.
		require.ErrorContains(t, err, "validation is incomplete", output.String())
		data, err := os.ReadFile(filepath.Join(root, "worker-ran"))
		require.NoError(t, err)
		require.Contains(t, string(data), "baseline\ninstrumented\n")
		data, err = os.ReadFile(filepath.Join(root, ".testoptimization", "testdrive.json"))
		require.NoError(t, err)
		var report struct {
			Success bool
			Runs    []struct {
				Name     string
				ExitCode *int `json:"exit_code"`
			}
		}
		require.NoError(t, json.Unmarshal(data, &report))
		require.False(t, report.Success)
		require.GreaterOrEqual(t, len(report.Runs), 2)
		for i, name := range []string{"baseline", "reporting-only"} {
			require.Equal(t, name, report.Runs[i].Name)
			require.NotNil(t, report.Runs[i].ExitCode)
			require.Zero(t, *report.Runs[i].ExitCode)
		}
	})
}

func TestRubyProjectEnvironment(t *testing.T) {
	requireRuntime(t, "bundle")
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	root := t.TempDir()
	writeFixture(t, root, "tracer/datadog-ci.gemspec", `Gem::Specification.new do |s|
  s.name = "datadog-ci"
  s.version = "1.31.0"
  s.summary = "Regression fixture"
  s.authors = ["DDTest"]
  s.files = []
end
`)
	writeFixture(t, root, "support/project_setup.rb", "puts 'startup log'\nwarn 'startup stderr'\nat_exit { puts 'shutdown log'; warn 'shutdown stderr' }\nPROJECT_TRACER_PATH = '../tracer'\n")
	writeFixture(t, root, "config/Gemfile.test", "gem 'datadog-ci', path: PROJECT_TRACER_PATH\n")
	t.Chdir(root)
	t.Setenv("BUNDLE_GEMFILE", filepath.Join(root, "config", "Gemfile.test"))
	t.Setenv("BUNDLE_USER_HOME", filepath.Join(root, "bundle-home"))
	t.Setenv("RUBYOPT", "-I./support -rproject_setup")
	runFixtureCommand(t, ctx, "bundle", "lock", "--local")
	ruby := platform.NewRuby(settings.TestSkippingLevelTest)
	t.Setenv("RUBYOPT", "")
	_, err := ruby.DetectTracer(ctx, platform.TracerOptions{})
	require.ErrorContains(t, err, "PROJECT_TRACER_PATH")
	t.Setenv("RUBYOPT", "-I./support -rproject_setup")
	require.NoError(t, ruby.SanityCheck(ctx))
	version, err := ruby.DetectTracer(ctx, platform.TracerOptions{})
	require.NoError(t, err)
	require.Equal(t, "  * datadog-ci (1.31.0)", version)
	installation, err := ruby.InstallTestdriveTracer(ctx, platform.TracerOptions{Directory: t.TempDir(), Version: "git:must-not-install"})
	require.NoError(t, err)
	require.True(t, installation.Project)
}

func TestPythonProjectEnvironment(t *testing.T) {
	requireRuntime(t, "python")
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	root := t.TempDir()
	// A private venv prevents a globally installed ddtrace from hiding a failure
	// to preserve PYTHONPATH. Metadata alone suffices for the version probe.
	runFixtureCommand(t, ctx, "python", "-m", "venv", "--without-pip", filepath.Join(root, "venv"))
	writeFixture(t, root, "packages/ddtrace-4.11.0.dist-info/METADATA", "Metadata-Version: 2.1\nName: ddtrace\nVersion: 4.11.0\n")
	writeFixture(t, root, "packages/sitecustomize.py", `import atexit, sys
print('startup log', end='')
print('startup stderr', file=sys.stderr)
atexit.register(lambda: print('shutdown log', end=''))
atexit.register(lambda: print('shutdown stderr', file=sys.stderr))
`)
	t.Chdir(root)
	bin := "bin"
	if runtime.GOOS == "windows" {
		bin = "Scripts"
	}
	t.Setenv("PATH", filepath.Join(root, "venv", bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PYTHONPATH", "")
	python := platform.NewPython()
	_, err := python.DetectTracer(ctx, platform.TracerOptions{Command: "python"})
	require.ErrorContains(t, err, "PackageNotFoundError")
	t.Setenv("PYTHONPATH", filepath.Join(root, "packages"))
	require.NoError(t, python.SanityCheck(ctx))
	version, err := python.DetectTracer(ctx, platform.TracerOptions{Command: "python"})
	require.NoError(t, err)
	require.Equal(t, "4.11.0", version)
}
