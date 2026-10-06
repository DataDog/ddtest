package telemetrytest

import (
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/telemetry"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/telemetry/internal/transport"
)

// The mini runtime retains CI metrics, but emits neither idle SDK heartbeats,
// dependency inventory nor REST endpoint inventory.
func TestCapturingClientCIOnly(t *testing.T) {
	client, capture := NewCapturingClient(t)
	defer client.Close()
	client.ProductStarted(telemetry.NamespaceCIVisibility)
	client.Count(telemetry.NamespaceCIVisibility, "event_created", []string{"event_type:test", "test_framework:testing"}).Submit(3)
	client.AppStart()
	client.Flush()
	client.Flush()
	var metric bool
	check := func(kind transport.RequestType, payload transport.Payload) {
		switch kind {
		case "app-heartbeat", "app-extended-heartbeat", "app-dependencies-loaded", "app-endpoints":
			t.Errorf("non-CI payload %s", kind)
		}
		if p, ok := payload.(*transport.GenerateMetrics); ok {
			for _, s := range p.Series {
				if s.Namespace == telemetry.NamespaceCIVisibility && s.Metric == "event_created" && len(s.Points) == 1 && s.Points[0][1] == float64(3) {
					metric = true
				}
			}
		}
	}
	for _, body := range capture.Bodies() {
		check(body.RequestType, body.Payload)
		if batch, ok := body.Payload.(transport.MessageBatch); ok {
			for _, message := range batch {
				check(message.RequestType, message.Payload)
			}
		}
	}
	if !metric {
		t.Fatal("CI metric not delivered")
	}
}
