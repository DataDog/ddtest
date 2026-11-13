package usagetelemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type privateDiagnosticError struct{}

func (privateDiagnosticError) Error() string { panic("diagnostics must never render an error") }

func TestTroubleshootingClassifiesErrorsWithoutRenderingThem(t *testing.T) {
	for _, tc := range []struct {
		err      error
		category string
	}{
		{privateDiagnosticError{}, "unknown"},
		{fmt.Errorf("PRIVATE_SECRET: %w", context.DeadlineExceeded), "timeout"},
		{context.Canceled, "cancelled"},
		{&json.SyntaxError{Offset: 9}, "invalid_json"},
		{&json.UnmarshalTypeError{Value: "PRIVATE_SECRET"}, "invalid_json"},
		{io.ErrUnexpectedEOF, "unexpected_eof"},
		{&exec.ExitError{}, "subprocess_exit"},
		{&net.DNSError{Err: "PRIVATE_SECRET", Name: "PRIVATE_SECRET"}, "network"},
		{&fs.PathError{Path: "/private/customer/PRIVATE_SECRET", Op: "PRIVATE_SECRET", Err: fs.ErrPermission}, "filesystem"},
	} {
		t.Run(tc.category, func(t *testing.T) {
			detail := captureTroubleshootingError("test_execution", tc.err)
			require.Equal(t, tc.category, detail.Category)
			require.Equal(t, "observation", detail.Origin)
			data, err := json.Marshal(detail)
			require.NoError(t, err)
			require.NotContains(t, string(data), "PRIVATE_SECRET")
			require.NotContains(t, string(data), "/private/customer")
		})
	}
}

func TestTroubleshootingNewDiagnosticsAreStrictlyValidated(t *testing.T) {
	_, original := diagnosticPayload(t, TroubleshootingContext{})
	var bundle troubleshootingBundle
	require.NoError(t, json.Unmarshal(original, &bundle))
	owned := OwnedFrame{Function: "github.com/DataDog/ddtest/internal/testdrive.(*Testdrive).Workflow", File: "internal/testdrive/workflow.go", Line: 123}
	bundle.Diagnostics.Error = &TroubleshootingError{Operation: "test_execution", Category: "unknown", Origin: "observation", Frames: []OwnedFrame{owned}}
	bundle.Diagnostics.Runs = []troubleshootingRun{{Role: "probe-feature", FrameworkVersions: []string{"30.1.2"}, TracerVersions: []string{"6.20.1"},
		TracerState: &TracerState{Initialization: "unknown", RequestedFeature: "auto-retries", Capabilities: []FeatureCapability{{Feature: "auto-retries", State: "unknown"}}}}}
	valid, err := marshalTroubleshooting(bundle)
	require.NoError(t, err)
	require.NoError(t, validateTroubleshooting(valid))
	for name, mutate := range map[string]func(*troubleshootingBundle){
		"external frame": func(b *troubleshootingBundle) {
			b.Diagnostics.Error.Frames[0].Function = "customer.example/PRIVATE_SECRET.Run"
		},
		"absolute path": func(b *troubleshootingBundle) { b.Diagnostics.Error.Frames[0].File = "/private/customer/workflow.go" },
		"traversal": func(b *troubleshootingBundle) {
			b.Diagnostics.Error.Frames[0].File = "internal/testdrive/../PRIVATE_SECRET.go"
		},
		"wrong package": func(b *troubleshootingBundle) { b.Diagnostics.Error.Frames[0].File = "internal/cmd/workflow.go" },
		"operation":     func(b *troubleshootingBundle) { b.Diagnostics.Error.Operation = "PRIVATE_SECRET" },
		"stack origin":  func(b *troubleshootingBundle) { b.Diagnostics.Error.Origin = "raw_stack" },
		"version ref":   func(b *troubleshootingBundle) { b.Diagnostics.Runs[0].TracerVersions[0] = "git:PRIVATE_SECRET" },
		"feature":       func(b *troubleshootingBundle) { b.Diagnostics.Runs[0].TracerState.RequestedFeature = "PRIVATE_SECRET" },
		"capability": func(b *troubleshootingBundle) {
			b.Diagnostics.Runs[0].TracerState.Capabilities[0].State = "PRIVATE_SECRET"
		},
		"commit": func(b *troubleshootingBundle) { b.Diagnostics.DDTestCommit = "PRIVATE_SECRET" },
	} {
		t.Run(name, func(t *testing.T) {
			var changed troubleshootingBundle
			require.NoError(t, json.Unmarshal(valid, &changed))
			mutate(&changed)
			data, err := marshalTroubleshooting(changed)
			require.NoError(t, err)
			require.Error(t, validateTroubleshooting(data))
		})
	}
	withMessage := strings.Replace(string(valid), `"stack_origin": "observation"`, `"stack_origin": "observation", "message": "PRIVATE_SECRET"`, 1)
	require.Error(t, validateTroubleshooting([]byte(withMessage)))
}

func TestTroubleshootingProjectsReportedVersionsAndUnknownControlState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"schema_version":1,"framework":"jest","tracer":"dd-trace@6.20.1","runs":[
{"role":"probe-feature","instrumented":true,"framework_versions":["v30.1.2","git:PRIVATE_SECRET"],"tracer_versions":["6.20.0-private"],
"tracer_state":{"initialization":"unknown","requested_feature":"auto-retries","settings_requests":null,"skippable_requests":0,"known_tests_requests":null,"management_requests":null,"capabilities":[{"feature":"auto-retries","state":"unknown"}]}}
]}`), 0600))
	_, data := diagnosticPayload(t, TroubleshootingContext{ReportPath: path})
	var bundle troubleshootingBundle
	require.NoError(t, json.Unmarshal(data, &bundle))
	run := bundle.Diagnostics.Runs[0]
	require.Equal(t, []string{"30.1.2", "unknown"}, run.FrameworkVersions)
	require.Equal(t, []string{"6.20.0"}, run.TracerVersions)
	require.Nil(t, run.TracerState.SettingsRequests)
	require.NotNil(t, run.TracerState.SkippableRequests)
	require.Zero(t, *run.TracerState.SkippableRequests)
	require.Equal(t, "unknown", run.TracerState.Initialization)
	require.NotContains(t, string(data), "PRIVATE_SECRET")
	require.NotContains(t, string(data), "-private")
}

func TestTroubleshootingIgnoresObservationsAfterFinish(t *testing.T) {
	a, _ := diagnosticPayload(t, TroubleshootingContext{})
	a.Finish(1, false)
	original := a.troubleshootingError
	a.ObserveTroubleshootingError("report_read", errors.New("PRIVATE_SECRET"))
	require.Same(t, original, a.troubleshootingError)
}

func TestTroubleshootingPrefersTypedErrorsToFallbackObservations(t *testing.T) {
	a := &Attempt{}
	a.ObserveResult("failed", "validation", "validation_incomplete")
	require.Equal(t, "reported_failure", a.troubleshootingError.Category)
	a.ObserveTroubleshootingError("test_execution", privateDiagnosticError{})
	require.Equal(t, "unknown", a.troubleshootingError.Category)
	a.ObserveTroubleshootingError("report_read", &json.SyntaxError{Offset: 9})
	require.Equal(t, "invalid_json", a.troubleshootingError.Category)
	require.Equal(t, "report_read", a.troubleshootingError.Operation)
	a.ObserveTroubleshootingError("command", privateDiagnosticError{})
	require.Equal(t, "invalid_json", a.troubleshootingError.Category)
}
