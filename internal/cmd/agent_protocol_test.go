package cmd

import (
	"bytes"
	"encoding/json"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"io"
	"os"
	"strings"
	"testing"
)

func TestStructuredCommandErrorsAreOneJSONDocument(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command func() *cobra.Command
		args    []string
		code    string
	}{
		{"prepare", newTestdriveCommand, []string{"--agent", "--yes"}, "validation_preparation"},
		{"invalid arguments", newTestdriveCommand, []string{"--agent", "unexpected"}, "invalid_options"},
		{"invalid flags", newTestdriveCommand, []string{"--agent", "--unknown"}, "invalid_options"},
		{"conflicting flags", newTestdriveCommand, []string{"--format", "json", "--report", "--all"}, "invalid_options"},
		{"consent", newTestdriveCommand, []string{"--format", "json"}, "consent_required"},
		{"missing report", newTestdriveCommand, []string{"--report", "--format", "json"}, "report_unavailable"},
		{"missing detail", newTestdriveCommand, []string{"--report", "--detail", "bogus", "--format", "json"}, "report_unavailable"},
		{"onboard prepare", func() *cobra.Command { return newOnboardCommand(nil) }, []string{"--apply", "--yes", "--format", "json"}, "validation_preparation"},
		{"onboard discovery", func() *cobra.Command { return newOnboardCommand(nil) }, []string{"--format", "json"}, "platform_unresolved"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			t.Setenv("TMPDIR", t.TempDir())
			t.Setenv("TMP", t.TempDir())
			t.Setenv("TEMP", t.TempDir())
			cmd := tc.command()
			cmd.SetArgs(tc.args)
			var out, diagnostics bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&diagnostics)
			require.Error(t, cmd.ExecuteContext(t.Context()))
			var result onboard.AgentResult
			require.NoError(t, json.Unmarshal(out.Bytes(), &result), "stdout=%s; stderr=%s", out.String(), diagnostics.String())
			require.Equal(t, tc.code, result.Code)
			require.Empty(t, diagnostics.String())
			if tc.code == "consent_required" {
				require.Contains(t, result.NextCommand, "--format=json")
				require.Contains(t, result.NextCommand, "--yes")
				require.Equal(t, "run_command", result.NextAction, "a next_command is only offered with run_command")
			}
		})
	}
}

func TestPlainFactsCannotBypassCoordinator(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("TMP", t.TempDir())
	t.Setenv("TEMP", t.TempDir())
	viper.Reset()
	settings.Init()
	t.Cleanup(func() { viper.Reset(); settings.Init() })
	require.NoError(t, os.WriteFile("package.json", []byte(`{"scripts":{"test":"jest"},"devDependencies":{"jest":"30.5.1"}}`), 0600))
	command := newTestdriveCommand()
	command.SetArgs([]string{"--all", "--facts", "missing-facts.json", "--yes"})
	var out bytes.Buffer
	command.SetOut(&out)
	command.SetErr(io.Discard)
	require.Error(t, command.ExecuteContext(t.Context()))
	// Plain output is text only; the coordinator still rejects the facts first.
	require.False(t, json.Valid([]byte(strings.TrimSpace(out.String()))), out.String())
	require.Contains(t, out.String(), "Result: needs_input (facts_invalid)")
	require.Contains(t, out.String(), "Location: missing-facts.json")
	require.NoDirExists(t, "node_modules", "missing facts must be checked before installation or execution")
}

func TestOnboardCLIInitialHandoffPreservesOptions(t *testing.T) {
	t.Chdir(t.TempDir())
	viper.Reset()
	settings.Init()
	t.Cleanup(func() { viper.Reset(); settings.Init() })
	require.NoError(t, os.WriteFile("package.json", []byte(`{"scripts":{"test":"vitest run"},"devDependencies":{"vitest":"4.1.11"}}`), 0600))
	command := newOnboardCommand(nil)
	command.SetArgs([]string{"--format", "json", "--site", "datadoghq.eu", "--tracer-version", "6.20.1", "--facts", "/external/facts.json", "--timeout", "13m", "--allow-reviewed-builds", "--accept-substitute-runtime"})
	var out bytes.Buffer
	command.SetOut(&out)
	command.SetErr(io.Discard)
	require.NoError(t, command.ExecuteContext(t.Context()))
	var state onboard.AgentResult
	require.NoError(t, json.Unmarshal(out.Bytes(), &state))
	for _, value := range []string{"json", "datadoghq.eu", "6.20.1", "/external/facts.json", "13m0s", "--allow-reviewed-builds", "--accept-substitute-runtime"} {
		require.Contains(t, state.NextCommand, value)
	}
}

func TestProtocolSelectionSurvivesEarlierFlagError(t *testing.T) {
	command := newTestdriveCommand()
	require.True(t, protocolArguments(command, []string{"testdrive", "--unknown", "--format", "json"}))
	require.True(t, protocolArguments(command, []string{"testdrive", "--unknown", "--agent"}))
	require.False(t, protocolArguments(command, []string{"testdrive", "--facts", "--agent", "--unknown"}))
	require.False(t, protocolArguments(command, []string{"testdrive", "--agent=false", "--unknown"}))
	require.False(t, protocolArguments(command, []string{"testdrive", "--", "--agent"}))
}

func TestUnsupportedLanguageDoesNotRequestExecutionConsent(t *testing.T) {
	for _, fixture := range []struct{ name, contents string }{{"pytest.ini", "[pytest]\n"}, {"Gemfile", "gem 'rspec'\n"}} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			viper.Reset()
			settings.Init()
			t.Cleanup(func() { viper.Reset(); settings.Init() })
			require.NoError(t, os.WriteFile(fixture.name, []byte(fixture.contents), 0600))
			command := newTestdriveCommand()
			command.SetArgs([]string{"--format", "json"})
			var out bytes.Buffer
			command.SetOut(&out)
			command.SetErr(io.Discard)
			require.Error(t, command.ExecuteContext(t.Context()))
			var state onboard.AgentResult
			require.NoError(t, json.Unmarshal(out.Bytes(), &state))
			require.Equal(t, "unsupported", state.Status)
			require.Equal(t, "framework_unsupported", state.Code)
			require.Empty(t, state.NextCommand)
		})
	}
}

func TestJavaOnboardAgentOffersManualSetupWithoutUnsupportedVerdict(t *testing.T) {
	t.Chdir(t.TempDir())
	viper.Reset()
	settings.Init()
	t.Cleanup(func() { viper.Reset(); settings.Init() })
	require.NoError(t, os.WriteFile("pom.xml", []byte(`<project><dependencies><dependency><groupId>junit</groupId><artifactId>junit</artifactId></dependency></dependencies></project>`), 0600))
	command := newOnboardCommand(nil)
	command.SetArgs([]string{"--agent"})
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(io.Discard)
	require.NoError(t, command.ExecuteContext(t.Context()))
	var result onboard.AgentResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	require.Equal(t, "manual_setup_required", result.Code)
	require.Contains(t, result.NextCommand, "--instructions")
	require.Contains(t, result.Checks[1].Command, "--check-only")
	require.NoDirExists(t, ".testoptimization")
}
