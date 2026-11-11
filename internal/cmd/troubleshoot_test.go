package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestTroubleshootingAgentFailureKeepsOneJSONAndAutomaticallySendsDiagnostics(t *testing.T) {
	events := captureUsage(t)
	command := newOnboardCommand(nil)
	command.SetArgs([]string{"--apply", "--yes", "--agent"})
	var output, stderr bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&stderr)
	command.RunE = func(cmd *cobra.Command, _ []string) error {
		return onboard.RenderAgentResult(usageWriter(cmd), onboard.AgentResult{
			Status: "validation_failed", Phase: "validation", Code: "validation_incomplete",
			Reason: "private failure", NextAction: "report_result", FinalResponse: "NOT COMPLETE",
		}, "agent")
	}
	require.NoError(t, executeWithUsage(t.Context(), command))
	decoder := json.NewDecoder(&output)
	var result onboard.AgentResult
	require.NoError(t, decoder.Decode(&result))
	require.ErrorIs(t, decoder.Decode(new(any)), io.EOF)
	require.Empty(t, stderr.String())
	require.Equal(t, "report_result", result.NextAction)
	require.Nil(t, result.NextCommand)
	require.NotContains(t, result.Instruction, "upload")
	require.NotContains(t, output.String(), "troubleshooting")
	require.Equal(t, "NOT COMPLETE", result.FinalResponse)
	finished, diagnostics := completedUsage(t, events, "onboard", "apply")
	require.Equal(t, "failed", finished.Outcome)
	require.NotEmpty(t, diagnostics.Diagnostics)
	require.NotContains(t, string(diagnostics.JSON), "private failure")
}

func TestTroubleshootingSkipsRunEInvalidOptionsInEveryFormat(t *testing.T) {
	for _, tc := range []struct {
		command func() *cobra.Command
		args    []string
	}{
		{func() *cobra.Command { return newOnboardCommand(nil) }, []string{"--format=bogus"}},
		{newTestdriveCommand, []string{"--format=bogus"}},
		{func() *cobra.Command { return newOnboardCommand(nil) }, []string{"--apply", "--instructions"}},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			events := captureUsage(t)
			directory := t.TempDir()
			t.Setenv("TMPDIR", directory)
			command := tc.command()
			command.SetArgs(tc.args)
			var output, stderr bytes.Buffer
			command.SetOut(&output)
			command.SetErr(&stderr)
			require.Error(t, executeWithUsage(t.Context(), command))
			finished, diagnostics := completedUsage(t, events, command.Name(), usageMode(command))
			require.Empty(t, diagnostics.EventName)
			require.Equal(t, "preparation", finished.Phase)
			require.Equal(t, "invalid_options", finished.Code)
			require.Equal(t, "failed", finished.Outcome)
			if usesProtocol(command) {
				var result onboard.AgentResult
				require.NoError(t, json.Unmarshal(output.Bytes(), &result))
				require.NotContains(t, output.String(), "troubleshooting")
			}
			require.NotContains(t, stderr.String(), "Optional troubleshooting JSON")
			files, err := os.ReadDir(directory)
			require.NoError(t, err)
			require.Empty(t, files, "invalid options must not generate a diagnostic file")
		})
	}
}

func TestTroubleshootingAutomaticallyReportsPlainInstructionsDiscoveryFailure(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("TMPDIR", t.TempDir())
	viper.Reset()
	t.Cleanup(viper.Reset)
	events := captureUsage(t)
	command := newOnboardCommand(nil)
	command.SetArgs([]string{"--instructions"})
	var output, stderr bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&stderr)
	require.Error(t, executeWithUsage(t.Context(), command))
	require.Empty(t, output.String())
	finished, diagnostics := completedUsage(t, events, "onboard", "instructions")
	require.Equal(t, "failed", finished.Outcome)
	require.NotEmpty(t, diagnostics.Diagnostics)
	require.NotContains(t, stderr.String(), "troubleshooting")
	require.Contains(t, string(diagnostics.JSON), `"outcome": "failed"`)
}

