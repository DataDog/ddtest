// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"

	"github.com/DataDog/ddtest/internal/testdrive/intake"
)

type verdict struct {
	Status          string   `json:"status"`
	Reason          string   `json:"reason"`
	DifferenceCount int      `json:"difference_count,omitempty"`
	Differences     []string `json:"differences,omitempty"`
}

type featureResult struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Reason string `json:"reason"`
}

// Detailed results are transient inputs to validation, never report contents.
type validationRun struct {
	root         string
	Name         string
	Command      string
	Instrumented bool
	ProbeMode    string
	ExitCode     int
	ResultError  string
	Tests        []jestTest
	SuiteErrors  []string
	Facts        intake.Facts
}

type testCounts struct {
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
	Other   int `json:"other"`
}

type runSummary struct {
	Name            string     `json:"name"`
	Command         string     `json:"command,omitempty"`
	Instrumented    bool       `json:"instrumented"`
	ProbeMode       string     `json:"probe_mode,omitempty"`
	ExitCode        *int       `json:"exit_code"`
	ResultError     string     `json:"result_error,omitempty"`
	TestCounts      testCounts `json:"test_counts"`
	SuiteErrorCount int        `json:"suite_error_count"`
	TestEventCount  int        `json:"test_event_count"`
}

func (r validationRun) summary() runSummary {
	summary := runSummary{Name: r.Name, Command: r.Command, Instrumented: r.Instrumented,
		ProbeMode: r.ProbeMode, ResultError: reportText(r.ResultError),
		SuiteErrorCount: len(r.SuiteErrors), TestEventCount: r.Facts.TestEventCount}
	// A setup failure before execution must not look like a successful command.
	if r.Command != "" {
		exitCode := r.ExitCode
		summary.ExitCode = &exitCode
	}
	for _, test := range r.Tests {
		switch test.Status {
		case "passed":
			summary.TestCounts.Passed++
		case "failed":
			summary.TestCounts.Failed++
		case "pending", "skipped", "todo", "disabled":
			summary.TestCounts.Skipped++
		default:
			summary.TestCounts.Other++
		}
	}
	return summary
}

type validationResult struct {
	Session          string                `json:"session"`
	CompletedAt      time.Time             `json:"completed_at"`
	CheckOnly        bool                  `json:"check_only"`
	ChecksPassed     bool                  `json:"checks_passed"`
	LocalSuccess     bool                  `json:"local_success"`
	Preflight        *jestPreflight        `json:"preflight,omitempty"`
	Selection        *platform.JSSelection `json:"tracer_selection,omitempty"`
	TracerSource     string                `json:"tracer_source,omitempty"`
	CISelection      *verdict              `json:"ci_tracer_selection,omitempty"`
	CIExecution      verdict               `json:"ci_execution"`
	Success          bool                  `json:"success"`
	WorkingDirectory string                `json:"working_directory"`
	Error            string                `json:"error,omitempty"`
	Framework        string                `json:"framework"`
	Tracer           string                `json:"tracer"`
	CIRuntime        *onboard.RuntimeCheck `json:"ci_runtime,omitempty"`
	Compatibility    verdict               `json:"compatibility"`
	Features         []featureResult       `json:"features"`
	Runs             []runSummary          `json:"runs"`
}

const maxReportDifferences = 10

// Bound diagnostics too: install errors can contain entire package-manager logs.
func reportText(value string) string {
	const limit = 1024
	text := []rune(value)
	if len(text) > limit {
		return string(text[:limit]) + "… [truncated]"
	}
	return value
}

const validationFilename = "testdrive.json"

func validationPath(repositoryRoot string) string {
	return filepath.Join(repositoryRoot, constants.PlanDirectory, validationFilename)
}

