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
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
)

type verdict struct {
	Status          string   `json:"status"`
	Code            string   `json:"code,omitempty"`
	Reason          string   `json:"reason"`
	DifferenceCount int      `json:"difference_count,omitempty"`
	Differences     []string `json:"differences,omitempty"`
}

type featureResult struct {
	Project string `json:"project,omitempty"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
}

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
	if check.Status == onboard.StatusNotApplicable {
		return nil
	}
	result := &verdict{Status: onboard.StatusCompatible, Reason: "Every statically resolved instrumented CI entry selects the same exact tracer version as the local selection. Floating selections are checked only at this run's resolution time."}
	if localVersion == "" {
		return &verdict{Status: onboard.StatusInconclusive, Reason: "Local tracer version is unresolved."}
	}
	checked := false
	for _, job := range check.Jobs {
		if job.Status == onboard.StatusExcluded {
			continue
		}
		if job.Tracer == "" {
			result.Status = onboard.StatusInconclusive
			result.Reason = fmt.Sprintf("%s / %s / step %d: %s", job.Workflow, job.Job, job.Step, job.Reason)
			if job.Code == onboard.CodeMissingInstrumentation {
				result.Status = onboard.StatusIncompatible
				return result
			}
			continue
		}
		checked = true
		if strings.TrimPrefix(job.Tracer, "dd-trace@") != localVersion {
			return &verdict{Status: onboard.StatusIncompatible, Reason: fmt.Sprintf("%s / %s selects %s, but local validation selected dd-trace@%s. Align selections or validate this CI version separately.", job.Workflow, job.Job, job.Tracer, localVersion)}
		}
	}
	if !checked {
		result.Status = onboard.StatusInconclusive
		result.Reason = "No instrumented CI tracer selection could be verified."
	}
	return result
}

type validationResult struct {
	SchemaVersion    int                      `json:"schema_version"`
	CompletedAt      time.Time                `json:"completed_at"`
	CheckOnly        bool                     `json:"check_only"`
	ChecksPassed     bool                     `json:"checks_passed"`
	Preflight        *jestPreflight           `json:"preflight,omitempty"`
	Selection        *platform.JSSelection    `json:"tracer_selection,omitempty"`
	TracerSource     string                   `json:"tracer_source,omitempty"`
	CISelection      *verdict                 `json:"ci_tracer_selection,omitempty"`
	CIExecution      verdict                  `json:"ci_execution"`
	WorkingDirectory string                   `json:"working_directory"`
	Error            string                   `json:"error,omitempty"`
	Framework        string                   `json:"framework"`
	Tracer           string                   `json:"tracer"`
	CIRuntime        *onboard.RuntimeCheck    `json:"ci_runtime,omitempty"`
	Scope            *onboard.ValidationScope `json:"scope,omitempty"`
	Compatibility    verdict                  `json:"compatibility"`
	Features         []featureResult          `json:"features"`
}

func finishValidation(output io.Writer, repositoryRoot string, result validationResult) error {
	result.SchemaVersion = 1
	result.Compatibility = verdict{Status: onboard.StatusNotExercised, Reason: "Configuration checks only; paired execution was not exercised."}
	result.Features = []featureResult{{Name: "all", Status: onboard.StatusNotExercised, Reason: "Configuration checks only."}}
	result.CompletedAt = time.Now().UTC()
	result.WorkingDirectory = repositoryRoot
	result.CIExecution = verdict{Status: onboard.StatusNotExercised, Reason: "Local-only validation; CI execution and Datadog backend processing were not exercised."}
	result.ChecksPassed = result.Error == "" && result.Preflight != nil && result.Preflight.Verdict.Status == onboard.StatusCompatible
	if result.Scope != nil {
		result.ChecksPassed = result.ChecksPassed && len(result.Scope.Unresolved) == 0
		for _, command := range result.Scope.Commands {
			result.ChecksPassed = result.ChecksPassed && len(command.UnvalidatedSetup) == 0 && len(command.Prerequisites) == 0
		}
	}
	if result.CISelection != nil {
		result.ChecksPassed = result.ChecksPassed && result.CISelection.Status == onboard.StatusCompatible
	}
	if result.CIRuntime != nil {
		result.ChecksPassed = result.ChecksPassed && (result.CIRuntime.Status == onboard.StatusCompatible || result.CIRuntime.Status == onboard.StatusNotApplicable)
		check := *result.CIRuntime
		check.Reason = reportText(check.Reason)
		check.Jobs = slices.Clone(check.Jobs)
		check.Review = slices.Clone(check.Review)
		for _, findings := range [][]onboard.RuntimeFinding{check.Jobs, check.Review} {
			for i := range findings {
				job := &findings[i]
				for _, field := range []*string{&job.Workflow, &job.Job, &job.Source, &job.Command, &job.Resolution, &job.Node, &job.NodeSource, &job.Action, &job.Tracer, &job.TracerRequested, &job.Requirement, &job.Reason} {
					*field = reportText(*field)
				}
			}
		}
		result.CIRuntime = &check
	}
	result.Error = reportText(result.Error)
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
			if job.Source != "" {
				label += " (" + job.Source + ")"
			}
			if job.Node != "" {
				label += " / Node " + job.Node
			}
			if job.NodeResolution != nil {
				label += " => " + job.NodeResolution.Version + " (" + job.NodeResolution.Source + ")"
			}
			_, _ = fmt.Fprintf(output, "  - %s: %s — %s", label, job.Status, job.Reason)
			if job.Tracer != "" {
				_, _ = fmt.Fprintf(output, " (%s; requires %s)", job.Tracer, job.Requirement)
			}
			_, _ = fmt.Fprintln(output)
		}
		for _, entry := range result.CIRuntime.Review {
			_, _ = fmt.Fprintf(output, "  - Review separately: %s / %s / step %d (%s): %s\n", entry.Workflow, entry.Job, entry.Step, entry.Command, entry.Reason)
		}
	}

	if result.CISelection != nil {
		_, _ = fmt.Fprintf(output, "\nCI tracer agreement: %s — %s\n", result.CISelection.Status, result.CISelection.Reason)
	}
	_, _ = fmt.Fprintln(output, "Configuration checks only. Compatibility tests and features were not rerun by this invocation.")
	if result.Error != "" {
		_, _ = fmt.Fprintf(output, "Validation error: %s\n", result.Error)
	}
	_, _ = fmt.Fprintf(output, "Results JSON: %s\n", validationPath(""))
	if result.ChecksPassed {
		return nil
	}
	return fmt.Errorf("configuration checks are incomplete or incompatible; see %s", path)
}
