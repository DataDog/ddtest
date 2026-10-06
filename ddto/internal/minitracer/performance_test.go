package minitracer

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/citransport"
)

type benchmarkTransport struct{}

func (benchmarkTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	_, _ = io.Copy(io.Discard, r.Body)
	_ = r.Body.Close()
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
}

// Compare CI-shaped options (including values outside Meta) with options that
// all add text tags. Capacity changes must handle both, including custom spans.
func BenchmarkSpanMetadata(b *testing.B) {
	common := NewCommonTags(map[string]string{"ci.provider.name": "fixture", "git.branch": "main"})
	for _, shape := range []string{"ci", "text-only"} {
		b.Run(shape, func(b *testing.B) {
			for _, tags := range []int{0, 4, 12, 32, 128} {
				b.Run(fmt.Sprintf("tags=%d", tags), func(b *testing.B) {
					options := []StartSpanOption{SpanType("test"), ResourceName("fixture.test"), StartTime(time.Now()),
						Tag("test_session_id", "1"), Tag("test_module_id", "2"), Tag("test_suite_id", "3"),
						Tag("test.name", "TestFixture"), Tag("test.status", "pass"), common.Option()}
					if shape == "text-only" {
						options = nil
					}
					for i := range tags {
						options = append(options, Tag(fmt.Sprintf("custom.%d", i), "value"))
					}
					b.ReportAllocs()
					b.ResetTimer()
					for range b.N {
						span, _ := newSpan(nil, context.Background(), "testing.test", options...)
						span.Finish()
					}
				})
			}
		})
	}
}

func BenchmarkEventLifecycle(b *testing.B) {
	for _, gzip := range []bool{false, true} {
		b.Run(fmt.Sprintf("gzip=%t", gzip), func(b *testing.B) {
			tags := make(map[string]string, 12)
			for i := range 12 {
				tags[fmt.Sprintf("ci.tag.%d", i)] = "representative-static-ci-value"
			}
			c, err := New(Config{Service: "fixture", Tags: tags, Transport: citransport.Config{Endpoint: "http://diagnostic.invalid", Agentless: gzip, APIKey: "fake-key", HTTPClient: &http.Client{Transport: benchmarkTransport{}}}})
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				s, _ := c.StartSpan(context.Background(), "test", SpanType("test"), Tag("test_session_id", "1"), Tag("test_module_id", "2"), Tag("test_suite_id", "3"), Tag("test.name", "name"), Tag("test.status", "pass"))
				s.Finish()
			}
			if err := c.Close(context.Background()); err != nil {
				b.Fatal(err)
			}
			b.StopTimer()
			if c.DroppedEvents() != 0 {
				b.Fatal("events dropped")
			}
		})
	}
}
