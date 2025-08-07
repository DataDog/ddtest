package framework

import (
	"os/exec"

	"github.com/DataDog/datadog-test-runner/internal/testoptimization"
)

const TestsDiscoveryFilePath = "./.dd/tests-discovery/tests.json"

type Framework interface {
	Name() string
	DiscoverTests() ([]testoptimization.Test, error)
	CreateDiscoveryCommand() *exec.Cmd
}
