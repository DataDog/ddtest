package testdrive

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/DataDog/ddtest/internal/onboard"
)

// Structured output uses the same telemetry engine and exit contract as plain
// testdrive; it must not promote telemetry to compatibility validation.
func (t *Testdrive) telemetryWorkflow(ctx context.Context, output io.Writer, format string) error {
	started := time.Now()
	runErr := t.Run(ctx, io.Discard)
	validation, reportErr := readCurrentOnboardingResult(t.repositoryRoot, started)
	result := onboard.AgentResult{Status: "telemetry_only", Phase: "validation", NextAction: "report_result",
		Reason: "The test command succeeded and test events reached the local intake. Compatibility and advanced features remain unvalidated."}
	if reportErr == nil {
		result.JSONReport = validationPath(t.repositoryRoot)
		result.FinalResponse = validation.Summary.FinalResponse
		if validation.HTMLReportCurrent {
			result.HTMLReport = htmlReportPath(t.repositoryRoot)
		}
	}
	err := errors.Join(runErr, reportErr)
	if err != nil {
		result.Status, result.Code, result.Reason = "validation_failed", "telemetry_check_failed", reportText(err.Error())
	}
	return errors.Join(err, onboard.RenderAgentResult(output, result, format))
}
