// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package intake

import (
	"net/http"
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
	payload = appendDetailedTest(payload, 10, 20, 30, "works", "one.test.js", "pass", 250*time.Millisecond, true)

	server := &Server{requests: []RawRequest{{Method: http.MethodPost, Path: testCyclePath, Body: payload}}}
	tests, err := server.testReferences()
	require.NoError(t, err)
	require.Equal(t, []testReference{{
		sessionID: 10,
		suiteID:   20,
		spanID:    30,
		name:      "works",
		suite:     "one.test.js",
		status:    "pass",
		duration:  250 * time.Millisecond,
		isRetry:   true,
	}}, tests)
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
) []byte {
	payload = msgp.AppendMapHeader(payload, 2)
	payload = msgp.AppendString(payload, "type")
	payload = msgp.AppendString(payload, "test")
	payload = msgp.AppendString(payload, "content")
	payload = msgp.AppendMapHeader(payload, 6)
	payload = msgp.AppendString(payload, "test_session_id")
	payload = msgp.AppendUint64(payload, sessionID)
	payload = msgp.AppendString(payload, "test_suite_id")
	payload = msgp.AppendUint64(payload, suiteID)
	payload = msgp.AppendString(payload, "span_id")
	payload = msgp.AppendUint64(payload, spanID)
	payload = msgp.AppendString(payload, "duration")
	payload = msgp.AppendInt64(payload, int64(duration))
	payload = msgp.AppendString(payload, "meta")
	payload = msgp.AppendMapHeader(payload, 4)
	payload = msgp.AppendString(payload, "test.name")
	payload = msgp.AppendString(payload, name)
	payload = msgp.AppendString(payload, "test.suite")
	payload = msgp.AppendString(payload, suite)
	payload = msgp.AppendString(payload, "test.status")
	payload = msgp.AppendString(payload, status)
	payload = msgp.AppendString(payload, "test.is_retry")
	payload = msgp.AppendString(payload, "false")
	payload = msgp.AppendString(payload, "metrics")
	payload = msgp.AppendMapHeader(payload, 1)
	payload = msgp.AppendString(payload, "test.is_retry")
	if isRetry {
		payload = msgp.AppendFloat64(payload, 1)
	} else {
		payload = msgp.AppendFloat64(payload, 0)
	}
	return payload
}
