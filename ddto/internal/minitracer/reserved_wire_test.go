package minitracer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/citransport"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/msgp/msgp"
)

func TestReservedTagsReachWireFieldsAndEventKind(t *testing.T) {
	var payload testCycleBatch
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := msgp.Decode(r.Body, &payload); err != nil {
			t.Error(err)
		}
		w.WriteHeader(202)
	}))
	defer server.Close()
	client, err := New(Config{Transport: citransport.Config{Endpoint: server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	span, _ := client.StartSpan(context.Background(), "old.name", SpanType("span"))
	for key, value := range map[string]string{"span.name": "test.name", "service.name": "test.service", "resource.name": "test.resource", "span.type": "test"} {
		span.SetTag(key, value)
	}
	span.Finish()
	if err := client.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(payload.Events) != 1 {
		t.Fatalf("events=%d", len(payload.Events))
	}
	event := payload.Events[0]
	if event.Type != "test" || event.Version != 2 || event.Content.Name != "test.name" || event.Content.Service != "test.service" || event.Content.Resource != "test.resource" || event.Content.Type != "test" {
		t.Fatalf("reserved fields lost: %+v", event)
	}
}
