// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func ciProviderFixture(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	t.Setenv("TMPDIR", t.TempDir())
	viper.Reset()
	settings.Init()
	t.Cleanup(func() { viper.Reset(); settings.Init(); onboard.UseCIProvider("") })
	require.NoError(t, os.WriteFile("package.json", []byte(`{"scripts":{"test":"vitest run"},"devDependencies":{"vitest":"4.1.11"}}`), 0600))
	require.NoError(t, os.MkdirAll(filepath.Join(".github", "workflows"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(".github", "workflows", "ci.yml"), []byte("jobs:\n  test:\n    steps:\n      - run: npm test\n"), 0600))
}

func runProtocolCommand(t *testing.T, command *cobra.Command, args ...string) (onboard.AgentResult, error) {
	t.Helper()
	command.SetArgs(args)
	var out bytes.Buffer
	command.SetOut(&out)
	command.SetErr(io.Discard)
	err := command.ExecuteContext(t.Context())
	var result onboard.AgentResult
	require.NoError(t, json.Unmarshal(out.Bytes(), &result), out.String())
	return result, err
}

func TestCIProviderFlagRejectsUnknownProviders(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command func() *cobra.Command
		args    []string
	}{
		{"onboard", func() *cobra.Command { return newOnboardCommand(nil) }, []string{"--agent", "--ci-provider", "gitlab"}},
		{"onboard apply", func() *cobra.Command { return newOnboardCommand(nil) }, []string{"--apply", "--yes", "--format", "json", "--ci-provider", "gitlab"}},
		{"testdrive", newTestdriveCommand, []string{"--agent", "--all", "--check-only", "--yes", "--ci-provider", "gitlab"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ciProviderFixture(t)
			result, err := runProtocolCommand(t, tc.command(), tc.args...)
			require.Error(t, err)
			require.Equal(t, "invalid_options", result.Code)
			require.Equal(t, "options", result.Phase)
			require.Equal(t, `unsupported --ci-provider "gitlab"; supported CI providers: github`, result.Reason)
			require.NoDirExists(t, ".testoptimization")
		})
	}
	t.Run("instructions", func(t *testing.T) {
		ciProviderFixture(t)
		command := newOnboardCommand(nil)
		command.SetArgs([]string{"--instructions", "--ci-provider", "gitlab"})
		var out bytes.Buffer
		command.SetOut(&out)
		command.SetErr(io.Discard)
		require.EqualError(t, command.ExecuteContext(t.Context()), `unsupported --ci-provider "gitlab"; supported CI providers: github`)
		require.Empty(t, out.String())
	})
}

// GitHub Actions is the only real provider, so the choice is simulated.
func TestProviderChoicePrecedesOnboardAndTestdriveExecution(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command func() *cobra.Command
		args    []string
	}{
		{"onboard", func() *cobra.Command { return newOnboardCommand(nil) }, []string{"--agent"}},
		{"onboard plain non-terminal", func() *cobra.Command { return newOnboardCommand(nil) }, nil},
		{"onboard apply", func() *cobra.Command { return newOnboardCommand(nil) }, []string{"--apply", "--yes", "--agent"}},
		{"testdrive", newTestdriveCommand, []string{"--agent", "--all", "--check-only", "--yes"}},
		{"testdrive report", newTestdriveCommand, []string{"--report", "--format", "json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ciProviderFixture(t)
			var requested []string
			selectCIProvider = func(_, provider string) (string, error) {
				requested = append(requested, provider)
				if provider != "" {
					return provider, nil
				}
				return "", &onboard.CIProviderChoice{Providers: []string{"github", "other"}}
			}
			t.Cleanup(func() { selectCIProvider = onboard.SelectCIProvider })
			result, err := runProtocolCommand(t, tc.command(), tc.args...)
			require.Error(t, err)
			require.Equal(t, "ci_provider_required", result.Code)
			require.Equal(t, "needs_input", result.Status)
			require.Equal(t, "ask_user", result.NextAction)
			require.Equal(t, []string{"github", "other"}, result.RequiredInput)
			require.Empty(t, result.NextCommand)
			require.Equal(t, []string{""}, requested, "the choice is made once, before discovery")
			require.NoDirExists(t, ".testoptimization")
			require.NoDirExists(t, "node_modules")
		})
	}
}

func TestSelectedProviderIsForwarded(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "detected", true: "explicit"}[explicit], func(t *testing.T) {
			ciProviderFixture(t)
			args := []string{"--format", "json"}
			if explicit {
				args = append(args, "--ci-provider", "github")
			}
			result, err := runProtocolCommand(t, newOnboardCommand(nil), args...)
			require.NoError(t, err)
			require.Equal(t, "ready", result.Status)
			require.Equal(t, 1, strings.Count(strings.Join(result.NextCommand, " "), "--ci-provider github"), result.NextCommand)

			// Consent commands carry the provider once, explicit or detected.
			result, err = runProtocolCommand(t, newTestdriveCommand(), append([]string{"--all"}, args...)...)
			require.Error(t, err)
			require.Equal(t, "consent_required", result.Code)
			require.Equal(t, "github", result.NextCommand[slices.Index(result.NextCommand, "--ci-provider")+1], result.NextCommand)
			require.Equal(t, 1, strings.Count(strings.Join(result.NextCommand, " "), "--ci-provider"), result.NextCommand)
		})
	}
	t.Run("no CI configuration", func(t *testing.T) {
		ciProviderFixture(t)
		require.NoError(t, os.RemoveAll(".github"))
		result, err := runProtocolCommand(t, newOnboardCommand(nil), "--format", "json")
		require.NoError(t, err)
		require.NotContains(t, result.NextCommand, "--ci-provider")
	})
}

func TestPromptCIProvider(t *testing.T) {
	choice := &onboard.CIProviderChoice{Providers: []string{"github", "other"}}
	for answer, want := range map[string]string{"2\n": "other", "github\n": "github", " other ": "other"} {
		var out bytes.Buffer
		selected, err := promptCIProvider(strings.NewReader(answer), &out, choice)
		require.NoError(t, err)
		require.Equal(t, want, selected)
		require.Contains(t, out.String(), "  1. github\n  2. other\n")
	}
	for _, answer := range []string{"3\n", "gitlab\n", ""} {
		_, err := promptCIProvider(strings.NewReader(answer), io.Discard, choice)
		require.EqualError(t, err, "choose one of github, other with --ci-provider")
	}
}

func TestUnsupportedProviderIsReportedNotRetried(t *testing.T) {
	var out bytes.Buffer
	command := newTestdriveCommand()
	command.SetOut(&out)
	err := protocolFailure(command, "preparation", "validation_preparation", &onboard.CIProviderUnsupportedError{Language: "ruby", Framework: "rspec", Provider: "Other CI"}, nil)
	require.Error(t, err)
	var result onboard.AgentResult
	require.NoError(t, json.Unmarshal(out.Bytes(), &result))
	require.Equal(t, "unsupported", result.Status)
	require.Equal(t, "ci_provider_unsupported", result.Code)
	require.Equal(t, "report_result", result.NextAction)
	require.Contains(t, result.Reason, "rspec CI setup for ruby on Other CI")
}
