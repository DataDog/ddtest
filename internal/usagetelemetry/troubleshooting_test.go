package usagetelemetry

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func diagnosticConfig(t *testing.T) Config {
	t.Helper()
	enabledEnvironment(t)
	return Config{Version: "v1.2.3-private-build", ClientToken: "dummy-public-token", IntakeSite: "datadoghq.com", Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
}

func diagnosticPayload(t *testing.T, info TroubleshootingContext) (*Attempt, []byte) {
	t.Helper()
	a := Start("testdrive", "validation", diagnosticConfig(t))
	t.Cleanup(func() { a.Finish(1, false) })
	a.ObserveResult("validation_failed", "validation", "validation_incomplete")
	a.SetTroubleshootingContext(info)
	data := a.troubleshootingPayload()
	require.NotEmpty(t, data)
	require.NoError(t, validateTroubleshooting(data))
	return a, data
}

func TestTroubleshootingProjectsPrivateReportWithoutFreeText(t *testing.T) {
	const secret = "PRIVATE_CANARY_customer_repo_token"
	path := filepath.Join(t.TempDir(), "private-validation.json")
	report := `{
  "schema_version":1,"framework":"jest","tracer":"5.12.3-private.build",
  "all":true,"working_directory":"/home/PRIVATE_CANARY_customer_repo_token",
  "error":"PRIVATE_CANARY_customer_repo_token", "command_environment":{"TOKEN":"PRIVATE_CANARY_customer_repo_token"},
  "configurations":[{"command":"PRIVATE_CANARY_customer_repo_token"}],
  "features":[{"name":"auto-retries","status":"failed","code":"tracer_control_not_applied","reason":"PRIVATE_CANARY_customer_repo_token","project":"PRIVATE_CANARY_customer_repo_token"},
              {"name":"PRIVATE_CANARY_customer_repo_token","status":"PRIVATE_CANARY_customer_repo_token","code":"PRIVATE_CANARY_customer_repo_token"}],
  "runs":[{"role":"baseline","instrumented":false,"exit_code":1,"test_counts":{"passed":3,"failed":2,"skipped":4,"other":0},"suite_error_count":1,"test_event_count":0,
           "timeout":"PRIVATE_CANARY_customer_repo_token", "result_error":"PRIVATE_CANARY_customer_repo_token", "runtimes":["v22.14.0","PRIVATE_CANARY_customer_repo_token"],
           "command":"PRIVATE_CANARY_customer_repo_token", "name":"PRIVATE_CANARY_customer_repo_token", "error_stack":"PRIVATE_CANARY_customer_repo_token"},
          {"role":"reporting-only","instrumented":true,"exit_code":137,"test_counts":{"passed":1},"test_event_count":1}]
}`
	require.NoError(t, os.WriteFile(path, []byte(report), 0o600))
	a, data := diagnosticPayload(t, TroubleshootingContext{ReportPath: path, Runtime: "v22.14.0"})
	require.NotContains(t, string(data), secret)
	require.NotContains(t, string(data), path)
	require.NotContains(t, string(data), "private.build")
	require.NotContains(t, string(data), "dummy-public-token")
	var bundle troubleshootingBundle
	require.NoError(t, json.Unmarshal(data, &bundle))
	require.Equal(t, a.event.AttemptID, bundle.AttemptID)
	require.Equal(t, "1.2.3", bundle.Version)
	require.Equal(t, "5.12.3", bundle.Diagnostics.TracerVersion)
	require.Equal(t, "22.14.0", bundle.Diagnostics.RuntimeVersion)
	require.Equal(t, "included", bundle.Diagnostics.ReportStatus)
	require.Equal(t, 1, bundle.Diagnostics.ConfigurationCount)
	require.Equal(t, troubleshootingFeature{Name: "unknown", Status: "unknown", Code: "unknown"}, bundle.Diagnostics.Features[1])
	run := bundle.Diagnostics.Runs[0]
	require.Equal(t, 3, run.Passed)
	require.Equal(t, 2, run.Failed)
	require.True(t, run.TimedOut)
	require.True(t, run.ResultError)
	require.Equal(t, []string{"22.14.0", "unknown"}, run.RuntimeVersions)
	require.Equal(t, 137, *bundle.Diagnostics.Runs[1].ExitCode, "an exit code is evidence, not an assertion of its cause")
}

func TestTroubleshootingMissingInvalidAndLargeReportsStaySafe(t *testing.T) {
	for _, kind := range []string{"missing", "invalid", "large"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "report.json")
			switch kind {
			case "invalid":
				require.NoError(t, os.WriteFile(path, []byte(`{"secret":"PRIVATE_CANARY"}`), 0o600))
			case "large":
				file, err := os.Create(path)
				require.NoError(t, err)
				require.NoError(t, file.Truncate(maxLocalReportBytes+1))
				require.NoError(t, file.Close())
			}
			_, data := diagnosticPayload(t, TroubleshootingContext{ReportPath: path, Framework: "private-framework", Tracer: "https://private-repo"})
			require.NotContains(t, string(data), "PRIVATE_CANARY")
			require.NotContains(t, string(data), path)
			require.NotContains(t, string(data), "private-framework")
			require.NotContains(t, string(data), "private-repo")
		})
	}
}

