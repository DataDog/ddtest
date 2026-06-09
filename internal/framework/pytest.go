package framework

import (
	"context"
	"log/slog"
	"maps"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization"
)

const (
	pytestTestFilePattern = "*_test.py"
	pytestRootDir         = "tests"
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
			"pattern", p.testPattern(), "fileCount", len(testFiles))
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

func (p *PyTest) testPattern() string {
	if custom := settings.GetTestsLocation(); custom != "" {
		return custom
	}
	return defaultTestPattern(pytestRootDir, pytestTestFilePattern)
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
