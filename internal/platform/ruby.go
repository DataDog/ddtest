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
	stderr, err := r.executor.StderrOutput(cmd)
	if err != nil {
		return nil, fmt.Errorf("failed to execute Ruby script: %w with output: %s", err, string(stderr))
	}

	// Parse the JSON output from stderr
	var rubyTags map[string]string
	if err := json.Unmarshal(stderr, &rubyTags); err != nil {
		return nil, fmt.Errorf("failed to parse runtime tags JSON: %w, tried to parse: %s", err, string(stderr))
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
