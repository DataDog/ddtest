package minitracer

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"maps"
	"strconv"
	"sync/atomic"
	_ "unsafe" // compiler hooks use go:linkname without importing the SDK

	"github.com/tonyredondo/dd-ci-testing-poc/propagation"
)

type sdkTestScopeKey struct{}
type sdkTestScope struct{ test *Span }
type sdkMirror struct {
	span  *Span
	scope *sdkTestScope
}
type sdkMirrorDelivery struct {
	client *Client
	event  *ciEvent
}

var sdkMirrorEnabled atomic.Bool

// SDKMirrorEnabled is set by the compiled SDK hook during package initialization.
// Mini-only binaries skip native context binding and its allocations entirely.
func SDKMirrorEnabled() bool { return sdkMirrorEnabled.Load() }

//go:linkname sdkMirrorEnable
func sdkMirrorEnable() { sdkMirrorEnabled.Store(true) }

// ContextWithTest binds the native cancellation lifetime to a CI test. SDK
// copies inherit this scope; the full SDK never reads our propagation key.
func ContextWithTest(ctx context.Context, test *Span) context.Context {
	if test == nil {
		return ctx
	}
	return propagation.WithContext(context.WithValue(ctx, sdkTestScopeKey{}, &sdkTestScope{test}), test.identity)
}

// sdkMirrorStart is called after the SDK has selected its own parent. The
// opaque state lives in the SDK SpanContext, which survives span pooling.
//
//go:linkname sdkMirrorStart
func sdkMirrorStart(ctx context.Context, parent any, name string) any {
	var scope *sdkTestScope
	if ctx != nil {
		scope, _ = ctx.Value(sdkTestScopeKey{}).(*sdkTestScope)
	}
	previous, _ := parent.(*sdkMirror)
	if scope == nil && previous != nil {
		scope = previous.scope
	}
	if scope == nil || scope.test.client == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if previous != nil && previous.scope == scope {
		ctx = propagation.WithContext(ctx, previous.span.identity)
	} else if identity, ok := propagation.FromContext(ctx); !ok || identity.TraceID != scope.test.identity.TraceID {
		ctx = propagation.WithContext(ctx, scope.test.identity)
	}
	span, _ := newSpan(scope.test.client, ctx, name)
	// Test defaults are immutable. Read hierarchy under its lock because tests
	// may finish concurrently with child creation; never retain their maps.
	scope.test.mu.Lock()
	span.common = scope.test.common
	span.hierarchy, span.hierarchySet = scope.test.hierarchy, scope.test.hierarchySet
	scope.test.mu.Unlock()
	return &sdkMirror{span: span, scope: scope}
}

// sdkMirrorContext carries the independent CI identity alongside the original
// SDK context. Detaching an SDK parent resets CI parentage to the owning test.
//
//go:linkname sdkMirrorContext
func sdkMirrorContext(ctx context.Context, state any) context.Context {
	if ctx == nil {
		return ctx
	}
	if mirror, ok := state.(*sdkMirror); ok {
		// ContextWithSpan can move a copied span onto context.Background().
		// Retain its test scope there, while an explicit current test wins.
		if _, present := ctx.Value(sdkTestScopeKey{}).(*sdkTestScope); !present {
			ctx = context.WithValue(ctx, sdkTestScopeKey{}, mirror.scope)
		}
		return propagation.WithContext(ctx, mirror.span.identity)
	}
	if scope, ok := ctx.Value(sdkTestScopeKey{}).(*sdkTestScope); ok {
		return propagation.WithContext(ctx, scope.test.identity)
	}
	return ctx
}

// sdkMirrorCapture receives finalized SDK fields while its span lock is held.
// Every map is detached before returning: the SDK may clear and reuse them as
// soon as its lock is released. Delivery runs after that release.
//
//go:linkname sdkMirrorCapture
func sdkMirrorCapture(state any, name, service, resource, kind string, start, duration int64, failed int32, meta func(func(string, string) bool), metrics map[string]float64, originalTrace [16]byte, originalSpan uint64) any {
	mirror, ok := state.(*sdkMirror)
	if !ok {
		return nil
	}
	s := mirror.span
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return nil
	}
	s.finished = true
	s.content.Name, s.content.Service, s.content.Resource, s.content.Type = name, service, resource, kind
	s.content.Start, s.content.Duration, s.content.Error = start, max(duration, 0), failed
	meta(func(key, value string) bool {
		switch key {
		case "_dd.origin", "_dd.p.tid", "_dd.p.dm", "_dd.parent_id", "runtime-id", "test_session_id", "test_module_id", "test_suite_id":
			// CI identity and runtime metadata belong to Mini.
		default:
			s.setMeta(key, value)
		}
		return true
	})
	if len(metrics) != 0 {
		s.content.Metrics = maps.Clone(metrics)
		for _, key := range []string{"_sampling_priority_v1", "_dd.top_level", "_dd.profiling.enabled", "_dd.agent_psr", "_dd.rule_psr", "_dd.limit_psr", "test_session_id", "test_module_id", "test_suite_id"} {
			delete(s.content.Metrics, key)
		}
	}
	// Keep the original APM identity for diagnosis without modifying its trace.
	s.setMeta("apm.trace_id", hex.EncodeToString(originalTrace[:]))
	s.setMeta("apm.span_id", strconv.FormatUint(originalSpan, 10))
	s.content.TraceID = binary.BigEndian.Uint64(s.identity.TraceID[8:])
	event := &ciEvent{Type: "span", Version: 1, Content: s.content, common: s.common}
	event.Content.SessionID, _ = strconv.ParseUint(s.hierarchy[0], 10, 64)
	event.Content.ModuleID, _ = strconv.ParseUint(s.hierarchy[1], 10, 64)
	event.Content.SuiteID, _ = strconv.ParseUint(s.hierarchy[2], 10, 64)
	return &sdkMirrorDelivery{client: s.client, event: event}
}

//go:linkname sdkMirrorDeliver
func sdkMirrorDeliver(state any) {
	if delivery, ok := state.(*sdkMirrorDelivery); ok {
		delivery.client.add(delivery.event)
	}
}
