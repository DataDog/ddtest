package minitracer

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/citransport"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"
)

// Close stops the background senders and then sends the remaining backlog
// concurrently, not one batch at a time: up to maxConcurrentSends requests,
// counting a background delivery still in flight. Every sender finishes
// before Close returns. The debug log reports the drain and, for each payload,
// a size that its accounting bounds.
func TestCloseDrainsBacklogConcurrently(t *testing.T) {
	recorder := &log.RecordLogger{}
	t.Cleanup(log.UseLogger(recorder))
	level := log.GetLevel()
	t.Cleanup(func() { log.SetLevel(level) })
	log.SetLevel(log.LevelDebug)
	var received deliveries
	var requests, active, peak atomic.Int32
	release := make(chan struct{})
	var releaseOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 2 {
			// A failure starts the backoff: the backlog waits for Close while
			// the first request stays in flight.
			w.WriteHeader(400)
			return
		}
		n := active.Add(1)
		defer active.Add(-1)
		for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
		}
		if n == maxConcurrentSends {
			// Leave time for a request beyond the bound to arrive.
			time.AfterFunc(100*time.Millisecond, func() { releaseOnce.Do(func() { close(release) }) })
		}
		select {
		case <-release:
		case <-time.After(2 * time.Second):
		}
		received.record(t, r)
		w.WriteHeader(202)
	}))
	defer server.Close()
	client, err := New(Config{MaxEvents: 2, Transport: citransport.Config{Endpoint: server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	finish := func(n int) {
		for range n {
			span, _ := client.StartSpan(context.Background(), "test", SpanType("test"))
			span.Finish()
		}
	}
	finish(4) // One batch in flight, one failed.
	waitFor(t, func() bool { return client.LastError() != nil })
	// Six more sealed batches reach the pending bound; an open one remains,
	// which Close seals beyond it.
	finish(13)
	client.mu.Lock()
	ready, inflight, open := len(client.ready), client.inflight, len(client.events)
	client.mu.Unlock()
	if ready != 7 || inflight != 1 || open != 1 {
		t.Fatalf("ready=%d inflight=%d open=%d before Close", ready, inflight, open)
	}
	if err := client.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if delivered := received.distinct(t); delivered != 17 || client.DroppedEvents() != 0 {
		t.Fatalf("delivered=%d dropped=%d of 17", delivered, client.DroppedEvents())
	}
	if p := peak.Load(); p != maxConcurrentSends {
		t.Fatalf("concurrent requests: %d", p)
	}
	waitForNoDrainWorkers(t)
	logs := strings.Join(recorder.Logs(), "\n")
	if !strings.Contains(logs, "ci mini tracer: final drain batches=8 in_flight=1 senders=7") {
		t.Fatalf("missing final drain:\n%s", logs)
	}
	encoded := 0
	for _, line := range recorder.Logs() {
		_, fields, found := strings.Cut(line, "ci mini tracer: batch encoded ")
		if !found {
			continue
		}
		var events, accounted, payload int
		if _, err := fmt.Sscanf(fields, "events=%d accounted_bytes=%d payload_bytes=%d", &events, &accounted, &payload); err != nil || payload > accounted {
			t.Fatalf("batch encoded %q: %v", fields, err)
		}
		encoded += events
	}
	if encoded != 2+17 { // The failed batch was encoded twice.
		t.Fatalf("encoded %d events:\n%s", encoded, logs)
	}
}

// With every slot taken by background deliveries, Close's partial batch waits
// for one to free instead of becoming a ninth concurrent request, and then
// starts at once, while the others are still in flight.
func TestCloseWaitsForAFreeSendSlot(t *testing.T) {
	recorder := &log.RecordLogger{}
	t.Cleanup(log.UseLogger(recorder))
	level := log.GetLevel()
	t.Cleanup(func() { log.SetLevel(level) })
	log.SetLevel(log.LevelDebug)
	var received deliveries
	var requests, active, peak atomic.Int32
	gate := make(chan struct{}, maxConcurrentSends)
	partial := make(chan int32, 1) // Requests in flight when the partial batch arrives.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
		}
		if requests.Add(1) > maxConcurrentSends {
			partial <- n
		} else {
			select {
			case <-gate:
			case <-time.After(5 * time.Second):
			}
		}
		received.record(t, r)
		w.WriteHeader(202)
	}))
	defer server.Close()
	client, err := New(Config{MaxEvents: 2, Transport: citransport.Config{Endpoint: server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	for range 2*maxConcurrentSends + 1 {
		span, _ := client.StartSpan(context.Background(), "test", SpanType("test"))
		span.Finish()
	}
	waitFor(t, func() bool { return active.Load() == maxConcurrentSends })
	closed := make(chan error, 1)
	go func() { closed <- client.Close(context.Background()) }()
	waitFor(t, func() bool {
		client.mu.Lock()
		defer client.mu.Unlock()
		return client.closed && len(client.ready) == 1 && client.slotFreed != nil
	})
	// Leave time for a request beyond the bound to arrive, then free one slot.
	time.Sleep(100 * time.Millisecond)
	gate <- struct{}{}
	select {
	case n := <-partial:
		if n != maxConcurrentSends {
			t.Fatalf("the partial batch arrived with %d requests in flight", n)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the partial batch waited for every delivery in flight")
	}
	for range maxConcurrentSends - 1 {
		gate <- struct{}{}
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if delivered := received.distinct(t); delivered != 2*maxConcurrentSends+1 || client.DroppedEvents() != 0 {
		t.Fatalf("delivered=%d dropped=%d of %d", delivered, client.DroppedEvents(), 2*maxConcurrentSends+1)
	}
	if p := peak.Load(); p != maxConcurrentSends {
		t.Fatalf("concurrent requests: %d", p)
	}
	if logs := strings.Join(recorder.Logs(), "\n"); !strings.Contains(logs, "final drain batches=1 in_flight=7 senders=1") {
		t.Fatalf("missing final drain:\n%s", logs)
	}
	waitForNoDrainWorkers(t)
}

// Waiting for a free slot ends with Close's deadline: the waiting batch is
// abandoned and the error returned.
func TestCloseSlotWaitHonorsDeadline(t *testing.T) {
	var active atomic.Int32
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		active.Add(1)
		defer active.Add(-1)
		select {
		case <-release:
		case <-time.After(5 * time.Second):
		}
		w.WriteHeader(202)
	}))
	defer server.Close()
	defer close(release)
	client, err := New(Config{MaxEvents: 2, Transport: citransport.Config{Endpoint: server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	for range 2*maxConcurrentSends + 1 {
		span, _ := client.StartSpan(context.Background(), "test", SpanType("test"))
		span.Finish()
	}
	waitFor(t, func() bool { return active.Load() == maxConcurrentSends })
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	started := time.Now()
	if err := client.Close(ctx); err != context.DeadlineExceeded {
		t.Fatalf("Close: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second || client.DroppedEvents() != 1 {
		t.Fatalf("Close took %v and dropped %d events", elapsed, client.DroppedEvents())
	}
}

func TestCloseReportsFailedBackgroundDelivery(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	c, err := New(Config{MaxEvents: 1, Transport: citransport.Config{Endpoint: server.URL, Attempts: 1}})
	if err != nil {
		t.Fatal(err)
	}
	s, _ := c.StartSpan(context.Background(), "test", SpanType("test"))
	s.Finish()
	<-entered
	result := make(chan error, 1)
	go func() { result <- c.Close(context.Background()) }()
	waitFor(t, func() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.closed })
	close(release)
	if err := <-result; err == nil {
		t.Fatal("Close hid a failed background delivery")
	}
	if c.DroppedEvents() != 1 {
		t.Fatal("wrong dropped count", c.DroppedEvents())
	}
	if err := c.Close(context.Background()); err != nil {
		t.Fatal("a later empty close reported an old failure", err)
	}
}
