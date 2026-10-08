package minitracer

import (
	"bytes"
	"context"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/citransport"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/msgp/msgp"
)

func effectiveCommonMeta(payload *testCycleBatch, event *ciEvent) map[string]string {
	values := make(map[string]string)
	maps.Copy(values, payload.Metadata["*"])
	maps.Copy(values, payload.Metadata[event.Type])
	maps.Copy(values, event.Content.Meta)
	return values
}

func TestCommonTagsWireOverridesAndGetters(t *testing.T) {
	for _, deferred := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "deferred"}[deferred], func(t *testing.T) {
			var payload testCycleBatch
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := msgp.Decode(r.Body, &payload); err != nil {
					t.Error(err)
				}
				w.WriteHeader(202)
			}))
			defer server.Close()
			client, err := New(Config{DeferUntilIdle: deferred, Metadata: map[string]map[string]string{"test": {"test_session.name": "session", "ci.job.name": "older-default"}}, Transport: citransport.Config{Endpoint: server.URL}})
			if err != nil {
				t.Fatal(err)
			}
			input := map[string]string{"ci.job.name": "job", "git.commit.sha": "commit", "os.platform": "linux"}
			common := NewCommonTags(input)
			input["ci.job.name"] = "caller-mutation"
			var spans []*Span
			for _, opts := range [][]StartSpanOption{
				{Tag("ci.job.name", "earlier"), common.Option()},
				{common.Option(), Tag("ci.job.name", "override")},
				{common.Option(), Tag("os.platform", 42)},
				{common.Option(), Tag("os.platform", 42), Tag("os.platform", "mac")},
			} {
				span, _ := client.StartSpan(context.Background(), "test", append([]StartSpanOption{SpanType("test")}, opts...)...)
				spans = append(spans, span)
				span.Finish()
			}
			child, _ := client.StartSpan(context.Background(), "child")
			child.Finish()
			if err := client.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(payload.Events) != 5 || payload.Metadata["test"]["ci.job.name"] != "older-default" || payload.Metadata["test"]["git.commit.sha"] != "commit" {
				t.Fatalf("common metadata not lifted: %+v", payload.Metadata)
			}
			if _, exists := payload.Metadata["test"]["os.platform"]; exists {
				t.Fatal("numeric event inherited a string default")
			}
			if payload.Events[0].Content.Meta["ci.job.name"] != "job" {
				t.Fatal("text fallback lost a peer's default")
			}
			for i, want := range []string{"job", "override", "job", "job"} {
				span := spans[i]
				span.SetTag("ci.job.name", "after-finish")
				got, exists := span.Meta("ci.job.name")
				if !exists || got != want || effectiveCommonMeta(&payload, payload.Events[i])["ci.job.name"] != want {
					t.Fatalf("getter/wire override %d: %q %t", i, got, exists)
				}
				if _, repeated := payload.Events[i].Content.Meta["git.commit.sha"]; repeated {
					t.Fatal("common Git value repeated on event")
				}
			}
			if _, text := spans[2].Meta("os.platform"); text || spans[2].content.Metrics["os.platform"] != 42 {
				t.Fatal("numeric getter changed")
			}
			if _, text := effectiveCommonMeta(&payload, payload.Events[2])["os.platform"]; text {
				t.Fatal("numeric wire value regained metadata")
			}
			if effectiveCommonMeta(&payload, payload.Events[0])["os.platform"] != "linux" || effectiveCommonMeta(&payload, payload.Events[3])["os.platform"] != "mac" {
				t.Fatal("numeric fallback lost peer or later text value")
			}
			if _, exists := effectiveCommonMeta(&payload, payload.Events[4])["ci.job.name"]; exists {
				t.Fatal("generic child gained CI tags")
			}
		})
	}
}

