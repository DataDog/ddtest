// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package cmd

import (
	"bytes"
	"github.com/stretchr/testify/require"
	"io"
	"os"
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

func TestOnboardApplyRequiresExplicitConsent(t *testing.T) {
	for _, args := range [][]string{{"--yes"}, {"--allow-dirty"}} {
		command := newOnboardCommand(func(io.Writer) error {
			t.Fatal("invalid apply flags must not run detection or execution")
			return nil
		})
		command.SetArgs(args)
		command.SetOut(io.Discard)
		command.SetErr(io.Discard)
		if err := command.ExecuteContext(t.Context()); err == nil {
			t.Fatalf("%v accepted without its required companion flag", args)
		}
	}
}

func TestOnboardApplyRefusesOtherLanguagesBeforeRuntimeSetup(t *testing.T) {
	for _, manifest := range []struct{ name, body string }{{"pyproject.toml", "[tool.pytest.ini_options]\n"}, {"Gemfile", "source 'https://rubygems.org'\ngem 'rspec'\n"}} {
		t.Run(manifest.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			require.NoError(t, os.WriteFile(manifest.name, []byte(manifest.body), 0600))
			command := newOnboardCommand(func(io.Writer) error { t.Fatal("apply must not print unrelated instructions"); return nil })
			command.SetArgs([]string{"--apply", "--yes"})
			var output bytes.Buffer
			command.SetOut(&output)
			command.SetErr(&output)
			err := command.ExecuteContext(t.Context())
			require.ErrorContains(t, err, "--apply supports JavaScript frameworks")
			require.NotContains(t, err.Error(), "--all")
			require.NotContains(t, output.String(), "Usage:")
		})
	}
}
