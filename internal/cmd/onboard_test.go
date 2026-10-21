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
	command := newOnboardCommand(func(output io.Writer) error {
		called = true
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

func TestOnboardApplyRequiresDeclaredConsent(t *testing.T) {
	command := newOnboardCommand(func(io.Writer) error { t.Fatal("must not fall back to manual guidance"); return nil })
	command.SetArgs([]string{"--apply"})
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	if err := command.ExecuteContext(t.Context()); err == nil {
		t.Fatal("--apply requires --yes before making edits or executing repository code")
	}
}

func TestAgentHelpOmitsUnrelatedPlannerFlags(t *testing.T) {
	command := newOnboardCommand(func(io.Writer) error { return nil })
	command.SetArgs([]string{"--help"})
	var output bytes.Buffer
	command.SetOut(&output)
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if !strings.Contains(text, "--facts") || !strings.Contains(text, "--framework") || strings.Contains(text, "--ci-job-overhead") {
		t.Fatalf("agent help is missing workflow flags or includes unrelated tuning: %s", text)
	}
}
