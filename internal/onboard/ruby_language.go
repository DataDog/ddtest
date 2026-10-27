// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	_ "embed"
	"fmt"
	"path/filepath"
	"regexp"
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

// rubyAgentInstructions are the CI setup steps for a coding agent; ddtest
// hands off the validation command separately.
//
//go:embed instructions/ruby_agent.md
var rubyAgentInstructions string

// rubyDiscovery onboards RSpec and Minitest with datadog-ci.
type rubyDiscovery struct{}

func (rubyDiscovery) name() string { return "ruby" }

func (rubyDiscovery) actionLanguage() string { return "ruby" }

func (rubyDiscovery) bootstrap(string) string { return rubyBootstrap }

func (rubyDiscovery) resolvesTestSteps(string) bool { return true }

// Agents apply Ruby CI setup by hand; validation then runs through the
// coordinator.
func (rubyDiscovery) coordinated() bool { return true }

func (rubyDiscovery) automaticCISetup() bool { return false }

// Every discovered test step runs after a complete Datadog action or
// explicitly skips it; at least one stays instrumented. A complete action has
// an API key, an exact tracer release and the selected site.
func (rubyDiscovery) ciSetupConfigured(root, framework, site string) bool {
	instrumented := false
	for _, job := range RubyCIConfiguration(root, framework).Jobs {
		if job.Status == "excluded" {
			continue
		}
		if job.Action == "" || !rubyActionComplete(job, site) {
			return false
		}
		instrumented = true
	}
	return instrumented
}

// exactRubyRelease is a single RubyGems release, never a requirement or "latest".
var exactRubyRelease = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:\.[0-9A-Za-z]+)*$`)

func rubyActionComplete(job RuntimeFinding, site string) bool {
	if strings.TrimSpace(job.setupInputs["api_key"]) == "" || !exactRubyRelease.MatchString(job.TracerRequested) {
		return false
	}
	configured := job.setupInputs["site"]
	if configured == "" {
		configured = defaultDatadogSite
	}
	return site == "" || configured == site
}

const defaultDatadogSite = "datadoghq.com"

func (rubyDiscovery) agentInstructions(framework, site string) string {
	siteInput := ""
	if site != "" && site != defaultDatadogSite {
		siteInput = ", `site: " + site + "` (the selected Datadog site)"
	}
	text := strings.NewReplacer("__FRAMEWORK__", framework, "__SITE_INPUT__", siteInput).Replace(rubyAgentInstructions)
	return withMinitestForkSetup(text, framework)
}

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
		inherited := strings.NewReplacer("${{ env.RUBYOPT }}", "", "${{env.RUBYOPT}}", "").Replace(value)
		if inherited != value && (job.Env["RUBYOPT"] != "" || workflow.Env["RUBYOPT"] != "") {
			return "", false, fmt.Errorf("RUBYOPT extends workflow or job Ruby options; validate with those options explicitly")
		}
		value = inherited
		return strings.TrimSpace(value), true, nil
	}
	return value, false, nil
}

func (rubyDiscovery) skipsUnsupportedNode() bool { return false }

func (rubyDiscovery) instructions(framework string) string {
	return withMinitestForkSetup(strings.ReplaceAll(rubyInstructions, "__FRAMEWORK__", framework), framework)
}

// withMinitestForkSetup inserts the forked-test transport setup after the
// heading of Minitest instructions.
func withMinitestForkSetup(text, framework string) string {
	if framework != "minitest" {
		return text
	}
	heading, rest, _ := strings.Cut(text, "\n\n")
	forkSetup := strings.ReplaceAll(rubyMinitestForkInstructions, "__FORK_SETUP__", strings.TrimSpace(RubyMinitestForkSetup))
	return heading + "\n\n" + forkSetup + "\n\n" + rest
}

func (rubyDiscovery) onboardingScope(string) (discover, instructionsWhenConfigured bool) {
	return true, true
}

const rubyBootstrap = "The action installs datadog-ci into the CI bundle and exports RUBYOPT=-rbundler/setup -rdatadog/ci/auto_instrument. Preserve RUBYOPT on the RSpec or Minitest step and merge any existing Ruby options. Keep the existing bundle exec or binstub entry point."
