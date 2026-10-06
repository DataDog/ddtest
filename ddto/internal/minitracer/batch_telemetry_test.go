package minitracer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/citransport"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/telemetry"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/telemetry/telemetrytest"
)

func recordedPayloadDrops(t *testing.T) func() float64 {
	t.Helper()
	recorder := &telemetrytest.RecordClient{}
	t.Cleanup(telemetry.MockClient(recorder))
	return func() float64 {
		return recorder.Count(telemetry.NamespaceCIVisibility, "endpoint_payload.dropped", []string{"endpoint:test_cycle"}).Get()
	}
}

func TestPayloadDropCountsTerminalBatches(t *testing.T) {
	drops := recordedPayloadDrops(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(503)
	}))
	defer server.Close()
	c, err := New(Config{MaxEvents: 2, Transport: citransport.Config{Endpoint: server.URL, Attempts: 3, RetryDelay: time.Millisecond}})
	if err != nil {
		t.Fatal(err)
	}
	finish := func() { s, _ := c.StartSpan(context.Background(), "test", SpanType("test")); s.Finish() }
	finish()
	finish()
	if err = c.Flush(context.Background()); err == nil || drops() != 0 || c.DroppedEvents() != 0 {
		t.Fatal("a retained batch must not count as discarded")
	}
	// The bounded queue retains later batches; beyond it, events are rejected
	// one by one without being counted as payloads.
	for range 2*maxPendingBatches + 1 {
		finish()
	}
	if c.DroppedEvents() != 1 || drops() != 0 {
		t.Fatalf("rejected events were counted as payloads: events=%d payloads=%v", c.DroppedEvents(), drops())
	}
	if err = c.Close(context.Background()); err == nil {
		t.Fatal("terminal delivery failure suppressed")
	}
	// Every retained batch is abandoned once: the ready batches and the open one.
	if drops() != maxPendingBatches+1 || c.DroppedEvents() != 2*(maxPendingBatches+1)+1 {
		t.Fatalf("abandoned batches: payloads=%v events=%d", drops(), c.DroppedEvents())
	}
	// Neither repeated close/flush nor finishing additional spans may count those
	// payloads twice or resend an abandoned batch.
	sent := calls.Load()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { _ = c.Close(context.Background()) })
	}
	wg.Wait()
	finish()
	if err = c.Flush(context.Background()); err != nil || drops() != maxPendingBatches+1 || calls.Load() != sent || c.DroppedEvents() != 2*(maxPendingBatches+1)+2 {
		t.Fatalf("discarded batch counted or sent again: payloads=%v events=%d attempts=%d err=%v", drops(), c.DroppedEvents(), calls.Load(), err)
	}
}

func TestRetainedBatchRecoveryDoesNotCountPayloadDrop(t *testing.T) {
	drops := recordedPayloadDrops(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(202)
	}))
	defer server.Close()
	c, err := New(Config{Transport: citransport.Config{Endpoint: server.URL, Attempts: 1}})
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		s, _ := c.StartSpan(context.Background(), "test", SpanType("test"))
		s.Finish()
	}
	if c.Flush(context.Background()) == nil {
		t.Fatal("first delivery unexpectedly succeeded")
	}
	if err = c.Close(context.Background()); err != nil || drops() != 0 || c.DroppedEvents() != 0 {
		t.Fatalf("recovered payload counted as lost: payloads=%v events=%d err=%v", drops(), c.DroppedEvents(), err)
	}
}

func TestCanceledCloseCountsInflightPayloadOnce(t *testing.T) {
	drops := recordedPayloadDrops(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(503)
	}))
	defer server.Close()
	defer unblock()
	c, err := New(Config{Transport: citransport.Config{Endpoint: server.URL, Attempts: 1}})
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		s, _ := c.StartSpan(context.Background(), "test", SpanType("test"))
		s.Finish()
	}
	done := make(chan error, 1)
	go func() { done <- c.Flush(context.Background()) }()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = c.Close(ctx); err != context.Canceled || drops() != 0 {
		t.Fatalf("close must cancel without counting a payload still in flight: drops=%v err=%v", drops(), err)
	}
	unblock()
	if err = <-done; err == nil || drops() != 1 || c.DroppedEvents() != 3 {
		t.Fatalf("inflight payload not abandoned once: drops=%v events=%d err=%v", drops(), c.DroppedEvents(), err)
	}
	if err = c.Close(context.Background()); err != nil || drops() != 1 {
		t.Fatalf("repeated close changed discard count: drops=%v err=%v", drops(), err)
	}
}

func TestOversizedRejectedEventIsNotDroppedPayload(t *testing.T) {
	drops := recordedPayloadDrops(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(202) }))
	defer server.Close()
	c, err := New(Config{Transport: citransport.Config{Endpoint: server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	s, _ := c.StartSpan(context.Background(), "test", SpanType("test"), Tag("large", strings.Repeat("x", citransport.TestCycleMaxPayloadBytes)))
	s.Finish()
	if c.DroppedEvents() != 1 || drops() != 0 || calls.Load() != 0 {
		t.Fatalf("an unbatched oversized event is not a discarded payload: events=%d payloads=%v calls=%d", c.DroppedEvents(), drops(), calls.Load())
	}
	if err = c.Close(context.Background()); err != nil || drops() != 0 {
		t.Fatal("empty close manufactured a discarded payload")
	}
}

func TestCanceledCloseAbandonsBufferedPayloadOnce(t *testing.T) {
	drops := recordedPayloadDrops(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(202) }))
	defer server.Close()
	c, err := New(Config{Transport: citransport.Config{Endpoint: server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		s, _ := c.StartSpan(context.Background(), "test", SpanType("test"))
		s.Finish()
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = c.Close(ctx); err != context.Canceled || drops() != 1 || c.DroppedEvents() != 3 || calls.Load() != 0 {
		t.Fatalf("canceled buffered payload: payloads=%v events=%d requests=%d err=%v", drops(), c.DroppedEvents(), calls.Load(), err)
	}
	if err = c.Close(context.Background()); err != nil || drops() != 1 || calls.Load() != 0 {
		t.Fatal("canceled terminal batch was counted or sent again")
	}
}
