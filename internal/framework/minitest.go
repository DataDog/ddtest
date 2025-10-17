package framework

import (
	"log/slog"
	"os"
	"os/exec"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/testoptimization"
)

type Minitest struct {
	executor ext.CommandExecutor
}

func NewMinitest() *Minitest {
	return &Minitest{
		executor: &ext.DefaultCommandExecutor{},
	}
}

func (m *Minitest) Name() string {
	return "minitest"
}

func (m *Minitest) createDiscoveryCommand() *exec.Cmd {
	// no-dd-sa:go-security/command-injection
	cmd := exec.Command("bundle", "exec", "rake", "test")
	cmd.Env = append(
		os.Environ(),
		"DD_TEST_OPTIMIZATION_DISCOVERY_ENABLED=1",
		"DD_TEST_OPTIMIZATION_DISCOVERY_FILE="+TestsDiscoveryFilePath,
	)
	return cmd
}

func (m *Minitest) DiscoverTests() ([]testoptimization.Test, error) {
	cleanupDiscoveryFile(TestsDiscoveryFilePath)

	cmd := m.createDiscoveryCommand()
	_, err := executeDiscoveryCommand(m.executor, cmd, m.Name())
	if err != nil {
		return nil, err
	}

	tests, err := parseDiscoveryFile(TestsDiscoveryFilePath)
	if err != nil {
		return nil, err
	}

	slog.Debug("Parsed Minitest report", "examples", len(tests))
	return tests, nil
}

func (m *Minitest) RunTests(testFiles []string, envMap map[string]string) error {
	args := []string{"exec", "rake", "test"}

	// Add test files as command-line arguments if provided
	if len(testFiles) > 0 {
		args = append(args, testFiles...)
	}

	// no-dd-sa:go-security/command-injection
	cmd := exec.Command("bundle", args...)

	applyEnvMap(cmd, envMap)

	return m.executor.Run(cmd)
}
