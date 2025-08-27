package ciprovider

import (
	"fmt"

	"github.com/DataDog/datadog-test-runner/civisibility/constants"
	"github.com/DataDog/datadog-test-runner/civisibility/utils"
)

type CIProvider interface {
	Name() string
	Configure() error
}

type CIProviderDetector interface {
	DetectCIProvider() (CIProvider, error)
}

type DatadogCIProviderDetector struct{}

func (d *DatadogCIProviderDetector) DetectCIProvider() (CIProvider, error) {
	return DetectCIProvider()
}

func DetectCIProvider() (CIProvider, error) {
	envTags := utils.GetCITags()
	providerName, ok := envTags[constants.CIProviderName]
	if !ok {
		return nil, fmt.Errorf("no CI provider detected")
	}

	var provider CIProvider
	switch providerName {
	case "github":
		provider = NewGitHub()
	default:
		return nil, fmt.Errorf("unsupported CI provider: %s", providerName)
	}

	return provider, nil
}

func NewCIProviderDetector() CIProviderDetector {
	return &DatadogCIProviderDetector{}
}
