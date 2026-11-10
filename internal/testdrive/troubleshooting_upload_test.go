package testdrive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/usagetelemetry"
	"github.com/stretchr/testify/require"
)

type troubleshootingOutput struct {
	bytes.Buffer
	*usagetelemetry.Attempt
}

type troubleshootingTransport struct{ payloads chan []byte }

func (transport troubleshootingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	var event struct {
		Name string `json:"event_name"`
	}
	if err := json.Unmarshal(data, &event); err != nil {
		return nil, err
	}
	if event.Name == "ddtest.troubleshooting" {
		transport.payloads <- data
	}
	return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader(""))}, nil
}

func TestTroubleshootingUsesCurrentValidationReportContract(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("DDTEST_TELEMETRY_DISABLED", "")
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "true")
	code := 1
	probe, identity := featureEvidenceFixture("auto-retries")
	probe.Facts.Events = probe.Facts.Events[:1]
	feature := evaluateFeature("auto-retries", probe, identity)
	feature.Project, feature.Reason = "PRIVATE_PROJECT", "PRIVATE_REASON"
	report, err := marshalValidation(validationResult{
		SchemaVersion: 1, Framework: "jest", Tracer: "dd-trace@5.12.3", All: true,
		WorkingDirectory: "/private/customer", Error: "PRIVATE_ERROR",
		Configurations: []configurationResult{{}},
		Features:       []featureResult{feature},
		Runs: []runSummary{{Role: roleReporting, Name: "PRIVATE_RUN", Command: "PRIVATE_COMMAND", Instrumented: true, ExitCode: &code,
			TestCounts: testCounts{Passed: 10, Failed: 2}, SuiteErrorCount: 1, TestEventCount: 12, Runtimes: []string{"v22.14.0"}, ResultError: "PRIVATE_RESULT_ERROR"}},
	})
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "testdrive.json")
	require.NoError(t, os.WriteFile(path, report, 0o600))
	payloads := make(chan []byte, 1)
	output := &troubleshootingOutput{Attempt: usagetelemetry.Start("testdrive", "validation", usagetelemetry.Config{
		Version: "1.2.3", ClientToken: "dummy", IntakeSite: "datadoghq.com", Transport: troubleshootingTransport{payloads: payloads},
	})}
	t.Cleanup(func() { output.Finish(1, false) })
	usagetelemetry.SetTroubleshootingReport(output, path)
	require.NoError(t, onboard.RenderAgentResult(output, onboard.AgentResult{Status: "validation_failed", Phase: "validation", Code: "validation_incomplete"}, "json"))
	var result onboard.AgentResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	require.Empty(t, payloads, "diagnostics are sent only when the command finishes")
	output.Finish(1, false)
	require.Len(t, payloads, 1)
	data := string(<-payloads)
	require.NotContains(t, data, "PRIVATE_")
	require.NotContains(t, data, "/private/customer")
	require.Contains(t, data, `"framework": "jest"`)
	require.Contains(t, data, `"tracer_version": "5.12.3"`)
	require.Contains(t, data, `"name": "auto-retries"`)
	require.Contains(t, data, `"role": "reporting-only"`)
	require.Contains(t, data, `"failed": 2`)
	require.Contains(t, data, `"test_event_count": 12`)
	var payload struct {
		Diagnostics struct {
			Features []struct {
				Evidence *usagetelemetry.FeatureEvidence `json:"evidence"`
			} `json:"features"`
		} `json:"diagnostics"`
	}
	require.NoError(t, json.Unmarshal([]byte(data), &payload))
	require.Len(t, payload.Diagnostics.Features, 1)
	evidence := payload.Diagnostics.Features[0].Evidence
	require.Equal(t, "retry_not_observed", evidence.FailureCategory)
	attempts := evidenceCount(t, evidence, "telemetry_attempts")
	require.Equal(t, "at_least", attempts.Comparison)
	require.Equal(t, 2, attempts.Expected)
	require.Equal(t, 1, *attempts.Observed)
}

