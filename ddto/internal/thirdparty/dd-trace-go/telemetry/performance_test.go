package telemetry

import (
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

type benchmarkHTTP struct{}

func (benchmarkHTTP) RoundTrip(request *http.Request) (*http.Response, error) {
	_, _ = io.Copy(io.Discard, request.Body)
	_ = request.Body.Close()
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
}

// This exercises real CI telemetry lookup, accumulation, collection and JSON
// delivery. Periodic flushes keep distributions bounded in both variants.
func BenchmarkCITelemetry(b *testing.B) {
	for _, parallel := range []bool{false, true} {
		name := "serial"
		if parallel {
			name = "parallel"
		}
		b.Run(name, func(b *testing.B) {
			c, err := NewClient("fixture", "benchmark", "1.0", ClientConfig{AgentURL: "http://fixture.invalid", HTTPClient: &http.Client{Transport: benchmarkHTTP{}}, MaxDistinctLogs: 100000})
			if err != nil {
				b.Fatal(err)
			}
			restore := MockClient(c)
			defer restore()
			defer c.Close()
			var sequence atomic.Uint64
			operation := func() {
				Count(NamespaceCIVisibility, "events_enqueued_for_serialization", nil).Submit(1)
				Distribution(NamespaceCIVisibility, "endpoint_payload.bytes", nil).Submit(1024)
				Log(NewRecord(LogWarn, "synthetic CI diagnostic"))
				if sequence.Add(1)%128 == 0 {
					c.Flush()
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			if parallel {
				b.RunParallel(func(pb *testing.PB) {
					for pb.Next() {
						operation()
					}
				})
			} else {
				for i := 0; i < b.N; i++ {
					operation()
				}
			}
			c.Flush()
			b.StopTimer()
		})
	}
}
func BenchmarkCITelemetryStartup(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		c, err := NewClient("fixture", "benchmark", "1.0", ClientConfig{AgentURL: "http://fixture.invalid", HTTPClient: &http.Client{Transport: benchmarkHTTP{}}})
		if err != nil {
			b.Fatal(err)
		}
		if err := c.Close(); err != nil {
			b.Fatal(err)
		}
	}
}
