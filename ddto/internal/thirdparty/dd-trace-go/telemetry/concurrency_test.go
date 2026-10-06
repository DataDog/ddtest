package telemetry

import (
	"math"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/telemetry/internal"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/telemetry/internal/transport"
)

func runConcurrent(workers int, f func(int)) {
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range workers {
		wg.Go(func() { <-start; f(i) })
	}
	close(start)
	wg.Wait()
}

func TestMetricStartupReplayIncludesConcurrentSubmissions(t *testing.T) {
	// Stay below the recorder's bound. This tests the handover, not overflow.
	const workers = 64
	for round := range 256 {
		handle := &swappableMetricHandle{recorder: internal.NewRecorder[MetricHandle]()}
		target := &count{}
		runConcurrent(workers+1, func(i int) {
			if i == workers {
				handle.swap(target)
			} else {
				handle.Submit(1)
			}
		})
		if got := target.Get(); got != workers {
			t.Fatalf("round %d: got %v of %d submissions", round, got, workers)
		}
	}
}

func TestMetricRegistrationIsCanonical(t *testing.T) {
	for _, kind := range []transport.MetricType{transport.CountMetric, transport.GaugeMetric, transport.RateMetric} {
		t.Run(string(kind), func(t *testing.T) {
			m := metrics{skipAllowlist: true}
			handles := make([]MetricHandle, 64)
			runConcurrent(len(handles), func(i int) {
				handles[i] = m.LoadOrStore(NamespaceCIVisibility, kind, "fixture", []string{"b:2", "a:1"})
			})
			for _, h := range handles {
				if h != handles[0] {
					t.Fatal("concurrent registration returned different handles")
				}
			}
			if kind == transport.RateMetric && handles[0].(*rate).intervalStart.Load() == nil {
				t.Fatal("rate interval was not initialized")
			}
		})
	}
}

// Each sample must land in exactly one collection, including submissions that
// overlap collection and the first registration of a metric.
func TestConcurrentMetricCollectionPreservesValues(t *testing.T) {
	const workers, iterations = 8, 64
	m := metrics{skipAllowlist: true}
	d := distributions{skipAllowlist: true, queueSize: internal.Range[int]{Min: 1024, Max: 1024}, pool: internal.NewSyncPool(func() []float64 { return make([]float64, 1024) })}
	handles := make([]MetricHandle, workers)
	seen := make([]int, workers*iterations)
	var countTotal float64
	collect := func() {
		if p := m.Payload(); p != nil {
			for _, s := range p.(transport.GenerateMetrics).Series {
				countTotal += s.Points[0][1].(float64)
			}
		}
		if p := d.Payload(); p != nil {
			for _, s := range p.(transport.Distributions).Series {
				for _, point := range s.Points {
					i := int(point)
					if i < 0 || i >= len(seen) {
						t.Errorf("unexpected distribution point %v", point)
						continue
					}
					seen[i]++
				}
			}
		}
	}
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				collect()
				runtime.Gosched()
			}
		}
	}()
	runConcurrent(workers, func(worker int) {
		c := m.LoadOrStore(NamespaceCIVisibility, transport.CountMetric, "fixture", nil)
		handles[worker] = d.LoadOrStore(NamespaceCIVisibility, "fixture", nil)
		for i := range iterations {
			c.Submit(1)
			handles[worker].Submit(float64(worker*iterations + i))
		}
	})
	close(stop)
	<-done
	collect()
	if countTotal != workers*iterations {
		t.Fatalf("count total = %v, want %v", countTotal, workers*iterations)
	}
	for _, h := range handles {
		if h != handles[0] {
			t.Fatal("distribution registration returned different handles")
		}
	}
	for i, count := range seen {
		if count != 1 {
			t.Errorf("sample %d collected %d times, want once", i, count)
		}
	}
}

