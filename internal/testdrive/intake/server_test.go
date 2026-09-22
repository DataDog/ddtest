// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package intake

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tinylib/msgp/msgp"
)

func TestStart(t *testing.T) {
	server, err := Start(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, server.Close())
	})

	serverURL, err := url.Parse(server.URL())
	require.NoError(t, err)
	require.Equal(t, "http", serverURL.Scheme)
	host, port, err := net.SplitHostPort(serverURL.Host)
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1", host)
	require.NotEqual(t, "0", port)

	response, err := testHTTPClient().Get(server.URL())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, response.Body.Close())
	})
	require.Equal(t, http.StatusNotFound, response.StatusCode)
}

func TestCloseStopsServer(t *testing.T) {
	server, err := Start(t.TempDir())
	require.NoError(t, err)

	require.NoError(t, server.Close())
	require.NoError(t, server.Close())

	_, err = testHTTPClient().Get(server.URL())
	require.Error(t, err)

	_, running := <-server.done
	require.False(t, running, "server goroutine did not finish")
}

func TestStartSupportsSimultaneousServers(t *testing.T) {
	first, err := Start(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, first.Close())
	})

	second, err := Start(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, second.Close())
	})

	require.NotEqual(t, first.URL(), second.URL())
	for _, server := range []*Server{first, second} {
		response, requestErr := testHTTPClient().Get(server.URL())
		require.NoError(t, requestErr)
		require.NoError(t, response.Body.Close())
		require.Equal(t, http.StatusNotFound, response.StatusCode)
	}
}

func TestServerCapturesRawRequests(t *testing.T) {
	sessionDirectory := t.TempDir()
	server, err := Start(sessionDirectory)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, server.Close())
	})

	request, err := http.NewRequest(http.MethodPost, server.URL()+"/observed", bytes.NewBufferString("raw body"))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/octet-stream")
	response, err := testHTTPClient().Do(request)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, response.Body.Close())
	})
	require.Equal(t, http.StatusOK, response.StatusCode)

	requests := server.Requests()
	require.Len(t, requests, 1)
	require.Equal(t, http.MethodPost, requests[0].Method)
	require.Equal(t, "/observed", requests[0].Path)
	require.Equal(t, "application/octet-stream", requests[0].Header.Get("Content-Type"))
	require.Equal(t, []byte("raw body"), requests[0].Body)

	storedBytes, err := os.ReadFile(filepath.Join(sessionDirectory, intakeDirectoryName, "001-request.json"))
	require.NoError(t, err)
	var stored storedRequest
	require.NoError(t, json.Unmarshal(storedBytes, &stored))
	require.Equal(t, http.MethodPost, stored.Method)
	require.Equal(t, "/observed", stored.Path)
	var storedBody string
	require.NoError(t, json.Unmarshal(stored.Body, &storedBody))
	require.Equal(t, "raw body", storedBody)
}

func TestServerStoresMessagePackAsJSON(t *testing.T) {
	sessionDirectory := t.TempDir()
	server, err := Start(sessionDirectory)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, server.Close())
	})

	payload := msgp.AppendMapHeader(nil, 1)
	payload = msgp.AppendString(payload, "events")
	payload = msgp.AppendArrayHeader(payload, 0)
	response, err := testHTTPClient().Post(server.URL()+testCyclePath, "application/msgpack", bytes.NewReader(payload))
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, http.StatusOK, response.StatusCode)

	intakeDirectory := filepath.Join(sessionDirectory, intakeDirectoryName)
	files, err := os.ReadDir(intakeDirectory)
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, "001-citestcycle.json", files[0].Name())
	storedBytes, err := os.ReadFile(filepath.Join(intakeDirectory, files[0].Name()))
	require.NoError(t, err)
	require.True(t, json.Valid(storedBytes))
	require.Contains(t, string(storedBytes), `"events": []`)
}

func TestServerStoresAndRecognizesGzippedMessagePack(t *testing.T) {
	sessionDirectory := t.TempDir()
	server, err := Start(sessionDirectory)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, server.Close())
	})

	payload := msgp.AppendMapHeader(nil, 1)
	payload = msgp.AppendString(payload, "events")
	payload = msgp.AppendArrayHeader(payload, 1)
	payload = appendEvent(payload, "test", 10, 20, 30)
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, err = writer.Write(payload)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	request, err := http.NewRequest(http.MethodPost, server.URL()+testCyclePath, bytes.NewReader(compressed.Bytes()))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/msgpack")
	request.Header.Set("Content-Encoding", "gzip")
	response, err := testHTTPClient().Do(request)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, http.StatusOK, response.StatusCode)

	storedBytes, err := os.ReadFile(filepath.Join(sessionDirectory, intakeDirectoryName, "001-citestcycle.json"))
	require.NoError(t, err)
	require.True(t, json.Valid(storedBytes))
	require.Contains(t, string(storedBytes), `"type": "test"`)
}

