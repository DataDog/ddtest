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
		Framework: displayName(t.framework.Name()), Tracer: result.Tracer + " · " + result.TracerSource,
		Command: run.Command, Output: run.commandOutput,
	}
	if run.commandError != nil {
		runtime.Error = run.commandError.Error()
	}
	model := buildReport(t.repositoryRoot, run.Facts, run.ExitCode != 0, runtime)
	if t.reportModels != nil {
		*t.reportModels = append(*t.reportModels, model)
		return nil
	}
	model.Artifacts = []reportArtifact{{Title: "Validation JSON", Href: validationFilename}}
	var content bytes.Buffer
	if err := testdriveReport.Execute(&content, model); err != nil {
		return fmt.Errorf("render testdrive HTML report: %w", err)
	}
	path := htmlReportPath(t.repositoryRoot)
	if err := writeValidation(path, content.Bytes()); err != nil {
		return fmt.Errorf("save testdrive HTML report: %w", err)
	}
	result.HTMLReportCurrent = true
	url, err := fileURL(path)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(output, "\nOpen report: %s\nThis HTML shows instrumented suite findings. Compatibility, feature checks and the onboarding verdict are in %s.\n", terminalLink(url, htmlReportPath("")), validationPath(""))
	return nil
}