func TestGlobalRegistrationReplaysAndSwapsOnce(t *testing.T) {
	restore := MockClient(nil)
	defer restore()
	makeClient := func() Client {
		c, err := NewClient("fixture", "test", "1", ClientConfig{AgentURL: "http://fixture.invalid", HTTPClient: &http.Client{Transport: benchmarkHTTP{}}})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	first, second := makeClient(), makeClient()
	defer first.Close()
	defer second.Close()
	handles := make([]MetricHandle, 128)
	runConcurrent(len(handles), func(i int) {
		handles[i] = Count(NamespaceCIVisibility, "events_enqueued_for_serialization", nil)
		handles[i].Submit(2)
	})
	for _, h := range handles {
		if h != handles[0] {
			t.Fatal("startup created duplicate swappable handles")
		}
	}
	SwapClient(first)
	if got := first.Count(NamespaceCIVisibility, "events_enqueued_for_serialization", nil).Get(); got != 256 {
		t.Fatalf("startup replay = %v, want 256", got)
	}
	SwapClient(second)
	if h := Count(NamespaceCIVisibility, "events_enqueued_for_serialization", nil); h != handles[0] {
		t.Fatal("swapping client replaced the cached handle")
	}
	handles[0].Submit(7)
	if got := second.Count(NamespaceCIVisibility, "events_enqueued_for_serialization", nil).Get(); got != 7 {
		t.Fatalf("swapped handle submitted %v, want 7", got)
	}
}

func TestMetricPointLifecycle(t *testing.T) {
	key := newMetricKey(NamespaceCIVisibility, transport.CountMetric, "events_enqueued_for_serialization", nil)
	c := &count{metric: metric{key: key}}
	if !math.IsNaN(c.Get()) || c.Payload().Type != "" {
		t.Fatal("an unused counter emitted a point")
	}
	before := time.Now().Unix()
	for _, value := range []float64{2.5, -1, 0} {
		c.Submit(value)
	}
	p := c.Payload()
	if p.Metric != key.name || p.Namespace != key.namespace || p.Type != transport.CountMetric || len(p.Points) != 1 || p.Points[0][1] != 1.5 {
		t.Fatalf("counter payload = %+v", p)
	}
	if timestamp := p.Points[0][0].(int64); timestamp < before || timestamp > time.Now().Unix() {
		t.Fatalf("counter timestamp = %d", timestamp)
	}
	if !math.IsNaN(c.Get()) || c.Payload().Type != "" {
		t.Fatal("collection did not reset the counter")
	}
	c.Submit(math.NaN())
	if !math.IsNaN(c.Payload().Points[0][1].(float64)) {
		t.Fatal("NaN counter semantics changed")
	}
	c.Submit(0)
	if p := c.Payload(); p.Type != transport.CountMetric || p.Points[0][1] != float64(0) {
		t.Fatalf("zero submission lost after reset: %+v", p)
	}
	g := &gauge{metric: metric{key: metricKey{namespace: key.namespace, kind: transport.GaugeMetric, name: "fixture"}}}
	g.Submit(2.5)
	g.Submit(-1)
	if g.Get() != -1 || g.Payload().Points[0][1] != float64(-1) || !math.IsNaN(g.Get()) {
		t.Fatal("gauge overwrite/reset semantics changed")
	}
	r := &rate{count: count{metric: metric{key: metricKey{namespace: key.namespace, kind: transport.RateMetric, name: "fixture"}}}}
	now := time.Now()
	r.intervalStart.Store(&now)
	r.Submit(5)
	if !math.IsNaN(r.Get()) || r.Payload().Type != "" || r.count.Get() != 5 {
		t.Fatal("a short rate interval consumed its count")
	}
	start := time.Now().Add(-2 * time.Second)
	r.intervalStart.Store(&start)
	p = r.Payload()
	if p.Type != transport.RateMetric || p.Interval != 2 || math.Abs(p.Points[0][1].(float64)-2.5) > 0.1 || !math.IsNaN(r.count.Get()) {
		t.Fatalf("rate interval/reset = %+v", p)
	}
}

func TestBoundCountReplaysSwapsAndResets(t *testing.T) {
	restore := MockClient(nil)
	defer restore()
	makeClient := func() Client {
		c, err := NewClient("fixture", "test", "1", ClientConfig{AgentURL: "http://fixture.invalid", HTTPClient: &http.Client{Transport: benchmarkHTTP{}}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		return c
	}
	tags := []string{"event_type:test", "test_framework:testing"}
	h := BindCount(NamespaceCIVisibility, "event_created", tags)
	tags[0] = "event_type:changed"
	runConcurrent(128, func(int) { h.Submit(2) })
	first := makeClient()
	SwapClient(first)
	metric := func(c Client) MetricHandle {
		return c.Count(NamespaceCIVisibility, "event_created", []string{"test_framework:testing", "event_type:test"})
	}
	if got := metric(first).Get(); got != 256 {
		t.Fatalf("bound startup replay/tag ownership = %v, want 256", got)
	}
	second := makeClient()
	SwapClient(second)
	h.Submit(7)
	if got := metric(second).Get(); got != 7 {
		t.Fatalf("bound client swap = %v, want 7", got)
	}
	third := makeClient()
	restoreMock := MockClient(third)
	h.Submit(3)
	if got := metric(third).Get(); got != 3 {
		t.Fatalf("bound mock reset = %v, want 3", got)
	}
	restoreMock()
	h.Submit(11)
	// MockClient clears the global registration cache, not the old client's
	// counter. Restoring that client must retain its earlier seven increments.
	if got := metric(second).Get(); got != 18 {
		t.Fatalf("bound mock restoration = %v, want 18", got)
	}
}

func TestConcurrentLogCollectionPreservesCounts(t *testing.T) {
	const workers, iterations = 8, 1000
	logger := newLoggerBackend(8192)
	var total uint64
	collect := func() {
		if p := logger.Payload(); p != nil {
			for _, entry := range p.(transport.Logs).Logs {
				total += uint64(entry.Count)
			}
		}
	}
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				collect()
				runtime.Gosched()
			}
		}
	}()
	runConcurrent(workers, func(int) {
		for range iterations {
			logger.Add(NewRecord(LogWarn, "fixture"), WithTags([]string{"ci:true"}))
		}
	})
	close(stop)
	<-done
	collect()
	if total != workers*iterations {
		t.Fatalf("log count = %d, want %d", total, workers*iterations)
	}
	if got := logger.distinctLogs.Load(); got != 0 {
		t.Fatalf("distinct logs after collection = %d", got)
	}
	if logger.Payload() != nil {
		t.Fatal("collection emitted a duplicate batch")
	}
}

