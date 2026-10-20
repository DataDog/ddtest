package framework

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization"
	"github.com/DataDog/ddtest/internal/utils"
)

const (
	// pytestDefaultPattern is used when no config file specifies testpaths/python_files.
	// Matches both pytest conventions (test_*.py and *_test.py) everywhere in the tree.
	pytestDefaultPattern = "**/{test_*,*_test}.py"
)

type PyTest struct {
	executor        ext.CommandExecutor
	commandOverride []string
	platform        PlatformEnvironment
}

func NewPytest(p PlatformEnvironment) *PyTest {
	return &PyTest{
		executor:        &ext.DefaultCommandExecutor{},
		commandOverride: loadCommandOverride(),
		platform:        p,
	}
}

func (p *PyTest) Platform() PlatformEnvironment { return p.platform }

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
	filePart := utils.JoinGlobPatterns(filePatterns)

	if len(cfg.Testpaths) == 0 {
		return "**/" + filePart
	}
	return utils.JoinGlobPatterns(cfg.Testpaths) + "/**/" + filePart
}

func (p *PyTest) DiscoverTests(ctx context.Context, testFiles discovery.TestFileSet) ([]testoptimization.Test, error) {
	discovery.Cleanup()

	if testFiles.Empty() {
		return []testoptimization.Test{}, nil
	}

	envMap, err := p.platform.DiscoveryEnv(ctx, FullDiscovery, RuntimeOptions{})
	if err != nil {
		return nil, err
	}

	command, args := p.Command()

	if testFiles.UseExplicitFiles() {
		args = withFrameworkFiles(command, args, "pytest", testFiles.ExplicitFiles)
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
		args = withFrameworkFiles(command, args, "pytest", files)
	}

	return discovery.DiscoverTests(ctx, p.executor, command, args, envMap)
}

// excludedTestDirs are directory names that never contain the project's own
// test files but do contain .py files matching test patterns: project
// virtualenvs (Python convention keeps them inside the repository root,
// and installed packages ship test_* modules), bytecode caches, tox
// environments, and JavaScript node_modules in mixed repos.
var excludedTestDirs = []string{
	".venv", "venv", ".tox", "__pycache__", "node_modules", "site-packages", ".git",
}

// isUnderExcludedDir reports whether any path segment is a well-known
// non-test directory.
func isUnderExcludedDir(file string) bool {
	for _, segment := range strings.Split(file, string(filepath.Separator)) {
		if slices.Contains(excludedTestDirs, segment) {
			return true
		}
	}
	return false
}

func (p *PyTest) DiscoverTestFiles(ctx context.Context, testFiles discovery.TestFileSet) ([]string, error) {
	if testFiles.Empty() {
		return []string{}, nil
	}
	files := testFiles.ExplicitFiles
	if !testFiles.UseExplicitFiles() {
		var err error
		files, err = discovery.DiscoverTestFiles(testFiles.Pattern, settings.GetTestsExcludePattern())
		if err != nil {
			return nil, err
		}
	}
	// Broad patterns can match bytecode and other artifacts. Clone before
	// filtering explicit selections, which belong to the caller.
	filtered := slices.DeleteFunc(slices.Clone(files), func(file string) bool {
		return filepath.Ext(file) != ".py" || isUnderExcludedDir(file)
	})
	if excluded := len(files) - len(filtered); excluded > 0 {
		slog.Info("Excluded discovered files under non-test directories (virtualenvs, caches, dependencies)",
			"excludedCount", excluded)
	}
	return filtered, nil
}

func (p *PyTest) SupportsFullTestDiscovery() bool {
	return true
}

func (p *PyTest) SourceFileForSuite(suite string) (string, bool) {
	return "", false
}

func (p *PyTest) HasUnskippableMarker(testFile string) bool {
	const marker = "datadog_itr_unskippable"
	if utils.FileContainsAll(testFile, marker) {
		return true
	}
	// Conftests can apply the marker to a whole subtree. Do not execute them
	// or interpret Python; even a comment keeps the affected files runnable.
	absolute, err := filepath.Abs(testFile)
	if err != nil {
		return true
	}
	for directory := filepath.Dir(absolute); ; directory = filepath.Dir(directory) {
		conftest := filepath.Join(directory, "conftest.py")
		if _, err := os.Lstat(conftest); !os.IsNotExist(err) {
			if err != nil || utils.FileContainsAll(conftest, marker) {
				return true
			}
		}
		if filepath.Dir(directory) == directory {
			return false
		}
	}
}

func (p *PyTest) RunTests(ctx context.Context, testFiles []string, envMap map[string]string) error {
	command, args := p.Command()
	slog.Info("Running tests with command", "command", command, "args", args)
	args = withFrameworkFiles(command, args, "pytest", testFiles)

	mergedEnv, err := p.platform.RunEnv(RuntimeOptions{Env: envMap})
	if err != nil {
		return err
	}
	err = p.executor.Run(ctx, command, args, mergedEnv)
	skippingPlatform, ok := p.platform.(interface {
		TestSkippingLevel() settings.TestSkippingLevel
	})
	var exitErr *exec.ExitError
	if ok && skippingPlatform.TestSkippingLevel() == settings.TestSkippingLevelSuite &&
		ctx.Err() == nil && errors.As(err, &exitErr) && exitErr.ExitCode() == 5 {
		// File-only planning cannot apply pytest's item selectors. A worker
		// may legitimately receive only files whose tests are all deselected.
		slog.Info("No tests collected in pytest suite-mode batch", "testFilesCount", len(testFiles))
		return nil
	}
	return err
}

func (p *PyTest) Command() (string, []string) {
	if len(p.commandOverride) > 0 {
		return p.commandOverride[0], p.commandOverride[1:]
	}
	return "python", []string{"-m", "pytest"}
}
