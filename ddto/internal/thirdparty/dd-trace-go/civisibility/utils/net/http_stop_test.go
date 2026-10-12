// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

package net

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// A handler with a context stops its request in flight, and its retries and
// their waits, when the context is done: ddto's Git worker stops this way.
func TestRequestHandlerStopsWithContext(t *testing.T) {
	var requests atomic.Int32
	arrived := make(chan struct{}, 4)
	// A request that the handler does not stop ends with the test, so that a
	// failure reports instead of hanging in server.Close.
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		arrived <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)

	ctx, cancel := context.WithCancel(context.Background())
	handler := &RequestHandler{Client: server.Client(), Context: ctx}
	done := make(chan error, 1)
	go func() {
		_, err := handler.SendRequest(RequestConfig{Method: http.MethodGet, URL: server.URL, MaxRetries: 3, Backoff: time.Hour})
		done <- err
	}()
	<-arrived
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("stopped request returned %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the stopped request kept waiting")
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("a stopped request was retried: %d requests", got)
	}
	if _, err := handler.SendRequest(RequestConfig{Method: http.MethodGet, URL: server.URL}); !errors.Is(err, context.Canceled) || requests.Load() != 1 {
		t.Fatalf("a request after the stop was sent: %v, %d requests", err, requests.Load())
	}
}
