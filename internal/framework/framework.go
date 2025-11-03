package framework

import (
	"context"
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
	DiscoverTests(ctx context.Context) ([]testoptimization.Test, error)
	DiscoverTestFiles() ([]string, error)
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
func executeDiscoveryCommand(ctx context.Context, executor ext.CommandExecutor, cmd *exec.Cmd, frameworkName string) ([]byte, error) {
	slog.Debug("Starting test discovery...", "framework", frameworkName)
	startTime := time.Now()

	output, err := executor.CombinedOutput(ctx, cmd)
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

// discoverTestFilesByPattern searches for test files matching a pattern in a given directory
func discoverTestFilesByPattern(rootDir string, pattern string) ([]string, error) {
	var testFiles []string

	err := filepath.WalkDir(rootDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if d.IsDir() {
			return nil
		}

		// Check if the file matches the pattern
		matched, err := filepath.Match(pattern, filepath.Base(path))
		if err != nil {
			return err
		}

		if matched {
			testFiles = append(testFiles, path)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return testFiles, nil
}
