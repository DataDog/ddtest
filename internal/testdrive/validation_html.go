// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"

	"github.com/DataDog/ddtest/internal/constants"
)

func htmlReportPath(root string) string {
	return filepath.Join(root, constants.PlanDirectory, reportFilename)
}

// Keep the upstream renderer unchanged. Only link artifacts that survive our
// cleanup, and publish the real instrumented suite, never synthetic probes.
func (t *Testdrive) writeHTMLReport(output io.Writer, result *validationResult, run validationRun) error {
	runtime := reportRuntime{
		Environment: t.environmentInfo, SetupFailure: t.environmentFailure,
		Framework: displayName(t.framework.Name()), Tracer: tracerDescription(*result),
		Command: run.Command, Output: run.commandOutput,
		TrafficDeleted: supportsValidationFramework(t.framework.Name()),
	}
	if run.commandError != nil {
		runtime.Error = run.commandError.Error()
	}
	model := buildReport(t.checkoutRoot(), run.Facts, run.ExitCode != 0, runtime)
	model.ExecutionCounts = nativeExecutionCounts(run)
	model.EvidenceRole, model.EvidenceName = run.Role, run.Name
	if supportsValidationFramework(t.framework.Name()) {
		writeRunFindings(output, run.Facts, false, model.Cards...)
	}
	if t.reportModels != nil {
		*t.reportModels = append(*t.reportModels, model)
		result.HTMLReportCurrent = true // The parent publishes all retained models, and propagates publication failure.
		return nil
	}
	if err := writeHTMLReportFile(t.repositoryRoot, model); err != nil {
		return err
	}
	result.HTMLReportCurrent = true
	return announceHTMLReport(output, t.repositoryRoot, t.framework.Name())
}

// writeHTMLReportFile publishes one run's report under root.
func writeHTMLReportFile(root string, model reportModel) error {
	model.Artifacts = []reportArtifact{{Title: "Validation JSON", Href: validationFilename}}
	var content bytes.Buffer
	if err := testdriveReport.Execute(&content, model); err != nil {
		return fmt.Errorf("render testdrive HTML report: %w", err)
	}
	if err := writeValidation(htmlReportPath(root), content.Bytes()); err != nil {
		return fmt.Errorf("save testdrive HTML report: %w", err)
	}
	return nil
}

func announceHTMLReport(output io.Writer, root, framework string) error {
	if err := writeHTMLReportLink(output, root); err != nil {
		return err
	}
	if supportsValidationFramework(framework) {
		_, _ = fmt.Fprintf(output, "This HTML shows instrumented suite findings. Compatibility, feature checks and the onboarding verdict are in %s.\n", validationPath(""))
	}
	return nil
}

func writeHTMLReportLink(output io.Writer, root string) error {
	url, err := fileURL(htmlReportPath(root))
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(output, "\nOpen report: %s\n", terminalLink(url, htmlReportPath("")))
	return nil
}
