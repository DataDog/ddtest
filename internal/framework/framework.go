package framework

import (
	"path/filepath"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/testoptimization"
)

var TestsDiscoveryFilePath = filepath.Join(".", constants.PlanDirectory, "tests-discovery/tests.json")

type Framework interface {
	Name() string
	DiscoverTests() ([]testoptimization.Test, error)
	RunTests(testFiles []string, envMap map[string]string) error
}
