// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package intake

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tinylib/msgp/msgp"

	"github.com/DataDog/ddtest/internal/constants"
)

func TestCoveredTestCountAssociatesSuiteCoverageWithEveryTestInTheSuite(t *testing.T) {
	const sessionID = 10

	events := msgp.AppendMapHeader(nil, 1)
	events = msgp.AppendString(events, "events")
	events = msgp.AppendArrayHeader(events, 2)
	events = appendEvent(events, "test", sessionID, 20, 100)
	events = appendEvent(events, "test", sessionID, 20, 200)

	server := serverWithCoverage(t, events, appendCoverage(nil, sessionID, 20, 0))

	count, err := server.CoveredTestCount()
	require.NoError(t, err)
	require.Equal(t, 2, count)
}

func TestCoveredTestCountAssociatesTestCoverageBySpan(t *testing.T) {
	const sessionID = 10

	events := msgp.AppendMapHeader(nil, 1)
	events = msgp.AppendString(events, "events")
	events = msgp.AppendArrayHeader(events, 2)
	events = appendEvent(events, "test", sessionID, 20, 100)
	events = appendEvent(events, "test", sessionID, 20, 200)

	server := serverWithCoverage(t, events, appendCoverage(nil, sessionID, 20, 200))

	count, err := server.CoveredTestCount()
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestCoverageReferencesCountFiles(t *testing.T) {
	events := msgp.AppendMapHeader(nil, 1)
	events = msgp.AppendString(events, "events")
	events = msgp.AppendArrayHeader(events, 1)
	events = appendEvent(events, "test", 10, 20, 100)
	server := serverWithCoverage(t, events, appendCoverage(nil, 10, 20, 100, "one.js", "two.js"))

	coverages, err := server.coverageReferences()
	require.NoError(t, err)
	require.Len(t, coverages, 1)
	require.Equal(t, 2, coverages[0].fileCount)
	require.Equal(t, []string{"one.js", "two.js"}, coverages[0].files)
}

func TestCoveredTestCountAcceptsMessagePackMediaTypes(t *testing.T) {
	for _, contentType := range []string{"application/msgpack", "application/x-msgpack", "application/x-msgpack; charset=binary"} {
		t.Run(contentType, func(t *testing.T) {
			events := msgp.AppendMapHeader(nil, 1)
			events = msgp.AppendString(events, "events")
			events = msgp.AppendArrayHeader(events, 1)
			events = appendEvent(events, "test", 10, 20, 100)
			server := serverWithCoverage(t, events, appendCoverage(nil, 10, 20, 100, "one.js"))
			server.requests[1].Body = bytes.Replace(server.requests[1].Body, []byte("application/msgpack"), []byte(contentType), 1)

			count, err := server.CoveredTestCount()
			require.NoError(t, err)
			require.Equal(t, 1, count)
		})
	}
}

func TestCoveredTestCountRejectsTrailingBytes(t *testing.T) {
	events := msgp.AppendMapHeader(nil, 1)
	events = msgp.AppendString(events, "events")
	events = msgp.AppendArrayHeader(events, 1)
	events = appendEvent(events, "test", 10, 20, 100)
	coverage := appendCoverage(nil, 10, 20, 100, "one.js")
	server := serverWithCoverage(t, events, msgp.AppendNil(coverage))

	count, err := server.CoveredTestCount()
	require.ErrorContains(t, err, "unexpected trailing MessagePack bytes")
	require.ErrorContains(t, err, "recognize coverage in "+constants.TestCoverageURLPath)
	require.Zero(t, count)
}

func serverWithCoverage(t *testing.T, events, coverageEntry []byte) *Server {
	t.Helper()

	coverage := msgp.AppendMapHeader(nil, 2)
	coverage = msgp.AppendString(coverage, "version")
	coverage = msgp.AppendInt(coverage, 2)
	coverage = msgp.AppendString(coverage, "coverages")
	coverage = msgp.AppendArrayHeader(coverage, 1)
	coverage = append(coverage, coverageEntry...)

	return &Server{requests: []RawRequest{
		{Method: http.MethodPost, Path: constants.TestCycleURLPath, Body: events},
		multipartCoverageRequest(t, coverage),
	}}
}

func appendCoverage(payload []byte, sessionID, suiteID, spanID uint64, files ...string) []byte {
	fieldCount := uint32(3)
	if spanID != 0 {
		fieldCount++
	}
	payload = msgp.AppendMapHeader(payload, fieldCount)
	payload = msgp.AppendString(payload, "test_session_id")
	payload = msgp.AppendUint64(payload, sessionID)
	payload = msgp.AppendString(payload, "test_suite_id")
	payload = msgp.AppendUint64(payload, suiteID)
	if spanID != 0 {
		payload = msgp.AppendString(payload, "span_id")
		payload = msgp.AppendUint64(payload, spanID)
	}
	payload = msgp.AppendString(payload, "files")
	payload = msgp.AppendArrayHeader(payload, uint32(len(files)))
	for _, file := range files {
		payload = msgp.AppendMapHeader(payload, 1)
		payload = msgp.AppendString(payload, "filename")
		payload = msgp.AppendString(payload, file)
	}
	return payload
}

func multipartCoverageRequest(t *testing.T, coverage []byte) RawRequest {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := textproto.MIMEHeader{
		"Content-Disposition": {`form-data; name="coverage1"; filename="coverage1.msgpack"`},
		"Content-Type":        {"application/msgpack"},
	}
	part, err := writer.CreatePart(header)
	require.NoError(t, err)
	_, err = part.Write(coverage)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	return RawRequest{
		Method: http.MethodPost,
		Path:   constants.TestCoverageURLPath,
		Header: http.Header{"Content-Type": {writer.FormDataContentType()}},
		Body:   body.Bytes(),
	}
}
