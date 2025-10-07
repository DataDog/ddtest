package platform

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"maps"
	"os/exec"

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

	// Execute the embedded Ruby script to get runtime tags
	cmd := exec.Command("bundle", "exec", "ruby", "-e", rubyEnvScript)
	output, err := r.executor.CombinedOutput(cmd)
	if err != nil {
		return nil, fmt.Errorf("failed to execute Ruby script: %w", err)
	}

	// Parse the JSON output directly
	var rubyTags map[string]string
	if err := json.Unmarshal(output, &rubyTags); err != nil {
		return nil, fmt.Errorf("failed to parse runtime tags JSON: %w", err)
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
	default:
		return nil, fmt.Errorf("framework '%s' is not supported by platform 'ruby'", frameworkName)
	}
}
