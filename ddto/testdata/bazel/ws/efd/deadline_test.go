package efd

import (
	"os"
	"testing"
	"time"
)

var attempts int

// This process smoke intentionally consumes time under Bazel's real deadline.
// Admission boundary tests use fixed times in gotesting instead.
func TestBudget(t *testing.T) {
	if _, present := t.Deadline(); !present {
		t.Fatal("Bazel's TEST_TIMEOUT did not reach Go testing")
	}
	attempts++
	time.Sleep(2 * time.Second)
	if attempts == 2 && os.Getenv("DEADLINE_FAIL_SECOND") == "true" {
		t.Fatal("late failed sample")
	}
}
