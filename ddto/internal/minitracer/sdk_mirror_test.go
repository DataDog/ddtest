package minitracer

import (
	"context"
	"maps"
	"sync"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/propagation"
)

func TestSDKMirrorScopeAndCancellation(t *testing.T) {
	client := &Client{service: "test-service"}
	owner, _ := newSpan(client, context.Background(), "test")
	native, cancel := context.WithCancel(context.Background())
	ctx := ContextWithTest(native, owner)
	root := sdkMirrorStart(ctx, nil, "root").(*sdkMirror)
	child := sdkMirrorStart(context.Background(), root, "child").(*sdkMirror)
	if child.span.event.Content.ParentID != root.span.identity.SpanID || child.span.identity.TraceID != owner.identity.TraceID {
		t.Fatal("explicit SDK parent lost its CI scope")
	}
	if sdkMirrorStart(context.Background(), nil, "outside") != nil {
		t.Fatal("contextless SDK span was associated with a test")
	}
	returned := sdkMirrorContext(ctx, root)
	identity, _ := propagation.FromContext(returned)
	if identity != root.span.identity {
		t.Fatal("SDK child context lost its independent identity")
	}
	detached := sdkMirrorContext(returned, nil)
	newRoot := sdkMirrorStart(detached, nil, "detached").(*sdkMirror)
	if newRoot.span.event.Content.ParentID != owner.identity.SpanID {
		t.Fatal("SDK detach retained the previous copy as a parent")
	}
	otherTest, _ := newSpan(client, context.Background(), "another test")
	other := sdkMirrorStart(ContextWithTest(returned, otherTest), root, "other").(*sdkMirror)
	if other.span.event.Content.ParentID != otherTest.identity.SpanID || other.span.identity.TraceID != otherTest.identity.TraceID {
		t.Fatal("a previous SDK parent overrode the current test")
	}
	cancel()
	if returned.Err() != context.Canceled || detached.Err() != context.Canceled {
		t.Fatal("native cancellation lost")
	}
}

func TestSDKMirrorCaptureOwnsFinalData(t *testing.T) {
	owner, _ := newSpan(&Client{}, context.Background(), "test", Tag("test_session_id", "11"), Tag("test_module_id", "22"), Tag("test_suite_id", "33"))
	owner.event.common = NewCommonTags(map[string]string{"git.commit.sha": "commit"})
	mirror := sdkMirrorStart(ContextWithTest(context.Background(), owner), nil, "initial").(*sdkMirror)
	meta := map[string]string{"custom": "final", "_dd.origin": "apm", "_dd.p.tid": "foreign", "_dd.parent_id": "foreign", "runtime-id": "sdk", "test_session_id": "foreign"}
	metrics := map[string]float64{"custom": 42, "_sampling_priority_v1": 0, "_dd.top_level": 1, "test_suite_id": 999}
	delivery := sdkMirrorCapture(mirror, "final", "service", "resource", "http", 100, 200, 1, maps.All(meta), metrics, [16]byte{1}, 101).(*sdkMirrorDelivery)
	clear(meta)
	clear(metrics) // Model the SDK clearing a pooled span before delivery.
	e := delivery.event
	if e.Type != "span" || e.Version != 1 || e.Content.Name != "final" || e.Content.Duration != 200 || e.Content.Meta.asMap()["custom"] != "final" || e.Content.Metrics.asMap()["custom"] != 42 || e.Content.Meta.asMap()["apm.span_id"] != "101" {
		t.Fatalf("finalized data lost: %+v", e)
	}
	if e.common != owner.event.common || e.Content.SessionID != 11 || e.Content.ModuleID != 22 || e.Content.SuiteID != 33 || e.Content.Meta.asMap()["_dd.origin"] != "ciapp-test" {
		t.Fatal("CI ownership was replaced by APM metadata")
	}
	if _, exists := e.Content.Metrics.asMap()["_sampling_priority_v1"]; exists {
		t.Fatal("APM sampling leaked into CI")
	}
	if _, exists := e.Content.Meta.asMap()["_dd.parent_id"]; exists {
		t.Fatal("APM reparenting metadata leaked into CI")
	}
	if sdkMirrorCapture(mirror, "again", "", "", "", 0, 0, 0, maps.All(meta), metrics, [16]byte{}, 0) != nil {
		t.Fatal("repeated Finish generated another copy")
	}
}

func TestSDKMirrorConcurrentCapture(t *testing.T) {
	owner, _ := newSpan(&Client{}, context.Background(), "test")
	mirror := sdkMirrorStart(ContextWithTest(context.Background(), owner), nil, "root")
	var wg sync.WaitGroup
	var mu sync.Mutex
	deliveries := 0
	for i := 0; i < 32; i++ {
		wg.Go(func() {
			if sdkMirrorCapture(mirror, "root", "", "", "", 10, -1, 0, maps.All(map[string]string{}), nil, [16]byte{}, 101) != nil {
				mu.Lock()
				deliveries++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if deliveries != 1 || mirror.(*sdkMirror).span.event.Content.Duration != 0 {
		t.Fatal("concurrent Finish was not idempotent")
	}
}

func TestSDKMirrorContextOnFreshParent(t *testing.T) {
	owner, _ := newSpan(&Client{}, context.Background(), "test")
	root := sdkMirrorStart(ContextWithTest(context.Background(), owner), nil, "root")
	// Clients can move a span to a fresh context, then detach the SDK parent.
	ctx := sdkMirrorContext(context.Background(), root)
	detached := sdkMirrorContext(ctx, nil)
	state := sdkMirrorStart(detached, nil, "new root")
	if state == nil || state.(*sdkMirror).span.event.Content.ParentID != owner.identity.SpanID {
		t.Fatal("ContextWithSpan lost the owning test on a fresh context")
	}
}

// An SDK span whose metrics are all filtered sends an empty metrics map, as
// its filtered copy did, not an absent one.
func TestSDKMirrorKeepsAnEmptyMetricSet(t *testing.T) {
	owner, _ := newSpan(&Client{}, context.Background(), "test")
	mirror := sdkMirrorStart(ContextWithTest(context.Background(), owner), nil, "initial").(*sdkMirror)
	metrics := map[string]float64{"_sampling_priority_v1": 1, "_dd.top_level": 1}
	delivery := sdkMirrorCapture(mirror, "span", "", "", "", 1, 1, 0, maps.All(map[string]string{}), metrics, [16]byte{}, 1).(*sdkMirrorDelivery)
	if m := delivery.event.Content.Metrics; m.IsZero() || m.len() != 0 {
		t.Fatalf("metrics: zero=%t len=%d", m.IsZero(), m.len())
	}
	empty, _ := newSpan(&Client{}, context.Background(), "test")
	mirror = sdkMirrorStart(ContextWithTest(context.Background(), empty), nil, "initial").(*sdkMirror)
	delivery = sdkMirrorCapture(mirror, "span", "", "", "", 1, 1, 0, maps.All(map[string]string{}), nil, [16]byte{}, 1).(*sdkMirrorDelivery)
	if !delivery.event.Content.Metrics.IsZero() {
		t.Fatal("an SDK span without metrics sends a metrics map")
	}
}
