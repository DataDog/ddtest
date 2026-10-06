package minitracer

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/citransport"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/msgp/msgp"
)

// deliveries records the events that reach a test-cycle endpoint by span ID.
// A transport retry after a network error can deliver a payload twice, as in
// the SDK, so tests require every event at least once and log repeats.
type deliveries struct {
	mu    sync.Mutex
	spans map[uint64]int
}

func (d *deliveries) record(t *testing.T, r *http.Request) {
	t.Helper()
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
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.spans == nil {
		d.spans = map[uint64]int{}
	}
	for _, event := range events {
		d.spans[event.Content.SpanID]++
	}
}

// distinct reports how many different events arrived.
func (d *deliveries) distinct(t *testing.T) int {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	for span, n := range d.spans {
		if n > 1 {
			t.Logf("span %d arrived %d times: a transport retry after a network error", span, n)
		}
	}
	return len(d.spans)
}

// A slow intake is the common agentless case. Finishers that outpace delivery
// wait for a sender instead of losing events, and senders run concurrently.
func TestSlowIntakeDeliversEveryEvent(t *testing.T) {
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
	client, err := New(Config{MaxEvents: 5, Transport: citransport.Config{Endpoint: server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	const total = 10 * 5 * maxPendingBatches
	for range total {
		span, _ := client.StartSpan(context.Background(), "test", SpanType("test"))
		span.Finish()
	}
	if err := client.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if delivered := received.distinct(t); delivered != total || client.DroppedEvents() != 0 {
		t.Fatalf("delivered=%d dropped=%d of %d", delivered, client.DroppedEvents(), total)
	}
	// The final flush may overlap the last background batches.
	if p := peak.Load(); p < 2 || p > maxConcurrentSends+1 {
		t.Fatalf("concurrent requests: %d", p)
	}
}

// After a failed delivery, events beyond the bound are rejected immediately:
// an unavailable intake must not stall finishing tests.
func TestFailingIntakeDoesNotStallFinishers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))
	defer server.Close()
	client, err := New(Config{MaxEvents: 1, Transport: citransport.Config{Endpoint: server.URL, Attempts: 1}})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	for range 10 * maxPendingBatches {
		span, _ := client.StartSpan(context.Background(), "test", SpanType("test"))
		span.Finish()
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("finishers waited for a failing intake: %s", elapsed)
	}
	if client.DroppedEvents() == 0 || client.LastError() == nil {
		t.Fatal("rejected events were not reported")
	}
	_ = client.Close(context.Background())
}

// A finisher waiting for space returns when the client closes, rejecting its
// event, even while deliveries are still in flight.
func TestCloseReleasesWaitingFinisher(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(202)
	}))
	defer server.Close()
	defer unblock()
	client, err := New(Config{MaxEvents: 1, Transport: citransport.Config{Endpoint: server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	finish := func() { span, _ := client.StartSpan(context.Background(), "test", SpanType("test")); span.Finish() }
	// Reach the pending bound and fill the open batch; deliveries stay in flight.
	for range maxPendingBatches + 1 {
		finish()
	}
	waiting := make(chan struct{})
	go func() { finish(); close(waiting) }()
	select {
	case <-waiting:
		t.Fatal("finisher did not wait at the pending bound of a healthy intake")
	case <-time.After(50 * time.Millisecond):
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := client.Close(ctx); err == nil {
		t.Fatal("close with stalled deliveries reported success")
	}
	select {
	case <-waiting:
	case <-time.After(5 * time.Second):
		t.Fatal("closing the client did not release the waiting finisher")
	}
	if client.DroppedEvents() == 0 {
		t.Fatal("the released event was not counted as dropped")
	}
	unblock()
}

// Deferred delivery sends only at idle checkpoints, so a finisher must never
// wait for space: the queue grows until the group finishes.
func TestDeferredDeliveryNeverWaitsForSpace(t *testing.T) {
	var received deliveries
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.record(t, r)
		w.WriteHeader(202)
	}))
	defer server.Close()
	client, err := New(Config{MaxEvents: 1, DeferUntilIdle: true, Transport: citransport.Config{Endpoint: server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	const total = 4 * maxPendingBatches
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range total {
			span, _ := client.StartSpan(context.Background(), "test", SpanType("test"))
			span.Finish()
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("deferred finishers waited for delivery")
	}
	if received.distinct(t) != 0 {
		t.Fatal("deferred delivery sent outside a checkpoint")
	}
	if err := client.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if delivered := received.distinct(t); delivered != total || client.DroppedEvents() != 0 {
		t.Fatalf("delivered=%d dropped=%d", delivered, client.DroppedEvents())
	}
}
