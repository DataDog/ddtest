package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/testoptimization"
	"github.com/DataDog/ddtest/internal/utils"
	"github.com/bmatcuk/doublestar/v4"
)

var TestsFilePath = filepath.Join(".", constants.PlanDirectory, "tests-discovery/tests.json")

const (
	MaxExplicitTestFiles           = 8_000
	discoveryCommandLogMaxLength   = 300
	discoveryCommandLogTruncSuffix = "..."
)

var excludedDirs = []string{"node_modules"}

type TestFileSetMatcher struct {
	includeMatcher utils.PathMatcher
	excludeMatcher utils.PathMatcher
	explicitFiles  map[string]struct{}
	matchAll       bool
}

type TestFileSet struct {
	Pattern       string
	ExplicitFiles []string
}

func ResolveTestFiles(pattern, excludePattern string) (TestFileSet, error) {
	testFiles := TestFileSet{Pattern: pattern}
	testFileMatcher, err := NewTestFileSetMatcher(TestFileSet{}, excludePattern)
	if err != nil {
		return TestFileSet{}, err
	}
	if testFileMatcher.excludeEmpty() {
		slog.Info("Using test discovery pattern", "pattern", pattern)
		return testFiles, nil
	}

	filteredFiles, err := discoverTestFiles(pattern, testFileMatcher)
	if err != nil {
		return TestFileSet{}, err
	}

	if len(filteredFiles) == 0 {
		testFiles.ExplicitFiles = filteredFiles
		slog.Info("No test files remain after applying test discovery exclude pattern",
			"pattern", pattern, "excludePattern", excludePattern)
		return testFiles, nil
	}
	if len(filteredFiles) > MaxExplicitTestFiles {
		slog.Warn("Too many test files remain after applying test discovery exclude pattern; using discovery pattern and planner-side post-filtering",
			"pattern", pattern, "excludePattern", excludePattern,
			"count", len(filteredFiles), "maxExplicitTestFiles", MaxExplicitTestFiles)
		return testFiles, nil
	}

	testFiles.ExplicitFiles = filteredFiles
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

func NewTestFileSetMatcher(testFiles TestFileSet, excludePattern string) (TestFileSetMatcher, error) {
	excludeMatcher, err := utils.NewPathMatcher(excludePattern)
	if err != nil {
		return TestFileSetMatcher{}, fmt.Errorf("invalid tests exclude pattern %q: %w", excludePattern, err)
	}

	if testFiles.UseExplicitFiles() {
		explicitFiles := make(map[string]struct{}, len(testFiles.ExplicitFiles))
		for _, testFile := range testFiles.ExplicitFiles {
			normalized := utils.NormalizePath(testFile)
			if normalized != "" {
				explicitFiles[normalized] = struct{}{}
			}
		}
		return TestFileSetMatcher{
			excludeMatcher: excludeMatcher,
			explicitFiles:  explicitFiles,
		}, nil
	}

	matcher, err := utils.NewPathMatcher(testFiles.Pattern)
	if err != nil {
		return TestFileSetMatcher{}, fmt.Errorf("invalid tests location pattern %q: %w", testFiles.Pattern, err)
	}
	if matcher.Empty() {
		return TestFileSetMatcher{
			excludeMatcher: excludeMatcher,
			matchAll:       true,
		}, nil
	}
	return TestFileSetMatcher{
		includeMatcher: matcher,
		excludeMatcher: excludeMatcher,
	}, nil
}

func (m TestFileSetMatcher) Match(path string) bool {
	return m.MatchNormalizedPath(utils.NormalizePath(path))
}

func (m TestFileSetMatcher) MatchNormalizedPath(normalizedPath string) bool {
	if normalizedPath == "" {
		return false
	}
	if m.excludesNormalizedPath(normalizedPath) {
		return false
	}
	if m.matchAll {
		return true
	}
	if m.explicitFiles != nil {
		_, ok := m.explicitFiles[normalizedPath]
		return ok
	}
	return m.includeMatcher.MatchNormalizedPath(normalizedPath)
}

func (m TestFileSetMatcher) excludeEmpty() bool {
	return m.excludeMatcher.Empty()
}

func (m TestFileSetMatcher) excludesNormalizedPath(normalizedPath string) bool {
	return m.excludeMatcher.MatchNormalizedPath(normalizedPath)
}

func DiscoverTestFiles(includePattern, excludePattern string) ([]string, error) {
	testFileMatcher, err := NewTestFileSetMatcher(TestFileSet{}, excludePattern)
	if err != nil {
		return nil, err
	}
	return discoverTestFiles(includePattern, testFileMatcher)
}

func discoverTestFiles(includePattern string, testFileMatcher TestFileSetMatcher) ([]string, error) {
	normalizedIncludePattern := normalizeDiscoveryPattern(includePattern)
	if normalizedIncludePattern == "" {
		return []string{}, nil
	}
	includeMatcher, err := utils.NewNormalizedPathMatcher(normalizedIncludePattern)
	if err != nil {
		return nil, fmt.Errorf("failed to discover test files with pattern %q: %w", includePattern, err)
	}

	walkRoot := discoveryWalkRoot(normalizedIncludePattern)
	if _, err := os.Lstat(walkRoot); err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("failed to discover test files with pattern %q: %w", includePattern, err)
	}

	testFiles := make([]string, 0)
	err = filepath.WalkDir(walkRoot, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			slog.Debug("Skipping path during test file discovery", "path", filePath, "error", walkErr)
			return nil
		}

		if entry.IsDir() && slices.Contains(excludedDirs, entry.Name()) {
			return filepath.SkipDir
		}

		normalizedPath := utils.NormalizePath(filePath)
		if normalizedPath == "" {
			return nil
		}

		if testFileMatcher.excludesNormalizedPath(normalizedPath) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if entry.IsDir() {
			return nil
		}

		if includeMatcher.MatchNormalizedPath(normalizedPath) {
			testFiles = append(testFiles, normalizedPath)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to discover test files with pattern %q: %w", includePattern, err)
	}

	slices.Sort(testFiles)
	return testFiles, nil
}

