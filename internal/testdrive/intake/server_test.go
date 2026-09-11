// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package intake

import (
	"bytes"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStart(t *testing.T) {
	server, err := Start()
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
	server, err := Start()
	require.NoError(t, err)

	require.NoError(t, server.Close())
	require.NoError(t, server.Close())

	_, err = testHTTPClient().Get(server.URL())
	require.Error(t, err)

	_, running := <-server.done
	require.False(t, running, "server goroutine did not finish")
}

func TestStartSupportsSimultaneousServers(t *testing.T) {
	first, err := Start()
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, first.Close())
	})

	second, err := Start()
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
	server, err := Start()
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
}

func testHTTPClient() *http.Client {
	return &http.Client{Timeout: time.Second}
}
