package testdrive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestPythonRubyExitCodeMatchesMain(t *testing.T) {
	for _, fixture := range []struct {
		language string
		runner   framework.Framework
	}{
		{"python", framework.NewPytest(platform.NewPython())},
		{"ruby", framework.NewRSpec(platform.NewRuby(settings.TestSkippingLevelTest))},
		{"ruby", framework.NewMinitest(platform.NewRuby(settings.TestSkippingLevelTest))},
	} {
		t.Run(fixture.runner.Name(), func(t *testing.T) {
			for _, tc := range []struct {
				name                 string
				events               int
				commandErr, closeErr error
				reportFailure        bool
				passed               bool
			}{
				{name: "events received", events: 1, passed: true},
				{name: "no events"},
				{name: "command failed", events: 1, commandErr: errors.New("tests failed")},
				{name: "canceled command", events: 1, commandErr: context.Canceled},
				{name: "intake failed", events: 1, closeErr: errors.New("intake failed")},
				{name: "report failed", events: 1, reportFailure: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					root := t.TempDir()
					if tc.reportFailure {
						require.NoError(t, os.MkdirAll(validationPath(root), 0755))
					}
					drive := &Testdrive{
						repositoryRoot: root, language: fixture.language, framework: fixture.runner,
						command: "test", platform: &fakeTracer{}, executor: &fakeTestdriveExecutor{err: tc.commandErr},
						startIntake: func(string, intake.Scenario) (localIntake, error) {
							return &fakeIntake{findings: intake.Facts{TestEventCount: tc.events}, closeErr: tc.closeErr}, nil
						},
					}
					var output bytes.Buffer
					err := drive.Workflow(t.Context(), &output, WorkflowOptions{Format: "verbose"})
					require.Equal(t, tc.passed, err == nil, "error: %v\n%s", err, output.String())
					if tc.commandErr != nil {
						require.ErrorIs(t, err, tc.commandErr)
					}
					for _, invalid := range []string{"INCOMPLETE", "UNVALIDATED (TELEMETRY ONLY)", "FINAL ONBOARDING RESPONSE"} {
						require.NotContains(t, output.String(), invalid)
					}
					if !tc.reportFailure {
						data, err := os.ReadFile(validationPath(root))
						require.NoError(t, err)
						var report map[string]json.RawMessage
						require.NoError(t, json.Unmarshal(data, &report))
						require.JSONEq(t, "true", string(report["telemetry_only"]))
						var summary validationSummary
						require.NoError(t, json.Unmarshal(report["summary"], &summary))
						require.Equal(t, "UNVALIDATED (TELEMETRY ONLY)", summary.Status)
						var success bool
						require.NoError(t, json.Unmarshal(report["success"], &success))
						require.Equal(t, tc.passed, success)
					}
				})
			}
			for _, format := range []string{"agent", "json"} {
				t.Run(format, func(t *testing.T) {
					root := t.TempDir()
					executor := &fakeTestdriveExecutor{}
					drive := &Testdrive{repositoryRoot: root, language: fixture.language, framework: fixture.runner, command: "test", executor: executor}
					var output bytes.Buffer
					require.Error(t, drive.Workflow(t.Context(), &output, WorkflowOptions{Format: format}))
					var result onboard.AgentResult
					require.NoError(t, json.Unmarshal(output.Bytes(), &result))
					require.Equal(t, "unsupported", result.Status)
					require.Equal(t, "framework_unsupported", result.Code)
					require.Empty(t, result.NextCommand)
					require.Empty(t, executor.command)
					require.NoFileExists(t, validationPath(root))
				})
			}
		})
	}
}

func TestPythonRubyAndCypressPreviewsDoNotPromiseUnsupportedChecks(t *testing.T) {
	for _, fixture := range []struct {
		language string
		runner   framework.Framework
	}{
		{"python", framework.NewPytest(nil)}, {"ruby", framework.NewRSpec(nil)}, {"ruby", framework.NewMinitest(nil)}, {"javascript", framework.NewCypress(nil)},
	} {
		t.Run(fixture.runner.Name(), func(t *testing.T) {
			drive := &Testdrive{repositoryRoot: t.TempDir(), language: fixture.language, framework: fixture.runner, command: "test", session: planSession()}
			var output bytes.Buffer
			drive.Preview(&output)
			require.NotContains(t, output.String(), "disable coverage thresholds")
			if fixture.language != "javascript" {

				require.NotContains(t, output.String(), "temporary probe test")
				require.NotContains(t, output.String(), "Node runtimes")
			} else {
				require.Contains(t, output.String(), "early-flake-detection, skipping, quarantine")
				require.NotContains(t, output.String(), "TIA skipping/per-test coverage:")
			}
		})
	}
}

func TestPythonRubyReportsDoNotEnterAgentValidation(t *testing.T) {
	for _, name := range []string{"pytest", "rspec", "minitest"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			data, err := json.Marshal(validationResult{Framework: name, Success: true})
			require.NoError(t, err)
			require.NoError(t, writeValidation(validationPath(root), data))
			var output bytes.Buffer
			require.Error(t, RenderReport(t.Context(), &output, "json"))
			var result onboard.AgentResult
			require.NoError(t, json.Unmarshal(output.Bytes(), &result))
			require.Equal(t, "unsupported", result.Status)
			require.Empty(t, result.NextCommand)
			require.NotContains(t, output.String(), "INCOMPLETE")
		})
	}
}
