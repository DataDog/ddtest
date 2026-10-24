package testdrive

import (
	"testing"

	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func webdriverIORun(tests ...jestTest) validationRun {
	run := validationRun{framework: "webdriverio"}
	traceStatus := map[string]string{"passed": "pass", "failed": "fail", "skipped": "skip"}
	for _, test := range tests {
		test.File = "/project/test/one.js"
		run.Tests = append(run.Tests, test)
		run.Facts.Tests = append(run.Facts.Tests, intake.Test{Name: test.Name, SourceFile: test.File, Attempts: []intake.TestRun{{Status: traceStatus[test.Status]}}})
		run.Facts.TestEventCount++
		if test.Status == "failed" {
			run.ExitCode = 1
		}
	}
	return run
}

func TestWebdriverIOPairedComparisonIncludesNativeRetryHistory(t *testing.T) {
	test := jestTest{Name: "renders", Status: "passed", WebdriverIOCapability: "0", WebdriverIOTestID: "0-0"}
	retriedTest := test
	retriedTest.WebdriverIOAttempts = []string{"failed", "passed"}
	retried := webdriverIORun(retriedTest)
	failedAttempt, passedAttempt := retried.Tests[0], retried.Tests[0]
	failedAttempt.Status = "failed"
	retried.webdriverIOAttempts = []jestTest{failedAttempt, passedAttempt}
	retried.Facts.Tests[0].Attempts = []intake.TestRun{{Status: "fail"}, {Status: "pass", Retry: true, RetryReason: "external"}}
	immediateTest := test
	immediateTest.WebdriverIOAttempts = []string{"passed"}
	immediate := webdriverIORun(immediateTest)
	control := compareJest(withoutTelemetry(retried), retried)
	require.Equal(t, "compatible", control.Status, control.Reason, control.Differences)
	require.Equal(t, "suspected regression", compareJest(withoutTelemetry(retried), immediate).Status, "a native retry the instrumented run no longer needs changes behavior")
	require.Equal(t, "suspected regression", compareRepeatedJavaScript(withoutTelemetry(retried), immediate, withoutTelemetry(retried), immediate).Status)
	require.Equal(t, "varying_outcomes", compareRepeatedJavaScript(withoutTelemetry(retried), immediate, withoutTelemetry(immediate), immediate).Code)
}

func TestWebdriverIORepeatedComparisonKeepsCapabilityIdentities(t *testing.T) {
	capability := func(index, status string) jestTest {
		return jestTest{Name: "renders", Status: status, WebdriverIOCapability: index, WebdriverIOTestID: "0-0"}
	}
	other := func(status string) jestTest {
		return jestTest{Name: "flaky elsewhere", Status: status, WebdriverIOCapability: "0", WebdriverIOTestID: "0-1"}
	}
	// The two capabilities swap outcomes in both pairs while another test varies.
	baseline := webdriverIORun(capability("0", "passed"), capability("1", "failed"), other("passed"))
	instrumented := webdriverIORun(capability("0", "failed"), capability("1", "passed"), other("passed"))
	baselineAgain := webdriverIORun(capability("0", "passed"), capability("1", "failed"), other("failed"))
	instrumentedAgain := webdriverIORun(capability("0", "failed"), capability("1", "passed"), other("passed"))
	result := compareRepeatedJavaScript(withoutTelemetry(baseline), instrumented, withoutTelemetry(baselineAgain), instrumentedAgain)
	require.Equal(t, "suspected regression", result.Status, result.Differences)
	require.Equal(t, "recurring_difference", result.Code)
	require.Contains(t, result.Differences[0], "capability 1")
}
