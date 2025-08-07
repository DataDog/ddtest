package platform

import (
	"github.com/DataDog/datadog-test-runner/civisibility/constants"
	"github.com/DataDog/datadog-test-runner/internal/framework"
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

func (r *Ruby) SupportedFrameworks() []string {
	return []string{"rspec"}
}

func (r *Ruby) DetectFramework() (framework.Framework, error) {
	return &framework.RSpec{}, nil
}
