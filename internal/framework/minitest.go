package framework

import (
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/testoptimization"
)

type Minitest struct {
	executor ext.CommandExecutor
}

func NewMinitest() *Minitest {
	return &Minitest{
		executor: &ext.DefaultCommandExecutor{},
	}
}

func (m *Minitest) Name() string {
	return "minitest"
}

func (m *Minitest) DiscoverTests() ([]testoptimization.Test, error) {
	cleanupDiscoveryFile(TestsDiscoveryFilePath)

	cmd := m.createDiscoveryCommand()
	_, err := executeDiscoveryCommand(m.executor, cmd, m.Name())
	if err != nil {
		return nil, err
	}

	tests, err := parseDiscoveryFile(TestsDiscoveryFilePath)
	if err != nil {
		return nil, err
	}

	slog.Debug("Parsed Minitest report", "tests", len(tests))
	return tests, nil
}

func (m *Minitest) RunTests(testFiles []string, envMap map[string]string) error {
	command, args, isRails := m.getMinitestCommand()

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

	// no-dd-sa:go-security/command-injection
	cmd := exec.Command(command, args...)

	applyEnvMap(cmd, envMap)

	return m.executor.Run(cmd)
}

// isRailsApplication determines if the current project is a Rails application
func (m *Minitest) isRailsApplication() bool {
	// Check if rails gem is installed
	// no-dd-sa:go-security/command-injection
	cmd := exec.Command("bundle", "show", "rails")
	output, err := m.executor.CombinedOutput(cmd)
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
	// no-dd-sa:go-security/command-injection
	cmd = exec.Command("bundle", "exec", "rails", "version")
	output, err = m.executor.CombinedOutput(cmd)
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
	if isRails {
		slog.Info("Found Ruby on Rails. Using bundle exec rails test for Minitest commands")
		return "bundle", []string{"exec", "rails", "test"}, true
	}

	slog.Info("No Ruby on Rails found. Using bundle exec rake test for Minitest commands")
	return "bundle", []string{"exec", "rake", "test"}, false
}

func (m *Minitest) createDiscoveryCommand() *exec.Cmd {
	command, args, _ := m.getMinitestCommand()

	// no-dd-sa:go-security/command-injection
	cmd := exec.Command(command, args...)
	cmd.Env = append(
		os.Environ(),
		"DD_TEST_OPTIMIZATION_DISCOVERY_ENABLED=1",
		"DD_TEST_OPTIMIZATION_DISCOVERY_FILE="+TestsDiscoveryFilePath,
	)
	return cmd
}
