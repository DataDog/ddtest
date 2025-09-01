package framework

import (
	"github.com/DataDog/datadog-test-runner/internal/testoptimization"
)

const TestsDiscoveryFilePath = "./.dd/tests-discovery/tests.json"

type Framework interface {
	Name() string
	DiscoverTests() ([]testoptimization.Test, error)
	RunTests(testFiles []string, envMap map[string]string) error
}