func TestStartRejectsInvalidSessionDirectory(t *testing.T) {
	sessionPath := filepath.Join(t.TempDir(), "session-file")
	require.NoError(t, os.WriteFile(sessionPath, []byte("not a directory"), 0644))

	_, err := Start(sessionPath)
	require.ErrorContains(t, err, "create local testdrive intake directory")
}

func TestDecodeMultipartStoresEveryPartAsJSON(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	jsonHeader := textproto.MIMEHeader{}
	jsonHeader.Set("Content-Disposition", `form-data; name="metadata"`)
	jsonHeader.Set("Content-Type", "application/json")
	jsonPart, err := writer.CreatePart(jsonHeader)
	require.NoError(t, err)
	_, err = jsonPart.Write([]byte(`{"framework":"jest"}`))
	require.NoError(t, err)

	msgpackHeader := textproto.MIMEHeader{}
	msgpackHeader.Set("Content-Disposition", `form-data; name="events"; filename="events.msgpack"`)
	msgpackHeader.Set("Content-Type", "application/msgpack")
	msgpackPart, err := writer.CreatePart(msgpackHeader)
	require.NoError(t, err)
	payload := msgp.AppendMapHeader(nil, 1)
	payload = msgp.AppendString(payload, "count")
	payload = msgp.AppendInt(payload, 2)
	_, err = msgpackPart.Write(payload)
	require.NoError(t, err)

	plainPart, err := writer.CreateFormField("note")
	require.NoError(t, err)
	_, err = plainPart.Write([]byte("hello"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	decoded, err := decodeRequestBody(body.Bytes(), writer.FormDataContentType())
	require.NoError(t, err)
	var stored struct {
		Parts []storedMultipartPart `json:"parts"`
	}
	require.NoError(t, json.Unmarshal(decoded, &stored))
	require.Len(t, stored.Parts, 3)
	require.Equal(t, "metadata", stored.Parts[0].Name)
	require.JSONEq(t, `{"framework":"jest"}`, string(stored.Parts[0].Body))
	require.Equal(t, "events.msgpack", stored.Parts[1].Filename)
	require.JSONEq(t, `{"count":2}`, string(stored.Parts[1].Body))
	require.JSONEq(t, `"hello"`, string(stored.Parts[2].Body))
}

func TestRequestDecodingRejectsMalformedPayloads(t *testing.T) {
	tests := []struct {
		name        string
		body        []byte
		contentType string
		errorText   string
	}{
		{name: "JSON", body: []byte("{"), contentType: "application/json", errorText: "invalid JSON"},
		{name: "MessagePack", body: []byte{0xc1}, contentType: "application/msgpack", errorText: "msgp"},
		{name: "trailing MessagePack", body: append(msgp.AppendInt(nil, 1), 0), contentType: "application/x-msgpack", errorText: "trailing"},
		{name: "multipart", body: []byte("--unfinished"), contentType: "multipart/form-data; boundary=boundary", errorText: "EOF"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := decodeRequestBody(test.body, test.contentType)
			require.ErrorContains(t, err, test.errorText)
		})
	}
}

func TestUncompressRequestBodyHandlesIdentityAndErrors(t *testing.T) {
	body := []byte("plain")
	for _, encoding := range []string{"", " identity "} {
		decoded, err := uncompressRequestBody(RawRequest{Header: http.Header{"Content-Encoding": {encoding}}, Body: body})
		require.NoError(t, err)
		require.Equal(t, body, decoded)
	}

	_, err := uncompressRequestBody(RawRequest{Header: http.Header{"Content-Encoding": {"br"}}, Body: body})
	require.ErrorContains(t, err, "unsupported content encoding")

	_, err = uncompressRequestBody(RawRequest{Header: http.Header{"Content-Encoding": {"gzip"}}, Body: body})
	require.ErrorContains(t, err, "open gzip request body")
}

func TestRequestFileLabel(t *testing.T) {
	tests := map[string]string{
		settingsPath:       "settings",
		testCyclePath:      "citestcycle",
		testCoveragePath:   "citestcov",
		knownTestsPath:     "known-tests",
		skippableTestsPath: "skippable-tests",
		testManagementPath: "test-management",
		"/other":           "request",
	}
	for path, expected := range tests {
		require.Equal(t, expected, requestFileLabel(path))
	}
}

func testHTTPClient() *http.Client {
	return &http.Client{Timeout: time.Second}
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
