package framework

import (
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"time"

	"github.com/DataDog/datadog-test-runner/internal/ext"
	"github.com/DataDog/datadog-test-runner/internal/testoptimization"
)

var CommandEntrypoint = "bundle"
var DiscoveryCommand = []string{"exec", "rspec", "--format", "progress", "--dry-run"}
var TestRunCommand = []string{"exec", "rspec", "--format", "progress"}

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

func (r *RSpec) createDiscoveryCommand() *exec.Cmd {
	cmd := exec.Command(CommandEntrypoint, DiscoveryCommand...)
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

func (r *RSpec) RunTests(testFiles []string) error {
	args := append(TestRunCommand, testFiles...)
	cmd := exec.Command(CommandEntrypoint, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
