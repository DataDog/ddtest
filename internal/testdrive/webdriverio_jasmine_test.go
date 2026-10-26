package testdrive

import (
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func wdioJasmineCheck(jasmineCore string) jestPreflight {
	return jestPreflight{Node: "20.18.2", Version: "9.32.0", WebdriverIO: &framework.WebdriverIOConfig{
		Runner: "local", Framework: "jasmine", JasmineCore: jasmineCore, Files: []string{"test/a.spec.ts"},
		Capabilities: []map[string]string{{"browserName": "chrome"}},
	}}
}

func TestWebdriverIOPreflightNamesTheJasmineCoreTracerLimitation(t *testing.T) {
	tracer := platform.JSSelection{Version: "5.132.0", Node: ">=18"}
	limited := checkWebdriverIOSupport(wdioJasmineCheck("5.9.0"), tracer)
	require.Equal(t, "compatible", limited.Status, "paired execution is still valid")
	require.Contains(t, limited.Reason, "jasmine-core 5.9.0 (<6)")
	require.Contains(t, limited.Reason, "WebdriverIO 10 (jasmine-core 6)")
	for _, check := range []jestPreflight{wdioJasmineCheck("6.3.0"), wdioJasmineCheck("")} {
		require.NotContains(t, checkWebdriverIOSupport(check, tracer).Reason, "Known dd-trace limitation")
	}
	mocha := wdioJasmineCheck("5.9.0")
	mocha.WebdriverIO.Framework = "mocha"
	require.NotContains(t, checkWebdriverIOSupport(mocha, tracer).Reason, "Known dd-trace limitation")
}

func TestJasmineCoreLimitationIsReportedNotPassed(t *testing.T) {
	check := wdioJasmineCheck("5.9.0")
	result := &validationResult{Framework: "webdriverio", Preflight: &check}
	failed := featureResult{Name: "disabled", Status: "failed", Reason: "Controlled disabled did not show the expected behavior."}

	limited := withTracerLimitation(result, failed)
	require.Equal(t, "known limitation", limited.Status)
	require.Equal(t, tracerLimitation, limited.Code)
	require.Contains(t, limited.Reason, "Observed: Controlled disabled")
	require.False(t, featureSatisfied("webdriverio", limited), "a limitation is never a passed check")

	// Quarantine works, a fixed tracer still passes, and newer jasmine-core is probed normally.
	require.Equal(t, "failed", withTracerLimitation(result, featureResult{Name: "quarantine", Status: "failed"}).Status)
	require.Equal(t, "passed", withTracerLimitation(result, featureResult{Name: "disabled", Status: "passed"}).Status)
	upgraded := wdioJasmineCheck("6.3.0")
	require.Equal(t, "failed", withTracerLimitation(&validationResult{Framework: "webdriverio", Preflight: &upgraded}, failed).Status)
}

func TestFeatureNamesATracerControlThatWasNotApplied(t *testing.T) {
	control := intake.Test{Module: "webdriverio", Suite: "probe", Name: probeName, SourceFile: "test/probe.spec.ts"}
	event := func(status string, extra map[string]string) intake.Event {
		tags := map[string]string{"test.module": control.Module, "test.suite": control.Suite, "test.name": control.Name, "test.status": status}
		for key, value := range extra {
			tags[key] = value
		}
		return intake.Event{Type: "test", Tags: tags}
	}
	one := 1
	// The disabled probe ran and failed as if no control existed.
	disabled := validationRun{framework: "webdriverio", ExitCode: 1, ProbeExecutions: &one, Tests: []jestTest{{Name: probeName, Status: "failed"}}, Facts: intake.Facts{TestEventCount: 1, Events: []intake.Event{event("fail", nil)}}}
	feature := evaluateFeature("disabled", disabled, control)
	require.Equal(t, "failed", feature.Status)
	require.Equal(t, tracerControlNotApplied, feature.Code)
	require.Contains(t, feature.Reason, "telemetry reported the probe once (0 passed, 1 failed)")
	require.Contains(t, feature.Reason, "runner ran it once (0 passed, 1 failed, 0 skipped; exit 1)")
	require.Contains(t, feature.Reason, "no customer change is expected")

	// Retry evidence means the tracer acted; that failure keeps the generic counts.
	retried := disabled
	retried.Facts.Events = []intake.Event{event("fail", nil), event("pass", map[string]string{"test.is_retry": "true", "test.retry_reason": "auto_test_retry"})}
	retried.Facts.TestEventCount = 2
	require.Empty(t, evaluateFeature("auto-retries", retried, control).Code)
	require.Contains(t, evaluateFeature("auto-retries", retried, control).Reason, "matching telemetry has 1 passed, 1 failed and 1 retries")
}

func TestOnlyTracerOnlyFeatureBlockersAreReportedNotRetried(t *testing.T) {
	features := []featureResult{
		{Project: "capability 1 (chrome)", Name: "auto-retries", Status: "known limitation", Code: tracerLimitation, Reason: "Known dd-trace limitation."},
		{Project: "capability 1 (chrome)", Name: "quarantine", Status: "passed"},
	}
	validation := validationResult{Framework: "webdriverio", Features: features,
		Configurations: []configurationResult{{TestCommand: onboard.TestCommand{Command: "npm run test:local"}, PairedExecution: true, Status: "incomplete", Compatibility: verdict{Status: "compatible"}}},
		Summary:        validationSummary{BlockingChecks: []string{"capability 1 (chrome)/auto-retries: known limitation", "not every project has every required feature result", "configuration npm run test:local: incomplete"}}}
	reportOnly, actionable := splitTracerOnlyBlockers(validation)
	require.Equal(t, []string{"capability 1 (chrome)/auto-retries: known limitation — Known dd-trace limitation."}, reportOnly)
	require.Empty(t, actionable)

	validation.Summary.BlockingChecks = append(validation.Summary.BlockingChecks, "unresolved CI scope: sauce.yaml")
	reportOnly, actionable = splitTracerOnlyBlockers(validation)
	require.Len(t, reportOnly, 1)
	require.Equal(t, []string{"unresolved CI scope: sauce.yaml"}, actionable)

	// An ordinary failed feature keeps every blocker actionable.
	validation.Features = append(validation.Features, featureResult{Project: "capability 1 (chrome)", Name: "disabled", Status: "failed"})
	validation.Summary.BlockingChecks = append([]string{"capability 1 (chrome)/disabled: failed"}, validation.Summary.BlockingChecks...)
	_, actionable = splitTracerOnlyBlockers(validation)
	require.True(t, strings.Contains(strings.Join(actionable, "\n"), "not every project has every required feature result"))
}
