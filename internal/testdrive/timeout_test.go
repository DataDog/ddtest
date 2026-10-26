package testdrive

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func timedOut(name string) validationRun {
	return markTimeout(validationRun{Name: name, framework: "cucumber", ExitCode: -1, ResultError: "Cucumber native results are incomplete",
		commandError: &ext.CommandTimeoutError{Name: "npm", Timeout: 20 * time.Second}})
}

func TestTimedOutRunNamesTheTimeoutInsteadOfItsPartialResults(t *testing.T) {
	run := timedOut("baseline")
	require.Equal(t, "baseline exceeded --timeout 20s; raise --timeout or fix the hanging step", run.Timeout)
	require.Equal(t, run.Timeout, run.ResultError)
	require.Equal(t, run.Timeout, run.summary().Timeout)

	other := markTimeout(validationRun{Name: "baseline", commandError: errors.New("exit status 1"), ResultError: "kept"})
	require.Empty(t, other.Timeout)
	require.Equal(t, "kept", other.ResultError)
}

func TestTimedOutPairIsReportedOnceAcrossCompatibilityAndBlockers(t *testing.T) {
	baseline, instrumented := timedOut("baseline"), timedOut("reporting-only")
	compatibility := compareJest(baseline, instrumented)
	require.Equal(t, "inconclusive", compatibility.Status)
	require.Equal(t, "baseline exceeded --timeout 20s; raise --timeout or fix the hanging step.", compatibility.Reason)
	require.Contains(t, compareJest(validationRun{Name: "baseline"}, instrumented).Reason, "reporting-only exceeded --timeout 20s; raise --timeout or fix the hanging step, while the uninstrumented run finished")

	reporting := instrumented.summary()
	reporting.Coverage = &coverageSummary{Status: "not observed", Reason: "No application source coverage observed."}
	result := validationResult{Framework: "cucumber", Compatibility: compatibility, Runs: []runSummary{baseline.summary(), reporting}}
	unavailableFeatures(&result, pairTimeout(baseline, instrumented))
	blockers := strings.Join(summarizeValidation(result).BlockingChecks, "\n")
	require.Contains(t, blockers, "baseline exceeded --timeout 20s; raise --timeout or fix the hanging step")
	require.Contains(t, blockers, "reporting-only exceeded --timeout 20s")
	require.NotContains(t, blockers, "real-suite coverage was not observed")
	require.NotContains(t, blockers, "/auto-retries")
	for _, feature := range result.Features {
		if feature.Status == "inconclusive" {
			require.Equal(t, compatibility.Reason, feature.Reason)
		}
	}
}

func TestTimedOutProbeNamesTheTimeout(t *testing.T) {
	run := timedOut("quarantine")
	feature := evaluateFeature("quarantine", run, intake.Test{Name: probeName, SourceFile: "features/ddtest.feature"})
	require.Equal(t, "inconclusive", feature.Status)
	require.Equal(t, "quarantine exceeded --timeout 20s; raise --timeout or fix the hanging step.", feature.Reason)

	calls := 0
	result := validationResult{Framework: "cucumber"}
	require.NoError(t, runProbeFeatures(func(name string, _ intake.Scenario, _ string) (validationRun, error) {
		calls++
		return validationRun{Name: name, commandError: &ext.CommandTimeoutError{Name: "node", Timeout: time.Minute}}, nil
	}, &result))
	require.Equal(t, 2, calls, "feature probes are skipped after a control times out")
	require.Equal(t, "probe-pass-control exceeded --timeout 1m0s; raise --timeout or fix the hanging step", result.Runs[0].Timeout)
	blockers := strings.Join(summarizeValidation(result).BlockingChecks, "\n")
	require.Contains(t, blockers, "probe-pass-control exceeded --timeout 1m0s")
	require.NotContains(t, blockers, "/quarantine: inconclusive")
}
