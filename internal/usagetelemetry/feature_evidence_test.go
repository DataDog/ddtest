package usagetelemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFeatureEvidenceProjectionAllowsOnlyOwnedNamesAndPrimitives(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	report := `{"schema_version":1,"framework":"jest","features":[{
  "name":"auto-retries","status":"known limitation","code":"tracer_limitation","observed_code":"tracer_control_not_applied",
  "reason":"PRIVATE_CANARY", "evidence":{
    "failure_category":"retry_not_observed", "error_stack":"PRIVATE_CANARY",
    "counts":[{"name":"matching_retries","comparison":"at_least","expected":1,"observed":0,"error":"PRIVATE_CANARY"},
              {"name":"probe_body_executions","comparison":"equal","expected":2,"observed":null},
              {"name":"PRIVATE_CANARY","comparison":"PRIVATE_CANARY","expected":-8,"observed":2000000000}],
    "flags":[{"name":"attempt_to_fix","expected":true,"observed":false},{"name":"PRIVATE_CANARY","expected":true,"observed":false}]
  }
}, {"name":"disabled","status":"inconclusive","evidence":{"failure_category":"PRIVATE_CANARY"}}]}`
	require.NoError(t, os.WriteFile(path, []byte(report), 0o600))
	_, data := diagnosticPayload(t, TroubleshootingContext{ReportPath: path})
	require.NotContains(t, string(data), "PRIVATE_CANARY")
	var bundle troubleshootingBundle
	require.NoError(t, json.Unmarshal(data, &bundle))
	feature := bundle.Diagnostics.Features[0]
	require.Equal(t, "known limitation", feature.Status)
	require.Equal(t, "tracer_control_not_applied", feature.ObservedCode)
	require.Equal(t, "retry_not_observed", feature.Evidence.FailureCategory)
	count := feature.Evidence.Counts[0]
	require.Equal(t, "matching_retries", count.Name)
	require.Equal(t, "at_least", count.Comparison)
	require.Equal(t, 1, count.Expected)
	require.Zero(t, *count.Observed)
	require.Nil(t, feature.Evidence.Counts[1].Observed)
	require.Equal(t, "unknown", feature.Evidence.Counts[2].Name)
	require.Equal(t, "unknown", feature.Evidence.Counts[2].Comparison)
	require.Zero(t, feature.Evidence.Counts[2].Expected)
	require.Equal(t, 1_000_000_000, *feature.Evidence.Counts[2].Observed)
	require.False(t, feature.Evidence.Flags[0].Observed)
	require.Equal(t, "unknown", feature.Evidence.Flags[1].Name)
	require.Equal(t, "unknown", bundle.Diagnostics.Features[1].Evidence.FailureCategory)
	require.NoError(t, validateTroubleshooting(data))
}

func TestFeatureEvidenceUploadRejectsUnsafeNamesAndNestedFreeText(t *testing.T) {
	observed := 0
	for _, evidence := range []FeatureEvidence{
		{FailureCategory: "PRIVATE_CANARY"},
		{Counts: []FeatureCount{{Name: "PRIVATE_CANARY", Comparison: "equal", Observed: &observed}}},
		{Counts: []FeatureCount{{Name: "exit_code", Comparison: "PRIVATE_CANARY", Observed: &observed}}},
		{Flags: []FeatureFlag{{Name: "PRIVATE_CANARY"}}},
	} {
		_, data := diagnosticPayload(t, TroubleshootingContext{})
		var bundle troubleshootingBundle
		require.NoError(t, json.Unmarshal(data, &bundle))
		bundle.Diagnostics.Features = []troubleshootingFeature{{Name: "auto-retries", Status: "failed", Evidence: &evidence}}
		unsafe, err := marshalTroubleshooting(bundle)
		require.NoError(t, err)
		require.Error(t, validateTroubleshooting(unsafe))
	}
	// The final schema gate must refuse nested free text too.
	_, data := diagnosticPayload(t, TroubleshootingContext{})
	var bundle troubleshootingBundle
	require.NoError(t, json.Unmarshal(data, &bundle))
	bundle.Diagnostics.Features = []troubleshootingFeature{{Name: "disabled", Status: "inconclusive", Evidence: &FeatureEvidence{FailureCategory: "timed_out"}}}
	valid, err := marshalTroubleshooting(bundle)
	require.NoError(t, err)
	require.NoError(t, validateTroubleshooting(valid))
	unsafe := strings.Replace(string(valid), `"failure_category": "timed_out"`, `"failure_category": "timed_out", "error_stack": "PRIVATE_CANARY"`, 1)
	require.Error(t, validateTroubleshooting([]byte(unsafe)))
}

func TestFeatureEvidenceKeepsLargeMatrixBundlesWithinUploadLimit(t *testing.T) {
	value := 1_000_000_000
	evidence := &FeatureEvidence{FailureCategory: "telemetry_count_mismatch"}
	for range maxFeatureCounts {
		evidence.Counts = append(evidence.Counts, FeatureCount{Name: "telemetry_attempts", Comparison: "at_least", Expected: value, Observed: &value})
	}
	for range maxFeatureFlags {
		evidence.Flags = append(evidence.Flags, FeatureFlag{Name: "native_repetition_evidence", Expected: true, Observed: false})
	}
	var features []troubleshootingFeature
	var runs []map[string]any
	for range maxDiagnosticEntries {
		features = append(features, troubleshootingFeature{Name: "early-flake-detection", Status: "failed", Evidence: evidence})
		runs = append(runs, map[string]any{"role": "probe-feature", "instrumented": true, "test_counts": map[string]int{"passed": value, "failed": value, "skipped": value, "other": value}, "test_event_count": value, "suite_error_count": value, "runtimes": []string{"22.14.0", "22.14.0", "22.14.0", "22.14.0", "22.14.0", "22.14.0", "22.14.0", "22.14.0"}})
	}
	report, err := json.Marshal(map[string]any{"schema_version": 1, "framework": "jest", "features": features, "runs": runs})
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "report.json")
	require.NoError(t, os.WriteFile(path, report, 0o600))
	info := TroubleshootingContext{ReportPath: path}
	oversized, err := marshalTroubleshooting(troubleshootingBundle{Diagnostics: troubleshootingDetails(info)})
	require.NoError(t, err)
	require.Greater(t, len(oversized), maxTroubleshootingBytes)
	_, data := diagnosticPayload(t, info)
	require.LessOrEqual(t, len(data), maxTroubleshootingBytes)
	var bundle troubleshootingBundle
	require.NoError(t, json.Unmarshal(data, &bundle))
	require.True(t, bundle.Diagnostics.Truncated)
	require.NotEmpty(t, bundle.Diagnostics.Features)
	require.Less(t, len(bundle.Diagnostics.Features), maxDiagnosticEntries)
	require.NoError(t, validateTroubleshooting(data))
}