func TestTroubleshootingPreservesTracerLabelsAndKnownFallback(t *testing.T) {
	for _, tc := range []struct{ label, fallback, expected string }{
		{"dd-trace@v5.12.3-private", "4.0.0", "5.12.3"},
		{"ddtrace@3.0.0", "", "3.0.0"},
		{"datadog-ci@1.0.0", "", "1.0.0"},
		{"dd-java-agent@1.67.1", "", "1.67.1"},
		{"5.12.3", "", "5.12.3"},
		{"", "dd-trace@4.0.0", "4.0.0"},
		{"PRIVATE_PACKAGE@1.2.3", "4.0.0", "4.0.0"},
		{"PRIVATE_PACKAGE@1.2.3", "", "unknown"},
		{"dd-trace@git:PRIVATE_REF", "", "unknown"},
	} {
		t.Run(tc.label+tc.fallback, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "report.json")
			data, err := json.Marshal(localTroubleshootingReport{SchemaVersion: 1, Framework: "jest", Tracer: tc.label})
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(path, data, 0o600))
			details := troubleshootingDetails(TroubleshootingContext{ReportPath: path, Tracer: tc.fallback})
			require.Equal(t, "included", details.ReportStatus)
			require.Equal(t, tc.expected, details.TracerVersion)
			data, err = json.Marshal(details)
			require.NoError(t, err)
			require.NotContains(t, string(data), "PRIVATE_")
		})
	}
}

func TestTroubleshootingReportRejectsOversizedAndNonRegularFiles(t *testing.T) {
	directory := t.TempDir()
	_, err := readTroubleshootingFile(directory, maxLocalReportBytes)
	require.Error(t, err)
	path := filepath.Join(directory, "oversized.json")
	file, err := os.Create(path)
	require.NoError(t, err)
	require.NoError(t, file.Truncate(maxLocalReportBytes+1))
	require.NoError(t, file.Close())
	_, err = readTroubleshootingFile(path, maxLocalReportBytes)
	require.Error(t, err)
}

func TestTroubleshootingBoundsCollectionsAndCounts(t *testing.T) {
	d := troubleshootingDiagnostics{ReportStatus: "included", ConfigurationCount: -1}
	for range 100 {
		d.Features = append(d.Features, troubleshootingFeature{Name: "disabled", Status: "failed"})
		d.Runs = append(d.Runs, troubleshootingRun{Role: "baseline", Passed: -4, Failed: 2_000_000_000, RuntimeVersions: []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}})
	}
	d = d.sanitized()
	require.True(t, d.Truncated)
	require.Len(t, d.Features, maxDiagnosticEntries)
	require.Len(t, d.Runs, maxDiagnosticEntries)
	require.Len(t, d.Runs[0].RuntimeVersions, 8)
	require.Zero(t, d.Runs[0].Passed)
	require.Equal(t, 1_000_000_000, d.Runs[0].Failed)
	require.Zero(t, d.ConfigurationCount)
}

