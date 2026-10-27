// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package java onboards Maven projects. It is selected by the commands that
// support Java and is independent of the JavaScript onboarding and testdrive
// code paths.
package java

import (
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/environment"
	"github.com/DataDog/ddtest/internal/maven"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/settings"
)

// Name is the --platform value that selects Java.
const Name = "java"

//go:embed instructions.md
var instructions string

var frameworkDisplayNames = map[string]string{
	"junit4": "JUnit 4", "junit5": "JUnit 5", "testng": "TestNG", "spock": "Spock",
	"cucumber-java": "Cucumber JVM", "karate": "Karate", "scalatest": "ScalaTest", "munit": "Scala MUnit",
}

// Selected reports whether a command should use Java. --platform java or a
// Maven framework in --framework selects it. Without either, a root pom.xml
// selects Java only when no other platform's project files exist, so
// repositories that also contain, for example, a JavaScript project keep
// their existing default.
func Selected() (bool, error) {
	platformHint, frameworkHint := settings.GetPlatform(), settings.GetFramework()
	switch {
	case platformHint == Name:
		if frameworkHint != "" && !slices.Contains(maven.FrameworkNames, frameworkHint) {
			return false, fmt.Errorf("framework '%s' is not supported by platform '%s'", frameworkHint, Name)
		}
		return true, nil
	case platformHint != "":
		return false, nil
	case frameworkHint != "":
		return slices.Contains(maven.FrameworkNames, frameworkHint), nil
	}
	info, err := os.Stat("pom.xml")
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect pom.xml: %w", err)
	}
	if info.IsDir() {
		return false, nil
	}
	_, err = platform.DetectPlatform()
	return errors.Is(err, platform.ErrNoPlatform), nil
}

// project summarizes what onboarding found without running Maven.
type project struct {
	frameworks []string
	// detectionError explains why the reactor could not be read completely.
	detectionError error
	workflows      []string
	configured     []string
	workflowError  error
}

func inspect(root string) project {
	var result project
	if hint := settings.GetFramework(); hint != "" {
		result.frameworks = []string{hint}
	} else if projects, err := maven.Projects(root); err != nil {
		result.detectionError = err
	} else {
		result.frameworks = maven.Frameworks(projects)
	}
	result.workflows, result.configured, result.workflowError = environment.NewGitHub().FindTestWorkflowsMatching(root, Name, func(args []string) bool {
		return maven.IsTestCommand(args[0], args[1:])
	})
	return result
}

func (p project) configuredEverywhere() bool {
	return p.workflowError == nil && len(p.workflows) > 0 && len(p.configured) == len(p.workflows)
}

func (p project) summary() string {
	var names []string
	for _, framework := range p.frameworks {
		names = append(names, frameworkDisplayNames[framework])
	}
	switch {
	case p.detectionError != nil:
		return fmt.Sprintf("DDTest found a Maven project, but could not read its reactor: %s. Test framework detection is incomplete.", p.detectionError)
	case len(names) == 0:
		return "DDTest found a Maven project without a supported test framework dependency. Dependencies inherited from parents or BOMs outside the repository are not inspected."
	}
	return fmt.Sprintf("DDTest found a Maven project with %s.", strings.Join(names, ", "))
}

func (p project) ciSummary() string {
	switch {
	case p.workflowError != nil:
		return fmt.Sprintf("CI discovery is incomplete: %s. The guidance below is manual.", p.workflowError)
	case len(p.workflows) == 0:
		return "CI discovery is incomplete: no GitHub Actions workflow runs a Maven test phase. The guidance below is manual."
	case p.configuredEverywhere():
		return "Datadog Test Optimization already appears in every detected Maven test workflow:\n  - " + strings.Join(p.workflows, "\n  - ")
	}
	return "Maven test workflow(s):\n  - " + strings.Join(p.workflows, "\n  - ")
}

func renderInstructions() string {
	text := strings.ReplaceAll(instructions, "__CONNECT_DATADOG__", strings.TrimSpace(onboard.ConnectDatadogInstructions))
	return onboard.PinSetupAction(text)
}

// Instructions prints the manual GitHub Actions setup for the Maven project in root.
func Instructions(output io.Writer, root string) error {
	found := inspect(root)
	text := found.summary() + "\n\n" + found.ciSummary() + "\n"
	if !found.configuredEverywhere() {
		text += "\n" + strings.TrimSpace(renderInstructions()) + "\n"
	}
	_, err := io.WriteString(output, text)
	return err
}

// Start returns the onboarding result for a coding agent. Maven setup is
// manual, so the next action prints the instructions.
func Start(output io.Writer, root string) error {
	found := inspect(root)
	reason := found.summary() + " " + found.ciSummary()
	if found.configuredEverywhere() {
		return onboard.WriteAgentResult(output, onboard.AgentResult{
			Status: "ready", Phase: "onboarding", Code: "setup_present", NextAction: "report_result",
			Reason: reason + " ddtest cannot run Java tests locally, so the setup is verified by the next CI run.",
		})
	}
	command := onboard.DDTestCommand("onboard", "--instructions", "--platform", Name)
	if len(command) == 0 {
		return errors.Join(errors.New(strings.TrimSuffix(onboard.TransientExecutableReason, ".")), onboard.WriteAgentResult(output, onboard.AgentResult{Status: "needs_input", Phase: "onboarding", Code: "transient_executable", Reason: onboard.TransientExecutableReason, NextAction: "supply_input"}))
	}
	return onboard.WriteAgentResult(output, onboard.AgentResult{
		Status: "ready", Phase: "onboarding", Code: "manual_setup_required", NextAction: "run_command", NextCommand: command,
		Reason: reason,
		Checks: []onboard.WorkflowCheck{
			{Name: "automatic_setup", Status: "not supported", Reason: "ddtest does not edit Maven workflows."},
			{Name: "local_validation", Status: "not supported", Reason: "ddtest testdrive does not run Java tests."},
		},
		Instruction: "Run next_command and apply its GitHub Actions setup to every workflow job that runs Maven tests. Keep the existing Maven commands, profiles, JVM options and matrices unchanged, and make no other edits without the user's approval. " + onboard.ReadWholeInstruction,
	})
}
