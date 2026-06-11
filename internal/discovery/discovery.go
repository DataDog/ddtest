package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/testoptimization"
	"github.com/DataDog/ddtest/internal/utils"
	"github.com/bmatcuk/doublestar/v4"
)

var TestsFilePath = filepath.Join(".", constants.PlanDirectory, "tests-discovery/tests.json")

type Excluder struct {
	pattern string
}

type TestFileSet struct {
	Pattern       string
	ExplicitFiles []string
}

func ResolveTestFiles(pattern, excludePattern string) (TestFileSet, error) {
	testFiles := TestFileSet{Pattern: pattern}
	if utils.NormalizePattern(excludePattern) == "" {
		slog.Info("Using test discovery pattern", "pattern", pattern)
		return testFiles, nil
	}

	filteredFiles, err := DiscoverTestFiles(pattern, excludePattern)
	if err != nil {
		return TestFileSet{}, err
	}

	testFiles.ExplicitFiles = filteredFiles
	if len(filteredFiles) == 0 {
		slog.Info("No test files remain after applying test discovery exclude pattern",
			"pattern", pattern, "excludePattern", excludePattern)
		return testFiles, nil
	}

	slog.Info("Using filtered test discovery files",
		"pattern", pattern, "excludePattern", excludePattern, "count", len(filteredFiles))
	return testFiles, nil
}

func (t TestFileSet) UseExplicitFiles() bool {
	return t.ExplicitFiles != nil
}

func (t TestFileSet) Empty() bool {
	return t.UseExplicitFiles() && len(t.ExplicitFiles) == 0
}

func NewExcluder(pattern string) (Excluder, error) {
	normalized := utils.NormalizePattern(pattern)
	if normalized == "" {
		return Excluder{}, nil
	}
	if !doublestar.ValidatePattern(normalized) {
		return Excluder{}, fmt.Errorf("invalid tests exclude pattern %q", pattern)
	}
	return Excluder{pattern: normalized}, nil
}

func (e Excluder) Match(path string) bool {
	if e.pattern == "" {
		return false
	}
	return doublestar.MatchUnvalidated(e.pattern, utils.NormalizePath(path))
}

func DiscoverTestFiles(includePattern, excludePattern string) ([]string, error) {
	matches, err := doublestar.FilepathGlob(includePattern, doublestar.WithFilesOnly())
	if err != nil {
		return nil, fmt.Errorf("failed to discover test files with pattern %q: %w", includePattern, err)
	}
	return filterTestFiles(matches, excludePattern)
}

func filterTestFiles(files []string, excludePattern string) ([]string, error) {
	excluder, err := NewExcluder(excludePattern)
	if err != nil {
		return nil, err
	}

	filtered := make([]string, 0, len(files))
	for _, file := range files {
		normalized := utils.NormalizePath(file)
		if normalized == "" || excluder.Match(normalized) {
			continue
		}
		filtered = append(filtered, normalized)
	}
	return filtered, nil
}

func Cleanup() {
	if err := os.Remove(TestsFilePath); err != nil && !os.IsNotExist(err) {
		slog.Warn("Warning: Failed to delete existing discovery file", "filePath", TestsFilePath, "error", err)
	}
}

func ExecuteDiscoveryCommand(ctx context.Context, executor ext.CommandExecutor, name string, args []string, envMap map[string]string, frameworkName string) error {
	slog.Debug("Starting test discovery...", "framework", frameworkName)
	startTime := time.Now()

	output, err := executor.CombinedOutput(ctx, name, args, envMap)
	if err != nil {
		slog.Warn("Failed to run test discovery", "framework", frameworkName, "output", string(output), "error", err)
		return err
	}

	duration := time.Since(startTime)
	slog.Debug("Finished test discovery", "framework", frameworkName, "duration", duration)

	return nil
}

func ParseTests() ([]testoptimization.Test, error) {
	file, err := os.Open(TestsFilePath)
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

// BaseEnv returns environment variables required for all test discovery processes.
// These env vars ensure the test framework runs in discovery mode without requiring
// actual Datadog credentials or agent connectivity.
func BaseEnv() map[string]string {
	return map[string]string{
		"DD_CIVISIBILITY_ENABLED":                "1",
		"DD_CIVISIBILITY_AGENTLESS_ENABLED":      "true",
		"DD_API_KEY":                             "dummy_key",
		"DD_TEST_OPTIMIZATION_DISCOVERY_ENABLED": "1",
		"DD_TEST_OPTIMIZATION_DISCOVERY_FILE":    TestsFilePath,
	}
}
