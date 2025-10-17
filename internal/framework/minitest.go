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
	if err := os.Remove(TestsDiscoveryFilePath); err != nil && !os.IsNotExist(err) {
		slog.Warn("Warning: Failed to delete existing discovery file", "filePath", TestsDiscoveryFilePath, "error", err)
	}

	slog.Debug("Starting Minitest test discovery...")
	startTime := time.Now()

	cmd := m.createDiscoveryCommand()
	output, err := m.executor.CombinedOutput(cmd)
	if err != nil {
		slog.Error("Failed to run Minitest test discovery", "output", string(output))
		return nil, err
	}

	duration := time.Since(startTime)
	slog.Debug("Finished Minitest test discovery", "duration", duration)

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

	// Set environment variables from envMap
	if len(envMap) > 0 {
		cmd.Env = os.Environ()
		for key, value := range envMap {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}

	return m.executor.Run(cmd)
}
