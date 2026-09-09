package compatibility

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/settings"
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
