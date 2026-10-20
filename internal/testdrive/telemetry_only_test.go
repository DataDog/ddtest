package testdrive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestPythonRubyTelemetryExitCode(t *testing.T) {
	for _, fixture := range []struct {
		language string
		runner   framework.Framework
	}{
		{"python", framework.NewPytest(platform.NewPython())},
		{"ruby", framework.NewRSpec(platform.NewRuby(settings.TestSkippingLevelTest))},
		{"ruby", framework.NewMinitest(platform.NewRuby(settings.TestSkippingLevelTest))},
	} {
		t.Run(fixture.runner.Name(), func(t *testing.T) {
			for _, format := range []string{"verbose", "agent", "json"} {
				t.Run(format, func(t *testing.T) {
					for _, tc := range []struct {
						name                 string
						events               int
						commandErr, closeErr error
						passed               bool
					}{
						{"telemetry received", 1, nil, nil, true},
						{"no telemetry", 0, nil, nil, false},
						{"test command failed", 1, errors.New("tests failed"), nil, false},
						{"canceled command", 1, context.Canceled, nil, false},
						{"intake close failed", 1, nil, errors.New("intake failed"), false},
					} {
						t.Run(tc.name, func(t *testing.T) {
							root := t.TempDir()
							drive := &Testdrive{
								repositoryRoot: root, language: fixture.language, framework: fixture.runner,
								command: "test", platform: &fakeTracer{}, executor: &fakeTestdriveExecutor{err: tc.commandErr},
								startIntake: func(string, intake.Scenario) (localIntake, error) {
									return &fakeIntake{findings: intake.Facts{TestEventCount: tc.events}, closeErr: tc.closeErr}, nil
								},
							}
							var output bytes.Buffer
							err := drive.Workflow(t.Context(), &output, WorkflowOptions{Format: format})
							require.Equal(t, tc.passed, err == nil, "error: %v\n%s", err, output.String())
							if format != "verbose" {
								var result onboard.AgentResult
								require.NoError(t, json.NewDecoder(strings.NewReader(output.String())).Decode(&result))
								require.Equal(t, "report_result", result.NextAction)
								require.Empty(t, result.NextCommand, "completed or failed telemetry checks must not suggest retrying themselves")
								if tc.passed {
									require.Equal(t, "telemetry_only", result.Status)
									require.Contains(t, output.String(), "UNVALIDATED (TELEMETRY ONLY)")
								} else {
									require.Equal(t, "validation_failed", result.Status)
								}
								if format == "json" {
									require.True(t, json.Valid(output.Bytes()), output.String())
								}
							}
							if tc.commandErr != nil {
								require.ErrorIs(t, err, tc.commandErr)
							}
							data, err := os.ReadFile(validationPath(root))
							require.NoError(t, err)
							var report validationResult
							require.NoError(t, json.Unmarshal(data, &report))
							require.False(t, report.Success, "telemetry does not prove full validation")
							require.False(t, report.LocalSuccess)
							require.Equal(t, "INCOMPLETE", report.Summary.Status)
							if tc.passed {
								require.Equal(t, "UNVALIDATED (TELEMETRY ONLY)", report.Summary.LocalValidation)
								require.Equal(t, "inconclusive", report.Compatibility.Status)
								require.Equal(t, "unvalidated", report.Features[0].Status)
							} else {
								require.NotEmpty(t, report.Error)
							}
						})
					}
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
				require.Contains(t, output.String(), "reporting-only telemetry")
				require.NotContains(t, output.String(), "temporary probe test")
				require.NotContains(t, output.String(), "Node runtimes")
			} else {
				require.Contains(t, output.String(), "application coverage validation are deferred")
				require.NotContains(t, output.String(), "EFD, skipping")
			}
		})
	}
}
