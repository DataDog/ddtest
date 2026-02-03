package platform

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"regexp"
	"strings"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/version"
)

//go:embed scripts/ruby_env.rb
var rubyEnvScript string

const (
	requiredGemName       = "datadog-ci"
	requiredGemMinVersion = "1.23.0"
	rubyOptEnvVar         = "RUBYOPT"
	rubyOptDefaultValue   = "-rbundler/setup -rdatadog/ci/auto_instrument"
)

type Ruby struct {
	executor ext.CommandExecutor
}

func NewRuby() *Ruby {
	return &Ruby{
		executor: &ext.DefaultCommandExecutor{},
	}
}

func (r *Ruby) Name() string {
	return "ruby"
}

// GetPlatformEnv returns environment variables required for Ruby commands.
// It sets RUBYOPT to auto-instrument with datadog-ci if not already set.
func (r *Ruby) GetPlatformEnv() map[string]string {
	envMap := make(map[string]string)

	// Check if RUBYOPT is already set in the environment
	if _, exists := os.LookupEnv(rubyOptEnvVar); !exists {
		slog.Debug("Setting RUBYOPT to auto-instrument with datadog-ci", "rubyOpt", rubyOptDefaultValue)

		envMap[rubyOptEnvVar] = rubyOptDefaultValue
	}

	return envMap
}

func (r *Ruby) CreateTagsMap() (map[string]string, error) {
	tags := make(map[string]string)
	tags["language"] = r.Name()

	// Create plan directory if it doesn't exist
	if err := os.MkdirAll(constants.PlanDirectory, 0755); err != nil {
		return nil, fmt.Errorf("failed to create plan directory: %w", err)
	}

	// Create a temporary file for the Ruby script output
	tempFile := constants.RubyEnvOutputPath
	defer func() { _ = os.Remove(tempFile) }()

	// Execute the embedded Ruby script to get runtime tags
	args := []string{"exec", "ruby", "-e", rubyEnvScript, tempFile}
	if err := r.executor.Run(context.Background(), "bundle", args, nil); err != nil {
		return nil, fmt.Errorf("failed to execute Ruby script: %w", err)
	}

	// Read the JSON output from the temp file
	fileContent, err := os.ReadFile(tempFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read Ruby script output file: %w", err)
	}

	// Parse the JSON output
	var rubyTags map[string]string
	if err := json.Unmarshal(fileContent, &rubyTags); err != nil {
		return nil, fmt.Errorf("failed to parse runtime tags JSON: %w, tried to parse: %s", err, string(fileContent))
	}

	// Merge the tags from the Ruby output
	maps.Copy(tags, rubyTags)

	return tags, nil
}

func (r *Ruby) DetectFramework() (framework.Framework, error) {
	frameworkName := settings.GetFramework()
	platformEnv := r.GetPlatformEnv()

	var fw framework.Framework
	switch frameworkName {
	case "rspec":
		fw = framework.NewRSpec()
	case "minitest":
		fw = framework.NewMinitest()
	default:
		return nil, fmt.Errorf("framework '%s' is not supported by platform 'ruby'", frameworkName)
	}

	fw.SetPlatformEnv(platformEnv)
	return fw, nil
}

func (r *Ruby) SanityCheck() error {
	args := []string{"info", requiredGemName}
	output, err := r.executor.CombinedOutput(context.Background(), "bundle", args, nil)
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			return fmt.Errorf("bundle info datadog-ci command failed: %w", err)
		}
		return fmt.Errorf("bundle info datadog-ci command failed: %s", message)
	}

	requiredVersion, err := version.Parse(requiredGemMinVersion)
	if err != nil {
		return err
	}

	gemVersion, err := parseBundlerInfoVersion(string(output), requiredGemName)
	if err != nil {
		return err
	}

	if gemVersion.Compare(requiredVersion) < 0 {
		return fmt.Errorf("datadog-ci gem version %s is lower than required >= %s", gemVersion.String(), requiredVersion.String())
	}

	return nil
}

// bundlerInfoRegex matches bundler info output format: "  * gem-name (version [hash])"
// Captures: 1=gem-name, 2=version
var bundlerInfoRegex = regexp.MustCompile(`^\s*\*\s+(\S+)\s+\((\d+\.\d+\.\d+)`)

func parseBundlerInfoVersion(output, gemName string) (version.Version, error) {
	for line := range strings.SplitSeq(output, "\n") {
		matches := bundlerInfoRegex.FindStringSubmatch(line)
		if matches == nil {
			continue
		}

		matchedGem := matches[1]
		if matchedGem != gemName {
			continue
		}

		versionString := matches[2]
		parsed, err := version.Parse(versionString)
		if err != nil {
			return version.Version{}, fmt.Errorf("failed to parse version from bundle info output: %w", err)
		}

		return parsed, nil
	}

	return version.Version{}, fmt.Errorf("unable to find datadog-ci gem version in bundle info output")
}
