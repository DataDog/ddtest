// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package onboard prints the smallest supported Test Optimization setup.
package onboard

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/platform"
)

// Run detects the first supported onboarding path and prints its instructions.
func Run(output io.Writer) error { return run(output, nil) }

// agentHandoff selects the setup instructions for a coding agent whose
// validation ddtest hands off separately.
type agentHandoff struct {
	// site is the selected Datadog site; empty selects the default.
	site string
}

// run prints the onboarding guidance; a non-nil agent selects the agent's
// setup instructions.
func run(output io.Writer, agent *agentHandoff) error {
	repositoryRoot, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("find repository root: %w", err)
	}
	detectedPlatform, err := platform.DetectPlatform()
	if err != nil {
		return err
	}
	runner, err := detectedPlatform.DetectFramework()
	if err != nil {
		return err
	}
	provider, err := currentCIProvider(repositoryRoot)
	if err != nil {
		return fmt.Errorf("onboard: %w", err)
	}
	files, err := provider.configurationFiles(repositoryRoot)
	if err != nil {
		return fmt.Errorf("onboard: %w", err)
	}
	if len(files) == 0 {
		return fmt.Errorf("onboard: no supported CI provider detected in repository")
	}
	if !languageSupportsCIProvider(detectedPlatform.Name(), provider) {
		return &CIProviderUnsupportedError{Language: detectedPlatform.Name(), Framework: runner.Name(), Provider: provider.displayName()}
	}
	return onboardCI(output, provider, repositoryRoot, detectedPlatform.Name(), runner.Name(), agent)
}

// onboardCI prints the onboarding guidance for the selected CI provider.
func onboardCI(output io.Writer, provider ciProvider, repositoryRoot, language, name string, agent *agentHandoff) error {
	workflows, configured, err := provider.findTestWorkflows(repositoryRoot, language, name)
	if err != nil {
		return err
	}
	var scope *ValidationScope
	var discover, instructionsWhenConfigured, validatesConfigurations bool
	if registered := discoveryLanguageNamed(language); registered != nil {
		discover, instructionsWhenConfigured = registered.onboardingScope(name)
		validatesConfigurations = discover || registered.coordinated()
	}
	if discover {
		discovered, err := DiscoverValidationScope(repositoryRoot, name)
		if err != nil {
			return err
		}
		scope = &discovered
		for _, entry := range discovered.Commands {
			for _, location := range entry.locations {
				if !slices.Contains(workflows, location.Workflow) {
					workflows = append(workflows, location.Workflow)
				}
			}
		}
		slices.Sort(workflows)
	}
	if len(workflows) == 0 {
		if language != "javascript" {
			return fmt.Errorf("onboard could not find a %s workflow that runs %s", provider.displayName(), name)
		}
		_, _ = fmt.Fprintf(output, "CI discovery is incomplete: no %s test workflow was resolved for %s. The guidance below is manual; it does not certify CI. Run ddtest testdrive --framework %s%s --command <your-test-command> --check-only --yes to inspect the local setup.\n\n", provider.displayName(), name, name, ciProviderFollowUpFlag(repositoryRoot))
		_, _ = fmt.Fprintln(output, strings.TrimSpace(instructions(language, name)))
		return nil
	}

	_, _ = fmt.Fprintf(output, "DDTest found %s, %s, and %s.\n", language, name, provider.displayName())
	_, _ = fmt.Fprintln(output)
	_, _ = fmt.Fprintln(output, "Test workflow(s):")
	for _, workflow := range workflows {
		_, _ = fmt.Fprintf(output, "  - %s\n", workflow)
	}

	if len(configured) == len(workflows) && agent == nil {
		_, _ = fmt.Fprintln(output)
		_, _ = fmt.Fprintln(output, "Datadog Test Optimization already appears in every detected test workflow.")
		command := "ddtest testdrive --framework " + name
		// A plain run needs one CI command; with several, --all checks each
		// where testdrive validates configurations.
		if scope, err := DiscoverValidationScope(repositoryRoot, name); validatesConfigurations && err == nil && len(scope.Commands) > 1 {
			command += " --all"
		}
		command += ciProviderFollowUpFlag(repositoryRoot)
		_, _ = fmt.Fprintf(output, "Run `%s` to check the setup locally.\n", command)
		_, _ = fmt.Fprintln(output, "After it finishes, post every `Open report:` link to the user so they can open the local Test Optimization report.")
		if !instructionsWhenConfigured {
			return nil
		}
	}

	_, _ = fmt.Fprintln(output)
	if scope != nil && agent != nil {
		writeAgentValidationScope(output, *scope)
	} else if scope != nil && RedactsCommandKeys(name) {
		WriteValidationScope(output, RedactCypressScope(*scope))
	} else if scope != nil {
		WriteValidationScope(output, *scope)
	}
	text := instructions(language, name)
	if registered := discoveryLanguageNamed(language); agent != nil && registered != nil && registered.agentInstructions(name, agent.site) != "" {
		text = registered.agentInstructions(name, agent.site)
	}
	_, _ = fmt.Fprintln(output, strings.TrimSpace(text))
	return nil
}
