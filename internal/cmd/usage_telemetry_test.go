package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/usagetelemetry"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

type usageEvent struct {
	EventName   string          `json:"event_name"`
	AttemptID   string          `json:"attempt_id"`
	Command     string          `json:"command"`
	Mode        string          `json:"mode"`
	Outcome     string          `json:"outcome"`
	Phase       string          `json:"phase"`
	Code        string          `json:"code"`
	ExitCode    int             `json:"exit_code"`
	Diagnostics json.RawMessage `json:"diagnostics"`
	JSON        []byte          `json:"-"`
}

type usageTransport func(*http.Request) (*http.Response, error)

func (f usageTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func captureUsage(t *testing.T) <-chan usageEvent {
	t.Helper()
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("DDTEST_TELEMETRY_DISABLED", "")
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "true")
	events := make(chan usageEvent, 10)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		var e usageEvent
		if err := json.Unmarshal(data, &e); err != nil {
			t.Error(err)
			return
		}
		e.JSON = data
		events <- e
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(server.Close)
	endpoint, err := url.Parse(server.URL)
	require.NoError(t, err)
	original := newUsageAttempt
	t.Cleanup(func() { newUsageAttempt = original })
	newUsageAttempt = func(ctx context.Context, command, mode string) *usagetelemetry.Attempt {
		return usagetelemetry.Start(command, mode, usagetelemetry.Config{
			Version: "1.2.3", ClientToken: "test-token", IntakeSite: "datadoghq.com",
			Context: ctx,
			Transport: usageTransport(func(r *http.Request) (*http.Response, error) {
				copy := r.Clone(r.Context())
				copy.URL.Scheme, copy.URL.Host = endpoint.Scheme, endpoint.Host
				copy.Host = endpoint.Host
				return server.Client().Transport.RoundTrip(copy)
			}),
		})
	}
	return events
}

func finishedUsage(t *testing.T, events <-chan usageEvent, command, mode string) usageEvent {
	t.Helper()
	finished, _ := completedUsage(t, events, command, mode)
	return finished
}

func completedUsage(t *testing.T, events <-chan usageEvent, command, mode string) (usageEvent, usageEvent) {
	t.Helper()
	require.Contains(t, []int{2, 3}, len(events), "one start, one finish and at most one diagnostic event")
	byName := make(map[string]usageEvent)
	attemptID := ""
	for len(events) > 0 {
		e := <-events
		require.Contains(t, []string{"ddtest.command.started", "ddtest.command.finished", "ddtest.troubleshooting"}, e.EventName)
		require.NotContains(t, byName, e.EventName, "an event must not be emitted twice")
		byName[e.EventName] = e
		require.NotEmpty(t, e.AttemptID)
		if attemptID == "" {
			attemptID = e.AttemptID
		}
		require.Equal(t, attemptID, e.AttemptID)
		require.Equal(t, command, e.Command)
		require.Equal(t, mode, e.Mode)
	}
	require.Contains(t, byName, "ddtest.command.started")
	require.Contains(t, byName, "ddtest.command.finished")
	return byName["ddtest.command.finished"], byName["ddtest.troubleshooting"]
}

func TestUsageCoversCommandErrorsAndPreservesProtocol(t *testing.T) {
	for _, tc := range []struct {
		name, mode, outcome, code string
		command                   func() *cobra.Command
		args                      []string
	}{
		{"invalid flags", "validation", "failed", "invalid_options", newTestdriveCommand, []string{"--agent", "--unknown"}},
		{"parsed report flag", "report", "failed", "invalid_options", newTestdriveCommand, []string{"--agent", "--report", "--unknown"}},
		{"invalid arguments", "discovery", "failed", "invalid_options", func() *cobra.Command { return newOnboardCommand(nil) }, []string{"--agent", "unexpected"}},
		{"missing consent", "validation", "blocked", "consent_required", newTestdriveCommand, []string{"--format", "json"}},
		{"failed preparation", "apply", "blocked", "validation_preparation", func() *cobra.Command { return newOnboardCommand(nil) }, []string{"--agent", "--apply", "--yes"}},
		{"saved report", "report", "blocked", "report_unavailable", newTestdriveCommand, []string{"--agent", "--report"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			t.Setenv("TMPDIR", t.TempDir())
			events := captureUsage(t)
			command := tc.command()
			command.SetArgs(tc.args)
			var output, diagnostics bytes.Buffer
			command.SetOut(&output)
			command.SetErr(&diagnostics)
			err := executeWithUsage(t.Context(), command)
			require.Error(t, err)
			var result onboard.AgentResult
			require.NoError(t, json.Unmarshal(output.Bytes(), &result), "stdout must remain one JSON document")
			require.Equal(t, tc.code, result.Code)
			require.Empty(t, diagnostics.String())
			finished := finishedUsage(t, events, command.Name(), tc.mode)
			require.Equal(t, tc.outcome, finished.Outcome)
			require.Equal(t, tc.code, finished.Code)
			require.Equal(t, ExitCode(err), finished.ExitCode)
			require.Same(t, &output, command.OutOrStdout(), "preserve the caller's writer")
		})
	}
}

