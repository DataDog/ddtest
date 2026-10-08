package minitracer

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/cidelivery"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/citransport"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/msgp/msgp"
)

// Payloads sent together at a checkpoint can arrive in any order; each keeps
// its events in order, and every event arrives exactly once.
func TestDeferredClientKeepsPayloadLimitsAndOrder(t *testing.T) {
	var active atomic.Bool
	var requests atomic.Int32
	var payloads [][]string
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
		names := make([]string, 0, len(events))
		for _, event := range events {
			names = append(names, event.Content.Name)
		}
		mu.Lock()
		payloads = append(payloads, names)
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
	order := []string{"first", "second", "third", "fourth", "fifth"}
	mu.Lock()
	defer mu.Unlock()
	slices.SortFunc(payloads, func(a, b []string) int { return slices.Index(order, a[0]) - slices.Index(order, b[0]) })
	if got := slices.Concat(payloads...); !slices.Equal(got, order) {
		t.Fatal("changed, lost or duplicated events", payloads)
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
	for i := 0; i < 5; i++ {
		s, _ := client.StartSpan(context.Background(), "test")
		s.Finish()
	}
	if err := client.Flush(context.Background()); err == nil {
		t.Fatal("lost delivery failure")
	}
	// The batches are sent together; only the failed one stays queued.
	if delivered.Load() >= 5 || client.DroppedEvents() != 0 {
		t.Fatal("failed batch was not retained", delivered.Load(), client.DroppedEvents())
	}
	failing.Store(false)
	if err := client.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if delivered.Load() != 5 {
		t.Fatal("lost or duplicated retained chunks", delivered.Load())
	}
}

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
	for i := 0; i < 10; i++ {
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

// waitForNoDrainWorkers fails unless every checkpoint sender has exited. A
// worker can still be returning just after the checkpoint joined it.
func waitForNoDrainWorkers(t *testing.T) {
	t.Helper()
	stacks := make([]byte, 1<<20)
	for deadline := time.Now().Add(2 * time.Second); ; {
		n := runtime.Stack(stacks, true)
		if !strings.Contains(string(stacks[:n]), "minitracer.(*Client).drainWorker") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("a checkpoint sender outlived its checkpoint:\n%s", stacks[:n])
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// An idle checkpoint sends up to maxConcurrentSends batches at once, and every
// sender finishes before the checkpoint returns and the next test starts.
func TestDeferredCheckpointSendsConcurrently(t *testing.T) {
	var received deliveries
	var active, peak atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
		}
		time.Sleep(20 * time.Millisecond)
		received.record(t, r)
		w.WriteHeader(202)
	}))
	defer server.Close()
	client, err := New(Config{DeferUntilIdle: true, MaxEvents: 1, Transport: citransport.Config{Endpoint: server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	const total = 4 * maxConcurrentSends
	for i := 0; i < total; i++ {
		span, _ := client.StartSpan(context.Background(), "test", SpanType("test"))
		span.Finish()
	}
	if err := client.deliverReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	if delivered := received.distinct(t); delivered != total || client.DroppedEvents() != 0 {
		t.Fatalf("delivered=%d dropped=%d of %d", delivered, client.DroppedEvents(), total)
	}
	if p := peak.Load(); p < 2 || p > maxConcurrentSends {
		t.Fatalf("concurrent requests: %d", p)
	}
	waitForNoDrainWorkers(t)
}

// After a failed delivery a checkpoint starts no further batch, so an
// unavailable intake costs one round of concurrent attempts, not one per batch.
func TestDeferredCheckpointStopsAfterFailure(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(503)
	}))
	defer server.Close()
	client, err := New(Config{DeferUntilIdle: true, MaxEvents: 1, Transport: citransport.Config{Endpoint: server.URL, Attempts: 1}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close(context.Background()) }()
	const total = 4 * maxConcurrentSends
	for i := 0; i < total; i++ {
		span, _ := client.StartSpan(context.Background(), "test", SpanType("test"))
		span.Finish()
	}
	if err := client.deliverReady(context.Background()); err == nil {
		t.Fatal("lost delivery failure")
	}
	if n := requests.Load(); n < 1 || n > maxConcurrentSends {
		t.Fatalf("requests after the first failure: %d", n)
	}
	client.mu.Lock()
	retained := len(client.ready)
	client.mu.Unlock()
	if retained != total || client.DroppedEvents() != 0 {
		t.Fatalf("retained=%d dropped=%d of %d", retained, client.DroppedEvents(), total)
	}
	waitForNoDrainWorkers(t)
}
