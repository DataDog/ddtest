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
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"errors"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/kballard/go-shellquote"
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
	ResumeCommand  []string       `json:"resume_command,omitempty"`
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

type StartOptions struct {
	Format              string
	Site                string
	Tracer              string
	Facts               string
	Platform            string
	Command             string
	Human               bool
	Timeout             string
	AllowReviewedBuilds bool
	// AcceptSubstituteRuntime is forwarded to the onboarding handoff.
	AcceptSubstituteRuntime bool
}

func Start(output io.Writer) error { return StartWithOptions(output, StartOptions{}) }

func StartWithOptions(output io.Writer, options StartOptions) error {
	fail := func(code string, err error) error {
		return errors.Join(err, WriteAgentResult(output, AgentResult{Status: "needs_input", Phase: "discovery", Code: code, Reason: err.Error(), NextAction: "supply_input"}))
	}

	detected, err := platform.DetectPlatform()
	if err != nil {
		return fail("platform_unresolved", err)
	}
	runner, err := detected.DetectFramework()
	if err != nil {
		return fail("framework_unresolved", err)
	}
	if !coordinatedLanguage(detected.Name()) {
		if options.Format != "" {
			err := fmt.Errorf("controlled validation is not available for %s", detected.Name())
			return errors.Join(err, WriteAgentResult(output, AgentResult{Status: "unsupported", Phase: "onboarding", Code: "framework_unsupported", Reason: err.Error(), NextAction: "report_result"}))
		}
		return Run(output)
	}
	if options.Command != "" {
		// onboard --apply validates every discovered configuration, which rejects
		// a single-command override; never hand off a command that cannot run.
		err := fmt.Errorf("--command selects one suite, but onboarding applies and validates every discovered CI configuration")
		return errors.Join(err, WriteAgentResult(output, AgentResult{Status: "needs_input", Phase: "discovery", Code: "invalid_options", Reason: err.Error(), NextAction: "supply_input", RequiredInput: []string{"Omit --command to onboard every discovered configuration, or validate one suite with testdrive --framework " + runner.Name() + " --command <command>."}}))
	}
	command := OnboardingCommand(runner.Name())
	if len(command) == 0 {
		return errors.Join(fmt.Errorf("%s", TransientExecutableReason), WriteAgentResult(output, AgentResult{Status: "needs_input", Phase: "onboarding", Code: "transient_executable", Reason: TransientExecutableReason, NextAction: "supply_input"}))
	}
	handoffFormat := options.Format
	if handoffFormat == "" && !options.Human {
		handoffFormat = "agent"
	}
	for _, flag := range []struct{ name, value string }{{"format", handoffFormat}, {"site", options.Site}, {"tracer-version", options.Tracer}, {"facts", options.Facts}, {"platform", options.Platform}, {"command", options.Command}, {"timeout", options.Timeout}} {
		if flag.value != "" {
			command = append(command, "--"+flag.name, flag.value)
		}
	}
	if options.AllowReviewedBuilds {
		command = append(command, "--allow-reviewed-builds")
	}
	if options.AcceptSubstituteRuntime {
		command = append(command, "--accept-substitute-runtime")
	}
	notice := ExecutableFallbackNotice()
	if options.Human {
		text := humanOnboardingHandoff(runner.Name(), command)
		if notice != "" {
			text += notice + "\n"
		}
		_, err := io.WriteString(output, text)
		return err
	}
	return WriteAgentResult(output, AgentResult{
		Status: "ready", Phase: "onboarding", NextAction: "run_command", NextCommand: command,
		Capabilities: []string{"registry metadata and package download network access", "local process execution and loopback listeners", "private temporary storage"},
		Reason:       strings.TrimSpace("Run next_command once in the foreground with the listed capabilities, then wait for its exit. Do not use nohup, shell background jobs, sleeps or process-name polling. If the host returns a task/session ID, wait with that host tool. " + AgentScope + " Finish any edits a result asks for before launching validation. While it runs, only wait: do not edit repository files or run parallel cleanup/checks. Edits invalidate the snapshot and require another full run. ddtest owns tracer selection, recognized CI edits, testdrive, package exclusions and cleanup. It returns exact blockers for unsupported setup. No Datadog key is required locally. Do not write CI or launch a parallel preflight first. " + notice),
	})
}

