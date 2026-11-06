// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/kballard/go-shellquote"
)

// AgentResult is the shared, versioned onboarding/testdrive protocol. Commands
// are argv, not shell programs. Failed checks never become agent decisions.
type AgentResult struct {
	RunID          string         `json:"run_id,omitempty"`
	Plan           *ExecutionPlan `json:"plan,omitempty"`
	PlanSummary    *PlanSummary   `json:"plan_summary,omitempty"`
	DetailsCommand []string       `json:"details_command,omitempty"`
	InputTemplate  *SetupFacts    `json:"input_template,omitempty"`
	SchemaVersion  int            `json:"schema_version"`
	Status         string         `json:"status"`
	Phase          string         `json:"phase"`
	Code           string         `json:"code,omitempty"`
	Reason         string         `json:"reason,omitempty"`
	Location       string         `json:"location,omitempty"`
	Needs          string         `json:"needs,omitempty"`
	Capabilities   []string       `json:"required_capabilities,omitempty"`
	RequiredInput  []string       `json:"required_input,omitempty"`
	NextAction     string         `json:"next_action,omitempty"`
	NextCommand    []string       `json:"next_command,omitempty"`
	// SetupCommands are the argv commands, run in order from the repository
	// root, that prepare dependencies before resume_command; next_command is
	// the first of them.
	SetupCommands [][]string `json:"setup_commands,omitempty"`
	ResumeCommand []string   `json:"resume_command,omitempty"`
	// AcceptResumeCommand is resume_command plus a decision only the user may
	// make, such as accepting a substitute runtime.
	AcceptResumeCommand []string        `json:"accept_resume_command,omitempty"`
	Instruction         string          `json:"agent_instruction,omitempty"`
	Fingerprint         string          `json:"input_fingerprint,omitempty"`
	Checks              []WorkflowCheck `json:"checks,omitempty"`
	SetupDiff           string          `json:"setup_diff,omitempty"`
	UndoInstructions    string          `json:"undo_instructions,omitempty"`
	Changes             []SetupChange   `json:"changes,omitempty"`
	FinalResponse       string          `json:"final_response,omitempty"`
	JSONReport          string          `json:"json_report,omitempty"`
	HTMLReport          string          `json:"html_report,omitempty"`
}

// PlanSummary keeps routine handoffs independent of CI matrix size.
type PlanSummary struct {
	UnsupportedFeatures map[string]string `json:"unsupported_features,omitempty"`
	KnownLimitations    map[string]string `json:"known_limitations,omitempty"`
	Features            []string          `json:"feature_scenarios,omitempty"`
	Framework           string            `json:"framework"`
	Tracer              string            `json:"tracer,omitempty"`
	Site                string            `json:"site"`
	Initialization      string            `json:"initialization"`
	Configurations      int               `json:"configurations"`
	Unresolved          int               `json:"unresolved"`
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
		result.PlanSummary = &PlanSummary{Framework: plan.Framework, Tracer: plan.Tracer, Site: plan.Site, Initialization: plan.Initialization, Configurations: len(plan.Scope.Commands), Unresolved: len(plan.Scope.Unresolved), Features: plan.Features, UnsupportedFeatures: plan.UnsupportedFeatures, KnownLimitations: plan.KnownLimitations}
		result.DetailsCommand = DDTestCommand("testdrive", "--report", "--detail", "plan")
		if format == "json" && len(result.DetailsCommand) > 0 {
			result.DetailsCommand = append(result.DetailsCommand, "--format", "json")
		}
		if len(result.DetailsCommand) > 0 && result.RunID != "" && result.Status != "complete_locally" && result.Status != "selected_passed" {
			result.DetailsCommand = append(result.DetailsCommand, "--resume", result.RunID)
		}
		result.Plan = nil
	}

	if result.FinalResponse != "" && !strings.Contains(result.Instruction, FinalResponseInstruction) {
		// First, so it is read before the rest of the instruction.
		result.Instruction = strings.TrimSpace(FinalResponseInstruction + " " + result.Instruction)
	}
	if !strings.Contains(result.Instruction, ReadWholeInstruction) && !strings.Contains(result.Reason, ReadWholeInstruction) {
		result.Instruction = strings.TrimSpace(result.Instruction + " " + ReadWholeInstruction)
	}
	if format == "verbose" {
		return writeAgentText(output, result)
	}
	return WriteAgentResult(output, result)
}

// writeAgentText renders a result for a person reading the terminal. Hosts
// select the agent or json format to receive the structured document.
func writeAgentText(output io.Writer, result AgentResult) error {
	var text strings.Builder
	status := result.Status
	if result.Code != "" {
		status += " (" + result.Code + ")"
	}
	_, _ = fmt.Fprintf(&text, "\nResult: %s, phase %s\n", status, result.Phase)
	for _, line := range []struct{ label, value string }{{"Reason", result.Reason}, {"Location", result.Location}, {"Needs", result.Needs}} {
		if line.value != "" {
			_, _ = fmt.Fprintf(&text, "%s: %s\n", line.label, line.value)
		}
	}
	for _, input := range result.RequiredInput {
		_, _ = fmt.Fprintf(&text, "Required input: %s\n", input)
	}
	for _, command := range result.SetupCommands {
		_, _ = fmt.Fprintf(&text, "Setup: %s\n", shellquote.Join(command...))
	}
	for _, change := range result.Changes {
		_, _ = fmt.Fprintf(&text, "Changed: %s\n", change.Path)
	}
	for _, check := range result.Checks {
		line := check.Name + ": " + check.Status
		if check.Reason != "" {
			line += " — " + check.Reason
		}
		_, _ = fmt.Fprintf(&text, "Check %s\n", line)
	}
	if result.InputTemplate != nil {
		data, err := json.MarshalIndent(result.InputTemplate, "", "  ")
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(&text, "Facts template (complete it, save it outside the checkout, then pass --facts <path>):\n%s\n", data)
	}
	for _, line := range []struct {
		label   string
		command []string
	}{{"Next", result.NextCommand}, {"Resume", result.ResumeCommand}, {"After user approval", result.AcceptResumeCommand}, {"Details", result.DetailsCommand}} {
		if len(line.command) > 0 {
			_, _ = fmt.Fprintf(&text, "%s: %s\n", line.label, shellquote.Join(line.command...))
		}
	}
	for _, report := range []struct{ label, path string }{{"JSON report", result.JSONReport}, {"HTML report", result.HTMLReport}} {
		if report.path != "" {
			_, _ = fmt.Fprintf(&text, "%s: %s\n", report.label, report.path)
		}
	}
	if result.FinalResponse != "" {
		_, _ = fmt.Fprintf(&text, "\nSummary:\n%s\n", result.FinalResponse)
	}
	_, err := io.WriteString(output, text.String())
	return err
}

// StandaloneVerdictInstruction states what a standalone validation's verdict
// certifies, after FinalResponseInstruction: trials called a pytest PASSED,
// and an incomplete report beside a passing local validation, done.
func StandaloneVerdictInstruction(framework string) string {
	return framework + " validation is standalone: its passing verdict is PASSED, never COMPLETE LOCALLY, and only COMPLETE LOCALLY certifies onboarding as complete. Report a PASSED verdict as validated locally, in final_response's words; do not call onboarding done, complete or successful."
}
