// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package intake

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tinylib/msgp/msgp"
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
		{Method: http.MethodPost, Path: testCyclePath, Body: payload},
	}}

	count, err := server.TestEventCount()
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestTestReferencesReadReportFields(t *testing.T) {
	payload := msgp.AppendMapHeader(nil, 1)
	payload = msgp.AppendString(payload, "events")
	payload = msgp.AppendArrayHeader(payload, 1)
	payload = appendDetailedTest(payload, 10, 20, 30, "works", "one.test.js", "pass", 250*time.Millisecond, true, "")

	server := &Server{requests: []RawRequest{{Method: http.MethodPost, Path: testCyclePath, Body: payload}}}
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

	server := &Server{requests: []RawRequest{{Method: http.MethodPost, Path: testCyclePath, Body: payload}}}
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

	server := &Server{requests: []RawRequest{{Method: http.MethodPost, Path: testCyclePath, Body: payload}}}
	tests, err := server.testReferences()
	require.NoError(t, err)
	require.Len(t, tests, 1)
	require.Equal(t, "fail", tests[0].status)
	require.Equal(t, "pass", tests[0].finalStatus)
}

func TestTestEventCountReportsInvalidPayload(t *testing.T) {
	server := &Server{requests: []RawRequest{{
		Method: http.MethodPost,
		Path:   testCyclePath,
		Body:   []byte("not msgpack"),
	}}}

	_, err := server.TestEventCount()
	require.ErrorContains(t, err, "recognize test events")
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
