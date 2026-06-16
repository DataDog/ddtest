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

func (r *RSpec) DiscoverTests(ctx context.Context, testFiles discovery.TestFileSet) ([]testoptimization.Test, error) {
	discovery.Cleanup()

	if testFiles.Empty() {
		return []testoptimization.Test{}, nil
	}

	executable := "bundle"
	args := []string{"exec", "rspec", "--format", "progress", "--dry-run"}
	if testFiles.UseExplicitFiles() {
		args = append(args, testFiles.ExplicitFiles...)
	} else {
		args = append(args, "--pattern", testFiles.Pattern)
	}

	return discovery.DiscoverTests(ctx, r.executor, executable, args, r.platformEnv)
}

func (r *RSpec) TestPattern() string {
	if custom := settings.GetTestsLocation(); custom != "" {
		return custom
	}
	return filepath.Join(rspecRootDir, "**", rspecTestFilePattern)
}

func (r *RSpec) TestExcludePattern() string {
	return ""
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
