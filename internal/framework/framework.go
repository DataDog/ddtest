package framework

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/testoptimization"
)

var TestsDiscoveryFilePath = filepath.Join(".", constants.PlanDirectory, "tests-discovery/tests.json")

type Framework interface {
	Name() string
	DiscoverTests(ctx context.Context) ([]testoptimization.Test, error)
	DiscoverTestFiles() ([]string, error)
	RunTests(ctx context.Context, testFiles []string, envMap map[string]string) error
	SetPlatformEnv(platformEnv map[string]string)
	GetPlatformEnv() map[string]string
}

// cleanupDiscoveryFile removes the discovery file, ignoring "not exists" errors
func cleanupDiscoveryFile(filePath string) {
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		slog.Warn("Warning: Failed to delete existing discovery file", "filePath", filePath, "error", err)
	}
}

// executeDiscoveryCommand runs the discovery command and logs timing
func executeDiscoveryCommand(ctx context.Context, executor ext.CommandExecutor, name string, args []string, envMap map[string]string, frameworkName string) ([]byte, error) {
	slog.Debug("Starting test discovery...", "framework", frameworkName)
	startTime := time.Now()

	output, err := executor.CombinedOutput(ctx, name, args, envMap)
	if err != nil {
		slog.Warn("Failed to run test discovery", "framework", frameworkName, "output", string(output), "error", err)
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

func defaultTestPattern(rootDir, filePattern string) string {
	return filepath.Join(rootDir, "**", filePattern)
}

func globTestFiles(pattern string) ([]string, error) {
	matches, err := doublestar.FilepathGlob(pattern, doublestar.WithFilesOnly())
	if err != nil {
		return nil, err
	}

	return matches, nil
}

// BaseDiscoveryEnv returns environment variables required for all test discovery processes.
// These env vars ensure the test framework runs in discovery mode without requiring
// actual Datadog credentials or agent connectivity.
func BaseDiscoveryEnv() map[string]string {
	return map[string]string{
		"DD_CIVISIBILITY_ENABLED":                "1",
		"DD_CIVISIBILITY_AGENTLESS_ENABLED":      "true",
		"DD_API_KEY":                             "dummy_key",
		"DD_TEST_OPTIMIZATION_DISCOVERY_ENABLED": "1",
		"DD_TEST_OPTIMIZATION_DISCOVERY_FILE":    TestsDiscoveryFilePath,
	}
}
