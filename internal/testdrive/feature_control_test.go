package testdrive

import (
	"testing"

	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestAttemptToFixTagWithoutRetriesIsAnUnappliedControl(t *testing.T) {
	control := intake.Test{Module: "webdriverio", Suite: "probe", Name: probeName, SourceFile: "test/probe.spec.ts"}
	tags := map[string]string{"test.module": control.Module, "test.suite": control.Suite, "test.name": control.Name, "test.status": "pass", "test.test_management.is_attempt_to_fix": "true"}
	one := 1
	// The tracer tagged the probe for attempt-to-fix but never retried it.
	tagged := validationRun{framework: "webdriverio", ProbeExecutions: &one, Tests: []jestTest{{Name: probeName, Status: "passed"}}, Facts: intake.Facts{TestEventCount: 1, Events: []intake.Event{{Type: "test", Tags: tags}}}}
	feature := evaluateFeature("attempt-to-fix", tagged, control)
	require.Equal(t, "failed", feature.Status)
	require.Equal(t, tracerControlNotApplied, feature.Code)

	// The tag still counts as applied evidence for other controls.
	require.Empty(t, evaluateFeature("auto-retries", tagged, control).Code)
}

func TestTracerLimitationKeepsTheObservedFailureCode(t *testing.T) {
	check := wdioJasmineCheck("5.9.0")
	result := &validationResult{Framework: "webdriverio", Preflight: &check}
	limited := withTracerLimitation(result, featureResult{Name: "disabled", Status: "failed", Code: tracerControlNotApplied, Reason: "The tracer did not apply the disabled control."})
	require.Equal(t, tracerLimitation, limited.Code)
	require.Equal(t, tracerControlNotApplied, limited.ObservedCode)

	generic := withTracerLimitation(result, featureResult{Name: "disabled", Status: "failed", Reason: "Controlled disabled did not show the expected behavior."})
	require.Empty(t, generic.ObservedCode)
}
