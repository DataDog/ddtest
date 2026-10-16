package minitracer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/citransport"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/ddtrace/ext"
)

type formattedError struct{}

func (formattedError) Error() string { return "formatted" }
func (formattedError) Format(s fmt.State, verb rune) {
	if s.Flag('+') {
		fmt.Fprint(s, "formatted with detail")
		return
	}
	fmt.Fprint(s, "formatted")
}

type nilStringer struct{ value string }

func (n *nilStringer) String() string { return n.value }

// Only Tag options reserve a metadata entry; options that set fields or shared
// defaults do not. The client adds its tags, env and version, and text tags
// fill the map exactly.
func TestMetadataCapacityCountsTagOptions(t *testing.T) {
	common := NewCommonTags(map[string]string{"git.branch": "main"})
	options := []StartSpanOption{ResourceName("resource"), SpanType("test"), StartTime(time.Now()), Service("service"), common.Option(),
		Tag("_dd.host.vcpu_count", 32.0), Tag("test.name", "name"), Tag(ext.ManualKeep, true)}
	if got := metadataCapacity(nil, options); got != 2+3 {
		t.Fatalf("capacity %d, want _dd.origin, _dd.p.tid and 3 tags", got)
	}
	client := &Client{tags: map[string]string{"team": "a", "tier": "b"}, env: "ci", serviceVersion: "1.0"}
	if got := metadataCapacity(client, options); got != 2+3+4 {
		t.Fatalf("capacity %d, want the client's 2 tags, env and version too", got)
	}
	text := []StartSpanOption{Tag("one", "1"), Tag("two", "2"), Tag("three", "3")}
	span, _ := newSpan(nil, context.Background(), "test", text...)
	if got, want := len(span.content.Meta), metadataCapacity(nil, text); got != want {
		t.Fatalf("text-only span has %d entries, reserved %d", got, want)
	}
}

func TestSetTagFollowsSDKValueRules(t *testing.T) {
	span, _ := newSpan(nil, context.Background(), "test")
	number, text := 42, "pointed"
	var nilNumber *int
	var stringer *nilStringer
	span.SetTag("pointer.number", &number)
	span.SetTag("pointer.text", &text)
	span.SetTag("pointer.nil", nilNumber)
	span.SetTag("stringer.nil", stringer)
	span.SetTag("bytes", []byte("raw"))
	span.SetTag("list", []any{1, "two"})
	span.SetTag("int64.large", int64(1<<53))
	span.SetTag("uint64.large", uint64(1<<53))
	span.SetTag("bool", true)
	for key, want := range map[string]string{"pointer.text": "pointed", "stringer.nil": "<nil>", "bytes": "raw", "list.1": "two", "int64.large": "9007199254740992", "uint64.large": "9007199254740992", "bool": "true"} {
		if got, ok := span.Meta(key); !ok || got != want {
			t.Errorf("meta %s=%q (%v), want %q", key, got, ok, want)
		}
	}
	for key, want := range map[string]float64{"pointer.number": 42, "pointer.nil": 0, "list.0": 1} {
		if got, ok := span.Metric(key); !ok || got != want {
			t.Errorf("metric %s=%v (%v), want %v", key, got, ok, want)
		}
	}
}

func TestErrorTagsFollowSDK(t *testing.T) {
	for _, tc := range []struct {
		name          string
		key           string
		value         any
		flag          int32
		message, kind string
		stack, handle bool
	}{
		{name: "error", key: ext.Error, value: errors.New("boom"), flag: 1, message: "boom", kind: "*errors.errorString", handle: true},
		{name: "formatter", key: ext.Error, value: formattedError{}, flag: 1, message: "formatted", kind: "minitracer.formattedError", stack: true, handle: true},
		{name: "no stack", key: ext.ErrorNoStackTrace, value: errors.New("quiet"), flag: 1, message: "quiet", kind: "*errors.errorString"},
		{name: "string", key: ext.Error, value: "text", flag: 1},
		{name: "true", key: ext.Error, value: true, flag: 1},
		{name: "false", key: ext.Error, value: false},
		{name: "nil", key: ext.Error, value: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			span, _ := newSpan(nil, context.Background(), "test")
			span.content.Error = 1 - tc.flag // Every case must set the flag explicitly.
			span.SetTag(tc.key, tc.value)
			if span.content.Error != tc.flag {
				t.Fatalf("error flag=%d, want %d", span.content.Error, tc.flag)
			}
			if got, _ := span.Meta(ext.ErrorMsg); got != tc.message {
				t.Fatalf("message=%q", got)
			}
			if got, _ := span.Meta(ext.ErrorType); got != tc.kind {
				t.Fatalf("type=%q", got)
			}
			if got, ok := span.Meta(ext.ErrorStack); ok != tc.stack || tc.stack && got != "formatted with detail" {
				t.Fatalf("stack=%q (%v)", got, ok)
			}
			got, ok := span.Meta(ext.ErrorHandlingStack)
			if ok != tc.handle || tc.handle && !strings.Contains(got, "TestErrorTagsFollowSDK") {
				t.Fatalf("handling stack=%q (%v)", got, ok)
			}
		})
	}
}

// dd-trace-go copies a test span's meta before it moves the ITR correlation to
// its own field, so a test event carries it in both; session, module and suite
// events keep it only in meta.
func TestTestEventKeepsITRCorrelationInMeta(t *testing.T) {
	client, err := New(Config{Transport: citransport.Config{Endpoint: "http://intake.invalid", HTTPClient: &http.Client{Transport: benchmarkTransport{}}}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	const key = "itr_correlation_id"
	for _, kind := range []string{"test", "test_session_end"} {
		span, _ := client.StartSpan(context.Background(), kind, SpanType(kind), Tag(key, "correlation"))
		span.Finish()
	}
	client.mu.Lock()
	test, session := client.events[0], client.events[1]
	client.mu.Unlock()
	if test.Content.CorrelationID != "correlation" || test.Content.Meta[key] != "correlation" {
		t.Fatalf("test event: field=%q meta=%v", test.Content.CorrelationID, test.Content.Meta)
	}
	if session.Content.CorrelationID != "" || session.Content.Meta[key] != "correlation" {
		t.Fatalf("session event: field=%q meta=%v", session.Content.CorrelationID, session.Content.Meta)
	}
}
