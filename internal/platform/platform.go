package platform

import (
	"github.com/DataDog/datadog-test-runner/internal/framework"
)

type Platform interface {
	Name() string
	CreateTagsMap() map[string]string
	SupportedFrameworks() []string
	DetectFramework() (framework.Framework, error)
}

func DetectPlatform() (Platform, error) {
	return &Ruby{}, nil
}
