package platform

import (
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

	detectedPlatform, err := DetectPlatform()
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

	_, err := DetectPlatform()
	if err == nil || !strings.Contains(err.Error(), "unsupported platform: node") {
		t.Fatalf("DetectPlatform() error = %v, want unsupported platform", err)
	}

	detector := &DatadogPlatformDetector{}
	_, err = detector.DetectPlatform()
	if err == nil || !strings.Contains(err.Error(), "unsupported platform: node") {
		t.Fatalf("DatadogPlatformDetector.DetectPlatform() error = %v, want unsupported platform", err)
	}
}
