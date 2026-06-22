package framework

import (
	"context"
	"log/slog"
	"maps"
	"strings"

	"github.com/DataDog/ddtest/internal/discovery"
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

// TestPattern returns the glob pattern used to discover pytest test files.
// Priority: explicit --tests-location flag > pytest config file > built-in default.
// Multiple testpaths or python_files from config are collapsed into brace-expansion
// syntax that doublestar handles natively, e.g. {tests,src}/**/{test_*,*_test}.py.
func (p *PyTest) TestPattern() string {
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

func (p *PyTest) DiscoverTests(ctx context.Context, testFiles discovery.TestFileSet) ([]testoptimization.Test, error) {
	discovery.Cleanup()

	if testFiles.Empty() {
		return []testoptimization.Test{}, nil
	}

	args := []string{"-m", "pytest"}

	if testFiles.UseExplicitFiles() {
		args = append(args, testFiles.ExplicitFiles...)
	} else {
		// pytest has no --pattern flag; resolve the glob pattern to explicit files
		files, err := discovery.DiscoverTestFiles(testFiles.Pattern, "")
		if err != nil {
			return nil, err
		}
		if len(files) == 0 {
			return []testoptimization.Test{}, nil
		}
		slog.Info("Constraining pytest test discovery", "pattern", testFiles.Pattern, "fileCount", len(files))
		args = append(args, files...)
	}

	return discovery.DiscoverTests(ctx, p.executor, "python", args, p.platformEnv)
}

// braceExpand collapses a list into a single glob token.
// A single item is returned as-is; multiple items are wrapped: {a,b,c}.
func braceExpand(items []string) string {
	if len(items) == 1 {
		return items[0]
	}
	return "{" + strings.Join(items, ",") + "}"
}

func (p *PyTest) SupportsFullTestDiscovery() bool {
	return true
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
