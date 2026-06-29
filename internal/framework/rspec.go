package framework

import (
	"context"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization"
	"github.com/DataDog/ddtest/internal/utils"
)

const (
	binRSpecPath                 = "bin/rspec"
	rspecTestFilePattern         = "*_spec.rb"
	rspecRootDir                 = "spec"
	rubySuiteSourceFileSeparator = " at "
	rubyCIQueueSuiteSuffix       = " (ci-queue running example "
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

	executable, baseArgs := r.getRSpecCommand()
	args := append([]string{}, baseArgs...)
	args = append(args, "--dry-run")
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

func (r *RSpec) SupportsFullTestDiscovery() bool {
	return true
}

func (r *RSpec) SourceFileForSuite(suite string) (string, bool) {
	return trailingRubySuiteSourceFile(suite)
}

func (r *RSpec) HasUnskippableMarker(testFile string) bool {
	return utils.FileContainsAll(testFile, "datadog_itr_unskippable")
}

func trailingRubySuiteSourceFile(suite string) (string, bool) {
	separatorIndex := strings.LastIndex(suite, rubySuiteSourceFileSeparator)
	if separatorIndex < 0 {
		return "", false
	}

	sourceFile := strings.TrimSpace(suite[separatorIndex+len(rubySuiteSourceFileSeparator):])
	if suffixIndex := strings.Index(sourceFile, rubyCIQueueSuiteSuffix); suffixIndex >= 0 {
		sourceFile = strings.TrimSpace(sourceFile[:suffixIndex])
	}
	if sourceFile == "" {
		return "", false
	}
	return sourceFile, true
}
