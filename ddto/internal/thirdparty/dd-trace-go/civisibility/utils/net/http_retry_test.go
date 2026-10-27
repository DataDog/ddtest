// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package net

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// recordRetrySleeps replaces the wait between attempts. Tests using it must not
// call t.Parallel: parallel tests in this package use real waits.
func recordRetrySleeps(t *testing.T) func() []time.Duration {
	t.Helper()
	var mu sync.Mutex
	var sleeps []time.Duration
	original := retrySleep
	retrySleep = func(d time.Duration) {
		mu.Lock()
		sleeps = append(sleeps, d)
		mu.Unlock()
	}
	t.Cleanup(func() { retrySleep = original })
	return func() []time.Duration {
		mu.Lock()
		defer mu.Unlock()
		return append([]time.Duration(nil), sleeps...)
	}
}

// Every failure kind keeps its attempt count, waits between attempts and
// returns its error as soon as the final attempt fails.
func TestSendRequestWaitsOnlyBetweenAttempts(t *testing.T) {
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()

	cases := []struct {
		name    string
		handler http.HandlerFunc
		url     string
		json    bool
	}{
		{name: "network error", url: closedURL},
		{name: "server error", handler: func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		}},
		{name: "rate limit without reset", handler: func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "slow down", HTTPStatusTooManyRequests)
		}},
		{name: "unexpected response format", json: true, handler: func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set(HeaderContentType, "text/plain")
			_, _ = w.Write([]byte("not json"))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sleeps := recordRetrySleeps(t)
			var attempts atomic.Int32
			url := tc.url
			if tc.handler != nil {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					attempts.Add(1)
					tc.handler(w, r)
				}))
				defer server.Close()
				url = server.URL
			}
			handler := NewRequestHandlerWithClient(createNewHTTPClient())
			response, err := handler.SendRequest(RequestConfig{Method: http.MethodGet, URL: url, MaxRetries: 3, Backoff: 100 * time.Millisecond, ExpectJSONResponse: tc.json})
			if err == nil || response != nil {
				t.Fatalf("expected exhausted retries, got response=%v err=%v", response, err)
			}
			if tc.handler != nil && attempts.Load() != 4 {
				t.Fatalf("attempts = %d, want 4", attempts.Load())
			}
			want := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond}
			if got := sleeps(); !reflect.DeepEqual(got, want) {
				t.Fatalf("waits = %v, want %v (none after the final attempt)", got, want)
			}
		})
	}
}

// The rate-limit reset wait also separates attempts only.
func TestSendRequestRateLimitResetWaitsOnlyBetweenAttempts(t *testing.T) {
	sleeps := recordRetrySleeps(t)
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.Header().Set(HeaderRateLimitReset, "2")
		http.Error(w, "slow down", HTTPStatusTooManyRequests)
	}))
	defer server.Close()

	handler := NewRequestHandlerWithClient(createNewHTTPClient())
	if response, err := handler.SendRequest(RequestConfig{Method: http.MethodGet, URL: server.URL, MaxRetries: 2, Backoff: time.Millisecond}); err == nil || response != nil {
		t.Fatalf("expected exhausted retries, got response=%v err=%v", response, err)
	}
	if attempts.Load() != 3 {
		t.Fatalf("attempts = %d, want 3", attempts.Load())
	}
	if got, want := sleeps(), []time.Duration{2 * time.Second, 2 * time.Second}; !reflect.DeepEqual(got, want) {
		t.Fatalf("waits = %v, want %v", got, want)
	}
}

// An x-ratelimit-reset within a minute is waited for; a later reset, as a
// Unix timestamp or as seconds, falls back to the exponential backoff, like
// the test-cycle client's Retry-After bound.
func TestSendRequestBoundsRateLimitResetWait(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reset func() string
		want  []time.Duration
	}{
		{name: "seconds within bound", reset: func() string { return "60" }, want: []time.Duration{time.Minute}},
		{name: "seconds beyond bound", reset: func() string { return "61" }, want: []time.Duration{10 * time.Millisecond}},
		{name: "timestamp beyond bound", reset: func() string { return strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10) }, want: []time.Duration{10 * time.Millisecond}},
		{name: "past reset", reset: func() string { return "0" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sleeps := recordRetrySleeps(t)
			var attempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				attempts.Add(1)
				w.Header().Set(HeaderRateLimitReset, tc.reset())
				http.Error(w, "slow down", HTTPStatusTooManyRequests)
			}))
			defer server.Close()

			handler := NewRequestHandlerWithClient(createNewHTTPClient())
			if response, err := handler.SendRequest(RequestConfig{Method: http.MethodGet, URL: server.URL, MaxRetries: 1, Backoff: 10 * time.Millisecond}); err == nil || response != nil {
				t.Fatalf("expected exhausted retries, got response=%v err=%v", response, err)
			}
			if attempts.Load() != 2 {
				t.Fatalf("attempts = %d, want 2", attempts.Load())
			}
			if got := sleeps(); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("waits = %v, want %v", got, tc.want)
			}
		})
	}
}
