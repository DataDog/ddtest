package compatibility

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/settings"
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
	writeFixture(t, root, "package.json", `{"name":"pnp-regression","private":true,"dependencies":{"dd-trace":"file:./tracer"}}`)
	writeFixture(t, root, "tracer/package.json", `{"name":"dd-trace","version":"1.0.0"}`)
	writeFixture(t, root, "tracer/ci/init.js", "module.exports = {};\n")
	t.Chdir(root)
	t.Setenv("NODE_OPTIONS", "")
	t.Setenv("NODE_PATH", "")
	runFixtureCommand(t, ctx, "yarn", "install", "--enable-pnp", "--offline", "--ignore-scripts", "--cache-folder", filepath.Join(root, "cache"))
	require.NoDirExists(t, filepath.Join(root, "node_modules"))
	loader := filepath.Join(root, ".pnp.js") // Yarn Classic's PnP loader.
	require.FileExists(t, loader)
	javascript := platform.NewJavaScript()
	// Prove the fixture cannot pass through ordinary node_modules resolution.
	_, err := javascript.DetectTracer(ctx, platform.TracerOptions{})
	require.ErrorContains(t, err, "Cannot find module 'dd-trace/ci/init'")
	t.Setenv("NODE_OPTIONS", "--require "+strconv.Quote(loader)+" --max-old-space-size=256")
	require.NoError(t, javascript.SanityCheck(ctx))
	path, err := javascript.DetectTracer(ctx, platform.TracerOptions{})
	require.NoError(t, err)
	require.FileExists(t, path)
	// Reusing the project must not fall back to a network installation.
	installation, err := javascript.InstallTestdriveTracer(ctx, platform.TracerOptions{Directory: t.TempDir(), Version: "git:must-not-install"})
	require.NoError(t, err)
	require.True(t, installation.Project)
	require.Equal(t, path, installation.Path)
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
	writeFixture(t, root, "support/project_setup.rb", "PROJECT_TRACER_PATH = '../tracer'\n")
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