func TestUsageFlagFailureKeepsRequestedMode(t *testing.T) {
	for _, tc := range []struct {
		name, command, mode string
		args                []string
	}{
		{"report after error", "testdrive", "report", []string{"--unknown", "--report"}},
		{"report before error", "testdrive", "report", []string{"--report", "--unknown"}},
		{"check after error", "testdrive", "configuration_check", []string{"--unknown", "--check-only"}},
		{"check before error", "testdrive", "configuration_check", []string{"--check-only", "--unknown"}},
		{"apply after error", "onboard", "apply", []string{"--unknown", "--apply"}},
		{"apply before error", "onboard", "apply", []string{"--apply", "--unknown"}},
		{"instructions after error", "onboard", "instructions", []string{"--unknown", "--instructions"}},
		{"instructions before error", "onboard", "instructions", []string{"--instructions", "--unknown"}},
		{"explicit true", "testdrive", "report", []string{"--unknown", "--report=true"}},
		{"explicit false", "testdrive", "validation", []string{"--unknown", "--report=false"}},
		{"last occurrence wins", "testdrive", "validation", []string{"--report", "--unknown", "--report=false"}},
		{"mode precedence", "testdrive", "report", []string{"--unknown", "--check-only", "--report"}},
		{"string value", "testdrive", "validation", []string{"--unknown", "--command", "--report"}},
		{"attached string value", "onboard", "discovery", []string{"--unknown", "--command=--apply"}},
		{"argument terminator", "testdrive", "validation", []string{"--unknown", "--", "--report"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := captureUsage(t)
			root := &cobra.Command{Use: "ddtest"}
			root.PersistentFlags().String("command", "", "test command")
			root.AddCommand(newOnboardCommand(nil), newTestdriveCommand())
			args := append([]string{tc.command}, tc.args...)
			root.SetArgs(args)
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			originalArgs := os.Args
			os.Args = append([]string{"ddtest"}, args...)
			t.Cleanup(func() { os.Args = originalArgs })
			require.Error(t, executeWithUsage(t.Context(), root))
			finished := finishedUsage(t, events, tc.command, tc.mode)
			require.Equal(t, "failed", finished.Outcome)
			require.Equal(t, "invalid_options", finished.Code)
			require.Equal(t, 1, finished.ExitCode)
		})
	}
}