func TestCommonMetadataMixedSnapshotsAndSealedMaps(t *testing.T) {
	one := NewCommonTags(map[string]string{"git.commit.sha": "one", "ci.job.name": "job"})
	two := NewCommonTags(map[string]string{"git.commit.sha": "two"})
	events := ciEvents{
		{Type: "test", Content: eventContent{Meta: map[string]string{"test.name": "a"}}, common: one},
		{Type: "test", common: two}, // The wire schema permits omitted local metadata.
		{Type: "test", Content: eventContent{Meta: map[string]string{"test.name": "c"}}},
		{Type: "test_module_end", Content: eventContent{Meta: map[string]string{"test.module": "module"}}, common: two},
	}
	base := map[string]map[string]string{"*": {"language": "go"}}
	metadata, wire := prepareCommonMetadata(base, events)
	if _, lifted := metadata["test"]; lifted || metadata["test_module_end"]["git.commit.sha"] != "two" {
		t.Fatalf("mixed kind defaults: %v", metadata)
	}
	for i, want := range []string{"one", "two", "", "two"} {
		if got := effectiveCommonMeta(&testCycleBatch{Metadata: metadata}, wire[i])["git.commit.sha"]; got != want {
			t.Fatalf("snapshot %d: %q", i, got)
		}
		if _, changed := events[i].Content.Meta["git.commit.sha"]; changed {
			t.Fatal("projection changed a sealed map")
		}
	}
	if !reflect.DeepEqual(base, map[string]map[string]string{"*": {"language": "go"}}) {
		t.Fatal("projection mutated configured metadata")
	}
}

func TestCommonMetadataConcurrentProjectionAndBounds(t *testing.T) {
	common := NewCommonTags(map[string]string{"git.commit.sha": "commit", "ci.large": strings.Repeat("a", 32<<10)})
	span, _ := newSpan(nil, context.Background(), "test", common.Option(), Tag("test.name", "name"))
	span.Finish()
	event := &ciEvent{Type: "test", Content: span.content, common: common}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			for i := 0; i < 30; i++ {
				metadata, events := prepareCommonMetadata(nil, ciEvents{event})
				var raw bytes.Buffer
				payload := testCycleBatch{Version: 1, Metadata: metadata, Events: events}
				if err := msgp.Encode(&raw, &payload); err != nil {
					t.Error(err)
				}
				if raw.Len() > eventSize(event)+(&testCyclePayload{Version: 1}).Msgsize()+msgp.ArrayHeaderSize {
					t.Error("shared metadata escaped byte accounting")
				}
				span.SetTag("git.commit.sha", "changed")
				if got, _ := span.Meta("git.commit.sha"); got != "commit" {
					t.Error("finished getter changed")
				}
			}
		})
	}
	wg.Wait()
}

func TestCommonMetadataFailureRetainsOriginalSnapshot(t *testing.T) {
	var attempts int
	var delivered []testCycleBatch
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(401)
			return
		}
		var batch testCycleBatch
		if err := msgp.Decode(r.Body, &batch); err != nil {
			t.Error(err)
		}
		delivered = append(delivered, batch)
		w.WriteHeader(202)
	}))
	defer server.Close()
	client, err := New(Config{Transport: citransport.Config{Endpoint: server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	one := NewCommonTags(map[string]string{"git.commit.sha": "old"})
	two := NewCommonTags(map[string]string{"git.commit.sha": "new"})
	first, _ := client.StartSpan(context.Background(), "first", SpanType("test"), one.Option())
	first.Finish()
	if err := client.Flush(context.Background()); err == nil {
		t.Fatal("failure not exercised")
	}
	second, _ := client.StartSpan(context.Background(), "second", SpanType("test"), two.Option())
	second.Finish()
	if err := client.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The retried batch keeps its own payload; the later event follows it.
	var got []string
	for i := range delivered {
		for _, event := range delivered[i].Events {
			got = append(got, effectiveCommonMeta(&delivered[i], event)["git.commit.sha"])
		}
	}
	if strings.Join(got, ",") != "old,new" || client.DroppedEvents() != 0 {
		t.Fatalf("retry lost, duplicated or reordered events: %v", got)
	}
	if got, _ := first.Meta("git.commit.sha"); got != "old" {
		t.Fatal("retry changed sealed getter")
	}
}
