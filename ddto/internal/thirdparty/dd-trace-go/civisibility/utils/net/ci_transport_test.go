// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package net

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Concurrent HTTPS requests to one host share a single HTTP/2 connection, so
// they start one dial: no extra dial finishes in the transport's goroutine
// after its request took another connection. Plain HTTP keeps concurrent
// connections.
func TestCITransportDialsOnceForConcurrentHTTPSRequests(t *testing.T) {
	var mu sync.Mutex
	protocols := map[string]bool{}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		protocols[r.Proto] = true
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}))
	server.EnableHTTP2 = true
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
		// A slow dial, as over a real network, lets the requests overlap it.
		time.Sleep(20 * time.Millisecond)
		return dial(ctx, network, address)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 5)
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := client.Get(server.URL)
			if err == nil {
				_, err = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := dials.Load(); n != 1 {
		t.Fatalf("%d dials for concurrent HTTPS requests, want 1", n)
	}
	mu.Lock()
	defer mu.Unlock()
	if !protocols["HTTP/2.0"] || len(protocols) != 1 {
		t.Fatalf("protocols %v, want HTTP/2.0 only", protocols)
	}
	if transport.plain.MaxConnsPerHost != 0 {
		t.Fatalf("plain HTTP is limited to %d connections per host", transport.plain.MaxConnsPerHost)
	}
}

// A connection uses TLS for an https destination, and for any destination
// through an https proxy; only those go through the transport that limits
// connections per host.
func TestCITransportRoutesTLSConnections(t *testing.T) {
	proxy := func(raw string) func(*http.Request) (*url.URL, error) {
		return func(*http.Request) (*url.URL, error) {
			if raw == "" {
				return nil, nil
			}
			return url.Parse(raw)
		}
	}
	for _, tc := range []struct {
		target, proxy string
		tls           bool
	}{
		{"http://agent:8126/evp_proxy/v2/x", "", false},
		{"https://api.datadoghq.com/x", "", true},
		{"http://agent:8126/evp_proxy/v2/x", "http://proxy:3128", false},
		{"http://agent:8126/evp_proxy/v2/x", "https://proxy:8443", true},
		{"https://api.datadoghq.com/x", "http://proxy:3128", true},
	} {
		transport := &ciTransport{plain: &http.Transport{Proxy: proxy(tc.proxy)}, secure: &http.Transport{}}
		req, err := http.NewRequest(http.MethodGet, tc.target, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := transport.usesTLS(req); got != tc.tls {
			t.Errorf("%s through %q: usesTLS=%t, want %t", tc.target, tc.proxy, got, tc.tls)
		}
	}
}

// The client does not follow a redirect, which would send the API key header
// to the redirect's host.
func TestCIClientDoesNotFollowRedirects(t *testing.T) {
	var reached atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
	}))
	defer other.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/steal", http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	client := createNewHTTPClient()
	defer client.CloseIdleConnections()
	request, err := http.NewRequest(http.MethodPost, redirect.URL+"/api/v2/libraries/tests/services/setting", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("dd-api-key", "secret")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusTemporaryRedirect || reached.Load() != 0 {
		t.Fatalf("status %d, redirect host reached %d times", response.StatusCode, reached.Load())
	}
}
