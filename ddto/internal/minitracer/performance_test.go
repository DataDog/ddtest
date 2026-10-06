package minitracer

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/citransport"
)

type benchmarkTransport struct{}

func (benchmarkTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	_, _ = io.Copy(io.Discard, r.Body)
	_ = r.Body.Close()
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
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
