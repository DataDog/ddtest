package framework

import (
	"context"
	"log/slog"
	"os"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization"
)

const (
	binRSpecPath         = "bin/rspec"
	rspecTestFilePattern = "*_spec.rb"
	rspecRootDir         = "spec"
)

type RSpec struct {
	executor        ext.CommandExecutor
	commandOverride []string
	platformEnv     map[string]string
}

func NewRSpec() *RSpec {
	return &RSpec{
		executor:        &ext.DefaultCommandExecutor{},
		commandOverride: loadCommandOverride(),
		platformEnv:     make(map[string]string),
	}
}

func (r *RSpec) SetPlatformEnv(platformEnv map[string]string) {
	r.platformEnv = platformEnv
}

func (r *RSpec) GetPlatformEnv() map[string]string {
	return r.platformEnv
}

func (r *RSpec) Name() string {
	return "rspec"
}

func (r *RSpec) DiscoverTests(ctx context.Context) ([]testoptimization.Test, error) {
	cleanupDiscoveryFile(TestsDiscoveryFilePath)

	pattern := r.testPattern()

	name, args, discoveryEnv := r.createDiscoveryCommand()
	args = append(args, "--pattern", pattern)

	envMap := mergeEnvMaps(r.platformEnv, discoveryEnv)

	slog.Info("Using test discovery pattern", "pattern", pattern)
	slog.Info("Discovering tests with command", "command", name, "args", args)
	_, err := executeDiscoveryCommand(ctx, r.executor, name, args, envMap, r.Name())
	if err != nil {
		return nil, err
	}

	tests, err := parseDiscoveryFile(TestsDiscoveryFilePath)
	if err != nil {
		return nil, err
	}

	slog.Debug("Parsed RSpec report", "tests", len(tests))
	return tests, nil
}

func (r *RSpec) DiscoverTestFiles() ([]string, error) {
	testFiles, err := globTestFiles(r.testPattern())
	if err != nil {
		return nil, err
	}

	slog.Debug("Discovered RSpec test files", "count", len(testFiles))
	return testFiles, nil
}

func (r *RSpec) testPattern() string {
	if custom := settings.GetTestsLocation(); custom != "" {
		return custom
	}
	return defaultTestPattern(rspecRootDir, rspecTestFilePattern)
}

func (r *RSpec) RunTests(ctx context.Context, testFiles []string, envMap map[string]string) error {
	command, baseArgs := r.getRSpecCommand()
	args := append(baseArgs, "--format", "progress")
	slog.Info("Running tests with command", "command", command, "args", args)
	args = append(args, testFiles...)

	mergedEnv := mergeEnvMaps(r.platformEnv, envMap)
	return r.executor.Run(ctx, command, args, mergedEnv)
}

// getRSpecCommand determines whether to use bin/rspec or bundle exec rspec
func (r *RSpec) getRSpecCommand() (string, []string) {
	if len(r.commandOverride) > 0 {
		return r.commandOverride[0], r.commandOverride[1:]
	}

	// Check if bin/rspec exists and is executable
	if info, err := os.Stat(binRSpecPath); err == nil && !info.IsDir() {
		// Check if file is executable
		if info.Mode()&0111 != 0 {
			slog.Debug("Using bin/rspec for RSpec commands")
			return binRSpecPath, []string{}
		}
	}

	slog.Debug("Using bundle exec rspec for RSpec commands")
	return "bundle", []string{"exec", "rspec"}
}

func (r *RSpec) createDiscoveryCommand() (string, []string, map[string]string) {
	command, baseArgs := r.getRSpecCommand()
	args := append(baseArgs, "--format", "progress", "--dry-run")

	discoveryEnv := map[string]string{
		"DD_TEST_OPTIMIZATION_DISCOVERY_ENABLED": "1",
		"DD_TEST_OPTIMIZATION_DISCOVERY_FILE":    TestsDiscoveryFilePath,
	}
	return command, args, discoveryEnv
}
