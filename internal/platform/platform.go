package platform

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/settings"
)

type Platform interface {
	Name() string
	Detect(repositoryRoot string) (bool, error)
	CreateTagsMap(ctx context.Context) (map[string]string, error)
	DetectFramework() (framework.Framework, error)
	SanityCheck(ctx context.Context) error
	TestSkippingLevel() settings.TestSkippingLevel
}

func detectAnyFile(repositoryRoot string, filenames ...string) (bool, error) {
	for _, filename := range filenames {
		path := filepath.Join(repositoryRoot, filename)
		info, err := os.Stat(path)
		if err == nil {
			if !info.IsDir() {
				return true, nil
			}
			continue
		}
		if !os.IsNotExist(err) {
			return false, fmt.Errorf("inspect %s: %w", path, err)
		}
	}
	return false, nil
}

// PlatformDetector defines interface for detecting platforms - needed to allow mocking in tests
type PlatformDetector interface {
	DetectPlatform(ctx context.Context) (Platform, error)
}

type DatadogPlatformDetector struct{}

func runtimeTagProbeError(message string, output []byte, err error) error {
	if diagnostic := strings.TrimSpace(string(output)); diagnostic != "" {
		return fmt.Errorf("%s: %s: %w", message, diagnostic, err)
	}
	return fmt.Errorf("%s: %w", message, err)
}

func (d *DatadogPlatformDetector) DetectPlatform(ctx context.Context) (Platform, error) {
	return DetectPlatform(ctx)
}

func DetectPlatform(ctx context.Context) (Platform, error) {
	platformName := settings.GetPlatform()

	var platform Platform
	switch platformName {
	case "ruby":
		platform = NewRuby(settings.GetTestSkippingLevel())
	case "javascript":
		platform = NewJavaScript()
	case "python":
		platform = NewPython()
	default:
		return nil, fmt.Errorf("unsupported platform: %s", platformName)
	}

	if err := platform.SanityCheck(ctx); err != nil {
		return nil, fmt.Errorf("sanity check failed for platform %s: %w", platform.Name(), err)
	}

	return platform, nil
}

func NewPlatformDetector() PlatformDetector {
	return &DatadogPlatformDetector{}
}
