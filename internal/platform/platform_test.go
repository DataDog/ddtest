package platform

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
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

func TestPlatformSanityChecksPropagateContext(t *testing.T) {
	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "sanity-check")

	tests := []struct {
		name   string
		output []byte
	}{
		{name: "ruby", output: []byte("  * datadog-ci (1.31.0)\n")},
		{name: "python", output: []byte("4.10.3\n")},
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

func TestDetectPlatformPythonWithFakeInterpreter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell script as the fake python executable")
	}

	viper.Reset()
	t.Cleanup(func() {
		viper.Reset()
		settings.Init()
	})

	binDir := t.TempDir()
	pythonPath := filepath.Join(binDir, "python")
	if err := os.WriteFile(pythonPath, []byte("#!/bin/sh\nprintf '4.10.3\\n'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	viper.Set("platform", "python")
	settings.Init()

	detectedPlatform, err := DetectPlatform(context.Background())
	if err != nil {
		t.Fatalf("DetectPlatform() unexpected error: %v", err)
	}
	if detectedPlatform == nil {
		t.Fatal("expected platform to be detected")
	}
	if detectedPlatform.Name() != "python" {
		t.Fatalf("expected python platform, got %q", detectedPlatform.Name())
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

	_, err := DetectPlatform(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unsupported platform: node") {
		t.Fatalf("DetectPlatform() error = %v, want unsupported platform", err)
	}

	detector := &DatadogPlatformDetector{}
	_, err = detector.DetectPlatform(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unsupported platform: node") {
		t.Fatalf("DatadogPlatformDetector.DetectPlatform() error = %v, want unsupported platform", err)
	}
}
