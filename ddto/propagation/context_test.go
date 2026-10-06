package propagation

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"net/http"
	"testing"
)

func TestW3CAndDatadogExchange(t *testing.T) {
	// The canonical W3C example is also used by an independent dd-trace-go
	// extractor in the integration suite.
	carrier := http.Header{}
	carrier.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	carrier.Set("tracestate", "vendor=value,dd=s:2;o:ciapp-test")
	c, err := Extract(carrier, W3C)
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(c.TraceID[:]) != "4bf92f3577b34da6a3ce929d0e0e4736" || c.SpanID != 0x00f067aa0ba902b7 || c.Priority != 2 || c.Origin != "ciapp-test" {
		t.Fatalf("wrong identity: %+v", c)
	}
	out := MapCarrier{}
	if err = Inject(c, out, Datadog); err != nil {
		t.Fatal(err)
	}
	restored, err := Extract(out, Datadog)
	if err != nil {
		t.Fatal(err)
	}
	if restored.TraceID != c.TraceID || restored.SpanID != c.SpanID || restored.Priority != c.Priority || restored.Origin != c.Origin {
		t.Fatalf("lost identity: %+v", restored)
	}
	out = MapCarrier{}
	if err = Inject(c, out, W3C); err != nil {
		t.Fatal(err)
	}
	restored, err = Extract(out, W3C)
	if err != nil {
		t.Fatal(err)
	}
	if restored.TraceID != c.TraceID || restored.SpanID != c.SpanID || restored.TraceState != "dd=s:2;o:ciapp-test,vendor=value" {
		t.Fatalf("lost state: %+v", restored)
	}
}
func TestMalformedRemoteContexts(t *testing.T) {
	for _, header := range []string{"", "00-00000000000000000000000000000000-00f067aa0ba902b7-01", "00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01", "ff-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", "00-4BF92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01-extra"} {
		t.Run(header, func(t *testing.T) {
			if _, err := Extract(MapCarrier{"traceparent": header}, W3C); err == nil {
				t.Fatal("accepted invalid parent")
			}
		})
	}
	for _, changes := range []MapCarrier{{"x-datadog-trace-id": "0"}, {"x-datadog-trace-id": "-1"}, {"x-datadog-parent-id": "18446744073709551616"}, {"x-datadog-sampling-priority": "3"}, {"x-datadog-tags": "_dd.p.tid=bad"}, {"x-datadog-tags": "_dd.p.tid=0000000000000001,_dd.p.tid=0000000000000002"}} {
		c := MapCarrier{"x-datadog-trace-id": "1", "x-datadog-parent-id": "2"}
		for k, v := range changes {
			c[k] = v
		}
		if _, err := Extract(c, Datadog); err == nil {
			t.Fatalf("accepted malformed carrier %v", c)
		}
	}
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	c.Origin = "bad\r\nheader"
	out := MapCarrier{}
	if err = Inject(c, out, W3C); err == nil || len(out) != 0 {
		t.Fatal("invalid origin wrote headers")
	}
}
func TestContextLifetimeAndChildren(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	parent, cancel := context.WithCancel(context.Background())
	ctx := WithContext(parent, c)
	cancel()
	if ctx.Err() != context.Canceled {
		t.Fatal("lost cancellation")
	}
	child, err := c.Child()
	if err != nil {
		t.Fatal(err)
	}
	if child.TraceID != c.TraceID || child.SpanID == c.SpanID || !child.Valid() {
		t.Fatal("invalid child identity")
	}
	if original, ok := FromContext(ctx); !ok || original != c {
		t.Fatal("context value changed")
	}
	if _, ok := FromContext(context.Background()); ok {
		t.Fatal("invented identity")
	}
}
func TestInvalidStateDoesNotDiscardValidParent(t *testing.T) {
	for _, state := range []string{"dd=s:2,dd=s:1", "bad key=value", "vendor=bad=value", "vendor="} {
		c, err := Extract(MapCarrier{"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", "tracestate": state}, W3C)
		if err != nil || !c.Valid() || c.TraceState != "" {
			t.Fatalf("invalid state broke parent: %+v %v", c, err)
		}
	}
}

func TestVendorStatePreservedAndInvalidPriorityWritesNothing(t *testing.T) {
	c, err := Extract(MapCarrier{"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", "tracestate": "dd=s:2;o:ciapp-test;t.dm:-4,vendor=value"}, W3C)
	if err != nil {
		t.Fatal(err)
	}
	out := MapCarrier{}
	if err = Inject(c, out, W3C); err != nil {
		t.Fatal(err)
	}
	if out["tracestate"] != "dd=s:2;o:ciapp-test;t.dm:-4,vendor=value" {
		t.Fatalf("lost vendor state: %v", out)
	}
	c.Priority = 4
	for _, format := range []Format{W3C, Datadog} {
		out = MapCarrier{}
		if err = Inject(c, out, format); err == nil || len(out) != 0 {
			t.Fatal("invalid priority wrote headers")
		}
	}
}

func TestGeneratedSpanIDsUse63Bits(t *testing.T) {
	high := false
	for i := 0; i < 2000; i++ {
		root, err := New()
		if err != nil {
			t.Fatal(err)
		}
		child, err := root.Child()
		if err != nil {
			t.Fatal(err)
		}
		if root.SpanID>>63 != 0 || child.SpanID>>63 != 0 || root.SpanID == 0 || child.SpanID == 0 {
			t.Fatalf("span IDs exceed 63 bits: root=%x child=%x", root.SpanID, child.SpanID)
		}
		if root.SpanID != binary.BigEndian.Uint64(root.TraceID[8:]) {
			t.Fatal("root span ID is not the trace ID's low half")
		}
		high = high || root.SpanID>>62 != 0
	}
	if !high {
		t.Fatal("63-bit range not exercised")
	}
}
