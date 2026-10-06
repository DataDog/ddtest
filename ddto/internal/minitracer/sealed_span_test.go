package minitracer

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/citransport"
)

func TestFinishedSpanSharesSealedMaps(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(202) }))
	defer server.Close()
	client, err := New(Config{Tags: map[string]string{"test_session_id": "10"}, Transport: citransport.Config{Endpoint: server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	span, _ := client.StartSpan(context.Background(), "test", SpanType("test"), Tag("test_module_id", "20"), Tag("test_suite_id", "30"), Tag("test.name", "original"), Tag("coverage", 0.5))
	span.Finish()
	client.mu.Lock()
	event := client.events[0]
	client.mu.Unlock()
	if event.Content.SessionID != 10 || event.Content.ModuleID != 20 || event.Content.SuiteID != 30 {
		t.Fatal("lost hierarchy")
	}
	for _, key := range []string{"test_session_id", "test_module_id", "test_suite_id"} {
		if _, ok := event.Content.Meta[key]; ok {
			t.Fatal("hierarchy duplicated in metadata")
		}
	}
	if fmt.Sprintf("%p", event.Content.Meta) != fmt.Sprintf("%p", span.content.Meta) || fmt.Sprintf("%p", event.Content.Metrics) != fmt.Sprintf("%p", span.content.Metrics) {
		t.Fatal("Finish copied sealed maps")
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				span.SetTag("test.name", "changed")
				span.SetTag("coverage", 99)
				span.SetTag("test_session_id", "999")
				span.Finish()
				if v, ok := span.Meta("test.name"); !ok || v != "original" {
					t.Error("metadata changed after Finish")
				}
				if v, ok := span.Meta("test_session_id"); !ok || v != "10" {
					t.Error("hierarchy getter changed")
				}
				if v, ok := span.Metric("coverage"); !ok || v != 0.5 {
					t.Error("metric changed after Finish")
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := client.Flush(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	wg.Wait()
	if event.Content.Meta["test.name"] != "original" || event.Content.Metrics["coverage"] != 0.5 {
		t.Fatal("event changed after Finish")
	}
	client.mu.Lock()
	capacity := cap(client.events) + cap(client.spare)
	client.mu.Unlock()
	if capacity == 0 {
		t.Fatal("successful flush did not retain queue capacity")
	}
	if err := client.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	client.mu.Lock()
	retained := cap(client.events) + cap(client.spare)
	client.mu.Unlock()
	if retained != capacity {
		t.Fatal("empty flush discarded queue capacity")
	}
	if err := client.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestHierarchyTagTypeTransitions(t *testing.T) {
	span, _ := newSpan(nil, context.Background(), "test")
	const key = "test_session_id"
	span.SetTag(key, "12")
	span.SetTag(key, 7)
	if _, ok := span.Meta(key); ok {
		t.Fatal("numeric tag retained metadata")
	}
	if v, ok := span.Metric(key); !ok || v != 7 {
		t.Fatal("lost numeric tag")
	}
	span.SetTag(key, uint64(1<<60))
	if v, ok := span.Meta(key); !ok || v != "1152921504606846976" {
		t.Fatal("lost precise identifier")
	}
	if _, ok := span.Metric(key); ok {
		t.Fatal("string tag retained metric")
	}
	span.Finish()
	if v, ok := span.Meta(key); !ok || v != "1152921504606846976" {
		t.Fatal("Finish lost identifier getter")
	}
}
