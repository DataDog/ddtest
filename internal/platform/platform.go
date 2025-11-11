package platform

import (
	"fmt"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/settings"
)

type Platform interface {
	Name() string
	CreateTagsMap() (map[string]string, error)
	DetectFramework() (framework.Framework, error)
	SanityCheck() error
}

// PlatformDetector defines interface for detecting platforms - needed to allow mocking in tests
type PlatformDetector interface {
	DetectPlatform() (Platform, error)
}

type DatadogPlatformDetector struct{}

func (d *DatadogPlatformDetector) DetectPlatform() (Platform, error) {
	return DetectPlatform()
}

func DetectPlatform() (Platform, error) {
	platformName := settings.GetPlatform()

	var platform Platform
	switch platformName {
	case "ruby":
		platform = NewRuby()
	default:
		return nil, fmt.Errorf("unsupported platform: %s", platformName)
	}

	if err := platform.SanityCheck(); err != nil {
		return nil, fmt.Errorf("sanity check failed for platform %s: %w", platform.Name(), err)
	}

	return platform, nil
}

func NewPlatformDetector() PlatformDetector {
	return &DatadogPlatformDetector{}
}
