package platform

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"strings"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/version"
)

//go:embed scripts/python_env.py
var pythonEnvScript string

const (
	requiredPackageName       = "datadog-test-lib"
	requiredPackageMinVersion = "0.1.0"
	pytestAddOptsEnvVar       = "PYTEST_ADDOPTS"
	pytestDefaultAddOpts      = "-p ddtrace.pytest_plugin"
)

type Python struct {
	executor ext.CommandExecutor
}

func NewPython() *Python {
	return &Python{
		executor: &ext.DefaultCommandExecutor{},
	}
}

func (p *Python) Name() string {
	return "python"
}

// GetPlatformEnv returns environment variables required for Python commands.
// It sets PYTEST_ADDOPTS to load the ddtrace pytest plugin if not already set.
func (p *Python) GetPlatformEnv() map[string]string {
	envMap := make(map[string]string)

	// Check if PYTEST_ADDOPTS is already set in the environment
	if _, exists := os.LookupEnv(pytestAddOptsEnvVar); !exists {
		envMap[pytestAddOptsEnvVar] = pytestDefaultAddOpts
	}

	return envMap
}

func (p *Python) CreateTagsMap() (map[string]string, error) {
	tags := make(map[string]string)
	tags["language"] = p.Name()

	// Create plan directory if it doesn't exist
	if err := os.MkdirAll(constants.PlanDirectory, 0755); err != nil {
		return nil, fmt.Errorf("failed to create plan directory: %w", err)
	}

	// Create a temporary file for the Python script output
	tempFile := constants.PythonEnvOutputPath
	defer func() { _ = os.Remove(tempFile) }()

	// Execute the embedded Python script to get runtime tags
	args := []string{"-c", pythonEnvScript, tempFile}
	if err := p.executor.Run(context.Background(), "python", args, nil); err != nil {
		return nil, fmt.Errorf("failed to execute Python script: %w", err)
	}

	// Read the JSON output from the temp file
	fileContent, err := os.ReadFile(tempFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read Python script output file: %w", err)
	}

	// Parse the JSON output
	var pythonTags map[string]string
	if err := json.Unmarshal(fileContent, &pythonTags); err != nil {
		return nil, fmt.Errorf("failed to parse runtime tags JSON: %w, tried to parse: %s", err, string(fileContent))
	}

	// Merge the tags from the Python output
	maps.Copy(tags, pythonTags)

	return tags, nil
}

func (p *Python) DetectFramework() (framework.Framework, error) {
	frameworkName := settings.GetFramework()
	platformEnv := p.GetPlatformEnv()

	var fw framework.Framework
	switch frameworkName {
	case "pytest":
		fw = framework.NewPytest()
	default:
		return nil, fmt.Errorf("framework '%s' is not supported by platform 'python'", frameworkName)
	}

	fw.SetPlatformEnv(platformEnv)
	return fw, nil
}

func (p *Python) SanityCheck() error {
	// Check if datadog-test-lib is installed
	args := []string{"-m", "pip", "show", requiredPackageName}
	output, err := p.executor.CombinedOutput(context.Background(), "python", args, nil)
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			return fmt.Errorf("pip show %s command failed: %w", requiredPackageName, err)
		}
		return fmt.Errorf("pip show %s command failed: %s", requiredPackageName, message)
	}

	requiredVersion, err := version.Parse(requiredPackageMinVersion)
	if err != nil {
		return err
	}

	pkgVersion, err := parsePipShowVersion(string(output), requiredPackageName)
	if err != nil {
		return err
	}

	if pkgVersion.Compare(requiredVersion) < 0 {
		return fmt.Errorf("%s version %s is lower than required >= %s", requiredPackageName, pkgVersion.String(), requiredVersion.String())
	}

	return nil
}

func parsePipShowVersion(output, packageName string) (version.Version, error) {
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "Version:") {
			// Format: "Version: 0.1.0"
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				versionString := strings.TrimSpace(parts[1])
				parsed, err := version.Parse(versionString)
				if err != nil {
					return version.Version{}, fmt.Errorf("failed to parse version from pip show output: %w", err)
				}
				return parsed, nil
			}
		}
	}

	return version.Version{}, fmt.Errorf("unable to find %s version in pip show output", packageName)
}
