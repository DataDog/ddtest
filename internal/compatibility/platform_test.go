package compatibility

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/stretchr/testify/require"
)

func TestRubyPlatformIntegration(t *testing.T) {
	datadogVersion := requireEnv(t, "DDTEST_RUBY_PLATFORM_VERSION")
	root := t.TempDir()
	gemfile := fmt.Sprintf("source \"https://rubygems.org\"\ngem \"datadog-ci\", %q\n", datadogVersion)
	if err := os.WriteFile(filepath.Join(root, "Gemfile"), []byte(gemfile), 0644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)

	ruby := platform.NewRuby(settings.TestSkippingLevelTest)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := ruby.SanityCheck(ctx); err != nil {
		t.Fatal(err)
	}
	tags, err := ruby.CreateTagsMap(ctx)
	if err != nil {
		t.Fatal(err)
	}
	requireRuntimeTags(t, tags, "ruby")

	// Compare the independent probe with the library's actual expressions in
	// every supported datadog-ci matrix entry, so changes in the library fail CI.
	expectedFile := filepath.Join(root, "library-tags.json")
	runFixtureCommand(t, ctx, "bundle", "exec", "ruby", "-e", `
require "json"
require "datadog/ci/ext/test"
require "datadog/core/environment/platform"
File.write(ARGV[0], {
  Datadog::CI::Ext::Test::TAG_OS_PLATFORM => RbConfig::CONFIG["host_os"],
  Datadog::CI::Ext::Test::TAG_OS_ARCHITECTURE => RbConfig::CONFIG["host_cpu"],
  Datadog::CI::Ext::Test::TAG_OS_VERSION => Datadog::Core::Environment::Platform.kernel_release,
  Datadog::CI::Ext::Test::TAG_RUNTIME_NAME => Datadog::Core::Environment::Ext::LANG_ENGINE,
  Datadog::CI::Ext::Test::TAG_RUNTIME_VERSION => Datadog::Core::Environment::Ext::ENGINE_VERSION
}.to_json)
`, expectedFile)
	data, err := os.ReadFile(expectedFile)
	require.NoError(t, err)
	var expected map[string]string
	require.NoError(t, json.Unmarshal(data, &expected))
	expected["language"] = "ruby"
	require.Equal(t, expected, tags)
}

func TestPythonPlatformIntegration(t *testing.T) {
	requireEnv(t, "DDTEST_PYTHON_PLATFORM_INTEGRATION")
	t.Chdir(t.TempDir())

	python := platform.NewPython()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := python.SanityCheck(ctx); err != nil {
		t.Fatal(err)
	}
	tags, err := python.CreateTagsMap(ctx)
	if err != nil {
		t.Fatal(err)
	}
	requireRuntimeTags(t, tags, "python")
}

func TestJavaScriptPlatformIntegration(t *testing.T) {
	nodeModules := requireEnv(t, "DDTEST_DD_TRACE_NODE_MODULES")
	root := t.TempDir()
	if err := os.Symlink(nodeModules, filepath.Join(root, "node_modules")); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)

	javascript := platform.NewJavaScript()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := javascript.SanityCheck(ctx); err != nil {
		t.Fatal(err)
	}
	tags, err := javascript.CreateTagsMap(ctx)
	if err != nil {
		t.Fatal(err)
	}
	requireRuntimeTags(t, tags, "javascript")
}

func TestJavaScriptActionPreloadIntegration(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprintf("explicit_NODE_OPTIONS=%t", explicit), func(t *testing.T) {
			testJavaScriptActionPreloadIntegration(t, explicit)
		})
	}
}

func testJavaScriptActionPreloadIntegration(t *testing.T, explicit bool) {
	t.Helper()
	nodeModules := requireEnv(t, "DDTEST_DD_TRACE_NODE_MODULES")
	preload := filepath.Join(nodeModules, "dd-trace", "ci", "init.js")
	if !filepath.IsAbs(preload) {
		t.Fatalf("action-style preload must be absolute: %q", preload)
	}
	preloadInfo, err := os.Stat(preload)
	if err != nil {
		t.Fatalf("external dd-trace preload is unavailable: %v", err)
	}

	root := t.TempDir()
	writeFixture(t, root, "package.json", `{"name":"external-tracer-fixture","private":true}`)
	t.Chdir(root)
	t.Setenv("DD_TRACE_PACKAGE", preload)
	t.Setenv("NODE_OPTIONS", "")
	if explicit {
		t.Setenv("NODE_OPTIONS", "-r "+preload)
	}

	// The action installs dd-trace outside the project, so the only usable
	// tracer is the action's exported path or an explicit customer preload.
	javascript := platform.NewJavaScript()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	if err := javascript.SanityCheck(ctx); err != nil {
		t.Fatalf("JavaScript sanity check rejected the action preload: %v", err)
	}
	resolved, err := javascript.DetectTracer(ctx, platform.TracerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	resolvedInfo, err := os.Stat(resolved)
	if err != nil {
		t.Fatalf("resolved tracer is unavailable: %v", err)
	}
	if !os.SameFile(preloadInfo, resolvedInfo) {
		t.Fatalf("resolved tracer = %q, want action preload %q", resolved, preload)
	}
}

func requireRuntimeTags(t *testing.T, tags map[string]string, language string) {
	t.Helper()
	want := map[string]string{
		"language":        language,
		"runtime.name":    "",
		"runtime.version": "",
		"os.platform":     "",
		"os.architecture": "",
		"os.version":      "",
	}
	for key, exact := range want {
		value, ok := tags[key]
		if !ok || value == "" {
			t.Fatalf("runtime tags missing %q: %v", key, tags)
		}
		if exact != "" && value != exact {
			t.Fatalf("runtime tag %q = %q, want %q", key, value, exact)
		}
	}
}
