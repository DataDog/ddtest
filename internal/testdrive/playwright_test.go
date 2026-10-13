package testdrive

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestPlaywrightNativeCountsAndAttemptTelemetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.json")
	requireWriteFile(t, path, `{"version":"1.51.0","tests":[{"file":"/repo/test.ts","name":"suite test","status":"passed","project":"desktop","browser":"chromium","repeat":0,"expectedStatus":"passed","attempts":[{"status":"failed","retry":0,"failure":"known failure"},{"status":"passed","retry":1,"failure":""}]}],"errors":[]}`)
	run := validationRun{framework: "playwright", root: "/repo"}
	_, err := readPlaywrightResults(path, &run)
	require.NoError(t, err)
	require.Equal(t, 1, run.summary().TestCounts.Passed)
	run.Facts = intake.Facts{TestEventCount: 2, Tests: []intake.Test{{Name: "suite test", SourceFile: "test.ts", Parameters: `{"arguments":{"browser":"desktop"},"metadata":{}}`, Attempts: []intake.TestRun{{Status: "fail"}, {Status: "pass", Retry: true}}}}}
	require.Empty(t, verifyPlaywrightTelemetry(run).Status, "native retries must match, not be mistaken for tracer retries")
	run.Facts.Tests[0].Parameters = `{"arguments":{"browser":"firefox"}}`
	require.Equal(t, "inconclusive", verifyPlaywrightTelemetry(run).Status)
	run.Facts.Tests[0].Parameters = `{"arguments":{"browser":"desktop"}}`
	run.Facts.Tests[0].Attempts = run.Facts.Tests[0].Attempts[1:]
	require.Equal(t, "inconclusive", verifyPlaywrightTelemetry(run).Status, "missing attempt must fail validation")
}

func TestPlaywrightComparisonPreservesProjectAndExpectedStatus(t *testing.T) {
	baseline := validationRun{Tests: []jestTest{{File: "/repo/a.ts", Name: "same", Status: "failed", Failure: "known failure", Playwright: &playwrightIdentity{Project: "desktop", Browser: "chromium", ExpectedStatus: "failed", Attempts: []playwrightAttempt{{Status: "failed"}}}}}}
	data, err := json.Marshal(baseline.Tests)
	require.NoError(t, err)
	var tests []jestTest
	require.NoError(t, json.Unmarshal(data, &tests))
	instrumented := validationRun{Tests: tests}
	require.Equal(t, outcomeKeys(baseline), outcomeKeys(instrumented))
	instrumented.Tests[0].Playwright.Project = "mobile"
	require.NotEqual(t, outcomeKeys(baseline), outcomeKeys(instrumented))
	instrumented.Tests[0].Playwright.Project = "desktop"
	instrumented.Tests[0].Playwright.ExpectedStatus = "passed"
	require.NotEqual(t, outcomeKeys(baseline), outcomeKeys(instrumented))
}

func TestPlaywrightUnsupportedSkippingIsNotAPassOrABlocker(t *testing.T) {
	result := pairedReport("playwright", "passed")
	result.Framework = "playwright"
	result.Features = append(result.Features, featureResult{Name: "skipping", Status: "not supported", Reason: playwrightSkippingReason})
	prepareValidationResult(t.TempDir(), &result)
	require.True(t, result.LocalSuccess)
	require.True(t, featureSatisfied("playwright", result.Features[len(result.Features)-1]))
	require.False(t, featureSatisfied("jest", result.Features[len(result.Features)-1]))
	require.False(t, featureSatisfied("playwright", featureResult{Name: "auto-retries", Status: "not supported"}))
	model := buildReport("/repo", intake.Facts{TestEventCount: 1, TestCount: 1, MissingCoverage: true}, false, reportRuntime{Framework: "Playwright"})
	data, err := json.Marshal(model)
	require.NoError(t, err)
	require.Contains(t, string(data), "Playwright coverage and skipping are not supported")
	require.NotContains(t, string(data), "Test Impact Analysis is not set up correctly")
}

func TestPlaywrightSupportUsesRuntimeAndKnownMinimum(t *testing.T) {
	check := jestPreflight{Node: "20.20.2", Version: "1.51.0", PlaywrightProjects: []playwrightProject{{Name: "chromium"}}}
	require.Equal(t, "compatible", checkPlaywrightSupport(check, platform.JSSelection{Version: "5.131.1", Node: ">=18"}).Status)
	require.Equal(t, "incompatible", checkPlaywrightSupport(check, platform.JSSelection{Version: "6.20.1", Node: ">=22"}).Status)
	check.Version = "1.37.0"
	require.Equal(t, "incompatible", checkPlaywrightSupport(check, platform.JSSelection{Node: ">=18"}).Status)
	check.Version = "2.0.0"
	require.Equal(t, "compatible", checkPlaywrightSupport(check, platform.JSSelection{Node: ">=18"}).Status, "do not add a separate major allowlist")
}

func TestPlaywrightMissingResultsAndInvalidProcessRecords(t *testing.T) {
	var run validationRun
	_, err := readPlaywrightResults(filepath.Join(t.TempDir(), "missing"), &run)
	require.Error(t, err)
	require.NotEmpty(t, run.ResultError)
	path := filepath.Join(t.TempDir(), "children")
	require.NoError(t, os.WriteFile(path, []byte("0\n"), 0600))
	require.Error(t, cleanupPlaywrightChildren(path))
}

func TestPlaywrightDisabledProbeWithoutModule(t *testing.T) {
	control := intake.Test{Module: "playwright", Suite: "probe.spec.ts", Name: probeName, SourceFile: "test/probe.spec.ts", Parameters: `{"arguments":{"browser":"chromium"}}`}
	tags := map[string]string{"test.suite": control.Suite, "test.name": control.Name, "test.source.file": control.SourceFile, "test.parameters": control.Parameters, "test.status": "skip", "test.test_management.is_test_disabled": "true"}
	run := validationRun{framework: "playwright", Tests: []jestTest{{Name: probeName, Status: "skipped", Playwright: &playwrightIdentity{Project: "chromium", Browser: "chromium"}}}, Facts: intake.Facts{TestEventCount: 1, Events: []intake.Event{{Type: "test", Tags: tags}}}}
	require.Equal(t, "passed", evaluateFeature("disabled", run, control).Status)
	for key, value := range map[string]string{"test.module": "another", "test.suite": "another", "test.name": "another", "test.source.file": "another", "test.parameters": `{"arguments":{"browser":"firefox"}}`, "test.status": "pass", "test.test_management.is_test_disabled": "false"} {
		t.Run(key, func(t *testing.T) {
			modified := maps.Clone(tags)
			modified[key] = value
			run.Facts.Events = []intake.Event{{Type: "test", Tags: modified}}
			require.NotEqual(t, "passed", evaluateFeature("disabled", run, control).Status)
		})
	}
	run.Facts.Events = []intake.Event{{Type: "test", Tags: tags}}
	delete(tags, "test.parameters")
	require.Equal(t, "passed", evaluateFeature("disabled", run, control).Status)
	run.Tests[0].Playwright.Project = "firefox"
	require.NotEqual(t, "passed", evaluateFeature("disabled", run, control).Status)
	run.Tests[0].Playwright.Project = "chromium"
	run.framework = "jest"
	require.NotEqual(t, "passed", evaluateFeature("disabled", run, control).Status)
}
