package platform

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/viper"
)

func TestNewPlatformDetector(t *testing.T) {
	if _, ok := NewPlatformDetector().(*DatadogPlatformDetector); !ok {
		t.Fatal("expected NewPlatformDetector to return DatadogPlatformDetector")
	}
}

func TestPlatformsDetectTheirProjectFiles(t *testing.T) {
	tests := []struct {
		name     string
		platform Platform
		filename string
	}{
		{name: "ruby", platform: NewRuby(settings.TestSkippingLevelSuite), filename: "Gemfile"},
		{name: "javascript", platform: NewJavaScript(), filename: "package.json"},
		{name: "python pyproject", platform: NewPython(), filename: "pyproject.toml"},
		{name: "python setup", platform: NewPython(), filename: "setup.py"},
		{name: "python requirements", platform: NewPython(), filename: "requirements.txt"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repositoryRoot := t.TempDir()
			detected, err := test.platform.Detect(repositoryRoot)
			if err != nil {
				t.Fatalf("Detect() unexpected error: %v", err)
			}
			if detected {
				t.Fatal("Detect() = true for an empty repository")
			}

			contents := "{}"
			if test.filename == "pyproject.toml" {
				contents = "[project]\nname = \"example\"\n"
			}
			if err := os.WriteFile(filepath.Join(repositoryRoot, test.filename), []byte(contents), 0644); err != nil {
				t.Fatal(err)
			}
			detected, err = test.platform.Detect(repositoryRoot)
			if err != nil {
				t.Fatalf("Detect() unexpected error: %v", err)
			}
			if !detected {
				t.Fatal("Detect() = false for a matching repository")
			}
		})
	}
}

func TestPlatformSanityChecksPropagateContext(t *testing.T) {
	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "sanity-check")

	tests := []struct {
		name   string
		output []byte
	}{
		{name: "ruby", output: []byte("  * datadog-ci (1.31.0)\n")},
		{name: "python", output: []byte("4.11.0\n")},
		{name: "javascript", output: []byte("v24.0.0\n")},
	}

	for i := range tests {
		t.Run(tests[i].name, func(t *testing.T) {
			executor := &mockCommandExecutor{combinedOutput: tests[i].output}
			var check func(context.Context) error
			switch tests[i].name {
			case "ruby":
				platform := NewRuby(settings.TestSkippingLevelTest)
				platform.executor = executor
				check = platform.SanityCheck
			case "python":
				platform := NewPython()
				platform.executor = executor
				check = platform.SanityCheck
			case "javascript":
				platform := NewJavaScript()
				platform.executor = executor
				check = platform.SanityCheck
			}

			if err := check(ctx); err != nil {
				t.Fatalf("SanityCheck() failed: %v", err)
			}
			if len(executor.combinedOutputCtx) == 0 {
				t.Fatal("SanityCheck() did not execute a command")
			}
			for _, got := range executor.combinedOutputCtx {
				if got != ctx {
					t.Fatal("SanityCheck() did not propagate its context")
				}
			}
		})
	}
}

func TestDetectPlatformUnsupported(t *testing.T) {
	viper.Reset()
	t.Cleanup(func() {
		viper.Reset()
		settings.Init()
	})
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_PLATFORM", "node")
	settings.Init()

	_, err := DetectPlatform("", "")
	if err == nil || !strings.Contains(err.Error(), "unsupported platform: node") {
		t.Fatalf("DetectPlatform() error = %v, want unsupported platform", err)
	}

	detector := &DatadogPlatformDetector{}
	_, err = detector.DetectPlatform("", "")
	if err == nil || !strings.Contains(err.Error(), "unsupported platform: node") {
		t.Fatalf("DatadogPlatformDetector.DetectPlatform() error = %v, want unsupported platform", err)
	}
}
