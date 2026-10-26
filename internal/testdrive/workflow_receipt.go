package testdrive

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// The public report is evidence, never an authority to certify itself. Only the
// coordinator records this private digest after finishing its own verification.
type workflowReceipt struct {
	RunID   string    `json:"run_id"`
	Root    string    `json:"root"`
	Engine  string    `json:"engine"`
	Digest  string    `json:"digest"`
	Expires time.Time `json:"expires"`
}

func receiptID(root string) string {
	return fmt.Sprintf("report_%x", sha256.Sum256([]byte(canonicalWorkflowRoot(root))))[:39]
}
func validationDigest(result validationResult) (string, error) {
	data, err := json.Marshal(result)
	return fmt.Sprintf("%x", sha256.Sum256(data)), err
}
func saveWorkflowReceipt(root string, result validationResult) error {
	digest, err := validationDigest(result)
	if err != nil {
		return err
	}
	engine := engineIdentity()
	if engine == "" {
		return fmt.Errorf("ddtest engine identity is unavailable")
	}
	receipt := workflowReceipt{RunID: receiptID(root), Root: canonicalWorkflowRoot(root), Engine: engine, Digest: digest, Expires: time.Now().Add(workflowCheckpointLifetime)}
	path, err := checkpointPath(receipt.RunID)
	if err != nil {
		return err
	}
	data, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	return writeValidation(path, data)
}
func verifyWorkflowReceipt(root string, result validationResult) error {
	id := receiptID(root)
	// readCheckpoint applies the same owner, permissions, size and expiry checks.
	if _, err := readCheckpoint(id); err != nil {
		return fmt.Errorf("private validation receipt unavailable: %w", err)
	}
	path, err := checkpointPath(id)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var receipt workflowReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		return err
	}
	digest, err := validationDigest(result)
	if err != nil {
		return err
	}
	if receipt.Root != canonicalWorkflowRoot(root) || receipt.Engine == "" || receipt.Engine != engineIdentity() || receipt.Digest != digest {
		return fmt.Errorf("saved report or ddtest engine differs from its private validation receipt")
	}
	return nil
}

func workflowVerdict(result validationResult) string {
	if result.Workflow == nil || !workflowChecksPassed(result.Workflow) || !result.Success {
		return "validation_failed"
	}
	if result.Summary.SelectedValidation != nil && result.Summary.SelectedValidation.Status == "PASSED" {
		return "selected_passed"
	}
	if result.Summary.Status == "COMPLETE LOCALLY" {
		return "complete_locally"
	}
	return "validation_failed"
}
