package integrations

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/citransport"
	tracer "github.com/tonyredondo/dd-ci-testing-poc/internal/minitracer"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils"
)

type commonMetadataTransport struct{ bytes, requests int64 }

func (c *commonMetadataTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	n, err := io.Copy(io.Discard, r.Body)
	_ = r.Body.Close()
	c.bytes += n
	c.requests++
	return &http.Response{StatusCode: 202, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, err
}

// Include SDK option construction, span creation, batching, encoding and delivery.
// The same test source is compiled before and after the metadata adaptation.
func BenchmarkCommonMetadataLifecycle(b *testing.B) {
	utils.ResetCITags()
	utils.ResetCIMetrics()
	b.Cleanup(utils.ResetCITags)
	b.Cleanup(utils.ResetCIMetrics)
	tags := make(map[string]string, 24)
	for i := range 24 {
		tags[fmt.Sprintf("ci.fixture.%d", i)] = "representative-common-ci-value"
	}
	utils.AddCITagsMap(tags)
	_ = utils.GetCITags()
	for _, gzip := range []bool{false, true} {
		b.Run(fmt.Sprintf("gzip=%t", gzip), func(b *testing.B) {
			sink := &commonMetadataTransport{}
			client, err := tracer.New(tracer.Config{Service: "fixture", Transport: citransport.Config{Endpoint: "http://diagnostic.invalid", Agentless: gzip, APIKey: "fixture", HTTPClient: &http.Client{Transport: sink}}})
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				opts := fillCommonTags([]tracer.StartSpanOption{tracer.SpanType("test"), tracer.Tag("test.name", "name"), tracer.Tag("test.status", "pass")})
				span, _ := client.StartSpan(context.Background(), "testing.test", opts...)
				span.Finish()
			}
			if err := client.Close(context.Background()); err != nil {
				b.Fatal(err)
			}
			b.StopTimer()
			if client.DroppedEvents() != 0 {
				b.Fatal("dropped events")
			}
			b.ReportMetric(float64(sink.bytes)/float64(b.N), "wire-B/event")
		})
	}
}