func TestUsageIncludesPrerequisiteFailures(t *testing.T) {
	t.Chdir(t.TempDir())
	events := captureUsage(t)
	t.Setenv("PATH", "")
	root := &cobra.Command{Use: "ddtest", PersistentPreRunE: runPersistentPreRun}
	root.AddCommand(newOnboardCommand(func(io.Writer) error {
		t.Fatal("missing git must stop before RunE")
		return nil
	}))
	root.SetArgs([]string{"onboard", "--agent"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	require.Error(t, executeWithUsage(t.Context(), root))
	finished := finishedUsage(t, events, "onboard", "discovery")
	require.Equal(t, "blocked", finished.Outcome)
	require.Equal(t, "git_unavailable", finished.Code)
	require.Equal(t, "preparation", finished.Phase)
}

func TestUsageRecordsTypedResultsWithoutCountingNestedWork(t *testing.T) {
	for _, format := range []string{"json", "verbose"} {
		t.Run(format, func(t *testing.T) {
			events := captureUsage(t)
			command := newOnboardCommand(nil)
			command.SetArgs([]string{"--apply", "--yes"})
			command.SetOut(io.Discard)
			command.SetErr(io.Discard)
			command.RunE = func(cmd *cobra.Command, _ []string) error {
				// Internal testdrive work reports its final result on the same observer.
				return onboard.RenderAgentResult(usageWriter(cmd), onboard.AgentResult{
					Status: "complete_locally", Phase: "complete", Reason: "/customer/repository must never be sent",
				}, format)
			}
			require.NoError(t, executeWithUsage(t.Context(), command))
			finished := finishedUsage(t, events, "onboard", "apply")
			require.Equal(t, "succeeded", finished.Outcome)
			require.Equal(t, "complete", finished.Phase)
		})
	}
}

func TestUsageDistinguishesHandoffCheckCancellationAndExitStatus(t *testing.T) {
	for _, tc := range []struct {
		name, status, phase, outcome string
		command                      func() *cobra.Command
		args                         []string
		err                          error
		cancel                       bool
	}{
		{"handoff", "ready", "onboarding", "handoff", func() *cobra.Command { return newOnboardCommand(nil) }, nil, nil, false},
		{"check", "ready", "configuration_check", "succeeded", newTestdriveCommand, []string{"--check-only"}, nil, false},
		{"declined", "cancelled", "consent", "cancelled", newTestdriveCommand, nil, nil, false},
		{"interrupted", "needs_input", "validation", "cancelled", newTestdriveCommand, nil, context.Canceled, true},
		{"exit status", "validation_failed", "validation", "failed", newTestdriveCommand, nil, usageExitError{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := captureUsage(t)
			command := tc.command()
			command.SetArgs(tc.args)
			command.SetOut(io.Discard)
			command.SetErr(io.Discard)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			command.RunE = func(cmd *cobra.Command, _ []string) error {
				observeUsage(cmd, tc.status, tc.phase, "")
				if tc.cancel {
					cancel()
				}
				return tc.err
			}
			err := executeWithUsage(ctx, command)
			require.True(t, errors.Is(err, tc.err))
			finished := finishedUsage(t, events, command.Name(), usageMode(command))
			require.Equal(t, tc.outcome, finished.Outcome)
			require.Equal(t, ExitCode(tc.err), finished.ExitCode)
		})
	}
}

type usageExitError struct{}

func (usageExitError) Error() string { return "validation subprocess failed" }
func (usageExitError) ExitCode() int { return 17 }

func TestUsageSkipsHelpVersionAndOtherCommands(t *testing.T) {
	for _, args := range [][]string{{"onboard", "--help"}, {"help", "onboard"}, {"--version"}, {"other"}} {
		t.Run(args[0], func(t *testing.T) {
			events := captureUsage(t)
			root := &cobra.Command{Use: "ddtest", Version: "test"}
			root.AddCommand(newOnboardCommand(nil), newTestdriveCommand(), &cobra.Command{Use: "other", Run: func(*cobra.Command, []string) {}})
			root.SetArgs(args)
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			require.NoError(t, executeWithUsage(t.Context(), root))
			require.Empty(t, events)
		})
	}
}

func TestUsageOutputPreservesTerminalDetection(t *testing.T) {
	for _, writer := range []io.Writer{os.Stdout, os.Stderr, &bytes.Buffer{}} {
		require.Equal(t, isTerminalWriter(writer), isTerminalWriter(&usageOutput{Writer: writer}))
	}
}

func TestUsageRepeatedInvocationsKeepWriterInheritanceAndDistinctIDs(t *testing.T) {
	t.Chdir(t.TempDir())
	events := captureUsage(t)
	root := &cobra.Command{Use: "ddtest"}
	command := newOnboardCommand(func(output io.Writer) error {
		_, err := io.WriteString(output, "handoff\n")
		return err
	})
	root.AddCommand(command)
	root.SetErr(io.Discard)
	previousID := ""
	for range 2 {
		var output bytes.Buffer
		root.SetOut(&output)
		root.SetArgs([]string{"onboard"})
		require.NoError(t, executeWithUsage(t.Context(), root))
		require.Equal(t, "handoff\n", output.String())
		finished := finishedUsage(t, events, "onboard", "discovery")
		require.Equal(t, "handoff", finished.Outcome)
		require.Equal(t, "onboarding", finished.Phase)
		require.NotEqual(t, previousID, finished.AttemptID)
		previousID = finished.AttemptID
	}
}
