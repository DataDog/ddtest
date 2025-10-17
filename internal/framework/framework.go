package framework

import (
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/testoptimization"
)

var TestsDiscoveryFilePath = filepath.Join(".", constants.PlanDirectory, "tests-discovery/tests.json")

type Framework interface {
	Name() string
	DiscoverTests() ([]testoptimization.Test, error)
	RunTests(testFiles []string, envMap map[string]string) error
}

// cleanupDiscoveryFile removes the discovery file, ignoring "not exists" errors
func cleanupDiscoveryFile(filePath string) {
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		slog.Warn("Warning: Failed to delete existing discovery file", "filePath", filePath, "error", err)
	}
}

// applyEnvMap sets environment variables from envMap onto the command
func applyEnvMap(cmd *exec.Cmd, envMap map[string]string) {
	if len(envMap) > 0 {
		cmd.Env = os.Environ()
		for key, value := range envMap {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
}

// executeDiscoveryCommand runs the discovery command and logs timing
func executeDiscoveryCommand(executor ext.CommandExecutor, cmd *exec.Cmd, frameworkName string) ([]byte, error) {
	slog.Debug("Starting test discovery...", "framework", frameworkName)
	startTime := time.Now()

	output, err := executor.CombinedOutput(cmd)
	if err != nil {
		slog.Error("Failed to run test discovery", "framework", frameworkName, "output", string(output))
		return nil, err
	}

	duration := time.Since(startTime)
	slog.Debug("Finished test discovery", "framework", frameworkName, "duration", duration)

	return output, nil
}

// parseDiscoveryFile reads and parses the test discovery JSON file
func parseDiscoveryFile(filePath string) ([]testoptimization.Test, error) {
	file, err := os.Open(filePath)
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

	return tests, nil
}
