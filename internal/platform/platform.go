package platform

import (
	"fmt"

	"github.com/DataDog/datadog-test-runner/internal/framework"
	"github.com/DataDog/datadog-test-runner/internal/settings"
)

type Platform interface {
	Name() string
	CreateTagsMap() map[string]string
	SupportedFrameworks() []string
	DetectFramework() (framework.Framework, error)
}

func DetectPlatform() (Platform, error) {
	platformName := settings.GetPlatform()

	switch platformName {
	case "ruby":
		return &Ruby{}, nil
	default:
		return nil, fmt.Errorf("unsupported platform: %s", platformName)
	}
}
