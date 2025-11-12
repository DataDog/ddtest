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

//go:embed scripts/ruby_env.rb
var rubyEnvScript string

const (
	requiredGemName       = "datadog-ci"
	requiredGemMinVersion = "1.23.0"
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

	switch frameworkName {
	case "rspec":
		return framework.NewRSpec(), nil
	case "minitest":
		return framework.NewMinitest(), nil
	default:
		return nil, fmt.Errorf("framework '%s' is not supported by platform 'ruby'", frameworkName)
	}
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

func parseBundlerInfoVersion(output, gemName string) (version.Version, error) {
	for line := range strings.SplitSeq(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if !strings.Contains(trimmed, gemName) {
			continue
		}

		start := strings.Index(trimmed, "(")
		end := strings.Index(trimmed, ")")
		if start == -1 || end == -1 || end <= start+1 {
			continue
		}

		versionToken := strings.TrimSpace(trimmed[start+1 : end])
		if versionToken == "" {
			continue
		}

		fields := strings.Fields(versionToken)
		versionString := fields[0]
		if !version.IsValid(versionString) {
			return version.Version{}, fmt.Errorf("unexpected version format in bundle info output: %q", versionToken)
		}

		parsed, err := version.Parse(versionString)
		if err != nil {
			return version.Version{}, fmt.Errorf("failed to parse version from bundle info output: %w", err)
		}

		return parsed, nil
	}

	return version.Version{}, fmt.Errorf("unable to find datadog-ci gem version in bundle info output")
}
