package framework

import (
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"time"

	"github.com/DataDog/datadog-test-runner/internal/testoptimization"
)

type RSpec struct{}

func (r *RSpec) Name() string {
	return "rspec"
}

func (r *RSpec) CreateDiscoveryCommand() *exec.Cmd {
	cmd := exec.Command("bundle", "exec", "rspec", "--format", "progress", "--dry-run")
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

	cmd := r.CreateDiscoveryCommand()
	output, err := cmd.CombinedOutput()
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
	defer file.Close()

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
