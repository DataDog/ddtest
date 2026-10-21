// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"testing"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/testassert/require"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/integrations"
)

// Ported from dd-trace-go #5695's retry_process_test.go and
// efd_faulty_session_test.go cases, whose files this port does not include.

// useFlakyRetrySettings restores the session's flaky retry and EFD settings after the test.
func useFlakyRetrySettings(t *testing.T) (*integrations.FlakyRetriesSetting, func(fiveS, tenS, thirtyS, fiveM int)) {
	t.Helper()
	settings := integrations.GetSettings()
	oldSettings := *settings
	flakyRetries := integrations.GetFlakyRetriesSettings()
	oldFlakyRetries := *flakyRetries
	t.Cleanup(func() {
		*settings = oldSettings
		*flakyRetries = oldFlakyRetries
	})
	return flakyRetries, func(fiveS, tenS, thirtyS, fiveM int) {
		retries := &settings.EarlyFlakeDetection.SlowTestRetries
		retries.FiveS, retries.TenS, retries.ThirtyS, retries.FiveM = fiveS, tenS, thirtyS, fiveM
	}
}

func TestDynamicFlakyRetryAdjustedRetryCount(t *testing.T) {
	flakyRetries, setSlowTestRetries := useFlakyRetrySettings(t)
	setSlowTestRetries(4, 3, 2, 1)
	flakyRetries.RetryCount = 7
	flakyRetries.DynamicRetryCounts = &[5]int64{6, 5, 4, 3, 2}

	dynamic := &testExecutionMetadata{isFlakyTestRetriesEnabled: true}
	require.Equal(t, int64(5), computeAdjustedRetryCount(dynamic, 10*time.Second), "dynamic retry counts replace the flat count, with inclusive bucket bounds")
	dynamicSlowEFD := &testExecutionMetadata{
		isEarlyFlakeDetectionEnabled: true,
		isANewTest:                   true,
		isFlakyTestRetriesEnabled:    true,
	}
	require.Equal(t, int64(3), computeAdjustedRetryCount(dynamicSlowEFD, 5*time.Minute), "EFD skips a 5m test, but dynamic retries keep it in the 5m bucket")
	require.True(t, dynamicSlowEFD.efdFellBackToFlakyRetries)
}

func TestDynamicFlakyRetryDurationBucket(t *testing.T) {
	for _, test := range []struct {
		duration time.Duration
		bucket   int
	}{
		{5 * time.Second, 0}, {5*time.Second + 1, 1},
		{10 * time.Second, 1}, {10*time.Second + 1, 2},
		{30 * time.Second, 2}, {30*time.Second + 1, 3},
		{5 * time.Minute, 3}, {5*time.Minute + 1, 4},
	} {
		require.Equal(t, test.bucket, dynamicRetryDurationBucket(test.duration), "duration %s", test.duration)
	}
}

func TestDynamicFlakyRetrySuppressedEFDUsesFirstExecutionDuration(t *testing.T) {
	flakyRetries, _ := useFlakyRetrySettings(t)
	flakyRetries.RetryCount = 3

	meta := &testExecutionMetadata{isFlakyTestRetriesEnabled: true}
	retryCount, ok := transitionSuppressedEFDToFlakyRetries(meta, time.Second, true, false)
	require.True(t, ok)
	require.Equal(t, int64(2), retryCount)

	flakyRetries.DynamicRetryCounts = &[5]int64{1, 2, 4, 1, 1}
	dynamicMeta := &testExecutionMetadata{isFlakyTestRetriesEnabled: true}
	retryCount, ok = transitionSuppressedEFDToFlakyRetries(dynamicMeta, 20*time.Second, true, false)
	require.True(t, ok)
	require.Equal(t, int64(3), retryCount, "the first execution duration selects the dynamic retry count")
}

// Before its first execution a test gets the retry wrapper whenever any bucket
// retries, so retries still happen with DD_CIVISIBILITY_FLAKY_RETRY_COUNT=0.
func TestDynamicFlakyRetryCountBeforeFirstExecution(t *testing.T) {
	require.Equal(t, int64(0), flakyRetryCountBeforeFirstExecution(&integrations.FlakyRetriesSetting{}))
	require.Equal(t, int64(4), flakyRetryCountBeforeFirstExecution(&integrations.FlakyRetriesSetting{DynamicRetryCounts: &[5]int64{2, 4, 1, 1, 1}}))
}
