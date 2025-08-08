package platform

import (
	"fmt"

	"github.com/DataDog/datadog-test-runner/internal/framework"
	"github.com/DataDog/datadog-test-runner/internal/settings"
)

type Platform interface {
	Name() string
	CreateTagsMap() map[string]string
	DetectFramework() (framework.Framework, error)
}

func DetectPlatform() (Platform, error) {
	platformName := settings.GetPlatform()

	var platform Platform
	switch platformName {
	case "ruby":
		platform = &Ruby{}
	default:
		return nil, fmt.Errorf("unsupported platform: %s", platformName)
	}

	return platform, nil
}