func TestTroubleshootingRecognizesNonJavaScriptFrameworks(t *testing.T) {
	for _, framework := range []string{"rspec", "minitest", "pytest", "junit4", "junit5", "testng", "cucumber-java"} {
		details := troubleshootingDetails(TroubleshootingContext{Framework: framework})
		require.Equal(t, framework, details.Framework)
	}
}

func TestTroubleshootingOnlySendsDiagnosticsForEligibleFailures(t *testing.T) {
	for _, status := range []string{"passed", "complete_locally", "ready", "handoff", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			a := Start("onboard", "discovery", diagnosticConfig(t))
			a.ObserveResult(status, "discovery", "")
			require.Nil(t, a.troubleshootingPayload())
			a.Finish(0, false)
		})
	}
	for _, phase := range []string{"options", "consent"} {
		a := Start("testdrive", "validation", diagnosticConfig(t))
		a.ObserveResult("failed", phase, "consent_required")
		require.Nil(t, a.troubleshootingPayload())
		a.Finish(1, false)
	}
	a := Start("testdrive", "report", diagnosticConfig(t))
	a.ObserveResult("failed", "report", "report_invalid")
	require.Nil(t, a.troubleshootingPayload())
	a.Finish(1, false)
	config := diagnosticConfig(t)
	config.ClientToken = ""
	require.Nil(t, Start("onboard", "apply", config).troubleshootingPayload())
	SetTroubleshootingContext(io.Discard, TroubleshootingContext{})
}

func TestTroubleshootingRejectsUnsafeOrAmbiguousJSON(t *testing.T) {
	_, original := diagnosticPayload(t, TroubleshootingContext{})
	var bundle troubleshootingBundle
	require.NoError(t, json.Unmarshal(original, &bundle))
	for _, mutate := range []func(*troubleshootingBundle){
		func(b *troubleshootingBundle) { b.Command = "private-command" },
		func(b *troubleshootingBundle) { b.Code = "private-error" },
		func(b *troubleshootingBundle) { b.Version = "private-version" },
		func(b *troubleshootingBundle) { b.AttemptID = "/home/private/repo" },
		func(b *troubleshootingBundle) { b.Diagnostics.Framework = "private-framework" },
		func(b *troubleshootingBundle) { b.Diagnostics.Runs = []troubleshootingRun{{Role: "private-role"}} },
		func(b *troubleshootingBundle) {
			b.Diagnostics.Features = []troubleshootingFeature{{Name: "private-feature"}}
		},
	} {
		copy := bundle
		mutate(&copy)
		data, err := marshalTroubleshooting(copy)
		require.NoError(t, err)
		require.Error(t, validateTroubleshooting(data))
	}
	for _, data := range []string{
		`{"extra_secret":"private",` + string(original[1:]),
		`{"message":"private",` + string(original[1:]),
		string(original) + `{"extra":"private"}`,
		`null`,
	} {
		require.Error(t, validateTroubleshooting([]byte(data)))
	}
}