func TestTroubleshootingStructuredCheckOnlyObservesCurrentReport(t *testing.T) {
	for _, tc := range []struct {
		language, tracer, version string
		framework                 framework.Framework
	}{
		{"javascript", "dd-trace@5.12.3", "5.12.3", &framework.Jest{}},
		{"python", "ddtrace@3.0.0", "3.0.0", &framework.PyTest{}},
	} {
		for _, format := range []string{"agent", "json"} {
			t.Run(tc.language+"/"+format, func(t *testing.T) {
				t.Setenv("DO_NOT_TRACK", "")
				t.Setenv("DDTEST_TELEMETRY_DISABLED", "")
				t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "true")
				t.Setenv("TMPDIR", t.TempDir())
				root := t.TempDir()
				t.Chdir(root)
				// An earlier report must not supply this attempt's diagnostic context.
				old, err := marshalValidation(validationResult{SchemaVersion: 1, Framework: "mocha", Tracer: "dd-trace@0.0.1"})
				require.NoError(t, err)
				require.NoError(t, writeValidation(validationPath(root), old))
				drive := &Testdrive{repositoryRoot: root, framework: tc.framework, language: tc.language,
					command: tc.framework.Name(), tracerLabel: tc.tracer, checkOnly: true, discoveryError: errors.New("PRIVATE_DISCOVERY_ERROR")}
				payloads := make(chan []byte, 1)
				output := &troubleshootingOutput{Attempt: usagetelemetry.Start("testdrive", "configuration_check", usagetelemetry.Config{
					Version: "1.2.3", ClientToken: "dummy", IntakeSite: "datadoghq.com", Transport: troubleshootingTransport{payloads: payloads}, Context: t.Context(),
				})}
				t.Cleanup(func() { output.Finish(1, false) })
				require.ErrorContains(t, drive.Workflow(t.Context(), output, WorkflowOptions{Format: format}), "configuration_check_failed")
				var result onboard.AgentResult
				decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
				require.NoError(t, decoder.Decode(&result), "runner output must remain suppressed")
				require.ErrorIs(t, decoder.Decode(new(any)), io.EOF)
				require.Empty(t, payloads, "diagnostics are sent only when the command finishes")
				output.Finish(1, false)
				require.Len(t, payloads, 1)
				data := string(<-payloads)
				var payload struct {
					Diagnostics struct {
						ReportStatus string `json:"report_status"`
						CheckOnly    bool   `json:"check_only"`
						Framework    string `json:"framework"`
						Tracer       string `json:"tracer_version"`
					} `json:"diagnostics"`
				}
				require.NoError(t, json.Unmarshal([]byte(data), &payload))
				require.Equal(t, "included", payload.Diagnostics.ReportStatus)
				require.True(t, payload.Diagnostics.CheckOnly)
				require.Equal(t, tc.framework.Name(), payload.Diagnostics.Framework)
				require.Equal(t, tc.version, payload.Diagnostics.Tracer)
				require.NotContains(t, data, "PRIVATE_")
				require.NotContains(t, data, "0.0.1")
			})
		}
	}
}

func TestTroubleshootingCheckOnlyDoesNotReuseUnobservedSavedReport(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("DDTEST_TELEMETRY_DISABLED", "")
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "true")
	t.Setenv("TMPDIR", t.TempDir())
	root := t.TempDir()
	t.Chdir(root)
	old, err := marshalValidation(validationResult{SchemaVersion: 1, Framework: "mocha", Tracer: "dd-trace@0.0.1"})
	require.NoError(t, err)
	require.NoError(t, writeValidation(validationPath(root), old))
	drive := &Testdrive{repositoryRoot: root, framework: &framework.PyTest{}, language: "python", checkOnly: true}
	drive.workflowRun = func(context.Context, io.Writer) error { return errors.New("PRIVATE_FAILURE_BEFORE_REPORT_WRITE") }
	payloads := make(chan []byte, 1)
	output := &troubleshootingOutput{Attempt: usagetelemetry.Start("testdrive", "configuration_check", usagetelemetry.Config{
		Version: "1.2.3", ClientToken: "dummy", IntakeSite: "datadoghq.com", Transport: troubleshootingTransport{payloads: payloads}, Context: t.Context(),
	})}
	t.Cleanup(func() { output.Finish(1, false) })
	require.Error(t, drive.Workflow(t.Context(), output, WorkflowOptions{Format: "agent"}))
	var result onboard.AgentResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	require.Empty(t, payloads, "diagnostics are sent only when the command finishes")
	output.Finish(1, false)
	require.Len(t, payloads, 1)
	data := string(<-payloads)
	require.Contains(t, data, `"report_status": "not_available"`)
	require.Contains(t, data, `"check_only": true`)
	require.NotContains(t, data, "0.0.1")
	require.NotContains(t, data, "PRIVATE_")
}

