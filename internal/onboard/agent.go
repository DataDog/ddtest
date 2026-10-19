// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/DataDog/ddtest/internal/platform"
)

// AgentResult is the shared, versioned onboarding/testdrive protocol. Commands
// are argv, not shell programs. Failed checks never become agent decisions.
type PrerequisiteFact struct {
	ForCommand    string   `json:"for_command"`
	Directory     string   `json:"directory,omitempty"`
	Argv          []string `json:"argv"`
	ExpectedFiles []string `json:"expected_files"`
}
type SetupFacts struct {
	SchemaVersion  int                 `json:"schema_version"`
	Fingerprint    string              `json:"input_fingerprint"`
	Prerequisites  []PrerequisiteFact  `json:"prerequisites"`
	Configurations []ConfigurationFact `json:"configurations,omitempty"`
}

type AgentResult struct {
	RunID          string          `json:"run_id,omitempty"`
	Plan           *ExecutionPlan  `json:"plan,omitempty"`
	PlanSummary    *PlanSummary    `json:"plan_summary,omitempty"`
	DetailsCommand []string        `json:"details_command,omitempty"`
	InputTemplate  *SetupFacts     `json:"input_template,omitempty"`
	SchemaVersion  int             `json:"schema_version"`
	Status         string          `json:"status"`
	Phase          string          `json:"phase"`
	Code           string          `json:"code,omitempty"`
	Reason         string          `json:"reason,omitempty"`
	Location       string          `json:"location,omitempty"`
	Needs          string          `json:"needs,omitempty"`
	Capabilities   []string        `json:"required_capabilities,omitempty"`
	RequiredInput  []string        `json:"required_input,omitempty"`
	NextAction     string          `json:"next_action,omitempty"`
	NextCommand    []string        `json:"next_command,omitempty"`
	ResumeCommand  []string        `json:"resume_command,omitempty"`
	Instruction    string          `json:"agent_instruction,omitempty"`
	Fingerprint    string          `json:"input_fingerprint,omitempty"`
	Checks         []WorkflowCheck `json:"checks,omitempty"`
	Changes        []SetupChange   `json:"changes,omitempty"`
	FinalResponse  string          `json:"final_response,omitempty"`
	JSONReport     string          `json:"json_report,omitempty"`
	HTMLReport     string          `json:"html_report,omitempty"`
}

// PlanSummary keeps routine handoffs independent of CI matrix size.
type PlanSummary struct {
	Framework      string `json:"framework"`
	Tracer         string `json:"tracer,omitempty"`
	Site           string `json:"site"`
	Initialization string `json:"initialization"`
	Configurations int    `json:"configurations"`
	Unresolved     int    `json:"unresolved"`
}

// WorkflowCheck records an owned check and its command, without unbounded logs.
type WorkflowCheck struct {
	Name    string   `json:"name"`
	Status  string   `json:"status"`
	Command []string `json:"command,omitempty"`
	Reason  string   `json:"reason,omitempty"`
}

func WriteAgentResult(output io.Writer, result AgentResult) error {
	result.SchemaVersion = 2
	return json.NewEncoder(output).Encode(result)
}

// RenderAgentResult gives hosts a structured response or renders its facts
// directly in the terminal. Rendering never asks a model to recompute a verdict.
func RenderAgentResult(output io.Writer, result AgentResult, format string) error {
	if result.Plan != nil {
		plan := result.Plan
		result.PlanSummary = &PlanSummary{Framework: plan.Framework, Tracer: plan.Tracer, Site: plan.Site, Initialization: plan.Initialization, Configurations: len(plan.Scope.Commands), Unresolved: len(plan.Scope.Unresolved)}
		executable, err := os.Executable()
		if err != nil {
			executable = "ddtest"
		}
		result.DetailsCommand = []string{executable, "testdrive", "--report", "--detail", "plan"}
		if result.RunID != "" && result.Status != "complete_locally" && result.Status != "selected_passed" {
			result.DetailsCommand = append(result.DetailsCommand, "--resume", result.RunID)
		}
		result.Plan = nil
	}

	if format == "json" || result.FinalResponse == "" {
		return WriteAgentResult(output, result)
	}
	response := result.FinalResponse
	result.FinalResponse = ""
	if err := WriteAgentResult(output, result); err != nil {
		return err
	}
	_, err := fmt.Fprintf(output, "\nFINAL RESULT — use this factual block as the opening response; no further validation or package/cache audit is needed unless a named check failed.\n\n%s\n", response)
	return err
}

func Start(output io.Writer) error {
	detected, err := platform.DetectPlatform()
	if err != nil {
		return WriteAgentResult(output, AgentResult{Status: "needs_input", Phase: "discovery", Code: "platform_unresolved", Reason: err.Error(), RequiredInput: []string{"selected platform and installed project dependencies"}, NextAction: "supply_input"})
	}
	runner, err := detected.DetectFramework()
	if err != nil {
		return WriteAgentResult(output, AgentResult{Status: "needs_input", Phase: "discovery", Code: "framework_unresolved", Reason: err.Error(), RequiredInput: []string{"framework"}, NextAction: "supply_input"})
	}
	if detected.Name() != "javascript" {
		return Run(output)
	}
	return WriteAgentResult(output, AgentResult{
		Status: "ready", Phase: "onboarding", NextAction: "run_command", NextCommand: OnboardingCommand(runner.Name()),
		Capabilities: []string{"registry metadata and package download network access", "local process execution and loopback listeners", "private temporary storage"},
		Reason:       "Run next_command once with the listed capabilities, then wait. ddtest owns tracer selection, recognized CI edits, testdrive, package exclusions and cleanup. It returns exact blockers for unsupported setup. No Datadog key is required locally. Do not write CI or launch a parallel preflight first.",
	})
}

func OnboardingCommand(frameworks ...string) []string {
	executable, err := os.Executable()
	if err != nil {
		executable = "ddtest"
	}
	framework := "vitest"
	if len(frameworks) > 0 {
		framework = frameworks[0]
	}
	return []string{executable, "onboard", "--framework", framework, "--apply", "--yes"}
}
