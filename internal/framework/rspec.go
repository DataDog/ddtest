package framework

import (
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"time"

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

func (r *RSpec) DiscoverTests() ([]testoptimization.Test, error) {
	if err := os.Remove(TestsDiscoveryFilePath); err != nil && !os.IsNotExist(err) {
		slog.Warn("Warning: Failed to delete existing discovery file", "filePath", TestsDiscoveryFilePath, "error", err)
	}

	slog.Debug("Starting RSpec dry run...")
	startTime := time.Now()

	cmd := r.createDiscoveryCommand()
	output, err := r.executor.CombinedOutput(cmd)
	if err != nil {
		slog.Error("Failed to run RSpec dry run", "output", string(output))
		return nil, err
	}

	duration := time.Since(startTime)
	slog.Debug("Finished RSpec dry run!", "duration", duration)

	file, err := os.Open(TestsDiscoveryFilePath)
	if err != nil {
		slog.Error("Error opening JSON file", "error", err)
		return nil, err
	}
	defer func() {
		_ = file.Close()
	}()

	var tests []testoptimization.Test
	decoder := json.NewDecoder(file)
	for decoder.More() {
		var test testoptimization.Test
		if err := decoder.Decode(&test); err != nil {
			slog.Error("Error parsing JSON", "error", err)
			return nil, err
		}
		tests = append(tests, test)
	}

	slog.Debug("Parsed RSpec report", "examples", len(tests))
	return tests, nil
}

func (r *RSpec) RunTests(testFiles []string, envMap map[string]string) error {
	command, baseArgs := r.getRSpecCommand()
	args := append(baseArgs, "--format", "progress")
	args = append(args, testFiles...)

	// no-dd-sa:go-security/command-injection
	cmd := exec.Command(command, args...)

	// Set environment variables from envMap
	if len(envMap) > 0 {
		cmd.Env = os.Environ()
		for key, value := range envMap {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}

	return r.executor.Run(cmd)
}
