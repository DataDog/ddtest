// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

//go:build go1.26

package net

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// A request that finds the HTTP/2 connection's streams in use waits for one
// instead of dialing another connection that it could leave unused.
func TestCITransportWaitsForHTTP2Streams(t *testing.T) {
	entered, release := make(chan struct{}, 2), make(chan struct{})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
	}))
	server.EnableHTTP2 = true
	server.Config.HTTP2 = &http.HTTP2Config{MaxConcurrentStreams: 1}
	server.StartTLS()
	defer server.Close()

	client := createNewHTTPClient()
	transport := client.Transport.(*ciTransport)
	defer transport.CloseIdleConnections()
	transport.secure.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	var dials atomic.Int32
	dial := transport.secure.DialContext
	transport.secure.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		dials.Add(1)
		return dial(ctx, network, address)
	}
	get := func() <-chan error {
		done := make(chan error, 1)
		go func() {
			resp, err := client.Get(server.URL)
			if err == nil {
				_, err = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
			done <- err
		}()
		return done
	}
	first := get()
	<-entered // The first request holds the connection's only stream.
	second := get()
	// Without strict streams, the second request dials meanwhile.
	time.Sleep(200 * time.Millisecond)
	close(release)
	for _, done := range []<-chan error{first, second} {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if n := dials.Load(); n != 1 {
		t.Fatalf("%d dials with the stream limit reached, want 1", n)
	}
}
