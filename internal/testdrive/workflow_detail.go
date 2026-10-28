package testdrive

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
)

// RenderWorkflowDetail inspects preparation evidence without claiming a verdict.
// Interrupted operations retain their plan in the existing private checkpoint.
func RenderWorkflowDetail(ctx context.Context, output io.Writer, runID, detail string) (err error) {
	defer func() {
		if err != nil {
			err = renderReportFailure(output, "json", err)
		}
	}()
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	root = workspace.CanonicalRoot(root)
	lock, err := workspace.AcquireLock(root)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	var plan *onboard.ExecutionPlan
	if runID != "" {
		checkpoint, err := readCheckpoint(runID)
		if err != nil {
			return err
		}
		if checkpoint.Root != root {
			return fmt.Errorf("checkpoint belongs to a different repository")
		}
		plan = checkpoint.Plan
	} else {
		validation, err := readPreviousValidation(root)
		if err != nil {
			return err
		}
		// A check-only run keeps the full run's plan and binding as history.
		if validation != nil && validation.CheckOnly && validation.Retained != nil && validation.Retained.Result != nil {
			validation = validation.Retained.Result
		}
		if validation != nil && validation.Workflow != nil {
			// The public report is evidence only: show its plan only while the
			// private receipt still binds it, so edited details aren't presented.
			if err := verifyWorkflowReceipt(root, *validation); err != nil {
				return fmt.Errorf("saved plan is not certified by this ddtest binary: %w; rerun the workflow", err)
			}
			plan = validation.Workflow.Plan
		} else if validation != nil {
			return fmt.Errorf("the saved report comes from a plain testdrive run, which has no workflow plan; run the agent workflow to create one")
		}
	}
	if plan == nil {
		return fmt.Errorf("no saved workflow plan")
	}
	fingerprint, err := workflowFingerprint(ctx, root, plan.Framework)
	if err != nil {
		return err
	}
	if fingerprint != plan.Fingerprint {
		return fmt.Errorf("saved plan is stale; rerun the workflow for current inputs")
	}
	if detail == "plan" {
		return writeWorkflowDetail(output, plan)
	}
	for _, configuration := range plan.Scope.Commands {
		if configuration.ID == detail {
			return writeWorkflowDetail(output, configuration)
		}
	}
	return fmt.Errorf("configuration %q is not in the saved plan", detail)
}

const workflowDetailLimit = 1 << 20

func writeWorkflowDetail(output io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(data) > workflowDetailLimit {
		return fmt.Errorf("requested detail exceeds 1 MiB; inspect a single configuration instead")
	}
	_, err = output.Write(append(data, '\n'))
	return err
}
