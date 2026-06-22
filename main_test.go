package main

import (
	"errors"
	"os"
	"testing"
)

func TestRunSuccess(t *testing.T) {
	t.Setenv("DD_CIVISIBILITY_ENABLED", "")
	t.Setenv("DD_TELEMETRY_DEPENDENCY_COLLECTION_ENABLED", "")

	calls := 0
	exitCode := run(func() error {
		calls++
		return nil
	})

	if exitCode != 0 {
		t.Fatalf("run() exit code = %d, want 0", exitCode)
	}
	if calls != 1 {
		t.Fatalf("expected execute to be called once, got %d", calls)
	}
	if got := os.Getenv("DD_CIVISIBILITY_ENABLED"); got != "1" {
		t.Fatalf("DD_CIVISIBILITY_ENABLED = %q, want 1", got)
	}
	if got := os.Getenv("DD_TELEMETRY_DEPENDENCY_COLLECTION_ENABLED"); got != "0" {
		t.Fatalf("DD_TELEMETRY_DEPENDENCY_COLLECTION_ENABLED = %q, want 0", got)
	}
}

func TestRunFailure(t *testing.T) {
	exitCode := run(func() error {
		return errors.New("boom")
	})

	if exitCode != 1 {
		t.Fatalf("run() exit code = %d, want 1", exitCode)
	}
}
