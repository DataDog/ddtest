package framework

import (
	"os/exec"

	"github.com/DataDog/datadog-test-runner/internal/testoptimization"
)

type Framework interface {
	Name() string
	DiscoverTests() ([]testoptimization.Test, error)
	CreateDiscoveryCommand() *exec.Cmd
}
