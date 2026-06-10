package framework

import (
	"context"
	"log/slog"
	"maps"
	"os"
	"path/filepath"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization"
	"github.com/DataDog/ddtest/internal/utils"
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
	discovery.Cleanup()

	pattern := r.testPattern()
	excludePattern := settings.GetTestsExcludePattern()
	useFilteredFiles := utils.NormalizePattern(excludePattern) != ""
	var testFiles []string
	if useFilteredFiles {
		var err error
		testFiles, err = discovery.DiscoverTestFiles(pattern, excludePattern)
		if err != nil {
			return nil, err
		}
		if len(testFiles) == 0 {
			slog.Info("No RSpec test files remain after applying test discovery exclude pattern",
				"pattern", pattern, "excludePattern", excludePattern)
			return []testoptimization.Test{}, nil
		}
	}

	name := "bundle"
	args := []string{"exec", "rspec", "--format", "progress", "--dry-run"}
	if useFilteredFiles {
		args = append(args, testFiles...)
		slog.Info("Using filtered RSpec test discovery files",
			"pattern", pattern, "excludePattern", excludePattern, "count", len(testFiles))
	} else {
		args = append(args, "--pattern", pattern)
		slog.Info("Using RSpec test discovery pattern", "pattern", pattern)
	}

	envMap := make(map[string]string)
	maps.Copy(envMap, r.platformEnv)
	maps.Copy(envMap, discovery.BaseEnv())

	slog.Info("Discovering tests with command", "command", name, "args", args)
	if err := discovery.ExecuteDiscoveryCommand(ctx, r.executor, name, args, envMap, r.Name()); err != nil {
		return nil, err
	}

	tests, err := discovery.ParseTests()
	if err != nil {
		return nil, err
	}

	slog.Debug("Parsed RSpec report", "tests", len(tests))
	return tests, nil
}

func (r *RSpec) DiscoverTestFiles() ([]string, error) {
	testFiles, err := discovery.DiscoverTestFiles(r.testPattern(), settings.GetTestsExcludePattern())
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
	return filepath.Join(rspecRootDir, "**", rspecTestFilePattern)
}

func (r *RSpec) RunTests(ctx context.Context, testFiles []string, envMap map[string]string) error {
	command, baseArgs := r.getRSpecCommand()
	args := append(baseArgs, "--format", "progress")
	slog.Info("Running tests with command", "command", command, "args", args)
	args = append(args, testFiles...)

	mergedEnv := make(map[string]string)
	maps.Copy(mergedEnv, r.GetPlatformEnv())
	maps.Copy(mergedEnv, envMap)
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
