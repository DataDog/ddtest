package framework

import (
	"log/slog"
	"os"
	"os/exec"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/testoptimization"
)

const binRSpecPath = "bin/rspec"

type RSpec struct {
	executor ext.CommandExecutor
}

func NewRSpec() *RSpec {
	return &RSpec{
		executor: &ext.DefaultCommandExecutor{},
	}
}

func (r *RSpec) Name() string {
	return "rspec"
}

func (r *RSpec) DiscoverTests() ([]testoptimization.Test, error) {
	cleanupDiscoveryFile(TestsDiscoveryFilePath)

	cmd := r.createDiscoveryCommand()
	_, err := executeDiscoveryCommand(r.executor, cmd, r.Name())
	if err != nil {
		return nil, err
	}

	tests, err := parseDiscoveryFile(TestsDiscoveryFilePath)
	if err != nil {
		return nil, err
	}

	slog.Debug("Parsed RSpec report", "tests", len(tests))
	return tests, nil
}

func (r *RSpec) RunTests(testFiles []string, envMap map[string]string) error {
	command, baseArgs := r.getRSpecCommand()
	args := append(baseArgs, "--format", "progress")
	args = append(args, testFiles...)

	// no-dd-sa:go-security/command-injection
	cmd := exec.Command(command, args...)

	applyEnvMap(cmd, envMap)

	return r.executor.Run(cmd)
}

// getRSpecCommand determines whether to use bin/rspec or bundle exec rspec
func (r *RSpec) getRSpecCommand() (string, []string) {
	// Check if bin/rspec exists and is executable
	if info, err := os.Stat(binRSpecPath); err == nil && !info.IsDir() {
		// Check if file is executable
		if info.Mode()&0111 != 0 {
			slog.Debug("Using bin/rspec for RSpec commands")
			return binRSpecPath, []string{}
		}
	}

	slog.Debug("Using bundle exec rspec for RSpec commands")
	return "bundle", []string{"exec", "rspec"}
}

func (r *RSpec) createDiscoveryCommand() *exec.Cmd {
	command, baseArgs := r.getRSpecCommand()
	args := append(baseArgs, "--format", "progress", "--dry-run")

	// no-dd-sa:go-security/command-injection
	cmd := exec.Command(command, args...)
	cmd.Env = append(
		os.Environ(),
		"DD_TEST_OPTIMIZATION_DISCOVERY_ENABLED=1",
		"DD_TEST_OPTIMIZATION_DISCOVERY_FILE="+TestsDiscoveryFilePath,
	)
	return cmd
}
