package testopt

import (
	"context"
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
