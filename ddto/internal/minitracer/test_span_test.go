package minitracer

import (
	"bytes"
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/citransport"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/msgp/msgp"
)

// startTestSpanBothWays starts the same test event from the integrations'
// option list and from a TestSpan, then finishes both alike. Spans add the
// client's tags in map order, so only one client tag gives both the same order.
func startTestSpanBothWays(t *testing.T, clientTags map[string]string, metrics map[string]float64, service string) (fromOptions, fromTestSpan *Span) {
	t.Helper()
	client, err := New(Config{Service: "client", Env: "ci", ServiceVersion: "1.0", Tags: clientTags,
		Transport: citransport.Config{Endpoint: "http://diagnostic.invalid", HTTPClient: &http.Client{Transport: benchmarkTransport{}}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close(context.Background()) })
	common := NewCommonTags(map[string]string{"git.branch": "main", "os.platform": "linux"})
	constant := []StartSpanOption{Tag("_dd.origin", "ciapp-test"), Tag("manual.keep", true)}
	// A metric replaces a common text tag of the same name, and a suite tag
	// replaces a metric, when the metrics have such keys.
	commonOptions := []StartSpanOption{Tag("_dd.library_capabilities.auto_test_retries", "1"), Tag("test.command", "go test"), Tag("from.common", "text"), common.Option()}
	suite := []StartSpanOption{Tag("test.type", "test"), Tag("test.module", "module"), Tag("test.type", "test"), Tag("test.suite", "suite"), Tag("from.suite", "text")}
	start := time.Unix(1_800_000_000, 5)
	options := append([]StartSpanOption{ResourceName("suite.TestX"), SpanType("test"), StartTime(start)}, constant...)
	options = append(options, commonOptions...)
	for key, value := range metrics {
		options = append(options, Tag(key, value))
	}
	options = append(options, suite...)
	options = append(options, Tag("test.name", "TestX"))
	if service != "" {
		options = append(options, Service(service))
	}
	fromOptions, _ = newSpan(client, context.Background(), "golang.org/pkg/testing.test", options...)
	fromTestSpan, _ = newTestSpan(client, context.Background(), &TestSpan{Operation: "golang.org/pkg/testing.test", Resource: "suite.TestX", Type: "test", Start: start,
		Constant: constant, Common: commonOptions, Metrics: metrics, Suite: suite, NameKey: "test.name", Name: "TestX", Service: service})
	for _, span := range []*Span{fromOptions, fromTestSpan} {
		span.SetStringTag("test_session_id", "11")
		span.SetStringTag("test_module_id", "22")
		span.SetStringTag("test_suite_id", "33")
		span.SetMetric("test.source.start", 5)
		span.FinishWithTags(map[string]any{"test.status": "pass"}, FinishTime(start.Add(time.Second)))
	}
	if fromOptions.event.Content.Meta.list != nil && cap(fromOptions.event.Content.Meta.list) != cap(fromTestSpan.event.Content.Meta.list) {
		t.Fatalf("reserved %d metadata entries, the option list %d", cap(fromTestSpan.event.Content.Meta.list), cap(fromOptions.event.Content.Meta.list))
	}
	return fromOptions, fromTestSpan
}

// encodeWithIdentityOf encodes event with the random identity of another.
func encodeWithIdentityOf(t *testing.T, event, identity ciEvent) []byte {
	t.Helper()
	tid, _ := identity.Content.Meta.get("_dd.p.tid")
	event.Content.SpanID, event.Content.TraceID = identity.Content.SpanID, identity.Content.TraceID
	event.Content.Meta = metaSet{event.Content.Meta.clone(0)} // In order.
	event.Content.Meta.set("_dd.p.tid", tid)
	var raw bytes.Buffer
	if err := msgp.Encode(&raw, &event); err != nil {
		t.Fatal(err)
	}
	return raw.Bytes()
}

// A test event that StartTestSpan starts encodes to the bytes of the one that
// the equivalent option list starts, apart from its random identity, with or
// without a service. The wire contract does not change.
func TestStartTestSpanEncodesAsOptions(t *testing.T) {
	for _, service := range []string{"", "suite-service"} {
		fromOptions, fromTestSpan := startTestSpanBothWays(t, map[string]string{"team": "a"}, map[string]float64{"_dd.host.vcpu_count": 32}, service)
		want := encodeWithIdentityOf(t, fromOptions.event, fromOptions.event)
		if got := encodeWithIdentityOf(t, fromTestSpan.event, fromOptions.event); !bytes.Equal(got, want) {
			t.Fatalf("service %q: StartTestSpan encodes\n%x\nthe option list\n%x", service, got, want)
		}
	}
}

// With several metrics, which both ways apply in map order, the events hold
// the same entries, including where metrics and text tags replace each other
// and a suite tag replaces a client tag.
func TestStartTestSpanMatchesOptions(t *testing.T) {
	fromOptions, fromTestSpan := startTestSpanBothWays(t, map[string]string{"team": "a", "test.suite": "client"},
		map[string]float64{"_dd.host.vcpu_count": 32, "manual.keep": 1, "from.common": 3, "from.suite": 4}, "")
	if v, _ := fromOptions.Metric("from.common"); v != 3 {
		t.Fatal("the fixture lost the metric that replaces a common tag")
	}
	if v, _ := fromOptions.Meta("from.suite"); v != "text" {
		t.Fatal("the fixture lost the suite tag that replaces a metric")
	}
	a, b := fromOptions.event.Content, fromTestSpan.event.Content
	b.SpanID, b.TraceID = a.SpanID, a.TraceID
	a.Meta.delete("_dd.p.tid")
	b.Meta.delete("_dd.p.tid")
	if !sameContent(a, b) || fromOptions.event.Type != fromTestSpan.event.Type || fromOptions.event.Version != fromTestSpan.event.Version {
		t.Fatalf("option list %+v\nStartTestSpan %+v", a, b)
	}
}
