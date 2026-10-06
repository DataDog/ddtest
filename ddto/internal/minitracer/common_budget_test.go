package minitracer

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/citransport"
)

func TestCommonMetadataOverridesKeepByteBudget(t *testing.T) {
	for _, override := range []any{float64(42), "replacement"} {
		t.Run(map[bool]string{true: "metric", false: "text"}[override == float64(42)], func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil || len(body) > citransport.TestCycleMaxPayloadBytes {
					t.Errorf("body=%d error=%v", len(body), err)
				}
				requests.Add(1)
				w.WriteHeader(202)
			}))
			defer server.Close()
			client, err := New(Config{Transport: citransport.Config{Endpoint: server.URL}})
			if err != nil {
				t.Fatal(err)
			}
			common := NewCommonTags(map[string]string{"ci.large": strings.Repeat("c", 3<<20)})
			span, _ := client.StartSpan(context.Background(), "test", SpanType("test"), common.Option(), Tag("ci.large", override), Tag("large", strings.Repeat("l", 3<<20)))
			span.Finish()
			if err := client.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			if requests.Load() != 1 || client.DroppedEvents() != 0 {
				t.Fatalf("masked common data consumed the event budget: requests=%d dropped=%d", requests.Load(), client.DroppedEvents())
			}
		})
	}
}
