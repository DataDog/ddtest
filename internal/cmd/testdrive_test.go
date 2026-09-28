// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package cmd

import (
	"bytes"
	"errors"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"os"
	"strings"
	"testing"
)

func TestIsTerminalRejectsDevNull(t *testing.T) {
	file, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Errorf("close os.DevNull: %v", err)
		}
	})
	if isTerminal(file) {
		t.Fatal("os.DevNull must not be treated as an interactive terminal")
	}
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

func TestTestdriveCommandUsage(t *testing.T) {
	for _, tc := range []struct {
		name          string
		args          []string
		wantUsage     bool
		preRunFailure bool
	}{
		{name: "runtime failure", args: []string{"--yes"}},
		{name: "prerequisite failure", args: []string{"--yes"}, preRunFailure: true},
		{name: "invalid argument", args: []string{"unexpected"}, wantUsage: true},
		{name: "invalid flag", args: []string{"--unknown"}, wantUsage: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			command := newTestdriveCommand()
			var output bytes.Buffer
			command.SetOut(&output)
			command.SetErr(&output)
			command.SetArgs(tc.args)
			if tc.preRunFailure {
				command.PersistentPreRunE = func(*cobra.Command, []string) error { return errors.New("prerequisite failed") }
			}
			if err := command.ExecuteContext(t.Context()); err == nil {
				t.Fatal("expected command failure")
			}
			if got := strings.Contains(output.String(), "Usage:"); got != tc.wantUsage {
				t.Fatalf("usage printed = %v, want %v: %s", got, tc.wantUsage, output.String())
			}
		})
	}
}

func TestTestdriveCommandPreview(t *testing.T) {
	for _, version := range []string{"latest", "6.15.0", "git:abc1234"} {
		t.Run(version, func(t *testing.T) {
			t.Chdir(t.TempDir())
			t.Setenv("PATH", t.TempDir())
			viper.Reset()
			settings.Init()
			t.Cleanup(func() { viper.Reset(); settings.Init() })
			if err := os.WriteFile("package.json", []byte(`{"devDependencies":{"jest":"30.2.0"}}`), 0600); err != nil {
				t.Fatal(err)
			}
			command := newTestdriveCommand()
			var output bytes.Buffer
			command.SetIn(strings.NewReader("yes\n"))
			command.SetOut(&output)
			command.SetErr(&output)
			command.SetArgs([]string{"--tracer-version", version})
			err := command.ExecuteContext(t.Context())
			if err == nil || !strings.Contains(err.Error(), "--yes") {
				t.Fatalf("expected confirmation error, got %v", err)
			}
			if !strings.Contains(output.String(), "dd-trace@"+version) {
				t.Fatal(output.String())
			}
			if _, err := os.Stat(".testoptimization"); !os.IsNotExist(err) {
				t.Fatalf("preview wrote artifacts: %v", err)
			}
		})
	}
}
