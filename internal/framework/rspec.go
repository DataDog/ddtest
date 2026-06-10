package framework

import (
	"context"
	"log/slog"
	"maps"
	"os"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/ext"
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
	discoveryConfig discovery.Config
}

func NewRSpec() *RSpec {
	executor := &ext.DefaultCommandExecutor{}
	rspec := &RSpec{
		executor:        executor,
		commandOverride: loadCommandOverride(),
	}
	rspec.discoveryConfig = discovery.Config{
		FrameworkName: "rspec",
		RootDir:       rspecRootDir,
		FilePattern:   rspecTestFilePattern,
		Executor:      executor,
		PlatformEnv:   make(map[string]string),
	}
	return rspec
}

func (r *RSpec) SetPlatformEnv(platformEnv map[string]string) {
	r.discoveryConfig.SetPlatformEnv(platformEnv)
}

func (r *RSpec) GetPlatformEnv() map[string]string {
	return r.discoveryConfig.PlatformEnvironment()
}

func (r *RSpec) Name() string {
	return "rspec"
}

func (r *RSpec) DiscoverTests(ctx context.Context) ([]testoptimization.Test, error) {
	return discovery.DiscoverTests(ctx, r)
}

func (r *RSpec) DiscoverTestFiles() ([]string, error) {
	testFiles, err := discovery.DiscoverFrameworkTestFiles(r)
	if err != nil {
		return nil, err
	}

	slog.Debug("Discovered RSpec test files", "count", len(testFiles))
	return testFiles, nil
}

func (r *RSpec) DiscoveryConfig() discovery.Config {
	return r.discoveryConfig
}

func (r *RSpec) BuildDiscoveryCommand() (ext.Command, discovery.DiscoveryInput) {
	name, args := r.buildDiscoveryCommand()
	return ext.Command{Name: name, Args: args}, discovery.DiscoveryInput{PatternFlag: "--pattern"}
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

func (r *RSpec) buildDiscoveryCommand() (string, []string) {
	// Always use bundle exec rspec for discovery, as bin/rspec is often customized
	return "bundle", []string{"exec", "rspec", "--format", "progress", "--dry-run"}
}