// AgentScope bounds what a coding agent changes during onboarding. Trials
// showed agents editing builds, manifests and test names to satisfy checks,
// and stopping a whole run to ask before an install ddtest itself prescribed.
const AgentScope = "Edit CI workflow and ignore files only where a result asks for it. " + PrescribedEditInstruction + " Any edit ddtest did not prescribe needs the user's approval first. Never rename tests, rewrite CI or package scripts so ddtest can parse them, disable an instrumentation ddtest did not prescribe disabling, or download a runtime; report the blocker instead. CI rows below Node 18, the oldest dd-trace major ddtest supports, are skipped by ddtest itself and reported as not tried; that needs no user approval. Do not inspect the ddtest binary. " + ReadWholeInstruction

// PrescribedEditInstruction lets the agent make the exact non-CI edits ddtest
// needs for validation (dev dependencies, package scripts, coverage config)
// without stopping to ask: the final report lists them with a revert.
const PrescribedEditInstruction = "Make the edits ddtest prescribes for validation, in a result or in onboard --instructions (dev dependencies through the exact install command, package scripts, coverage config and coverage.edit), exactly as given and without asking: do not modify or extend them. The final report lists every change outside CI and ignore files with a revert command."

// ReadWholeInstruction repeats in every result: trials truncated results with
// head/tail and missed the next step.
const ReadWholeInstruction = "Each result is one bounded JSON document: read it whole, without head, tail or other truncation."

// FinalResponseInstruction keeps the agent's answer factual: trials showed
// paraphrases turning an incomplete verdict into a completion claim.
const FinalResponseInstruction = "Begin your answer with final_response exactly, as its first block, and add context only after it. When it starts with NOT COMPLETE, do not call onboarding complete or successful, and do not record that result as expected or final in notes or memory."

// humanOnboardingHandoff points at the preview, which writes nothing, before
// the command that applies the edits and runs local validation.
func humanOnboardingHandoff(framework string, command []string) string {
	preview := slices.DeleteFunc(slices.Clone(command), func(arg string) bool { return arg == "--yes" })
	return fmt.Sprintf("Detected %s. Preview the recognized CI setup first; this writes nothing:\n  %s\nThen apply it and validate it locally:\n  %s\n", framework, shellquote.Join(preview...), shellquote.Join(command...))
}

// A go-run executable disappears after the command exits. It cannot be used in
// a protocol handoff; require a persistent build unless one is already on PATH.
const TransientExecutableReason = "The temporary go run executable cannot be reused. Run make build in the ddtest source checkout, then invoke that persistent ddtest binary to continue."

func persistentExecutable(path string) bool {
	if filepath.Base(filepath.Dir(path)) != "exe" {
		return true
	}
	for directory := filepath.Dir(path); filepath.Dir(directory) != directory; directory = filepath.Dir(directory) {
		if strings.HasPrefix(filepath.Base(directory), "go-build") {
			return false
		}
	}
	return true
}

func DDTestCommand(args ...string) []string {
	executable, _ := ddtestExecutable()
	if executable == "" {
		return nil
	}
	return append([]string{executable}, args...)
}

// ddtestExecutable prefers the running executable. Only a temporary go run
// binary falls back to ddtest on PATH, which may be a different version.
func ddtestExecutable() (path string, fromPATH bool) {
	executable, err := os.Executable()
	if err == nil && persistentExecutable(executable) {
		return executable, false
	}
	executable, err = exec.LookPath("ddtest")
	if err != nil || !persistentExecutable(executable) {
		return "", false
	}
	return executable, true
}

// ExecutableFallbackNotice says when handoff commands use ddtest from PATH
// instead of the running executable; empty otherwise.
func ExecutableFallbackNotice() string {
	if executable, fromPATH := ddtestExecutable(); fromPATH {
		return "This executable is temporary (go run), so commands use ddtest from PATH (" + executable + "), which may be a different version."
	}
	return ""
}

func OnboardingCommand(frameworks ...string) []string {
	framework := "vitest"
	if len(frameworks) > 0 {
		framework = frameworks[0]
	}
	return DDTestCommand("onboard", "--framework", framework, "--apply", "--yes")
}
