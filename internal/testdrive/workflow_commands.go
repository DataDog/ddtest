// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"strings"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/settings"
)

// workflowCommand preserves the options needed to restart this operation.
func (t *Testdrive) workflowCommand(options WorkflowOptions) []string {
	name := t.framework.Name()
	command := onboard.OnboardingCommand(name)
	if len(command) == 0 {
		return nil
	}
	if !options.Apply {
		command = onboard.DDTestCommand("testdrive", "--framework", name, "--agent", "--yes")
		if t.allConfigurations {
			command = append(command, "--all")
		}
		if override := strings.TrimSpace(settings.GetCommand()); !t.allConfigurations && override != "" {
			command = append(command, "--command", override)
		}
	}
	if options.Format == "json" {
		command = append(command, "--format", "json")
	} else if options.Apply && options.Format == "agent" {
		command = append(command, "--agent")
	}

	command = append(command, t.workflowExecutionArguments()...)
	if options.ExplicitVersion {
		command = append(command, "--tracer-version", t.tracerVersion)
	}
	if options.FactsFile != "" {
		command = append(command, "--facts", options.FactsFile)
	}
	if selectedPlatform := settings.GetPlatform(); selectedPlatform != "" {
		command = append(command, "--platform", selectedPlatform)
	}
	if options.Apply && settings.GetCommand() != "" {
		command = append(command, "--command", settings.GetCommand())
	}
	if options.AllowDirty {
		command = append(command, "--allow-dirty")
	}
	if options.Site != "" {
		command = append(command, "--site", options.Site)
	}
	return command
}

func (t *Testdrive) workflowValidationResume(executable, name, selected string, options WorkflowOptions) []string {
	command := []string{executable, "testdrive", "--framework", name, "--all", "--agent", "--tracer-version", selected, "--yes"}
	if options.Format == "json" {
		command = append(command, "--format", "json")
	}
	for _, flag := range []struct{ name, value string }{{"ci-provider", onboard.ActiveCIProvider()}, {"facts", options.FactsFile}, {"site", options.Site}, {"platform", settings.GetPlatform()}, {"command", settings.GetCommand()}} {
		if flag.value != "" {
			command = append(command, "--"+flag.name, flag.value)
		}
	}
	command = append(command, onboard.CIStatementArguments()...)
	return append(command, t.workflowExecutionArguments()...)
}

// workflowExecutionArguments keeps execution policy identical on restart and resume.
func (t *Testdrive) workflowExecutionArguments() []string {
	var arguments []string
	if t.commandTimeout > 0 {
		arguments = append(arguments, "--timeout", t.commandTimeout.String())
	}
	if t.allowReviewedBuilds {
		arguments = append(arguments, "--allow-reviewed-builds")
	}
	if t.acceptSubstituteRuntime {
		arguments = append(arguments, AcceptSubstituteRuntimeFlag)
	}
	return arguments
}
