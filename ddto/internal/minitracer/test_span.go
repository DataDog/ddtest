package minitracer

import (
	"context"
	"time"
)

// TestSpan describes a test event that the integrations would otherwise start
// with StartSpanFromContext(ctx, Operation, options...), where options are, in
// this order:
//
//	ResourceName(Resource), SpanType(Type), StartTime(Start), Constant...,
//	Common..., Tag(key, value) for each of Metrics, Suite..., Tag(NameKey, Name),
//	and Service(Service) unless it is empty.
//
// Per-test values are fields, set without closures, and the shared option
// lists are applied in place, so starting a test builds no option list.
type TestSpan struct {
	Operation, Resource, Type string
	Start                     time.Time
	Constant, Common          []StartSpanOption // Shared and read-only.
	Metrics                   map[string]float64
	Suite                     []StartSpanOption // Shared and read-only.
	NameKey, Name             string
	Service                   string
}

// StartTestSpan starts a test event with the process-wide client. The event is
// the one that the equivalent option list starts, so its wire form is too.
func StartTestSpan(ctx context.Context, t *TestSpan) (*Span, context.Context) {
	return newTestSpan(active.Load(), ctx, t)
}

func newTestSpan(client *Client, ctx context.Context, t *TestSpan) (*Span, context.Context) {
	s, spanCtx := startSpan(client, ctx, t.Operation, metadataCapacity(client, len(t.Metrics)+1, t.Constant, t.Common, t.Suite))
	s.event.Content.Resource = t.Resource
	s.event.Content.Type = t.Type
	s.event.Content.Start = t.Start.UnixNano()
	for _, option := range t.Constant {
		option.apply(s)
	}
	for _, option := range t.Common {
		option.apply(s)
	}
	for key, value := range t.Metrics {
		s.SetMetric(key, value)
	}
	for _, option := range t.Suite {
		option.apply(s)
	}
	s.SetStringTag(t.NameKey, t.Name)
	if t.Service != "" {
		s.event.Content.Service = t.Service
	}
	return s, spanCtx
}
