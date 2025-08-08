package platform

import (
	"fmt"

	"github.com/DataDog/datadog-test-runner/civisibility/constants"
	"github.com/DataDog/datadog-test-runner/internal/framework"
	"github.com/DataDog/datadog-test-runner/internal/settings"
)

type Ruby struct{}

func (r *Ruby) Name() string {
	return "ruby"
}

func (r *Ruby) CreateTagsMap() map[string]string {
	tags := make(map[string]string)
	tags[constants.RuntimeName] = "ruby"
	tags[constants.RuntimeVersion] = "3.3.0"
	tags[constants.OSPlatform] = "darwin23"
	tags[constants.OSVersion] = "24.5.0"
	tags["language"] = r.Name()
	return tags
}

func (r *Ruby) DetectFramework() (framework.Framework, error) {
	frameworkName := settings.GetFramework()

	switch frameworkName {
	case "rspec":
		return &framework.RSpec{}, nil
	default:
		return nil, fmt.Errorf("framework '%s' is not supported by platform 'ruby'", frameworkName)
	}
}
