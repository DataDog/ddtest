package minitracer

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/citransport"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/msgp/msgp"
	"github.com/tonyredondo/dd-ci-testing-poc/propagation"
)

func TestNativeEventsConcurrentFinishAndHierarchy(t *testing.T) {
	var mu sync.Mutex
	var received []*ciEvent
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		var envelope testCyclePayload
		if err = msgp.Decode(bytes.NewReader(data), &envelope); err != nil {
			t.Error(err)
			return
		}
		var events ciEvents
		if err = msgp.Decode(bytes.NewReader(envelope.Events), &events); err != nil {
			t.Error(err)
			return
		}
		if envelope.Version != 1 || envelope.Metadata["*"]["language"] != "go" {
			t.Error("invalid envelope")
		}
		mu.Lock()
		received = append(received, events...)
		mu.Unlock()
		w.WriteHeader(202)
	}))
	defer server.Close()
	client, err := New(Config{Service: "fixture", Env: "test", Transport: citransport.Config{Endpoint: server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			span, ctx := client.StartSpan(context.Background(), "testing.test", SpanType("test"), Tag("test_session_id", "10"), Tag("test_module_id", "20"), Tag("test_suite_id", "30"), Tag("test.status", "pass"))
			identity, ok := propagation.FromContext(ctx)
			if !ok || identity.SpanID != span.Context().SpanID() {
				t.Error("missing trace context")
			}
			span.Finish()
			span.Finish()
		}()
	}
	wg.Wait()
	if err = client.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(received) != 100 {
		t.Fatalf("got %d events", len(received))
	}
	ids := map[uint64]bool{}
	for _, e := range received {
		if e.Type != "test" || e.Version != 2 || e.Content.SessionID != 10 || e.Content.ModuleID != 20 || e.Content.SuiteID != 30 || e.Content.ParentID != 0 || e.Content.Meta["test.status"] != "pass" {
			t.Fatalf("invalid native event: %+v", e)
		}
		if ids[e.Content.SpanID] {
			t.Fatal("duplicate span ID")
		}
		ids[e.Content.SpanID] = true
		if _, exists := e.Content.Meta["test_session_id"]; exists {
			t.Fatal("duplicated hierarchy field")
		}
	}
}
func TestFailedFlushRetainsBatch(t *testing.T) {
	var attempts atomic.Int32
	var events atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.WriteHeader(401)
			return
		}
		var envelope testCyclePayload
		if err := msgp.Decode(r.Body, &envelope); err != nil {
			t.Error(err)
			return
		}
		var batch ciEvents
		if err := msgp.Decode(bytes.NewReader(envelope.Events), &batch); err != nil {
			t.Error(err)
			return
		}
		events.Add(int32(len(batch)))
		w.WriteHeader(202)
	}))
	defer server.Close()
	client, err := New(Config{Transport: citransport.Config{Endpoint: server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	span, _ := client.StartSpan(context.Background(), "test", SpanType("test"))
	span.Finish()
	if err = client.Flush(context.Background()); err == nil || client.LastError() == nil {
		t.Fatal("lost delivery failure")
	}
	if err = client.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if events.Load() != 1 {
		t.Fatal("lost or duplicated retained event")
	}
}

func TestFlushCancellationWhileAnotherFlushRuns(t *testing.T) {
	for _, deferred := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "deferred"}[deferred], func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release; w.WriteHeader(202) }))
			defer server.Close()
			client, err := New(Config{DeferUntilIdle: deferred, Transport: citransport.Config{Endpoint: server.URL}})
			if err != nil {
				t.Fatal(err)
			}
			span, _ := client.StartSpan(context.Background(), "test", SpanType("test"))
			span.Finish()
			done := make(chan error, 1)
			go func() { done <- client.Flush(context.Background()) }()
			<-entered
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			err = client.Flush(ctx)
			close(release)
			if err != context.DeadlineExceeded {
				t.Fatalf("uncancelable queue: %v", err)
			}
			if err = <-done; err != nil {
				t.Fatal(err)
			}
			if err = client.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestQueueBoundAndConcurrentBackpressure(t *testing.T) {
	var failing atomic.Bool
	failing.Store(true)
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failing.Load() {
			w.WriteHeader(401)
			return
		}
		var envelope testCyclePayload
		if err := msgp.Decode(r.Body, &envelope); err != nil {
			t.Error(err)
			return
		}
		var events ciEvents
		if err := msgp.Decode(bytes.NewReader(envelope.Events), &events); err != nil {
			t.Error(err)
			return
		}
		count.Add(int32(len(events)))
		w.WriteHeader(202)
	}))
	defer server.Close()
	client, err := New(Config{MaxEvents: 2, Transport: citransport.Config{Endpoint: server.URL, Attempts: 1}})
	if err != nil {
		t.Fatal(err)
	}
	finish := func() { span, _ := client.StartSpan(context.Background(), "test", SpanType("test")); span.Finish() }
	// While intake fails, sealed batches are retained up to the bound plus the
	// open batch. Once a delivery has failed, further events are rejected
	// instead of waiting for a sender.
	retained := 2 * (maxPendingBatches + 1)
	for range retained + 3 {
		finish()
	}
	if client.DroppedEvents() != 3 || client.LastError() == nil {
		t.Fatal("unreported rejected events")
	}
	client.mu.Lock()
	sealed, open := len(client.ready)+client.inflight, len(client.events)
	client.mu.Unlock()
	if sealed != maxPendingBatches || open != 2 {
		t.Fatalf("queue exceeded bound: sealed=%d open=%d", sealed, open)
	}
	// A successful explicit flush delivers the retained batches and clears the
	// background backoff. Concurrent finishers within the bound all succeed.
	failing.Store(false)
	if err := client.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range retained {
		wg.Go(finish)
	}
	wg.Wait()
	if err := client.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if count.Load() != int32(2*retained) || client.DroppedEvents() != 3 {
		t.Fatalf("delivery=%d dropped=%d", count.Load(), client.DroppedEvents())
	}
	finish()
	if client.DroppedEvents() != 4 {
		t.Fatal("post-close event accepted")
	}
}

// TestFinishNeverWaitsForIntake covers the failure mode that previously made
// tests slow: below the pending bound, a blackholed endpoint delays delivery,
// never Finish.
func TestFinishNeverWaitsForIntake(t *testing.T) {
	release := make(chan struct{})
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		var envelope testCyclePayload
		if err := msgp.Decode(r.Body, &envelope); err != nil {
			t.Error(err)
			return
		}
		var events ciEvents
		if err := msgp.Decode(bytes.NewReader(envelope.Events), &events); err != nil {
			t.Error(err)
			return
		}
		count.Add(int32(len(events)))
		w.WriteHeader(202)
	}))
	defer server.Close()
	client, err := New(Config{MaxEvents: 10, Transport: citransport.Config{Endpoint: server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	for range 35 {
		span, _ := client.StartSpan(context.Background(), "test", SpanType("test"))
		span.Finish()
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Finish waited for a stalled intake: %s", elapsed)
	}
	close(release)
	if err := client.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if count.Load() != 35 || client.DroppedEvents() != 0 {
		t.Fatalf("delivered=%d dropped=%d", count.Load(), client.DroppedEvents())
	}
}
