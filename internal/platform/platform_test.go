package platform

import (
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/viper"
)

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
