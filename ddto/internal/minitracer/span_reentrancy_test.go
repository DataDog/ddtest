package minitracer

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/ddtrace/ext"
)

func TestReservedTagsUpdateEventFields(t *testing.T) {
	for _, tc := range []struct {
		key   string
		field func(*Span) string
	}{
		{"span.name", func(s *Span) string { return s.content.Name }},
		{"service.name", func(s *Span) string { return s.content.Service }},
		{"resource.name", func(s *Span) string { return s.content.Resource }},
		{"span.type", func(s *Span) string { return s.content.Type }},
	} {
		t.Run(tc.key, func(t *testing.T) {
			span, _ := newSpan(nil, context.Background(), "original", SpanType("test"))
			span.SetTag(tc.key, 1)
			span.SetTag(tc.key, "updated")
			if got := tc.field(span); got != "updated" {
				t.Fatalf("event field=%q, want updated", got)
			}
			if _, ok := span.Meta(tc.key); ok {
				t.Fatal("reserved field remained in metadata")
			}
			if _, ok := span.Metric(tc.key); ok {
				t.Fatal("string override kept its numeric tag")
			}
			span.Finish()
			span.SetTag(tc.key, "after finish")
			if tc.field(span) != "updated" {
				t.Fatal("finished field was modified")
			}
		})
	}
}

type spanReaderStringer struct {
	span   *Span
	finish bool
}

func (v spanReaderStringer) String() string {
	value, _ := v.span.Meta("initial")
	if v.finish {
		v.span.Finish()
	}
	return value
}

type spanReaderError struct{ span *Span }

func (v spanReaderError) Error() string { value, _ := v.span.Meta("initial"); return value }
func (v spanReaderError) Format(state fmt.State, _ rune) {
	value, _ := v.span.Meta("initial")
	io.WriteString(state, value)
}

type spanReaderFormatter struct{ span *Span }

func (v spanReaderFormatter) Format(state fmt.State, _ rune) {
	value, _ := v.span.Meta("initial")
	io.WriteString(state, value)
}

func TestTagFormattingCanReadItsSpan(t *testing.T) {
	for _, name := range []string{"stringer", "formatter", "slice", "error", "error-no-stack", "finish"} {
		t.Run(name, func(t *testing.T) {
			span, _ := newSpan(nil, context.Background(), "test", Tag("initial", "read safely"))
			key, value := "formatted", any(spanReaderStringer{span: span})
			switch name {
			case "formatter":
				value = spanReaderFormatter{span}
			case "slice":
				value = []any{spanReaderStringer{span: span}}
			case "error":
				key, value = "error", spanReaderError{span}
			case "error-no-stack":
				key, value = ext.ErrorNoStackTrace, spanReaderError{span}
			case "finish":
				value = spanReaderStringer{span: span, finish: true}
			}
			done := make(chan struct{})
			go func() { span.SetTag(key, value); close(done) }()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("tag formatting blocked on its own span")
			}
			if name == "finish" {
				if _, ok := span.Meta("formatted"); ok {
					t.Fatal("formatting added a tag after Finish")
				}
				return
			}
			if name == "slice" {
				key = "formatted.0"
			}
			if name == "error" || name == "error-no-stack" {
				key = "error.message"
			}
			if got, _ := span.Meta(key); got != "read safely" {
				t.Fatalf("formatted metadata=%q", got)
			}
		})
	}
}
