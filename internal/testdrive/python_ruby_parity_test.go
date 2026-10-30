package testdrive

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
	"github.com/stretchr/testify/require"
)

func TestPythonTelemetryAloneCannotValidateSuite(t *testing.T) {
	for _, count := range []int{0, 1} {
		root := t.TempDir()
		t.Chdir(root)
		session := workspace.PlanSession()
		require.NoError(t, session.Create())
		t.Cleanup(func() { require.NoError(t, session.Close()) })
		drive := &Testdrive{repositoryRoot: root, language: "python", framework: framework.NewPytest(platform.NewPython()),
			command: "python", args: []string{"-m", "pytest"}, executor: &fakeTestdriveExecutor{},
			startIntake: func(string, intake.Scenario) (localIntake, error) {
				return &fakeIntake{findings: intake.Facts{TestEventCount: count}}, nil
			},
		}
		result := validationResult{Framework: "pytest"}
		require.NoError(t, drive.runFrameworkValidation(t.Context(), &bytes.Buffer{}, session, "", &result))
		require.Equal(t, "inconclusive", result.Compatibility.Status)
		require.Equal(t, "native_results_unreadable", result.Compatibility.Code)
		require.Error(t, finishValidation(&bytes.Buffer{}, root, result))
		report := readValidationReport(t, root)
		require.False(t, report.Success)
		require.False(t, report.TelemetryOnly)
		require.Len(t, report.Runs, 2)
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
			drive := &Testdrive{repositoryRoot: t.TempDir(), language: fixture.language, framework: fixture.runner, command: "test", session: workspace.PlanSession()}
			var output bytes.Buffer
			drive.Preview(&output)
			if fixture.language == "python" {
				require.Contains(t, output.String(), "disable coverage thresholds only for isolated probes")
			} else {
				require.NotContains(t, output.String(), "disable coverage thresholds")
			}
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

func TestPythonReportsExposeHistoricalEvidence(t *testing.T) {
	for _, name := range []string{"pytest"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			data, err := json.Marshal(validationResult{Framework: name, Success: true})
			require.NoError(t, err)
			require.NoError(t, writeValidation(validationPath(root), data))
			var output bytes.Buffer
			require.NoError(t, RenderReport(t.Context(), &output, "json"))
			var result onboard.AgentResult
			require.NoError(t, json.Unmarshal(output.Bytes(), &result))
			require.Equal(t, "historical_report", result.Status)
			require.Equal(t, "freshness_unverified", result.Code)
			require.Empty(t, result.NextCommand, "missing command metadata must not fall back to a default test selection")
			require.NotContains(t, output.String(), "INCOMPLETE")
		})
	}
}
