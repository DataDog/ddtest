// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	_ "embed"
	"fmt"
	"path/filepath"
	"strings"
)

//go:embed instructions/ruby.md
var rubyInstructions string

// RubyMinitestForkSetup configures the same synchronous transport in CI and
// local validation for test runners that terminate forked children with exit!.
//
//go:embed instructions/ruby_minitest_fork.rb
var RubyMinitestForkSetup string

//go:embed instructions/ruby_minitest_fork.md
var rubyMinitestForkInstructions string

// rubyDiscovery onboards RSpec and Minitest with datadog-ci.
type rubyDiscovery struct{}

func (rubyDiscovery) name() string { return "ruby" }

func (rubyDiscovery) actionLanguage() string { return "ruby" }

func (rubyDiscovery) bootstrap(string) string { return rubyBootstrap }

func (rubyDiscovery) resolvesTestSteps(string) bool { return true }

func (rubyDiscovery) coordinated() bool { return false }

func (rubyDiscovery) frameworks() []string { return []string{"rspec", "minitest"} }

func (rubyDiscovery) resolveStep(root string, workflow ciWorkflow, job runtimeJob, step runtimeStep, framework string) (commandResolution, bool) {
	return resolveRubyStep(root, workflow, job, step, framework), true
}

func (rubyDiscovery) localCommands(root, command, framework string) ([]TestCommand, bool) {
	return resolveRubyStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: command}, framework).Tests, true
}

func (rubyDiscovery) variantValue(workflow ciWorkflow, job runtimeJob, step runtimeStep, row map[string]any, key, value string) (string, bool, error) {
	switch key {
	case "BUNDLE_GEMFILE":
		// Bundler resolves ordinary paths from the test working directory;
		// github.workspace paths instead start at the checkout root.
		relative := strings.NewReplacer("${{ github.workspace }}/", "", "${{github.workspace}}/", "").Replace(value)
		workspacePath := relative != value
		value, err := interpolateMatrix(relative, row)
		if err != nil {
			return "", false, err
		}
		directory := filepath.Clean(stepDirectory(workflow, job, step))
		target := filepath.Join(directory, value)
		if workspacePath {
			target = value
		}
		if !filepath.IsLocal(target) || filepath.IsAbs(value) || strings.ContainsAny(value, "$`~\\\x00") {
			return "", false, fmt.Errorf("BUNDLE_GEMFILE must resolve to a repository-local path: %s", value)
		}
		if workspacePath {
			value, err = filepath.Rel(directory, target)
			if err != nil {
				return "", false, err
			}
			value = filepath.ToSlash(value)
		}
		return value, true, nil
	case "RUBYOPT":
		// Local validation owns the tracer preload; preserve companion Ruby options.
		value = strings.NewReplacer("${{ env.RUBYOPT }}", "", "${{env.RUBYOPT}}", "").Replace(value)
		return strings.TrimSpace(value), true, nil
	}
	return value, false, nil
}

func (rubyDiscovery) skipsUnsupportedNode() bool { return false }

func (rubyDiscovery) instructions(framework string) string {
	text := strings.ReplaceAll(rubyInstructions, "__FRAMEWORK__", framework)
	if framework == "minitest" {
		heading, rest, _ := strings.Cut(text, "\n\n")
		forkSetup := strings.ReplaceAll(rubyMinitestForkInstructions, "__FORK_SETUP__", strings.TrimSpace(RubyMinitestForkSetup))
		text = heading + "\n\n" + forkSetup + "\n\n" + rest
	}
	return text
}

func (rubyDiscovery) onboardingScope(string) (discover, instructionsWhenConfigured bool) {
	return true, true
}

const rubyBootstrap = "The action installs datadog-ci into the CI bundle and exports RUBYOPT=-rbundler/setup -rdatadog/ci/auto_instrument. Preserve RUBYOPT on the RSpec or Minitest step and merge any existing Ruby options. Keep the existing bundle exec or binstub entry point."