func TestLogLimitAndStacktracePreserved(t *testing.T) {
	logger := newLoggerBackend(2)
	logger.Add(NewRecord(LogWarn, "first"), WithTags([]string{"ci:true"}), WithStacktrace())
	logger.Add(NewRecord(LogWarn, "first"), WithTags([]string{"ci:true"}), WithStacktrace())
	logger.Add(NewRecord(LogError, "second"))
	for range 4 {
		logger.Add(NewRecord(LogError, "dropped"))
	}
	logs := logger.Payload().(transport.Logs).Logs
	if len(logs) != 3 {
		t.Fatalf("log entries = %d, want first, second and one limit warning", len(logs))
	}
	var warnings int
	for _, entry := range logs {
		switch {
		case entry.Message == "first":
			if entry.Count != 2 || entry.Tags != "ci:true" || !strings.Contains(entry.StackTrace, "TestLogLimitAndStacktracePreserved") {
				t.Errorf("first log lost count/tags/caller stack: %+v", entry)
			}
		case strings.Contains(entry.Message, "exceeded maximum"):
			warnings++
			if entry.Count != 1 || entry.StackTrace == "" {
				t.Errorf("invalid limit warning: %+v", entry)
			}
		case entry.Message != "second":
			t.Errorf("unexpected log: %+v", entry)
		}
	}
	if warnings != 1 {
		t.Fatalf("limit warnings = %d, want 1", warnings)
	}
	logger.Add(NewRecord(LogWarn, "next"))
	if got := logger.Payload().(transport.Logs).Logs; len(got) != 1 || got[0].Message != "next" {
		t.Fatalf("logger did not resume after collection: %+v", got)
	}
}

// This exercises the global callback handover separately from metric handles.
func TestGlobalClientStartupReplaysEveryCall(t *testing.T) {
	restore := MockClient(nil)
	defer restore()
	client, err := NewClient("fixture", "test", "1", ClientConfig{AgentURL: "http://fixture.invalid", HTTPClient: &http.Client{Transport: benchmarkHTTP{}}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for round := range 256 {
		globalClient.Store(nil)
		globalClientRecorder.Clear()
		var calls atomic.Int32
		runConcurrent(65, func(i int) {
			if i == 64 {
				SwapClient(client)
			} else {
				globalClientCall(func(Client) { calls.Add(1) })
			}
		})
		if got := calls.Load(); got != 64 {
			t.Fatalf("round %d: got %d of 64 calls", round, got)
		}
	}
}
