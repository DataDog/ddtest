package minitracer

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"runtime"
	"testing"
	"time"
	"weak"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/msgp/msgp"
	"github.com/tonyredondo/dd-ci-testing-poc/propagation"
)

func (m metaSet) asMap() map[string]string {
	out := make(map[string]string, m.len())
	for k, v := range m.all {
		out[k] = v
	}
	return out
}

func (m metricSet) asMap() map[string]float64 {
	out := make(map[string]float64, m.len())
	for k, v := range m.all {
		out[k] = v
	}
	return out
}

func metaOf(values map[string]string) metaSet {
	out := metaSet{newEntrySet[string](len(values))}
	for k, v := range values {
		out.set(k, v)
	}
	return out
}

func metricsOf(values map[string]float64) metricSet {
	var out metricSet
	for k, v := range values {
		out.set(k, v)
	}
	return out
}

// sameContent compares events regardless of the order of their entries,
// which projection takes from a map.
func sameContent(a, b eventContent) bool {
	if !reflect.DeepEqual(a.Meta.asMap(), b.Meta.asMap()) || !reflect.DeepEqual(a.Metrics.asMap(), b.Metrics.asMap()) {
		return false
	}
	a.Meta, a.Metrics, b.Meta, b.Metrics = metaSet{}, metricSet{}, metaSet{}, metricSet{}
	return reflect.DeepEqual(a, b)
}

// A set keeps up to linearEntries entries in its slice and switches to a map
// beyond; both forms get, replace, delete, filter and clone alike.
func TestEntrySetForms(t *testing.T) {
	for _, n := range []int{3, linearEntries, linearEntries + 5} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			var set metaSet
			want := map[string]string{}
			for i := range n {
				key := fmt.Sprint("key.", i)
				set.set(key, "first")
				set.set(key, fmt.Sprint(i)) // Replaces.
				want[key] = fmt.Sprint(i)
			}
			if big := set.big != nil; big != (n > linearEntries) || set.len() != n {
				t.Fatalf("%d entries: map=%t len=%d", n, big, set.len())
			}
			set.delete("key.1")
			set.delete("missing")
			delete(want, "key.1")
			set.deleteIf(func(key string) bool { return key == "key.2" })
			delete(want, "key.2")
			if v, ok := set.get("key.0"); !ok || v != "0" {
				t.Fatalf("key.0 = %q, %t", v, ok)
			}
			if _, ok := set.get("key.1"); ok {
				t.Fatal("deleted key.1 remains")
			}
			if !reflect.DeepEqual(set.asMap(), want) {
				t.Fatalf("set %v, want %v", set.asMap(), want)
			}
			clone := metaSet{set.clone(1)}
			clone.set("added", "x")
			if _, ok := set.get("added"); ok || clone.len() != len(want)+1 {
				t.Fatal("clone shares entries with its source")
			}
		})
	}
}

// Sets encode as the msgpack maps that the event fields were, and an absent
// set leaves its field off the wire.
func TestEntrySetsEncodeAsMaps(t *testing.T) {
	for _, n := range []int{1, linearEntries + 1} {
		content := eventContent{Name: "test", Meta: metaSet{newEntrySet[string](n)}}
		for i := range n {
			content.Meta.set(fmt.Sprint("key.", i), fmt.Sprint(i))
		}
		var raw bytes.Buffer
		if err := msgp.Encode(&raw, &content); err != nil {
			t.Fatal(err)
		}
		decoded, _, err := msgp.ReadMapStrIntfBytes(raw.Bytes(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if meta, ok := decoded["meta"].(map[string]any); !ok || len(meta) != n || meta["key.0"] != "0" {
			t.Fatalf("%d entries: meta %v", n, decoded["meta"])
		}
		if _, present := decoded["metrics"]; present {
			t.Fatalf("an absent metric set was encoded: %v", decoded["metrics"])
		}
		var back eventContent
		if err := msgp.Decode(bytes.NewReader(raw.Bytes()), &back); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(back.Meta.asMap(), content.Meta.asMap()) || !back.Metrics.IsZero() {
			t.Fatalf("decoded %v %v", back.Meta.asMap(), back.Metrics.asMap())
		}
	}
}

// A span with more tags than a slice holds keeps SetTag's semantics.
func TestSpanWithManyTags(t *testing.T) {
	span, _ := newSpan(nil, context.Background(), "test")
	for i := range 2 * linearEntries {
		span.SetTag(fmt.Sprint("tag.", i), "text")
	}
	span.SetTag("tag.3", 4) // A number replaces the text.
	span.SetTag("tag.4", "replaced")
	span.Finish()
	if _, ok := span.Meta("tag.3"); ok {
		t.Fatal("tag.3 stayed text")
	}
	if v, _ := span.Metric("tag.3"); v != 4 {
		t.Fatalf("tag.3 metric = %v", v)
	}
	if v, _ := span.Meta("tag.4"); v != "replaced" || span.event.Content.Meta.big == nil {
		t.Fatalf("tag.4 = %q, map=%t", v, span.event.Content.Meta.big != nil)
	}
}

// The context that newSpan returns carries the span's identity, other values
// and its parent's cancellation, as context.WithValue did.
func TestSpanContext(t *testing.T) {
	type key struct{}
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), key{}, "value"))
	span, ctx := newSpan(nil, parent, "test")
	if identity, ok := propagation.FromContext(ctx); !ok || identity != span.identity {
		t.Fatalf("identity %v, %t", identity, ok)
	}
	child, _ := newSpan(nil, context.WithValue(ctx, "other", 1), "child")
	if child.event.Content.ParentID != span.identity.SpanID {
		t.Fatal("a child of a derived context lost its parent")
	}
	if ctx.Value(key{}) != "value" {
		t.Fatal("the parent's values are lost")
	}
	derived, stop := context.WithCancel(ctx)
	defer stop()
	cancel()
	select {
	case <-derived.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not reach a context derived from the span's")
	}
}

// The context that newSpan returns does not keep the span, or its tags, alive.
func TestSpanContextDoesNotRetainTheSpan(t *testing.T) {
	span, ctx := newSpan(nil, context.Background(), "test", Tag("large", string(make([]byte, 1<<20))))
	span.Finish()
	finished := weak.Make(span)
	span = nil
	runtime.GC()
	if finished.Value() != nil {
		t.Fatal("a kept context retains its finished span")
	}
	if _, ok := propagation.FromContext(ctx); !ok {
		t.Fatal("the context lost the identity")
	}
}

// Options that testopt hands out leave a finished span's event, which the
// client may be sending, unchanged.
func TestPublicOptionsLeaveFinishedSpansUnchanged(t *testing.T) {
	span, _ := newSpan(nil, context.Background(), "test", SpanType("test"), ResourceName("resource"), Service("service"))
	span.Finish()
	before := span.event.Content
	for _, option := range []StartSpanOption{ResourceName("changed"), SpanType("changed"), Service("changed"), StartTime(time.Unix(1, 0)), Tag("tag", "changed")} {
		option.Func()(span)
	}
	if !sameContent(span.event.Content, before) {
		t.Fatalf("event changed after Finish: %+v", span.event.Content)
	}
}
