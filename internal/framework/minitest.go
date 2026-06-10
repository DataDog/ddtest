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

func (m *Minitest) DiscoverTests(ctx context.Context) ([]testoptimization.Test, error) {
	discovery.Cleanup()

	pattern := m.testPattern()
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
			slog.Info("No Minitest test files remain after applying test discovery exclude pattern",
				"pattern", pattern, "excludePattern", excludePattern)
			return []testoptimization.Test{}, nil
		}
	}

	name, args, isRails := m.getMinitestCommand()

	envMap := make(map[string]string)
	maps.Copy(envMap, m.platformEnv)
	maps.Copy(envMap, discovery.BaseEnv())

	if useFilteredFiles {
		if isRails {
			args = append(args, testFiles...)
		} else {
			envMap["TEST"] = strings.Join(testFiles, ",")
		}
		slog.Info("Using filtered Minitest test discovery files",
			"pattern", pattern, "excludePattern", excludePattern, "count", len(testFiles))
	} else if isRails {
		args = append(args, pattern)
		slog.Info("Using Minitest test discovery pattern", "pattern", pattern)
	} else {
		envMap["TEST"] = pattern
		slog.Info("Using Minitest test discovery pattern", "pattern", pattern)
	}

	slog.Info("Discovering tests with command", "command", name, "args", args)
	if err := discovery.ExecuteDiscoveryCommand(ctx, m.executor, name, args, envMap, m.Name()); err != nil {
		return nil, err
	}

	tests, err := discovery.ParseTests()
	if err != nil {
		return nil, err
	}

	slog.Debug("Parsed Minitest report", "tests", len(tests))
	return tests, nil
}

func (m *Minitest) DiscoverTestFiles() ([]string, error) {
	testFiles, err := discovery.DiscoverTestFiles(m.testPattern(), settings.GetTestsExcludePattern())
	if err != nil {
		return nil, err
	}

	slog.Debug("Discovered Minitest test files", "count", len(testFiles))
	return testFiles, nil
}

func (m *Minitest) testPattern() string {
	if custom := settings.GetTestsLocation(); custom != "" {
		return custom
	}
	return filepath.Join(minitestRootDir, "**", minitestTestFilePattern)
}

func (m *Minitest) RunTests(ctx context.Context, testFiles []string, envMap map[string]string) error {
	command, args, isRails := m.getMinitestCommand()
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
func (m *Minitest) isRailsApplication() bool {
	// Check if rails gem is installed
	output, err := m.executor.CombinedOutput(context.Background(), "bundle", []string{"show", "rails"}, nil)
	if err != nil {
		slog.Debug("Not a Rails application: bundle show rails failed", "output", string(output), "error", err)
		return false
	}

	// Verify the output is a valid filepath that exists
	railsPath := strings.TrimSpace(string(output))
	if railsPath == "" {
		slog.Debug("Not a Rails application: bundle show rails returned empty output")
		return false
	}
	if _, err := os.Stat(railsPath); err != nil {
		slog.Debug("Not a Rails application: rails gem path does not exist", "path", railsPath, "error", err)
		return false
	}

	// Check if rails command works
	output, err = m.executor.CombinedOutput(context.Background(), "bundle", []string{"exec", "rails", "version"}, nil)
	if err != nil {
		slog.Debug("Not a Rails application: bundle exec rails version failed", "output", string(output), "error", err)
		return false
	}

	// Verify the output starts with "Rails <version>"
	versionOutput := strings.TrimSpace(string(output))
	if !strings.HasPrefix(versionOutput, "Rails ") {
		slog.Debug("Not a Rails application: rails version output does not start with 'Rails '", "output", versionOutput)
		return false
	}

	slog.Debug("Detected Rails application", "version_output", versionOutput)
	return true
}

// getMinitestCommand determines whether to use rails test or rake test
// Returns: command, args, isRails
func (m *Minitest) getMinitestCommand() (string, []string, bool) {
	isRails := m.isRailsApplication()
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
