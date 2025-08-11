package platform

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"

	"github.com/DataDog/datadog-test-runner/internal/ext"
	"github.com/DataDog/datadog-test-runner/internal/framework"
	"github.com/DataDog/datadog-test-runner/internal/settings"
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

	// Execute the embedded Ruby script to write tags to file
	cmd := exec.Command("bundle", "exec", "ruby", "-e", rubyEnvScript)
	if _, err := r.executor.CombinedOutput(cmd); err != nil {
		return nil, fmt.Errorf("failed to execute Ruby script: %w", err)
	}

	// Read the tags from the generated file
	data, err := os.ReadFile(".dd/runtime_tags.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read runtime tags file: %w", err)
	}

	// Parse the JSON from the file
	var rubyTags map[string]string
	if err := json.Unmarshal(data, &rubyTags); err != nil {
		return nil, fmt.Errorf("failed to parse runtime tags file: %w", err)
	}

	// Merge the tags from the file
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
