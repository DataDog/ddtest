package platform

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"maps"
	"os"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/settings"
)

//go:embed scripts/ruby_env.rb
var rubyEnvScript string

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
