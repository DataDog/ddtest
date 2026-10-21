package testdrive

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/DataDog/ddtest/internal/onboard"
)

// RenderWorkflowDetail inspects preparation evidence without claiming a verdict.
// Interrupted operations retain their plan in the existing private checkpoint.
func RenderWorkflowDetail(ctx context.Context, output io.Writer, runID, detail string) error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	root = canonicalWorkflowRoot(root)
	lock, err := acquireWorkflowLock(root)
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
		if validation != nil && validation.Workflow != nil {
			plan = validation.Workflow.Plan
		}
	}
	if plan == nil {
		return fmt.Errorf("no saved workflow plan")
	}
	fingerprint, err := InputFingerprint(ctx, root)
	if err != nil {
		return err
	}
	if fingerprint != plan.Fingerprint {
		return fmt.Errorf("saved plan is stale; rerun the workflow for current inputs")
	}
	if detail == "plan" {
		return json.NewEncoder(output).Encode(plan)
	}
	for _, configuration := range plan.Scope.Commands {
		if configuration.ID == detail {
			return json.NewEncoder(output).Encode(configuration)
		}
	}
	return fmt.Errorf("configuration %q is not in the saved plan", detail)
}
