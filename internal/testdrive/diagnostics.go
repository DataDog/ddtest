// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/DataDog/ddtest/internal/testdrive/intake"
)

const diagnosticsFilename = "findings.json"

type runDiagnostics struct {
	Environment     environmentInfo     `json:"environment"`
	RepositoryRoot  string              `json:"repository_root"`
	Framework       string              `json:"framework"`
	Command         string              `json:"command"`
	Tracer          string              `json:"tracer"`
	CommandFailed   bool                `json:"command_failed"`
	CommandError    string              `json:"command_error,omitempty"`
	RunFailed       bool                `json:"run_failed"`
	Findings        []diagnosticFinding `json:"findings"`
	Summary         string              `json:"summary"`
	DurationUnit    string              `json:"duration_unit"`
	Facts           intake.Facts        `json:"facts"`
	Troubleshooting []intake.Advice     `json:"troubleshooting"`
}

type diagnosticFinding struct {
	Kind     string `json:"kind"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Count    int    `json:"count"`
	Context  string `json:"context"`
}

func writeDiagnostics(repositoryRoot, sessionDirectory string, facts intake.Facts, model reportModel, commandFailed bool) (string, error) {
	advices := make([]intake.Advice, 0)
	findings := make([]diagnosticFinding, 0, len(model.Cards))
	for _, card := range model.Cards {
		advices = append(advices, card.Advices...)
		findings = append(findings, diagnosticFinding{Kind: card.Kind, Severity: card.Severity, Title: card.Title, Count: card.Count, Context: card.Context})
	}
	data, err := json.MarshalIndent(runDiagnostics{
		Environment: model.Runtime.Environment, RepositoryRoot: repositoryRoot, Framework: model.Runtime.Framework,
		Command: model.Runtime.Command, Tracer: model.Runtime.Tracer,
		CommandFailed: commandFailed, CommandError: model.Runtime.Error,
		RunFailed: commandFailed || facts.TestEventCount == 0, Findings: findings,
		Summary: model.Summary, DurationUnit: "nanoseconds", Facts: facts, Troubleshooting: advices,
	}, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode testdrive findings: %w", err)
	}
	path := filepath.Join(sessionDirectory, diagnosticsFilename)
	if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
		return "", fmt.Errorf("save testdrive findings: %w", err)
	}
	return path, nil
}

func writeTroubleshooting(output io.Writer, cards []reportCard, kind string) {
	for _, card := range cards {
		if card.Kind != kind {
			continue
		}
		for _, advice := range card.Advices {
			_, _ = fmt.Fprintf(output, "  Fix: %s\n", advice.Title)
			_, _ = fmt.Fprintf(output, "    %s\n", strings.ReplaceAll(advice.Text, "\n", "\n    "))
			if advice.URL != "" {
				_, _ = fmt.Fprintf(output, "    Setup guide: %s\n", advice.URL)
			}
		}
	}
}

func writeSourceLocation(output io.Writer, file string, start, end int) {
	if file == "" {
		return
	}
	location := file
	if start > 0 {
		location += fmt.Sprintf(":%d", start)
		if end > start {
			location += fmt.Sprintf("-%d", end)
		}
	}
	_, _ = fmt.Fprintf(output, "    Source: %s\n", location)
}

func writeDiagnosticPaths(output io.Writer, repositoryRoot, findingsPath, testOutputPath, trafficPath string) {
	relative := func(path string) string {
		if label, err := filepath.Rel(repositoryRoot, path); err == nil {
			return label
		}
		return path
	}
	_, _ = fmt.Fprintf(output, "\nSaved diagnostics (relative to %s):\n", repositoryRoot)
	if findingsPath != "" {
		_, _ = fmt.Fprintf(output, "  Findings and troubleshooting (JSON): %s\n", relative(findingsPath))
		_, _ = fmt.Fprintln(output, "  JSON durations: nanoseconds · Source paths: repository-relative")
	} else {
		_, _ = fmt.Fprintln(output, "  Structured findings: unavailable (collection error)")
	}
	_, _ = fmt.Fprintf(output, "  Full test output: %s\n", relative(testOutputPath))
	if trafficPath != "" {
		_, _ = fmt.Fprintf(output, "  Captured tracer traffic (JSON): %s\n", filepath.Join(relative(trafficPath), "*.json"))
	}
}