func finishValidation(output io.Writer, repositoryRoot string, result validationResult) error {
	result.CompletedAt = time.Now().UTC()
	result.WorkingDirectory = repositoryRoot
	result.CIExecution = verdict{Status: "not exercised", Reason: "Local-only validation; CI execution and Datadog backend processing were not exercised."}
	result.LocalSuccess = result.Compatibility.Status == "compatible" && result.Error == ""
	for _, feature := range result.Features {
		result.LocalSuccess = result.LocalSuccess && feature.Status == "passed"
	}
	result.Success = result.LocalSuccess
	result.ChecksPassed = result.Error == "" && result.Preflight != nil && result.Preflight.Verdict.Status == "compatible"
	if result.CISelection != nil {
		result.Success = result.Success && result.CISelection.Status == "compatible"
		result.ChecksPassed = result.ChecksPassed && result.CISelection.Status == "compatible"
	}
	if result.CIRuntime != nil {
		ciOK := result.CIRuntime.Status == "compatible" || result.CIRuntime.Status == "not applicable"
		result.Success = result.Success && ciOK
		result.ChecksPassed = result.ChecksPassed && ciOK
		check := *result.CIRuntime
		check.Reason = reportText(check.Reason)
		check.Jobs = slices.Clone(check.Jobs)
		for i := range check.Jobs {
			job := &check.Jobs[i]
			for _, field := range []*string{&job.Workflow, &job.Job, &job.Command, &job.Resolution, &job.Node, &job.Action, &job.Tracer, &job.TracerRequested, &job.Requirement, &job.Reason} {
				*field = reportText(*field)
			}
		}
		result.CIRuntime = &check
	}
	result.Error = reportText(result.Error)
	result.Compatibility.Reason = reportText(result.Compatibility.Reason)
	result.Compatibility.DifferenceCount = len(result.Compatibility.Differences)
	result.Compatibility.Differences = slices.Clone(result.Compatibility.Differences[:min(len(result.Compatibility.Differences), maxReportDifferences)])
	for i, difference := range result.Compatibility.Differences {
		result.Compatibility.Differences[i] = reportText(difference)
	}
	result.Features = slices.Clone(result.Features)
	for i, feature := range result.Features {
		result.Success = result.Success && feature.Status == "passed"
		result.Features[i].Reason = reportText(feature.Reason)
	}
	if result.CheckOnly {
		result.Success = false
		result.LocalSuccess = false
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("encode validation results: %w", err)
	}
	path := validationPath(repositoryRoot)
	if err := writeValidation(path, append(data, '\n')); err != nil {
		return err
	}

	if result.CIRuntime != nil {
		_, _ = fmt.Fprintf(output, "\nCI runtime compatibility: %s\n%s\n", result.CIRuntime.Status, result.CIRuntime.Reason)
		for _, job := range result.CIRuntime.Jobs {
			label := job.Workflow + " / " + job.Job
			if job.Step != 0 {
				label += fmt.Sprintf(" / step %d", job.Step)
			}
			if job.Node != "" {
				label += " / Node " + job.Node
			}
			_, _ = fmt.Fprintf(output, "  - %s: %s — %s", label, job.Status, job.Reason)
			if job.Tracer != "" {
				_, _ = fmt.Fprintf(output, " (%s; requires %s)", job.Tracer, job.Requirement)
			}
			_, _ = fmt.Fprintln(output)
		}
	}
	if result.CISelection != nil {
		_, _ = fmt.Fprintf(output, "\nCI tracer agreement: %s — %s\n", result.CISelection.Status, result.CISelection.Reason)
	}
	if result.CheckOnly {
		_, _ = fmt.Fprintln(output, "Configuration checks only. Compatibility tests and features were not exercised.")
	}
	_, _ = fmt.Fprintf(output, "\nCompatibility: %s\n%s\n", result.Compatibility.Status, result.Compatibility.Reason)
	for _, difference := range result.Compatibility.Differences {
		_, _ = fmt.Fprintf(output, "  - %s\n", difference)
	}
	if result.Error != "" {
		_, _ = fmt.Fprintf(output, "Validation error: %s\n", result.Error)
	}
	for _, feature := range result.Features {
		_, _ = fmt.Fprintf(output, "Feature %s: %s — %s\n", feature.Name, feature.Status, feature.Reason)
	}
	_, _ = fmt.Fprintf(output, "Tracer: %s · %s\nResults JSON: %s\n", result.Tracer, result.TracerSource, path)
	if result.CheckOnly && result.ChecksPassed {
		return nil
	}
	if !result.Success {
		return fmt.Errorf("validation is incomplete or found an unexpected difference; see %s", path)
	}
	return nil
}

func commandExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	return -1
}

// Write next to the destination and rename only a complete report. Concurrent
// runs use distinct scratch files; the last completed report replaces the old one.
func writeValidation(path string, data []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0755); err != nil {
		return fmt.Errorf("create validation report directory: %w", err)
	}
	file, err := os.CreateTemp(directory, ".testdrive-report-*")
	if err != nil {
		return fmt.Errorf("create validation report: %w", err)
	}
	defer func() { _ = os.Remove(file.Name()) }()
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return fmt.Errorf("write validation report: %w", err)
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return fmt.Errorf("replace validation report: %w", err)
	}
	return nil
}

func compareCISelection(check onboard.RuntimeCheck, localVersion string) *verdict {
	if check.Status == "not applicable" {
		return nil
	}
	result := &verdict{Status: "compatible", Reason: "Every statically resolved instrumented CI entry selects the same exact tracer version as the local selection. Floating selections are checked only at this run's resolution time."}
	if localVersion == "" {
		return &verdict{Status: "inconclusive", Reason: "Local tracer version is unresolved."}
	}
	checked := false
	for _, job := range check.Jobs {
		if job.Status == "excluded" {
			continue
		}
		if job.Tracer == "" {
			result.Status = "inconclusive"
			result.Reason = "Some CI tracer selections could not be resolved; preserve unknown workflow syntax for review."
			continue
		}
		checked = true
		if strings.TrimPrefix(job.Tracer, "dd-trace@") != localVersion {
			return &verdict{Status: "incompatible", Reason: fmt.Sprintf("%s / %s selects %s, but local validation selected dd-trace@%s. Align selections or validate this CI version separately.", job.Workflow, job.Job, job.Tracer, localVersion)}
		}
	}
	if !checked {
		result.Status = "inconclusive"
		result.Reason = "No instrumented CI tracer selection could be verified."
	}
	return result
}
