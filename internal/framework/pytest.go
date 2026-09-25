package framework

import (
	"context"
	"log/slog"
	"maps"
	"os/exec"

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

	args := []string{"-m", "pytest"}
	command := "python"
	if len(p.commandOverride) > 0 {
		command = p.commandOverride[0]
		args = p.commandOverride[1:]
	}

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

	return discovery.DiscoverTests(ctx, p.executor, command, args, p.platformEnv)
}

func (p *PyTest) DiscoverTestFiles(ctx context.Context, testFiles discovery.TestFileSet) ([]string, error) {
	if testFiles.Empty() {
		return []string{}, nil
	}
	if testFiles.UseExplicitFiles() {
		return testFiles.ExplicitFiles, nil
	}
	return discovery.DiscoverTestFiles(testFiles.Pattern, settings.GetTestsExcludePattern())
}

func (p *PyTest) SupportsFullTestDiscovery() bool {
	return true
}

func (p *PyTest) SourceFileForSuite(suite string) (string, bool) {
	return "", false
}

func (p *PyTest) HasUnskippableMarker(testFile string) bool {
	return false
}

func (p *PyTest) RunTests(ctx context.Context, testFiles []string, envMap map[string]string) error {
	command := "python"
	args := []string{"-m", "pytest"}
	if len(p.commandOverride) > 0 {
		command = p.commandOverride[0]
		args = p.commandOverride[1:]
	}
	slog.Info("Running tests with command", "command", command, "args", args)
	args = append(args, testFiles...)

	mergedEnv := make(map[string]string)
	maps.Copy(mergedEnv, p.platformEnv)
	maps.Copy(mergedEnv, envMap)
	return p.executor.Run(ctx, command, args, mergedEnv)
}

func (p *PyTest) Command() (string, []string) {
	if len(p.commandOverride) > 0 {
		return p.commandOverride[0], p.commandOverride[1:]
	}
	interpreter := "python"
	if _, err := exec.LookPath(interpreter); err != nil {
		interpreter = "python3"
	}
	return interpreter, []string{"-m", "pytest"}
}
