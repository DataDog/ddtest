// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package cmd

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestOnboardCommandRunsDetection(t *testing.T) {
	called := false
	command := newOnboardCommand(func(repositoryRoot string, output io.Writer) error {
		called = true
		if strings.TrimSpace(repositoryRoot) == "" {
			t.Fatal("repository root is empty")
		}
		_, _ = io.WriteString(output, "onboarding instructions\n")
		return nil
	})
	if command.Short != "Start here: onboard this repository to Test Optimization" {
		t.Fatalf("Short = %q", command.Short)
	}
	var output bytes.Buffer
	command.SetOut(&output)

	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("ExecuteContext() unexpected error: %v", err)
	}
	if !called {
		t.Fatal("onboard command did not run detection")
	}
	if output.String() != "onboarding instructions\n" {
		t.Fatalf("output = %q", output.String())
	}
}
