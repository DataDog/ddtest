// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package onboard prints the smallest supported Test Optimization setup.
package onboard

import (
	"fmt"
	"io"
	"maps"
	"os"
	"slices"

	"github.com/DataDog/ddtest/internal/environment"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/settings"
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
	selected, err := SelectCIProvider(repositoryRoot, settings.GetCIProvider())
	if err != nil {
		return err
	}
	if selected != "" && selected != "github" {
		return nativeInstructions(output, repositoryRoot, selected, detectedPlatform.Name(), runner.Name())
	}
	provider, err := environment.DetectRepositoryCIProvider(repositoryRoot)
	if err != nil {
		return fmt.Errorf("onboard: %w", err)
	}
	switch provider := provider.(type) {
	case *environment.GitHub:
		return onboardGitHub(output, provider, repositoryRoot, detectedPlatform.Name(), runner.Name(), agent)
	default:
		return fmt.Errorf("onboard does not support CI provider %s", provider.Name())
	}
}

func nativeInstructions(output io.Writer, root, provider, language, framework string) error {
	if _, err := fmt.Fprintf(output, "DDTest found %s, %s, and %s.\n", language, framework, provider); err != nil {
		return err
	}
	if language != "javascript" {
		_, err := fmt.Fprintf(output, "Use the existing %s tracer initialization for %s in the %s test job. Run ddtest testdrive --framework %s locally for telemetry-only checks; automatic source edits and controlled CI validation remain JavaScript-only. Store DD_API_KEY in the provider secret store, never in source or chat.\n", language, framework, provider, framework)
		return err
	}
	scope, err := DiscoverValidationScope(root, framework)
	if err != nil {
		return err
	}
	WriteValidationScope(output, scope)
	workflows, err := readWorkflows(root)
	if err != nil {
		return err
	}
	for _, workflow := range workflows {
		for _, jobName := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			job := workflow.Jobs[jobName]
			consumer := false
			for _, step := range job.Steps {
				consumer = consumer || resolveTestStep(root, workflow, job, step, "javascript", framework).Matched
			}
			if !consumer {
				continue
			}
			node := job.Node
			if node == "" {
				node = "unresolved (not inferred from the local machine)"
			}
			_, _ = fmt.Fprintf(output, "CI runtime: %s / %s: Node %s; source: %s\n", workflow.Path, jobName, node, job.NodeSource)
		}
	}
	if !SupportsAutomaticSetup(framework) {
		_, err = fmt.Fprintf(output, "Automatic source edits are not available for %s. Install the same exact dd-trace release in the %s test environment, then initialize the existing test command with NODE_OPTIONS='-r dd-trace/ci/init', preserving existing Node options and hooks. Inspect it with ddtest testdrive --framework %s --ci-provider %s --all --check-only --yes. Application coverage instrumentation remains a separate setup requirement. Expose DD_API_KEY through the provider's secret binding; never paste it into chat.\n", framework, provider, framework, provider)
		return err
	}
	_, err = fmt.Fprintf(output, "Preview setup without executing tests: ddtest onboard --framework %s --ci-provider %s --apply\nAfter reviewing, use --apply --yes to apply the edits and run local validation. For static inspection only: ddtest testdrive --framework %s --ci-provider %s --all --check-only --yes. Configure DD_API_KEY in this provider's secret store; never paste it into chat. No CI jobs or Datadog backend processing are exercised locally.\n", framework, provider, framework, provider)
	return err
}