func TestTroubleshootingExecutionResultConflictKeepsCurrentReport(t *testing.T) {
	for _, format := range []string{"agent", "json", "verbose"} {
		t.Run(format, func(t *testing.T) {
			t.Setenv("DO_NOT_TRACK", "")
			t.Setenv("DDTEST_TELEMETRY_DISABLED", "")
			t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "true")
			t.Setenv("TMPDIR", t.TempDir())
			root := t.TempDir()
			t.Chdir(root)
			requireWriteFile(t, filepath.Join(root, "package.json"), `{"private":true}`)
			drive := &Testdrive{repositoryRoot: root, framework: &framework.Vitest{}, language: "javascript",
				tracerVersion: "6.20.1", projectTracer: "fixture", executor: workflowCheckExecutor{}, nodeVersion: currentNodeVersion}
			drive.workflowRun = func(_ context.Context, out io.Writer) error {
				report := completeValidationFixture("vitest")
				report.Tracer = "dd-trace@6.20.1"
				report.Runs[1].TestCounts.Passed, report.Runs[1].TestEventCount = 7, 7
				report.Runs[1].Command = "PRIVATE_COMMAND"
				observed := 2
				report.Features[0].Evidence = &usagetelemetry.FeatureEvidence{Counts: []usagetelemetry.FeatureCount{
					{Name: "telemetry_attempts", Comparison: "at_least", Expected: 2, Observed: &observed},
				}}
				require.NoError(t, finishValidation(out, root, report))
				return errors.New("PRIVATE_EXECUTION_ERROR")
			}
			payloads := make(chan []byte, 1)
			output := &troubleshootingOutput{Attempt: usagetelemetry.Start("testdrive", "validation", usagetelemetry.Config{
				Version: "1.2.3", ClientToken: "dummy", IntakeSite: "datadoghq.com", Transport: troubleshootingTransport{payloads: payloads}, Context: t.Context(),
			})}
			t.Cleanup(func() { output.Finish(1, false) })
			require.ErrorContains(t, drive.Workflow(t.Context(), output, WorkflowOptions{Format: format}), "execution_result_conflict")
			if format != "verbose" {
				var result onboard.AgentResult
				decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
				require.NoError(t, decoder.Decode(&result))
				require.ErrorIs(t, decoder.Decode(new(any)), io.EOF)
				require.Equal(t, "execution_result_conflict", result.Code)
				currentReport, err := filepath.EvalSymlinks(validationPath(root))
				require.NoError(t, err)
				require.Equal(t, currentReport, result.JSONReport)
			}
			output.Finish(1, false)
			require.Len(t, payloads, 1)
			data := string(<-payloads)
			require.Contains(t, data, `"report_status": "included"`)
			require.Contains(t, data, `"framework": "vitest"`)
			require.Contains(t, data, `"tracer_version": "6.20.1"`)
			require.Contains(t, data, `"name": "telemetry_attempts"`)
			require.Contains(t, data, `"observed": 2`)
			require.Contains(t, data, `"role": "reporting-only"`)
			require.Contains(t, data, `"passed": 7`)
			require.Contains(t, data, `"test_event_count": 7`)
			require.Contains(t, data, `"operation": "test_execution"`)
			require.Contains(t, data, `"stack_origin": "observation"`)
			require.Contains(t, data, `"file": "internal/testdrive/workflow.go"`)
			require.NotContains(t, data, "PRIVATE_")
			require.NotContains(t, data, root)
		})
	}
}

