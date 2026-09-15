// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package cmd

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
)

type fakeTestdriveExecution struct {
	previewed bool
	run       bool
}

func (f *fakeTestdriveExecution) Preview(output io.Writer) {
	f.previewed = true
	_, _ = io.WriteString(output, "preview\n")
}

func (f *fakeTestdriveExecution) Run(context.Context, io.Writer) error {
	f.run = true
	return nil
}

func TestConfirmTestdriveInteractive(t *testing.T) {
	var output bytes.Buffer
	confirmed, err := confirmTestdrive(strings.NewReader("yes\n"), &output, true)
	if err != nil {
		t.Fatalf("confirmTestdrive() unexpected error: %v", err)
	}
	if !confirmed {
		t.Fatal("confirmTestdrive() = false, want true")
	}
	if !strings.Contains(output.String(), "--yes") {
		t.Fatalf("prompt does not explain the agent path: %q", output.String())
	}
}

func TestConfirmTestdriveDefaultsToNo(t *testing.T) {
	confirmed, err := confirmTestdrive(strings.NewReader("\n"), &bytes.Buffer{}, true)
	if err != nil {
		t.Fatalf("confirmTestdrive() unexpected error: %v", err)
	}
	if confirmed {
		t.Fatal("confirmTestdrive() = true, want false")
	}
}

func TestConfirmTestdriveNonInteractiveRequiresYes(t *testing.T) {
	confirmed, err := confirmTestdrive(strings.NewReader("yes\n"), &bytes.Buffer{}, false)
	if confirmed {
		t.Fatal("confirmTestdrive() accepted piped input")
	}
	if err == nil || !strings.Contains(err.Error(), "ddtest testdrive --yes") {
		t.Fatalf("confirmTestdrive() error = %v", err)
	}
}

func TestTestdriveCommandHasYesFlag(t *testing.T) {
	if testdriveCmd.Flags().Lookup("yes") == nil {
		t.Fatal("testdrive command does not define --yes")
	}
}

func TestTestdriveCommandYesPreviewsAndRuns(t *testing.T) {
	execution := &fakeTestdriveExecution{}
	command := newTestdriveCommand(func(string) (testdriveExecution, error) {
		return execution, nil
	})
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetArgs([]string{"--yes"})

	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("ExecuteContext() unexpected error: %v", err)
	}
	if !execution.previewed || !execution.run {
		t.Fatalf("previewed = %v, run = %v", execution.previewed, execution.run)
	}
}

func TestTestdriveCommandNonInteractivePreviewsWithoutRunning(t *testing.T) {
	execution := &fakeTestdriveExecution{}
	command := newTestdriveCommand(func(string) (testdriveExecution, error) {
		return execution, nil
	})
	var output bytes.Buffer
	command.SetIn(strings.NewReader("yes\n"))
	command.SetOut(&output)

	err := command.ExecuteContext(t.Context())
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
	if !execution.previewed || execution.run {
		t.Fatalf("previewed = %v, run = %v", execution.previewed, execution.run)
	}
}
