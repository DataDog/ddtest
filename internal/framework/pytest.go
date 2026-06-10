package framework

import (
	"context"
	"log/slog"
	"maps"
	"strings"

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

	args := []string{"-m", "pytest"}

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
	testFiles, err := globTestFiles(p.testPattern())
	if err != nil {
		return nil, err
	}
	slog.Debug("Discovered pytest test files", "count", len(testFiles))
	return testFiles, nil
}

// testPattern returns the single glob pattern used to discover test files.
// Priority: explicit --tests-location flag > pytest config file > built-in default.
// Multiple testpaths or python_files from config are collapsed into brace-expansion
// syntax that doublestar handles natively, e.g. {tests,src}/**/{test_*,*_test}.py.
func (p *PyTest) testPattern() string {
	if custom := settings.GetTestsLocation(); custom != "" {
		return custom
	}

	cfg := loadPytestConfig()

	filePatterns := cfg.PythonFiles
	if len(filePatterns) == 0 {
		filePatterns = []string{"{test_*,*_test}.py"}
	}
	filePart := braceExpand(filePatterns)

	if len(cfg.Testpaths) == 0 {
		return "**/" + filePart
	}
	return braceExpand(cfg.Testpaths) + "/**/" + filePart
}

// braceExpand collapses a list into a single glob token.
// A single item is returned as-is; multiple items are wrapped: {a,b,c}.
func braceExpand(items []string) string {
	if len(items) == 1 {
		return items[0]
	}
	return "{" + strings.Join(items, ",") + "}"
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
