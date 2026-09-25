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
	binRailsPath            = "bin/rails"
	minitestTestFilePattern = "*_test.rb"
	minitestRootDir         = "test"
)

type Minitest struct {
	executor        ext.CommandExecutor
	commandOverride []string
	platformEnv     map[string]string
}

func NewMinitest() *Minitest {
	return &Minitest{
		executor:        &ext.DefaultCommandExecutor{},
		commandOverride: loadCommandOverride(),
		platformEnv:     make(map[string]string),
	}
}

func (m *Minitest) SetPlatformEnv(platformEnv map[string]string) {
	m.platformEnv = platformEnv
}

func (m *Minitest) GetPlatformEnv() map[string]string {
	return m.platformEnv
}

func (m *Minitest) Name() string {
	return "minitest"
}

func (m *Minitest) DiscoverTests(ctx context.Context, testFiles discovery.TestFileSet) ([]testoptimization.Test, error) {
	discovery.Cleanup()

	if testFiles.Empty() {
		return []testoptimization.Test{}, nil
	}

	executable, args, isRails := m.getMinitestCommand(ctx)

	envMap := make(map[string]string)
	maps.Copy(envMap, m.platformEnv)
	if isRails {
		if testFiles.UseExplicitFiles() {
			args = append(args, testFiles.ExplicitFiles...)
		} else {
			args = append(args, testFiles.Pattern)
		}
	} else {
		// Non-Rails Minitest discovery uses Rake's TEST pattern input; planner post-filtering removes excluded files.
		envMap["TEST"] = testFiles.Pattern
	}

	return discovery.DiscoverTests(ctx, m.executor, executable, args, envMap)
}

func (m *Minitest) TestPattern() string {
	if custom := settings.GetTestsLocation(); custom != "" {
		return custom
	}
	return filepath.Join(minitestRootDir, "**", minitestTestFilePattern)
}

func (m *Minitest) DiscoverTestFiles(ctx context.Context, testFiles discovery.TestFileSet) ([]string, error) {
	if testFiles.Empty() {
		return []string{}, nil
	}
	if testFiles.UseExplicitFiles() {
		return testFiles.ExplicitFiles, nil
	}
	return discovery.DiscoverTestFiles(testFiles.Pattern, settings.GetTestsExcludePattern())
}

func (m *Minitest) RunTests(ctx context.Context, testFiles []string, envMap map[string]string) error {
	command, args, isRails := m.getMinitestCommand(ctx)
	slog.Info("Running tests with command", "command", command, "args", args)

	// Add test files if provided
	if len(testFiles) > 0 {
		if isRails {
			// Rails test accepts files as command-line arguments
			args = append(args, testFiles...)
		} else {
			// Rake test requires TEST_FILES environment variable
			if envMap == nil {
				envMap = make(map[string]string)
			}
			envMap["TEST_FILES"] = strings.Join(testFiles, " ")
		}
	}

	mergedEnv := make(map[string]string)
	maps.Copy(mergedEnv, m.platformEnv)
	maps.Copy(mergedEnv, envMap)
	return m.executor.Run(ctx, command, args, mergedEnv)
}

// isRailsApplication determines if the current project is a Rails application
func (m *Minitest) isRailsApplication(ctx context.Context) bool {
	// Check if rails gem is installed
	output, err := m.executor.CombinedOutput(ctx, "bundle", []string{"show", "rails"}, nil)
	if err != nil {
		slog.Debug("Not a Rails application: bundle show rails failed", "output", string(output), "error", err)
		return false
	}

	// Bundler can emit tracer debug logs alongside the gem path (DD_TRACE_DEBUG).
	// Find the path on its own line rather than treating all output as a path.
	var railsPath string
	for line := range strings.SplitSeq(string(output), "\n") {
		candidate := strings.TrimSpace(line)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			railsPath = candidate
			break
		}
	}
	if railsPath == "" {
		slog.Debug("Not a Rails application: bundle show rails returned no existing gem directory", "output", string(output))
		return false
	}

	// Check if rails command works
	output, err = m.executor.CombinedOutput(ctx, "bundle", []string{"exec", "rails", "version"}, nil)
	if err != nil {
		slog.Debug("Not a Rails application: bundle exec rails version failed", "output", string(output), "error", err)
		return false
	}

	// Rails version output can also be surrounded by tracer debug logs.
	for line := range strings.SplitSeq(string(output), "\n") {
		versionOutput := strings.TrimSpace(line)
		if strings.HasPrefix(versionOutput, "Rails ") {
			slog.Debug("Detected Rails application", "version_output", versionOutput)
			return true
		}
	}

	slog.Debug("Not a Rails application: rails version output has no line starting with 'Rails '", "output", string(output))
	return false
}

// getMinitestCommand determines whether to use rails test or rake test
// Returns: command, args, isRails
func (m *Minitest) getMinitestCommand(ctx context.Context) (string, []string, bool) {
	isRails := m.isRailsApplication(ctx)
	if len(m.commandOverride) > 0 {
		return m.commandOverride[0], m.commandOverride[1:], isRails
	}
	if isRails {
		// Check if bin/rails exists and is executable
		if info, err := os.Stat(binRailsPath); err == nil && !info.IsDir() {
			// Check if file is executable
			if info.Mode()&0111 != 0 {
				slog.Info("Found Ruby on Rails. Using bin/rails test for Minitest commands")
				return binRailsPath, []string{"test"}, true
			}
		}
		slog.Info("Found Ruby on Rails. Using bundle exec rails test for Minitest commands")
		return "bundle", []string{"exec", "rails", "test"}, true
	}

	slog.Info("No Ruby on Rails found. Using bundle exec rake test for Minitest commands")
	return "bundle", []string{"exec", "rake", "test"}, false
}

func (m *Minitest) SupportsFullTestDiscovery() bool {
	return true
}

func (m *Minitest) SourceFileForSuite(suite string) (string, bool) {
	return trailingRubySuiteSourceFile(suite)
}

func (m *Minitest) HasUnskippableMarker(testFile string) bool {
	return utils.FileContainsAll(testFile, "datadog_itr_unskippable")
}

func (m *Minitest) Command() (string, []string) {
	if len(m.commandOverride) > 0 {
		return m.commandOverride[0], m.commandOverride[1:]
	}
	if info, err := os.Stat(binRailsPath); err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
		return binRailsPath, []string{"test"}
	}
	return "bundle", []string{"exec", "rake", "test"}
}
