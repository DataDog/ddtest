package minitracer

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/cidelivery"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/citransport"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/msgp/msgp"
)

func TestDeferredClientKeepsPayloadLimitsAndOrder(t *testing.T) {
	var active atomic.Bool
	var requests atomic.Int32
	var names []string
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if active.Load() {
			t.Error("sent inside an active test")
		}
		requests.Add(1)
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		if len(data) > citransport.TestCycleMaxPayloadBytes {
			t.Error("oversized deferred batch")
		}
		var payload testCyclePayload
		var events ciEvents
		if err := msgp.Decode(bytes.NewReader(data), &payload); err != nil {
			t.Error(err)
			return
		}
		if err := msgp.Decode(bytes.NewReader(payload.Events), &events); err != nil {
			t.Error(err)
			return
		}
		if len(events) > 2 {
			t.Error("event count exceeded the batch limit")
		}
		mu.Lock()
		for _, event := range events {
			names = append(names, event.Content.Name)
		}
		mu.Unlock()
		w.WriteHeader(202)
	}))
	defer server.Close()
	client, err := New(Config{DeferUntilIdle: true, MaxEvents: 2, Transport: citransport.Config{Endpoint: server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	release := cidelivery.Begin()
	active.Store(true)
	for _, name := range []string{"first", "second", "third", "fourth", "fifth"} {
		span, _ := client.StartSpan(context.Background(), name, Tag("large", strings.Repeat("x", 1<<20)))
		span.Finish()
	}
	if err := client.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 0 {
		t.Fatal("flush ran while the test was active")
	}
	active.Store(false)
	release()
	if requests.Load() != 3 || client.DroppedEvents() != 0 {
		t.Fatal("invalid deferred delivery", requests.Load(), client.DroppedEvents())
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(names, ",") != "first,second,third,fourth,fifth" {
		t.Fatal("changed event order", names)
	}
}

func TestDeferredFailureRetainsUnsentChunks(t *testing.T) {
	var attempts atomic.Int32
	var delivered atomic.Int32
	var failing atomic.Bool
	failing.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 2 && failing.Load() {
			w.WriteHeader(401)
			return
		}
		var payload testCyclePayload
		var events ciEvents
		if err := msgp.Decode(r.Body, &payload); err != nil {
			t.Error(err)
			return
		}
		if err := msgp.Decode(bytes.NewReader(payload.Events), &events); err != nil {
			t.Error(err)
			return
		}
		delivered.Add(int32(len(events)))
		w.WriteHeader(202)
	}))
	defer server.Close()
	client, err := New(Config{DeferUntilIdle: true, MaxEvents: 2, Transport: citransport.Config{Endpoint: server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		s, _ := client.StartSpan(context.Background(), "test")
		s.Finish()
	}
	if err := client.Flush(context.Background()); err == nil {
		t.Fatal("lost delivery failure")
	}
	if delivered.Load() != 2 || client.DroppedEvents() != 0 {
		t.Fatal("failed flush discarded events")
	}
	failing.Store(false)
	if err := client.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if delivered.Load() != 5 {
		t.Fatal("lost or duplicated retained chunks", delivered.Load())
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// A serial suite reaches an idle checkpoint after every test. Only full
// batches are delivered there; the rest waits for Close.
func TestDeferredCheckpointsDeliverOnlyFullBatches(t *testing.T) {
	var requests, events atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var payload testCyclePayload
		var batch ciEvents
		if err := msgp.Decode(r.Body, &payload); err != nil {
			t.Error(err)
			return
		}
		if err := msgp.Decode(bytes.NewReader(payload.Events), &batch); err != nil {
			t.Error(err)
			return
		}
		events.Add(int32(len(batch)))
		w.WriteHeader(202)
	}))
	defer server.Close()
	client, err := New(Config{DeferUntilIdle: true, MaxEvents: 4, Transport: citransport.Config{Endpoint: server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		release := cidelivery.Begin()
		span, _ := client.StartSpan(context.Background(), "test", SpanType("test"))
		span.Finish()
		release()
	}
	if requests.Load() != 2 || events.Load() != 8 {
		t.Fatalf("checkpoints sent partial batches: requests=%d events=%d", requests.Load(), events.Load())
	}
	if err := client.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 3 || events.Load() != 10 {
		t.Fatalf("close lost the partial batch: requests=%d events=%d", requests.Load(), events.Load())
	}
}
