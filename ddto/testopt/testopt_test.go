package testopt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// Options are functions: callers can write their own and apply them directly.
func TestStartSpanOptionsAreFunctions(t *testing.T) {
	client, err := New(Config{Transport: TransportConfig{Endpoint: "http://127.0.0.1:9"}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	var team StartSpanOption = func(s *Span) { s.SetTag("team", "payments") }
	options := []StartSpanOption{ResourceName("resource"), SpanType("test"), Tag("key", "value"), team}
	span, _ := client.StartSpan(context.Background(), "test", options...)
	Tag("applied", "directly")(span)
	for key, want := range map[string]string{"team": "payments", "key": "value", "applied": "directly"} {
		if got, _ := span.Meta(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

// A nil client still starts events, which are never sent.
func TestNilClientStartsEvents(t *testing.T) {
	var client *Client
	span, _ := client.StartSpan(context.Background(), "test", Tag("key", "value"))
	span.Finish()
	if got, _ := span.Meta("key"); got != "value" {
		t.Fatalf("key = %q", got)
	}
}

func observe[T any](option func(*T), hook func()) func(*T) {
	return func(config *T) {
		hook()
		option(config)
	}
}

// Finish options are functions of an opaque configuration, which callers can
// decorate.
func TestFinishOptionsCanBeDecorated(t *testing.T) {
	span, _ := (*Client)(nil).StartSpan(context.Background(), "test")
	called := false
	span.Finish(observe(FinishTime(time.Now()), func() { called = true }))
	if !called {
		t.Fatal("the decorated option did not run")
	}
}

// An explicit client delivers its events through the public API alone, and
// reports neither an error nor a dropped event once they arrive.
func TestClientDeliversThroughThePublicAPI(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	client, err := New(Config{Transport: TransportConfig{Endpoint: server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(-time.Second)
	span, ctx := client.StartSpan(Context(t), "test", StartTime(start), SpanType("test"))
	if ctx == nil {
		t.Fatal("no context for the event")
	}
	span.Finish(FinishTime(start.Add(time.Millisecond)))
	if err := client.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if requests.Load() == 0 || client.LastError() != nil || client.DroppedEvents() != 0 {
		t.Fatal("delivery", requests.Load(), client.LastError(), client.DroppedEvents())
	}
	if err := client.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	RegisterTestEnvironment("fingerprint")
	if callerEnvironment != "fingerprint" {
		t.Fatalf("callerEnvironment = %q", callerEnvironment)
	}
}