func TestTroubleshootingStandaloneFocusedFailureUsesCurrentReport(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("DDTEST_TELEMETRY_DISABLED", "")
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "true")
	root := t.TempDir()
	t.Chdir(root)
	requireWriteFile(t, filepath.Join(root, "pytest.ini"), "[pytest]\n")
	previous := completeValidationFixture("pytest")
	previous.All, previous.Tracer = true, "ddtrace@1.2.3"
	previous.Runs[1].TestEventCount = 99
	require.NoError(t, finishValidation(io.Discard, root, previous))
	retained, err := os.ReadFile(validationPath(root))
	require.NoError(t, err)
	drive := pytestDrive(root)
	drive.allConfigurations, drive.projectTracer = false, "fixture"
	drive.workflowRun = func(_ context.Context, out io.Writer) error {
		current := completeValidationFixture("pytest")
		current.Tracer = "ddtrace@4.16.1"
		current.Features[0].Status = "failed"
		current.Runs[1].TestCounts.Failed, current.Runs[1].TestEventCount = 2, 2
		return finishValidation(out, root, current)
	}
	payloads := make(chan []byte, 1)
	output := &troubleshootingOutput{Attempt: usagetelemetry.Start("testdrive", "validation", usagetelemetry.Config{
		Version: "1.2.3", ClientToken: "dummy", IntakeSite: "datadoghq.com", Transport: troubleshootingTransport{payloads: payloads}, Context: t.Context(),
	})}
	t.Cleanup(func() { output.Finish(1, false) })
	require.ErrorContains(t, drive.Workflow(t.Context(), output, WorkflowOptions{Format: "agent"}), "validation_incomplete")
	var result onboard.AgentResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	focused := validationResult{KeptReport: validationPath("")}
	require.Equal(t, focused.jsonReportPath(drive.repositoryRoot), result.JSONReport)
	require.Equal(t, focused.htmlReportPath(drive.repositoryRoot), result.HTMLReport)
	output.Finish(1, false)
	require.Len(t, payloads, 1)
	data := string(<-payloads)
	require.Contains(t, data, `"tracer_version": "4.16.1"`)
	require.Contains(t, data, `"failed": 2`)
	require.Contains(t, data, `"test_event_count": 2`)
	require.NotContains(t, data, `"test_event_count": 99`)
	require.NotContains(t, data, `"tracer_version": "1.2.3"`)
	after, err := os.ReadFile(validationPath(root))
	require.NoError(t, err)
	require.Equal(t, retained, after, "the previous --all report stays intact")
}

func TestTroubleshootingPreservesSupportedJVMFrameworks(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("DDTEST_TELEMETRY_DISABLED", "")
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "true")
	for _, name := range javaProbeFrameworks {
		t.Run(name, func(t *testing.T) {
			payloads := make(chan []byte, 1)
			attempt := usagetelemetry.Start("testdrive", "validation", usagetelemetry.Config{
				Version: "1.2.3", ClientToken: "dummy", IntakeSite: "datadoghq.com", Transport: troubleshootingTransport{payloads: payloads},
			})
			attempt.SetTroubleshootingContext(usagetelemetry.TroubleshootingContext{Framework: framework.NewJVM(name, nil).Name()})
			attempt.ObserveResult("needs_input", "preparation", "runtime_unavailable")
			attempt.Finish(1, false)
			require.Len(t, payloads, 1)
			var payload struct {
				Diagnostics struct {
					Framework string `json:"framework"`
				} `json:"diagnostics"`
			}
			require.NoError(t, json.Unmarshal(<-payloads, &payload))
			require.Equal(t, name, payload.Diagnostics.Framework)
		})
	}
}