func TestTroubleshootingPreservesRuntimeBeforeExecutionAndPlanCompaction(t *testing.T) {
	for _, format := range []string{"agent", "json", "verbose"} {
		t.Run(format, func(t *testing.T) {
			t.Setenv("TMPDIR", t.TempDir())
			events := captureUsage(t)
			command := newOnboardCommand(nil)
			command.SetArgs([]string{"--agent", "--apply", "--yes"})
			var output, stderr bytes.Buffer
			command.SetOut(&output)
			command.SetErr(&stderr)
			command.RunE = func(cmd *cobra.Command, _ []string) error {
				writer := usageWriter(cmd)
				err := onboard.RenderAgentResult(writer, onboard.AgentResult{
					Status: "needs_input", Phase: "preparation", Code: "project_dependencies_missing", Reason: "PRIVATE_REASON",
					Plan: &onboard.ExecutionPlan{Framework: "jest", Runtime: "v22.14.0", Tracer: "dd-trace@5.12.3"},
				}, format)
				return err
			}
			require.NoError(t, executeWithUsage(t.Context(), command))
			finished, diagnostics := completedUsage(t, events, "onboard", "apply")
			require.Contains(t, string(diagnostics.JSON), `"runtime_version": "22.14.0"`)
			require.Contains(t, string(diagnostics.JSON), `"tracer_version": "5.12.3"`)
			require.Contains(t, string(diagnostics.JSON), `"report_status": "not_available"`)
			require.NotContains(t, string(diagnostics.JSON), "PRIVATE_REASON")
			if format != "verbose" {
				var result onboard.AgentResult
				require.NoError(t, json.Unmarshal(output.Bytes(), &result))
				require.Nil(t, result.Plan)
				require.Equal(t, "v22.14.0", result.PlanSummary.Runtime)
			}
			require.Equal(t, "blocked", finished.Outcome)
			require.Empty(t, stderr.String())
		})
	}
}

func TestTroubleshootingSuppressesCancelledStructuredFailures(t *testing.T) {
	for _, format := range []string{"agent", "json", "verbose"} {
		for _, status := range []string{"blocked_environment", "validation_failed"} {
			for _, interrupted := range []bool{true, false} {
				t.Run(fmt.Sprintf("%s/%s/interrupted=%t", format, status, interrupted), func(t *testing.T) {
					events := captureUsage(t)
					directory := t.TempDir()
					t.Setenv("TMPDIR", directory)
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					command := newTestdriveCommand()
					command.SetArgs([]string{"--agent", "--yes"})
					var output, stderr bytes.Buffer
					command.SetOut(&output)
					command.SetErr(&stderr)
					command.RunE = func(cmd *cobra.Command, _ []string) error {
						cmd.SilenceErrors, cmd.SilenceUsage = true, true
						if interrupted {
							cancel()
						}
						err := onboard.RenderAgentResult(usageWriter(cmd), onboard.AgentResult{
							Status: status, Phase: "validation", Code: "validation_incomplete",
						}, format)
						if interrupted {
							return errors.Join(context.Canceled, err)
						}
						// A child operation timing out must still send diagnostics
						// while the top-level command context remains active.
						return errors.Join(context.DeadlineExceeded, err)
					}
					require.Error(t, executeWithUsage(ctx, command))
					finished, diagnostics := completedUsage(t, events, "testdrive", "validation")
					if interrupted {
						require.Equal(t, "cancelled", finished.Outcome)
						require.Empty(t, diagnostics.EventName)
						files, err := os.ReadDir(directory)
						require.NoError(t, err)
						require.Empty(t, files, "cancelled results must not create a diagnostic file")
					} else {
						require.NotEqual(t, "cancelled", finished.Outcome)
						require.NotEmpty(t, diagnostics.Diagnostics)
					}
					if format != "verbose" {
						var result onboard.AgentResult
						require.NoError(t, json.Unmarshal(output.Bytes(), &result))
						require.NotContains(t, output.String(), "troubleshooting")
					}
					require.Empty(t, stderr.String())
				})
			}
		}
	}
}
