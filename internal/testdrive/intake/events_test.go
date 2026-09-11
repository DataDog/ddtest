// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package intake

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tinylib/msgp/msgp"
)

func TestTestEventCountRecognizesOneTest(t *testing.T) {
	payload := msgp.AppendMapHeader(nil, 2)
	payload = msgp.AppendString(payload, "version")
	payload = msgp.AppendInt(payload, 1)
	payload = msgp.AppendString(payload, "events")
	payload = msgp.AppendArrayHeader(payload, 3)
	payload = appendEvent(payload, "test_session_end")
	payload = appendEvent(payload, "test")
	payload = appendEvent(payload, "test_suite_end")

	server := &Server{requests: []RawRequest{
		{Method: http.MethodPost, Path: "/another-endpoint", Body: []byte("not msgpack")},
		{Method: http.MethodPost, Path: testCyclePath, Body: payload},
	}}

	count, err := server.TestEventCount()
	require.NoError(t, err)
	require.Equal(t, 1, count)
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

func appendEvent(payload []byte, eventType string) []byte {
	payload = msgp.AppendMapHeader(payload, 2)
	payload = msgp.AppendString(payload, "type")
	payload = msgp.AppendString(payload, eventType)
	payload = msgp.AppendString(payload, "content")
	payload = msgp.AppendMapHeader(payload, 0)
	return payload
}