func normalizeDiscoveryPattern(pattern string) string {
	normalized := utils.NormalizePattern(pattern)
	if normalized == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Clean(normalized))
}

func discoveryWalkRoot(includePattern string) string {
	base, _ := doublestar.SplitPattern(includePattern)
	return filepath.FromSlash(path.Clean(base))
}

func Cleanup() {
	if err := os.Remove(TestsFilePath); err != nil && !os.IsNotExist(err) {
		slog.Warn("Warning: Failed to delete existing discovery file", "filePath", TestsFilePath, "error", err)
	}
}

func DiscoverTests(
	ctx context.Context,
	executor ext.CommandExecutor,
	executable string,
	args []string,
	envMap map[string]string,
) ([]testoptimization.Test, error) {
	discoveryEnv := make(map[string]string)
	maps.Copy(discoveryEnv, envMap)
	maps.Copy(discoveryEnv, BaseEnv())

	slog.Info("Discovering tests with command", "command", discoveryCommandLogValue(executable, args))
	if err := executeCommand(ctx, executor, executable, args, discoveryEnv); err != nil {
		return nil, err
	}

	tests, err := parseTestsFile(TestsFilePath)
	if err != nil {
		slog.Error("Error parsing JSON", "error", err)
		return nil, err
	}

	slog.Debug("Parsed test discovery report", "tests", len(tests))
	return tests, nil
}

func discoveryCommandLogValue(executable string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, executable)
	parts = append(parts, args...)

	command := strings.Join(parts, " ")
	if len(command) <= discoveryCommandLogMaxLength {
		return command
	}

	return command[:discoveryCommandLogMaxLength-len(discoveryCommandLogTruncSuffix)] + discoveryCommandLogTruncSuffix
}

func executeCommand(ctx context.Context, executor ext.CommandExecutor, executable string, args []string, envMap map[string]string) error {
	slog.Debug("Starting test discovery...")
	startTime := time.Now()

	output, err := executor.CombinedOutput(ctx, executable, args, envMap)
	if err != nil {
		if ctx.Err() != nil {
			slog.Debug("Test discovery was cancelled")
		} else {
			slog.Warn("Failed to run test discovery", "output", string(output), "error", err)
		}
		return err
	}

	duration := time.Since(startTime)
	slog.Debug("Finished test discovery", "duration", duration)

	return nil
}

func parseTestsFile(filePath string) ([]testoptimization.Test, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = file.Close()
	}()

	decoder := json.NewDecoder(file)
	tests := make([]testoptimization.Test, 0)
	for {
		var test testoptimization.Test
		if err := decoder.Decode(&test); err != nil {
			if err == io.EOF {
				return tests, nil
			}
			return nil, err
		}
		tests = append(tests, test)
	}
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
