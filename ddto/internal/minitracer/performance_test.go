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
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/telemetry"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/telemetry/telemetrytest"
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
					for i := 0; i < tags; i++ {
						options = append(options, Tag(fmt.Sprintf("custom.%d", i), "value"))
					}
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						span, _ := newSpan(nil, context.Background(), "testing.test", options...)
						span.Finish()
					}
				})
			}
		})
	}
}

// benchmarkTelemetry starts a telemetry client, as a test binary does. Before
// startup, every counter submission allocates an entry to replay later.
func benchmarkTelemetry(b *testing.B) {
	b.Cleanup(telemetry.MockClient(&telemetrytest.RecordClient{}))
}

func BenchmarkEventLifecycle(b *testing.B) {
	benchmarkTelemetry(b)
	for _, gzip := range []bool{false, true} {
		b.Run(fmt.Sprintf("gzip=%t", gzip), func(b *testing.B) {
			tags := make(map[string]string, 12)
			for i := 0; i < 12; i++ {
				tags[fmt.Sprintf("ci.tag.%d", i)] = "representative-static-ci-value"
			}
			c, err := New(Config{Service: "fixture", Tags: tags, Transport: citransport.Config{Endpoint: "http://diagnostic.invalid", Agentless: gzip, APIKey: "fake-key", HTTPClient: &http.Client{Transport: benchmarkTransport{}}}})
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
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

// testEventCommonTags are the CI, Git, OS and runtime tags of a Git checkout,
// which test events share through payload metadata.
var testEventCommonTags = map[string]string{
	"ci.workspace_path":          "/home/runner/work/project/project",
	"git.branch":                 "main",
	"git.commit.author.date":     "2026-10-16T10:00:00+02:00",
	"git.commit.author.email":    "author@example.com",
	"git.commit.author.name":     "Author",
	"git.commit.committer.date":  "2026-10-16T10:00:00+02:00",
	"git.commit.committer.email": "author@example.com",
	"git.commit.committer.name":  "Author",
	"git.commit.message":         "Change the project",
	"git.commit.sha":             "4880210c4b0d5b5e8c0f0f6c8e7a4f3f2b1a0c9d",
	"git.repository_url":         "https://github.com/example/project.git",
	"os.architecture":            "amd64",
	"os.platform":                "linux",
	"os.version":                 "6.8.0",
	"runtime.name":               "go",
	"runtime.version":            "go1.27.0",
}

// BenchmarkTestEventLifecycle starts, tags, finishes and delivers events shaped
// like an instrumented binary's test events: the integrations' 27 start options
// in their order, the tags set while the test runs, then 22 metadata entries and
// 3 metrics, with the CI tags in a shared snapshot. Run it with GOMAXPROCS=1 to
// include delivery in ns/op.
func BenchmarkTestEventLifecycle(b *testing.B) {
	benchmarkTelemetry(b)
	c, err := New(Config{Service: "project", Env: "ci", Metadata: map[string]map[string]string{"test": {"test_session.name": "go test ./..."}},
		Transport: citransport.Config{Endpoint: "http://diagnostic.invalid", HTTPClient: &http.Client{Transport: benchmarkTransport{}}}})
	if err != nil {
		b.Fatal(err)
	}
	common := NewCommonTags(testEventCommonTags)
	// The options that the integrations build once per process, session,
	// module or suite. Module tags repeat three of the session's.
	shared := []StartSpanOption{
		Tag("_dd.origin", "ciapp-test"), Tag("manual.keep", true),
		Tag("_dd.library_capabilities.auto_test_retries", "1"), Tag("_dd.library_capabilities.coverage_report_upload", "1"),
		Tag("_dd.library_capabilities.early_flake_detection", "1"), Tag("_dd.library_capabilities.test_impact_analysis", "1"),
		Tag("_dd.library_capabilities.test_management.attempt_to_fix", "5"), Tag("_dd.library_capabilities.test_management.disable", "1"),
		Tag("_dd.library_capabilities.test_management.quarantine", "1"), Tag("_dd.test.is_user_provided_service", "true"),
		common.Option(), Tag("_dd.host.vcpu_count", float64(32)),
		Tag("test.type", "test"), Tag("test.command", "go test ./..."), Tag("test.working_directory", "."),
		Tag("test.framework", "golang.org/pkg/testing"), Tag("test.framework_version", "go1.27.0"),
		Tag("test.type", "test"), Tag("test.module", "example.com/project"),
		Tag("test.framework", "golang.org/pkg/testing"), Tag("test.framework_version", "go1.27.0"),
		Tag("test.suite", "project_test.go"),
	}
	finishTags := map[string]any{"test.final_status": "pass"}
	var options []StartSpanOption
	test := func() *Span {
		start := time.Now()
		// The integrations build each test's option slice.
		options = append(make([]StartSpanOption, 0, 27), ResourceName("project_test.go.TestProject"), SpanType("test"), StartTime(start))
		options = append(options, shared...)
		options = append(options, Tag("test.name", "TestProject"), Service("project"))
		s, _ := c.StartSpan(context.Background(), "golang.org/pkg/testing.test", options...)
		s.SetTag("test_session_id", "8160361045226661673")
		s.SetTag("test_module_id", "9051586845277556044")
		s.SetTag("test_suite_id", "7700980063684605712")
		s.SetTag("test.source.file", "project_test.go")
		s.SetTag("test.source.start", 5)
		s.SetTag("test.source.end", 9)
		s.SetTag("test.status", "pass")
		s.FinishWithTags(finishTags, FinishTime(start))
		return s
	}
	if s := test(); len(options) != 27 || s.event.Content.Meta.len() != 22 || s.event.Content.Metrics.len() != 3 {
		b.Fatalf("event shape: %d options, %d metadata entries, %d metrics", len(options), s.event.Content.Meta.len(), s.event.Content.Metrics.len())
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		test()
	}
	if err := c.Close(context.Background()); err != nil {
		b.Fatal(err)
	}
	b.StopTimer()
	if c.DroppedEvents() != 0 {
		b.Fatal("events dropped")
	}
}
