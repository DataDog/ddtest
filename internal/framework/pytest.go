package framework

import (
	"context"
	"log/slog"
	"maps"
	"path/filepath"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization"
)

const (
	// pytestDefaultPattern is used when no config file specifies testpaths/python_files.
	// Matches both pytest conventions (test_*.py and *_test.py) everywhere in the tree.
	pytestDefaultPattern = "**/{test_*,*_test}.py"
)

type PyTest struct {
	executor        ext.CommandExecutor
	commandOverride []string
	platformEnv     map[string]string
}

func NewPytest() *PyTest {
	return &PyTest{
		executor:        &ext.DefaultCommandExecutor{},
		commandOverride: loadCommandOverride(),
		platformEnv:     make(map[string]string),
	}
}

func (p *PyTest) SetPlatformEnv(platformEnv map[string]string) {
	p.platformEnv = platformEnv
}

func (p *PyTest) GetPlatformEnv() map[string]string {
	return p.platformEnv
}

func (p *PyTest) Name() string {
	return "pytest"
}

func (p *PyTest) DiscoverTests(ctx context.Context) ([]testoptimization.Test, error) {
	cleanupDiscoveryFile(TestsDiscoveryFilePath)

	args := []string{"-m", "pytest", "--collect-only", "-q"}

	// When a custom tests location is configured, resolve the glob to actual files
	// and pass them to pytest so collection is constrained to only those files.
	// Without this, --tests-location would be silently ignored during discovery.
	if settings.GetTestsLocation() != "" {
		testFiles, err := p.DiscoverTestFiles()
		if err != nil {
			return nil, err
		}
		slog.Info("Constraining test discovery to custom location",
			"pattern", settings.GetTestsLocation(), "fileCount", len(testFiles))
		args = append(args, testFiles...)
	}

	// Merge env maps: platform env -> base discovery env
	envMap := make(map[string]string)
	maps.Copy(envMap, p.platformEnv)
	maps.Copy(envMap, BaseDiscoveryEnv())

	slog.Info("Discovering tests with command", "command", "python", "args", args)
	_, err := executeDiscoveryCommand(ctx, p.executor, "python", args, envMap, p.Name())
	if err != nil {
		return nil, err
	}

	tests, err := parseDiscoveryFile(TestsDiscoveryFilePath)
	if err != nil {
		return nil, err
	}

	slog.Debug("Parsed pytest report", "tests", len(tests))
	return tests, nil
}

func (p *PyTest) DiscoverTestFiles() ([]string, error) {
	seen := make(map[string]struct{})
	var allFiles []string
	for _, pattern := range p.testPatterns() {
		files, err := globTestFiles(pattern)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			if _, ok := seen[f]; !ok {
				seen[f] = struct{}{}
				allFiles = append(allFiles, f)
			}
		}
	}
	slog.Debug("Discovered pytest test files", "count", len(allFiles))
	return allFiles, nil
}

// testPatterns returns the glob patterns used to discover test files.
// Priority: explicit --tests-location flag > pytest config file > built-in default.
func (p *PyTest) testPatterns() []string {
	if custom := settings.GetTestsLocation(); custom != "" {
		return []string{custom}
	}

	cfg := loadPytestConfig()

	filePatterns := cfg.PythonFiles
	if len(filePatterns) == 0 {
		filePatterns = []string{"{test_*,*_test}.py"}
	}

	if len(cfg.Testpaths) == 0 {
		// No testpaths configured: search the whole tree.
		patterns := make([]string, 0, len(filePatterns))
		for _, fp := range filePatterns {
			patterns = append(patterns, "**/"+fp)
		}
		return patterns
	}

	patterns := make([]string, 0, len(cfg.Testpaths)*len(filePatterns))
	for _, tp := range cfg.Testpaths {
		for _, fp := range filePatterns {
			patterns = append(patterns, filepath.Join(tp, "**", fp))
		}
	}
	return patterns
}

func (p *PyTest) RunTests(ctx context.Context, testFiles []string, envMap map[string]string) error {
	command := "python"
	args := []string{"-m", "pytest"}
	slog.Info("Running tests with command", "command", command, "args", args)
	args = append(args, testFiles...)

	mergedEnv := make(map[string]string)
	maps.Copy(mergedEnv, p.platformEnv)
	maps.Copy(mergedEnv, envMap)
	return p.executor.Run(ctx, command, args, mergedEnv)
}
