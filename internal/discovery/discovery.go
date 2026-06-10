package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization"
	ciutils "github.com/DataDog/ddtest/internal/utils"
	"github.com/bmatcuk/doublestar/v4"
)

var TestsFilePath = filepath.Join(".", constants.PlanDirectory, "tests-discovery/tests.json")

type DiscoveryInput struct {
	// PatternFlag is used by frameworks that accept a glob via a flag, e.g. RSpec --pattern.
	PatternFlag string
	// EnvVar is used by frameworks that read the discovery input from an environment variable.
	EnvVar string
	// EnvFileSeparator joins explicit file lists for EnvVar. Defaults to ",".
	EnvFileSeparator string
}

type Config struct {
	FrameworkName string
	RootDir       string
	FilePattern   string
	Executor      ext.CommandExecutor
	PlatformEnv   map[string]string
}

func (c *Config) SetPlatformEnv(platformEnv map[string]string) {
	c.PlatformEnv = platformEnv
}

func (c Config) PlatformEnvironment() map[string]string {
	return c.PlatformEnv
}

type ConfigProvider interface {
	DiscoveryConfig() Config
	BuildDiscoveryCommand() (ext.Command, DiscoveryInput)
}

type Excluder struct {
	pattern string
}

func NewExcluder(pattern string) (Excluder, error) {
	normalized := ciutils.NormalizePattern(pattern)
	if normalized == "" {
		return Excluder{}, nil
	}
	if !doublestar.ValidatePattern(normalized) {
		return Excluder{}, fmt.Errorf("invalid tests exclude pattern %q", pattern)
	}
	return Excluder{pattern: normalized}, nil
}

func (e Excluder) Empty() bool {
	return e.pattern == ""
}

func (e Excluder) Match(path string) bool {
	if e.Empty() {
		return false
	}
	return doublestar.MatchUnvalidated(e.pattern, ciutils.NormalizePath(path))
}

func PatternSet(pattern string) bool {
	return ciutils.NormalizePattern(pattern) != ""
}

func globTestFiles(includePattern, excludePattern string) ([]string, error) {
	matches, err := doublestar.FilepathGlob(includePattern, doublestar.WithFilesOnly())
	if err != nil {
		return nil, fmt.Errorf("failed to discover test files with pattern %q: %w", includePattern, err)
	}
	return filterTestFiles(matches, excludePattern)
}

func DiscoverFrameworkTestFiles(provider ConfigProvider) ([]string, error) {
	return discoverConfiguredTestFiles(provider.DiscoveryConfig(), testsExcludePattern())
}

func discoverConfiguredTestFiles(config Config, excludePattern string) ([]string, error) {
	return globTestFiles(TestPattern(config), excludePattern)
}

func DiscoverTests(ctx context.Context, provider ConfigProvider) ([]testoptimization.Test, error) {
	Cleanup()

	config := provider.DiscoveryConfig()
	pattern := TestPattern(config)
	excludePattern := testsExcludePattern()
	filteredTestFiles, useFilteredFiles, err := filteredFiles(config, excludePattern)
	if err != nil {
		return nil, err
	}
	if useFilteredFiles && len(filteredTestFiles) == 0 {
		slog.Info("No test files remain after applying test discovery exclude pattern",
			"framework", config.FrameworkName, "pattern", pattern, "excludePattern", excludePattern)
		return []testoptimization.Test{}, nil
	}

	command, discoveryInput := provider.BuildDiscoveryCommand()
	if useFilteredFiles {
		applyDiscoveryFiles(&command, discoveryInput, filteredTestFiles)
		slog.Info("Using filtered test discovery files",
			"framework", config.FrameworkName, "pattern", pattern,
			"excludePattern", excludePattern, "count", len(filteredTestFiles))
	} else {
		applyDiscoveryPattern(&command, discoveryInput, pattern)
		slog.Info("Using test discovery pattern", "framework", config.FrameworkName, "pattern", pattern)
	}

	envMap := make(map[string]string)
	maps.Copy(envMap, config.PlatformEnv)
	maps.Copy(envMap, BaseEnv())
	maps.Copy(envMap, command.Env)

	slog.Info("Discovering tests with command", "command", command.Name, "args", command.Args)
	_, err = ExecuteDiscoveryCommand(ctx, config.Executor, command.Name, command.Args, envMap, config.FrameworkName)
	if err != nil {
		return nil, err
	}

	tests, err := ParseFile(TestsFilePath)
	if err != nil {
		return nil, err
	}

	slog.Debug("Parsed test discovery report", "framework", config.FrameworkName, "tests", len(tests))
	return tests, nil
}

func applyDiscoveryPattern(command *ext.Command, discoveryInput DiscoveryInput, pattern string) {
	switch {
	case discoveryInput.PatternFlag != "":
		command.AppendArgs(discoveryInput.PatternFlag, pattern)
	case discoveryInput.EnvVar != "":
		command.SetEnv(discoveryInput.EnvVar, pattern)
	default:
		command.AppendArgs(pattern)
	}
}

func applyDiscoveryFiles(command *ext.Command, discoveryInput DiscoveryInput, testFiles []string) {
	if discoveryInput.EnvVar == "" {
		command.AppendArgs(testFiles...)
		return
	}
	separator := discoveryInput.EnvFileSeparator
	if separator == "" {
		separator = ","
	}
	command.SetEnv(discoveryInput.EnvVar, strings.Join(testFiles, separator))
}

func filteredFiles(config Config, excludePattern string) ([]string, bool, error) {
	if !PatternSet(excludePattern) {
		return nil, false, nil
	}
	testFiles, err := discoverConfiguredTestFiles(config, excludePattern)
	if err != nil {
		return nil, true, err
	}
	return testFiles, true, nil
}

func filterTestFiles(files []string, excludePattern string) ([]string, error) {
	excluder, err := NewExcluder(excludePattern)
	if err != nil {
		return nil, err
	}

	filtered := make([]string, 0, len(files))
	for _, file := range files {
		normalized := ciutils.NormalizePath(file)
		if normalized == "" || excluder.Match(normalized) {
			continue
		}
		filtered = append(filtered, normalized)
	}
	return filtered, nil
}

func Cleanup() {
	cleanupFile(TestsFilePath)
}

func cleanupFile(filePath string) {
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		slog.Warn("Warning: Failed to delete existing discovery file", "filePath", filePath, "error", err)
	}
}

func ExecuteDiscoveryCommand(ctx context.Context, executor ext.CommandExecutor, name string, args []string, envMap map[string]string, frameworkName string) ([]byte, error) {
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

func ParseFile(filePath string) ([]testoptimization.Test, error) {
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

func DefaultTestPattern(rootDir, filePattern string) string {
	return filepath.Join(rootDir, "**", filePattern)
}

func TestPattern(config Config) string {
	if custom := settings.GetTestsLocation(); custom != "" {
		return custom
	}
	return DefaultTestPattern(config.RootDir, config.FilePattern)
}

func testsExcludePattern() string {
	return settings.GetTestsExcludePattern()
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
