// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package intake

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tinylib/msgp/msgp"

	"github.com/DataDog/ddtest/internal/constants"
)

func TestTestEventCountRecognizesOneTest(t *testing.T) {
	payload := msgp.AppendMapHeader(nil, 2)
	payload = msgp.AppendString(payload, "version")
	payload = msgp.AppendInt(payload, 1)
	payload = msgp.AppendString(payload, "events")
	payload = msgp.AppendArrayHeader(payload, 3)
	payload = appendEvent(payload, "test_session_end", 10, 20, 30)
	payload = appendEvent(payload, "test", 10, 20, 30)
	payload = appendEvent(payload, "test_suite_end", 10, 20, 30)

	server := &Server{requests: []RawRequest{
		{Method: http.MethodPost, Path: "/another-endpoint", Body: []byte("not msgpack")},
		{Method: http.MethodPost, Path: constants.TestCycleURLPath, Body: payload},
	}}

	count, err := server.TestEventCount()
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestEventsInheritCapabilitiesFromPayloadMetadata(t *testing.T) {
	const capability = "_dd.library_capabilities.auto_test_retries"
	payload, err := msgp.AppendIntf(nil, map[string]any{
		"metadata": map[string]any{
			"*":              map[string]any{"library_version": "1.2.3", capability: "0"},
			"test":           map[string]any{capability: "1"},
			"test_suite_end": map[string]any{"suite-only": "yes"},
		},
		"events": []any{
			map[string]any{"type": "test", "content": map[string]any{"meta": map[string]any{"test.name": "inherited"}}},
			map[string]any{"type": "test", "content": map[string]any{"meta": map[string]any{"test.name": "overridden", capability: "2"}}},
			map[string]any{"type": "test_suite_end", "content": map[string]any{}},
		},
	})
	require.NoError(t, err)
	server := &Server{collectEvents: true, requests: []RawRequest{{Method: http.MethodPost, Path: constants.TestCycleURLPath, Body: payload}}}
	facts, err := server.Facts()
	require.NoError(t, err)
	require.Len(t, facts.Events, 3)
	require.Equal(t, "1", facts.Events[0].Tags[capability], "event type metadata overrides wildcard metadata")
	require.Equal(t, "2", facts.Events[1].Tags[capability], "event metadata overrides shared metadata")
	require.Equal(t, "0", facts.Events[2].Tags[capability], "test metadata does not leak into suites")
	require.Equal(t, "1.2.3", facts.Events[0].Tags["library_version"])
	require.NotContains(t, facts.Events[0].Tags, "suite-only")
}

func TestFactsRetainsOnlyNormalizedCapabilitiesBeforeDiscardingEvents(t *testing.T) {
	const prefix = "_dd.library_capabilities."
	payload, err := msgp.AppendIntf(nil, map[string]any{
		"metadata": map[string]any{
			"*": map[string]any{prefix + "auto_test_retries": "0"},
			"test": map[string]any{
				prefix + "auto_test_retries": "1", prefix + "test_management.quarantine": "2",
				prefix + "test_management.disable": "PRIVATE_SECRET", prefix + "test_management.attempt_to_fix": "-1",
			},
		},
		"events": []any{
			map[string]any{"type": "test", "content": map[string]any{"meta": map[string]any{"test.name": "PRIVATE_CUSTOMER_TEST", prefix + "PRIVATE_FEATURE": "PRIVATE_SECRET"}}},
			map[string]any{"type": "test", "content": map[string]any{"meta": map[string]any{prefix + "auto_test_retries": "0", prefix + "test_impact_analysis": "0"}}},
			map[string]any{"type": "test_suite_end", "content": map[string]any{"meta": map[string]any{prefix + "early_flake_detection": "1"}}},
		},
	})
	require.NoError(t, err)
	for _, collect := range []bool{false, true} {
		server := &Server{collectEvents: collect, requests: []RawRequest{{Method: http.MethodPost, Path: constants.TestCycleURLPath, Body: payload}}}
		facts, err := server.Facts()
		require.NoError(t, err)
		require.Equal(t, map[string]string{
			"auto_test_retries": "mixed", "test_impact_analysis": "unavailable", "test_management.quarantine": "available",
			"test_management.disable": "unknown", "test_management.attempt_to_fix": "unknown",
		}, facts.LibraryCapabilities)
		require.NotContains(t, facts.LibraryCapabilities, "early_flake_detection", "suite-only advertisements are not test capabilities")
		if collect {
			require.Len(t, facts.Events, 3)
		} else {
			require.Empty(t, facts.Events, "ordinary runs still discard customer tags")
		}
		data, err := json.Marshal(facts.LibraryCapabilities)
		require.NoError(t, err)
		require.NotContains(t, string(data), "PRIVATE_")
	}
}

func TestFactsReportsEffectiveVersionsAndObservedControlRequests(t *testing.T) {
	payload, err := msgp.AppendIntf(nil, map[string]any{
		"metadata": map[string]any{
			"*":    map[string]any{"library_version": "6.20.0", "runtime.version": "22.14.0"},
			"test": map[string]any{"test.framework_version": "30.1.2"},
		},
		"events": []any{map[string]any{"type": "test", "content": map[string]any{"meta": map[string]any{"library_version": "6.20.1"}}}},
	})
	require.NoError(t, err)
	server := &Server{requests: []RawRequest{
		{Method: http.MethodPost, Path: constants.SettingsURLPath},
		{Method: http.MethodPost, Path: constants.KnownTestsURLPath},
		{Method: http.MethodPost, Path: constants.TestManagementTestsURLPath},
		{Method: http.MethodGet, Path: constants.SkippableTestsURLPath},
		{Method: http.MethodPost, Path: constants.TestCycleURLPath, Body: payload},
	}}
	facts, err := server.Facts()
	require.NoError(t, err)
	require.True(t, facts.RequestsObserved)
	require.Equal(t, []string{"6.20.1"}, facts.TracerVersions, "event overrides must not make the default look loaded")
	require.Equal(t, []string{"30.1.2"}, facts.FrameworkVersions)
	require.Equal(t, []string{"22.14.0"}, facts.RuntimeVersions)
	require.Equal(t, 1, facts.SettingsRequests)
	require.Equal(t, 1, facts.KnownTestsRequests)
	require.Equal(t, 1, facts.ManagementRequests)
	require.Zero(t, facts.SkippableRequests, "GET is not a tracer control request")
	require.Empty(t, facts.Events, "ordinary runs must not retain customer event tags")
	_, err = (&Server{requests: []RawRequest{{Method: http.MethodPost, Path: constants.TestCycleURLPath, Body: []byte("invalid")}}}).Facts()
	require.Error(t, err)
}

func TestOrdinaryFactsAvoidsCopyingAllSharedTags(t *testing.T) {
	shared := map[string]any{"runtime.version": "22.14.0", "library_version": "6.20.1"}
	for i := range 256 {
		shared[fmt.Sprintf("private.tag.%d", i)] = strings.Repeat("private-value", 8)
	}
	var events []any
	for i := range 128 {
		events = append(events, map[string]any{"type": "test", "content": map[string]any{"span_id": uint64(i + 1)}})
	}
	payload, err := msgp.AppendIntf(nil, map[string]any{"metadata": map[string]any{"*": shared}, "events": events})
	require.NoError(t, err)
	allocated := func(collect bool) int64 {
		server := &Server{collectEvents: collect, requests: []RawRequest{{Method: http.MethodPost, Path: constants.TestCycleURLPath, Body: payload}}}
		measurement := testing.Benchmark(func(b *testing.B) {
			for range b.N {
				if _, err := server.Facts(); err != nil {
					b.Fatal(err)
				}
			}
		})
		return measurement.AllocedBytesPerOp()
	}
	ordinary, probe := allocated(false), allocated(true)
	t.Logf("ordinary facts: %d bytes/op; collected probe facts: %d bytes/op", ordinary, probe)
	require.Less(t, ordinary*2, probe, "ordinary runs must avoid the full tag-copy allocation required by probe runs")
}

func TestTestReferencesReadReportFields(t *testing.T) {
	payload := msgp.AppendMapHeader(nil, 1)
	payload = msgp.AppendString(payload, "events")
	payload = msgp.AppendArrayHeader(payload, 1)
	payload = appendDetailedTest(payload, 10, 20, 30, "works", "one.test.js", "pass", 250*time.Millisecond, true, "")

	server := &Server{requests: []RawRequest{{Method: http.MethodPost, Path: constants.TestCycleURLPath, Body: payload}}}
	tests, err := server.testReferences()
	require.NoError(t, err)
	require.Equal(t, []testReference{{
		sessionID:   10,
		suiteID:     20,
		spanID:      30,
		name:        "works",
		suite:       "one.test.js",
		sourceFile:  "one.test.js",
		sourceStart: 12,
		sourceEnd:   14,
		status:      "pass",
		duration:    250 * time.Millisecond,
		isRetry:     true,
		retryReason: "early_flake_detection",
	}}, tests)
}

func TestTestReferencesReadErrors(t *testing.T) {
	payload := msgp.AppendMapHeader(nil, 1)
	payload = msgp.AppendString(payload, "events")
	payload = msgp.AppendArrayHeader(payload, 1)
	payload = appendDetailedTest(payload, 10, 20, 30, "breaks", "one.test.js", "fail", time.Millisecond, false, "expected true")

	server := &Server{requests: []RawRequest{{Method: http.MethodPost, Path: constants.TestCycleURLPath, Body: payload}}}
	tests, err := server.testReferences()
	require.NoError(t, err)
	require.Len(t, tests, 1)
	require.Equal(t, "AssertionError", tests[0].errorType)
	require.Equal(t, "expected true", tests[0].errorMessage)
	require.Equal(t, "stack trace", tests[0].errorStack)
}

func TestTestReferencesKeepAttemptAndFinalStatus(t *testing.T) {
	payload := msgp.AppendMapHeader(nil, 1)
	payload = msgp.AppendString(payload, "events")
	payload = msgp.AppendArrayHeader(payload, 1)
	payload = appendDetailedTestWithFinalStatus(payload, 10, 20, 30, "flaky", "one.test.js", "fail", "pass", time.Millisecond, true, "")

	server := &Server{requests: []RawRequest{{Method: http.MethodPost, Path: constants.TestCycleURLPath, Body: payload}}}
	tests, err := server.testReferences()
	require.NoError(t, err)
	require.Len(t, tests, 1)
	require.Equal(t, "fail", tests[0].status)
	require.Equal(t, "pass", tests[0].finalStatus)
}

func TestTestEventCountReportsInvalidPayload(t *testing.T) {
	server := &Server{requests: []RawRequest{{
		Method: http.MethodPost,
		Path:   constants.TestCycleURLPath,
		Body:   []byte("not msgpack"),
	}}}

	_, err := server.TestEventCount()
	require.ErrorContains(t, err, "recognize test events")
}

func TestTestEventCountRejectsTrailingBytes(t *testing.T) {
	payload := msgp.AppendMapHeader(nil, 1)
	payload = msgp.AppendString(payload, "events")
	payload = msgp.AppendArrayHeader(payload, 1)
	payload = appendEvent(payload, "test", 10, 20, 30)
	payload = msgp.AppendNil(payload)
	server := &Server{requests: []RawRequest{{
		Method: http.MethodPost,
		Path:   constants.TestCycleURLPath,
		Body:   payload,
	}}}

	count, err := server.TestEventCount()
	require.ErrorContains(t, err, "unexpected trailing MessagePack bytes")
	require.ErrorContains(t, err, "request 1 to "+constants.TestCycleURLPath)
	require.Zero(t, count)
}

func appendEvent(payload []byte, eventType string, sessionID, suiteID, spanID uint64) []byte {
	payload = msgp.AppendMapHeader(payload, 2)
	payload = msgp.AppendString(payload, "type")
	payload = msgp.AppendString(payload, eventType)
	payload = msgp.AppendString(payload, "content")
	payload = msgp.AppendMapHeader(payload, 3)
	payload = msgp.AppendString(payload, "test_session_id")
	payload = msgp.AppendUint64(payload, sessionID)
	payload = msgp.AppendString(payload, "test_suite_id")
	payload = msgp.AppendUint64(payload, suiteID)
	payload = msgp.AppendString(payload, "span_id")
	payload = msgp.AppendUint64(payload, spanID)
	return payload
}

func appendDetailedTest(
	payload []byte,
	sessionID, suiteID, spanID uint64,
	name, suite, status string,
	duration time.Duration,
	isRetry bool,
	errorMessage string,
) []byte {
	return appendDetailedTestWithFinalStatus(payload, sessionID, suiteID, spanID, name, suite, status, "", duration, isRetry, errorMessage)
}

func appendDetailedTestWithFinalStatus(
	payload []byte,
	sessionID, suiteID, spanID uint64,
	name, suite, status, finalStatus string,
	duration time.Duration,
	isRetry bool,
	errorMessage string,
) []byte {
	payload = msgp.AppendMapHeader(payload, 2)
	payload = msgp.AppendString(payload, "type")
	payload = msgp.AppendString(payload, "test")
	payload = msgp.AppendString(payload, "content")
	payload = msgp.AppendMapHeader(payload, 6)
	metadataFields := uint32(5)
	if isRetry {
		metadataFields++
	}
	if errorMessage != "" {
		metadataFields += 3
	}
	if finalStatus != "" {
		metadataFields++
	}
	payload = msgp.AppendString(payload, "test_session_id")
	payload = msgp.AppendUint64(payload, sessionID)
	payload = msgp.AppendString(payload, "test_suite_id")
	payload = msgp.AppendUint64(payload, suiteID)
	payload = msgp.AppendString(payload, "span_id")
	payload = msgp.AppendUint64(payload, spanID)
	payload = msgp.AppendString(payload, "duration")
	payload = msgp.AppendInt64(payload, int64(duration))
	payload = msgp.AppendString(payload, "meta")
	payload = msgp.AppendMapHeader(payload, metadataFields)
	payload = msgp.AppendString(payload, "test.name")
	payload = msgp.AppendString(payload, name)
	payload = msgp.AppendString(payload, "test.suite")
	payload = msgp.AppendString(payload, suite)
	payload = msgp.AppendString(payload, "test.status")
	payload = msgp.AppendString(payload, status)
	if finalStatus != "" {
		payload = msgp.AppendString(payload, "test.final_status")
		payload = msgp.AppendString(payload, finalStatus)
	}
	payload = msgp.AppendString(payload, "test.source.file")
	payload = msgp.AppendString(payload, suite)
	payload = msgp.AppendString(payload, "test.is_retry")
	payload = msgp.AppendString(payload, strconv.FormatBool(isRetry))
	if isRetry {
		payload = msgp.AppendString(payload, "test.retry_reason")
		payload = msgp.AppendString(payload, "early_flake_detection")
	}
	if errorMessage != "" {
		payload = msgp.AppendString(payload, "error.type")
		payload = msgp.AppendString(payload, "AssertionError")
		payload = msgp.AppendString(payload, "error.message")
		payload = msgp.AppendString(payload, errorMessage)
		payload = msgp.AppendString(payload, "error.stack")
		payload = msgp.AppendString(payload, "stack trace")
	}
	payload = msgp.AppendString(payload, "metrics")
	payload = msgp.AppendMapHeader(payload, 3)
	payload = msgp.AppendString(payload, "test.is_retry")
	if isRetry {
		payload = msgp.AppendFloat64(payload, 1)
	} else {
		payload = msgp.AppendFloat64(payload, 0)
	}
	payload = msgp.AppendString(payload, "test.source.start")
	payload = msgp.AppendFloat64(payload, 12)
	payload = msgp.AppendString(payload, "test.source.end")
	payload = msgp.AppendFloat64(payload, 14)
	return payload
}
