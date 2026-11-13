package framework

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/constants"
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
// non-test directory. Discovery returns slash-separated paths on every
// platform, so normalize before splitting: on Windows filepath.Separator is
// a backslash and an unnormalized path would stay a single segment, letting
// dependency tests bypass the exclusion.
func isUnderExcludedDir(file string) bool {
	for _, segment := range strings.Split(utils.NormalizePath(file), "/") {
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
	mergedEnv, err := p.platform.RunEnv(RuntimeOptions{Env: envMap})
	if err != nil {
		return err
	}
	if envMap[constants.TestRunPartialEnvVar] == "true" && syrupyFailsOnUnused(args) && p.syrupyInstalled(ctx, mergedEnv) {
		// syrupy fails the session on unused snapshots. With the suite's files
		// split over workers, the tests of the files other workers run use
		// some of each snapshot directory, so every worker would fail.
		args = append(args, "--snapshot-warn-unused")
	}
	selection, err := selectSplitFileTests(testFiles)
	if err != nil {
		return err
	}
	filter := ""
	if len(selection.filters) > 0 {
		var cleanup func()
		filter, cleanup, err = writeSplitFilter(selection.filters, mergedEnv)
		if err != nil {
			return err
		}
		defer cleanup()
		testFiles = selection.args
	}
	slog.Info("Running tests with command", "command", command, "args", args)
	args = withFrameworkFiles(command, args, "pytest", testFiles)

	err = p.executor.Run(ctx, command, args, mergedEnv)
	if filter != "" && ctx.Err() == nil && (err == nil || exitCode(err) == 1 || exitCode(err) == 5) {
		// pytest collected the tests, so the plugin had its turn. Without
		// it, the last part of a split file runs the tests of its other parts
		// again: a wrapper that clears the environment drops the plugin.
		if _, statErr := os.Stat(filter + ".applied"); statErr != nil {
			return errors.Join(err, fmt.Errorf("pytest ran without ddtest's split filter plugin; is %s or PYTHONPATH cleared by a wrapper?", splitFilterEnvVar))
		}
	}
	if exitCode(err) == 5 && ctx.Err() == nil && selection.onlySplitFiles {
		// The batch only runs parts of split files, and every test the plan
		// gave them is gone, with no new test in the last part of a file.
		slog.Info("No tests collected in a batch of split test files", "testFilesCount", len(testFiles))
		return nil
	}
	skippingPlatform, ok := p.platform.(interface {
		TestSkippingLevel() settings.TestSkippingLevel
	})
	if ok && skippingPlatform.TestSkippingLevel() == settings.TestSkippingLevelSuite &&
		ctx.Err() == nil && exitCode(err) == 5 {
		// File-only planning cannot apply pytest's item selectors. A worker
		// may legitimately receive only files whose tests are all deselected.
		slog.Info("No tests collected in pytest suite-mode batch", "testFilesCount", len(testFiles))
		return nil
	}
	return err
}

// exitCode is the exit code of a command that exited with one, or -1.
func exitCode(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

// syrupyFailsOnUnused reports whether pytest's arguments leave syrupy failing
// the session on unused snapshots: not when they already make it warn or update
// the snapshots, or turn the plugin off.
func syrupyFailsOnUnused(args []string) bool {
	for i, arg := range args {
		switch arg {
		case "--snapshot-warn-unused", "--snapshot-update", "-pno:syrupy":
			return false
		case "-p":
			if i+1 < len(args) && args[i+1] == "no:syrupy" {
				return false
			}
		}
	}
	return true
}

// syrupyInstalled reports whether the tests' Python environment has syrupy.
func (p *PyTest) syrupyInstalled(ctx context.Context, env map[string]string) bool {
	_, err := p.executor.CombinedOutput(ctx, "python", []string{
		"-c", "import importlib.util, sys; sys.exit(importlib.util.find_spec('syrupy') is None)",
	}, env)
	return err == nil
}

func (p *PyTest) Command() (string, []string) {
	if len(p.commandOverride) > 0 {
		return p.commandOverride[0], p.commandOverride[1:]
	}
	return "python", []string{"-m", "pytest"}
}