func TestTroubleshootingAutomaticallySendsFinalSnapshotOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private-report.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"schema_version":1,"framework":"jest"}`), 0600))
	requests := make(chan []byte, 3)
	config := localConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		if r.URL.Path != "/v1/input/test-token" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "" || r.Header.Get("DD-API-KEY") != "" {
			t.Error("unexpected destination or customer credentials")
		}
		requests <- data
		w.WriteHeader(http.StatusAccepted)
	}))
	a := Start("testdrive", "validation", config)
	a.SetTroubleshootingContext(TroubleshootingContext{Framework: "jest", Runtime: "v22.14.0"})
	a.SetTroubleshootingReport(path)
	a.ObserveResult("validation_failed", "validation", "validation_incomplete")
	// Final error observations and report contents must be included, rather
	// than an earlier snapshot frozen while rendering an intermediate result.
	a.ObserveTroubleshootingError("report_read", &json.SyntaxError{Offset: 1})
	require.NoError(t, os.WriteFile(path, []byte(`{"schema_version":1,"framework":"jest","runs":[{"role":"baseline","test_counts":{"failed":2}}]}`), 0600))
	a.Finish(1, false)
	a.Finish(1, false)
	require.Len(t, requests, 3)
	var diagnostics []byte
	for range 3 {
		data := <-requests
		var e event
		require.NoError(t, json.Unmarshal(data, &e))
		require.Equal(t, a.event.AttemptID, e.AttemptID)
		if e.EventName == troubleshootingEvent {
			require.Nil(t, diagnostics)
			diagnostics = data
		}
	}
	require.NotEmpty(t, diagnostics)
	require.NoError(t, validateTroubleshooting(diagnostics))
	require.Contains(t, string(diagnostics), `"failed": 2`)
	require.Contains(t, string(diagnostics), `"runtime_version": "22.14.0"`)
	require.Contains(t, string(diagnostics), `"category": "invalid_json"`)
	require.NotContains(t, string(diagnostics), path)
}

func TestTroubleshootingRespectsOptOutAtFinish(t *testing.T) {
	for _, name := range []string{"DO_NOT_TRACK", "DDTEST_TELEMETRY_DISABLED", "DD_INSTRUMENTATION_TELEMETRY_ENABLED"} {
		t.Run(name, func(t *testing.T) {
			config := diagnosticConfig(t)
			var diagnostics atomic.Int32
			config.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				var e event
				if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
					t.Error(err)
				}
				if e.EventName == troubleshootingEvent {
					diagnostics.Add(1)
				}
				return &http.Response{StatusCode: 202, Body: io.NopCloser(strings.NewReader(""))}, nil
			})
			a := Start("testdrive", "validation", config)
			a.ObserveResult("validation_failed", "validation", "validation_incomplete")
			value := "true"
			if name == "DD_INSTRUMENTATION_TELEMETRY_ENABLED" {
				value = "false"
			}
			t.Setenv(name, value)
			a.Finish(1, false)
			require.Zero(t, diagnostics.Load())
		})
	}
}

func TestTroubleshootingDeliveryFailuresDoNotRetryOrAffectResults(t *testing.T) {
	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusTooManyRequests, http.StatusInternalServerError, 0} {
		config := diagnosticConfig(t)
		var requests atomic.Int32
		config.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests.Add(1)
			if status == 0 {
				return nil, errors.New("private token and proxy credentials")
			}
			return &http.Response{StatusCode: status, Header: http.Header{"Location": []string{"https://private.invalid"}}, Body: io.NopCloser(strings.NewReader("private response"))}, nil
		})
		a := Start("testdrive", "validation", config)
		a.ObserveResult("validation_failed", "validation", "validation_incomplete")
		a.Finish(1, false)
		require.Equal(t, "failed", a.event.Outcome)
		require.EqualValues(t, 3, requests.Load(), "one request per event, no redirects or retries")
	}
}

func TestAutomaticTelemetryExcludesSensitiveStringsFromEveryRequest(t *testing.T) {
	const secret = "PRIVATE_CANARY_customer_repo_token"
	path := filepath.Join(t.TempDir(), "private-report.json")
	report := `{"schema_version":1,"framework":"PRIVATE_CANARY_customer_repo_token",
"tracer":"dd-trace@6.20.1-PRIVATE_CANARY_customer_repo_token",
"repository_url":"https://PRIVATE_CANARY_customer_repo_token@private.example/repo",
"customer_org":"PRIVATE_CANARY_customer_repo_token","hostname":"PRIVATE_CANARY_customer_repo_token",
"working_directory":"/Users/PRIVATE_CANARY_customer_repo_token/repo",
"environment":{"API_KEY":"PRIVATE_CANARY_customer_repo_token"},"error_stack":"PRIVATE_CANARY_customer_repo_token",
"features":[{"name":"auto-retries","status":"failed","reason":"PRIVATE_CANARY_customer_repo_token",
"evidence":{"failure_category":"PRIVATE_CANARY_customer_repo_token","counts":[{"name":"PRIVATE_CANARY_customer_repo_token","comparison":"PRIVATE_CANARY_customer_repo_token","expected":2,"observed":1}],
"flags":[{"name":"PRIVATE_CANARY_customer_repo_token","expected":true,"observed":false}]}}],
"runs":[{"role":"PRIVATE_CANARY_customer_repo_token","instrumented":true,"name":"PRIVATE_CANARY_customer_repo_token",
"command":"PRIVATE_CANARY_customer_repo_token","timeout":"PRIVATE_CANARY_customer_repo_token","result_error":"PRIVATE_CANARY_customer_repo_token",
"runtimes":["22.14.0-PRIVATE_CANARY_customer_repo_token"],"framework_versions":["git:PRIVATE_CANARY_customer_repo_token"],
"tracer_versions":["https://private.example/PRIVATE_CANARY_customer_repo_token"],
"tracer_state":{"initialization":"PRIVATE_CANARY_customer_repo_token","requested_feature":"PRIVATE_CANARY_customer_repo_token",
"capabilities":[{"feature":"PRIVATE_CANARY_customer_repo_token","state":"PRIVATE_CANARY_customer_repo_token"}],"headers":{"Authorization":"PRIVATE_CANARY_customer_repo_token"}}}]}`
	require.NoError(t, os.WriteFile(path, []byte(report), 0600))
	requests := make(chan []byte, 3)
	config := localConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		if r.Header.Get("Authorization") != "" || r.Header.Get("DD-API-KEY") != "" || r.URL.RawQuery != "" {
			t.Error("customer authentication or query parameters must not be forwarded")
		}
		requests <- data
		w.WriteHeader(http.StatusAccepted)
	}))
	for _, name := range []string{"DD_API_KEY", "DD_APP_KEY", "DD_ACCESS_TOKEN", "DD_SITE", "DD_TRACE_AGENT_URL", "DD_CIVISIBILITY_AGENTLESS_URL", "DD_GIT_REPOSITORY_URL", "DD_GIT_COMMIT_SHA", "DD_SERVICE", "DD_ENV", "DD_HOSTNAME", "DD_TAGS"} {
		t.Setenv(name, secret)
	}
	const buildSuffix = "PRIVATE-CANARY-customer-repo-token"
	config.Version = "1.2.3-" + buildSuffix
	a := Start("testdrive", "validation", config)
	a.SetTroubleshootingContext(TroubleshootingContext{ReportPath: path, Framework: secret, Tracer: secret, Runtime: secret})
	a.ObserveResult("validation_failed", secret, secret)
	a.ObserveTroubleshootingError("test_execution", &os.PathError{Op: secret, Path: path, Err: os.ErrPermission})
	a.Finish(1, false)
	require.Len(t, requests, 3)
	var diagnostic []byte
	for range 3 {
		data := <-requests
		require.NotContains(t, string(data), secret)
		require.NotContains(t, string(data), buildSuffix)
		require.NotContains(t, string(data), path)
		require.NotContains(t, string(data), "private.example")
		require.NotContains(t, string(data), config.ClientToken)
		var e event
		require.NoError(t, json.Unmarshal(data, &e))
		require.Equal(t, "1.2.3", e.Version, "usage events also strip custom build suffixes")
		if e.EventName == troubleshootingEvent {
			diagnostic = data
		}
	}
	require.NoError(t, validateTroubleshooting(diagnostic))
	require.Contains(t, string(diagnostic), `"category": "filesystem"`)
}
