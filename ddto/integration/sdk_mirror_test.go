package integration

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// By default an SDK span under a test joins the test's trace, propagates the
// ciapp-test origin and reaches Test Optimization only, through its copy.
// DD_CIVISIBILITY_APM_SPANS_ENABLED=true keeps the SDK's trace and APM
// delivery, with independent copies.
func TestMiniSDKSpanMirror(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	source := `package fixture_test

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/ddtest/ddto/propagation"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSDKMirror(t *testing.T) {
	if err := tracer.Start(tracer.WithAgentAddr(os.Getenv("POC_APM_ADDR")), tracer.WithLogStartup(false)); err != nil {
		t.Fatal(err)
	}
	defer tracer.Stop()
	apmSpans, _ := strconv.ParseBool(os.Getenv("DD_CIVISIBILITY_APM_SPANS_ENABLED"))
	test, ok := propagation.FromContext(t.Context())
	if !ok {
		t.Fatal("no test identity")
	}
	started := time.Unix(1700000000, 0)
	root, ctx := tracer.StartSpanFromContext(t.Context(), "sdk.root", tracer.WithSpanID(101), tracer.StartTime(started))
	child, childCtx := tracer.StartSpanFromContext(ctx, "sdk.child", tracer.WithSpanID(202), tracer.StartTime(started))
	headers := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { headers <- r.Header.Clone() }))
	defer server.Close()
	request, err := http.NewRequestWithContext(childCtx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := tracer.Inject(child.Context(), tracer.HTTPHeadersCarrier(request.Header)); err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	received := <-headers
	portable, ok := propagation.FromContext(childCtx)
	if apmSpans {
		if received.Get("x-datadog-parent-id") != "202" || received.Get("x-datadog-trace-id") != "101" || received.Get("x-datadog-origin") != "" {
			t.Fatal("APM propagation changed", received)
		}
		if !ok || portable.SpanID == 202 || portable.TraceID == child.Context().TraceIDBytes() {
			t.Fatal("copy identity was not independent")
		}
	} else {
		lower := strconv.FormatUint(binary.BigEndian.Uint64(test.TraceID[8:]), 10)
		traceparent := fmt.Sprintf("00-%s-%016x-01", hex.EncodeToString(test.TraceID[:]), 202)
		tags := received.Get("x-datadog-tags")
		if received.Get("x-datadog-parent-id") != "202" || received.Get("x-datadog-trace-id") != lower || received.Get("x-datadog-origin") != "ciapp-test" ||
			received.Get("x-datadog-sampling-priority") != "1" || received.Get("traceparent") != traceparent ||
			!strings.Contains(tags, "_dd.p.tid="+hex.EncodeToString(test.TraceID[:8])) || !strings.Contains(tags, "_dd.p.dm=-4") {
			t.Fatal("the server did not receive the test's trace", received)
		}
		if !ok || portable.SpanID != 202 || portable.TraceID != test.TraceID || child.Context().TraceIDBytes() != test.TraceID {
			t.Fatal("copy identity differs from the SDK's", portable)
		}
	}
	child.SetOperationName("sdk.child.final")
	child.SetTag("resource.name", "final resource")
	child.SetTag("custom.tag", "final value")
	child.SetTag("custom.metric", 42)
	child.SetTag("_dd.parent_id", "0123456789abcdef")
	child.Finish(tracer.FinishTime(started.Add(time.Second)), tracer.WithError(errors.New("operation error")))
	child.Finish()
	root.Finish(tracer.FinishTime(started.Add(2 * time.Second)))
	outside, _ := tracer.StartSpanFromContext(context.Background(), "sdk.outside", tracer.WithSpanID(303))
	outside.Finish()
	tracer.Flush()
}
`
	if err := os.WriteFile(filepath.Join(dir, "sdk_mirror_test.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), executableName("mirror.test"))
	out, stderr, code := command(t, dir, testEnv(), driver, "test", "-mod=mod", "-c", "-o", bin, ".")
	if code != 0 {
		t.Fatal(out, stderr)
	}
	if strings.Contains(out+stderr, "also go to APM") {
		t.Fatal("the pinned SDK lacks adoption's hooks", out, stderr)
	}
	for _, apmSpans := range []bool{false, true} {
		for _, deferred := range []bool{false, true} {
			t.Run(fmt.Sprintf("apm=%t/deferred=%t", apmSpans, deferred), func(t *testing.T) {
				events, apm := runSDKMirror(t, dir, bin, "TestSDKMirror", deferred, true, sdkMirrorMode(apmSpans))
				want := []uint64{303}
				if apmSpans {
					want = []uint64{101, 202, 303}
				}
				if len(apm) != len(want) {
					t.Fatalf("original APM spans: %v", apm)
				}
				for _, span := range apm {
					if !slices.Contains(want, span["span_id"].(uint64)) {
						t.Fatalf("SDK span under a test sent to APM: %v", span)
					}
					wantParent := uint64(0)
					if span["span_id"] == uint64(202) {
						wantParent = 101
					}
					if parent, _ := span["parent_id"].(uint64); parent != wantParent {
						t.Fatalf("SDK parent changed: %v", span)
					}
					wantTrace := span["span_id"]
					if span["span_id"] == uint64(202) {
						wantTrace = uint64(101)
					}
					if span["trace_id"] != wantTrace {
						t.Fatalf("SDK trace changed: %v", span)
					}
				}
				counts, err := countCIEvents(events)
				if err != nil || counts != (eventCounts{Sessions: 1, Modules: 1, Suites: 1, Tests: 1, Spans: 2}) {
					t.Fatalf("CI copies: %+v %v", counts, err)
				}
				if err = validateEventGraph(events); err != nil {
					t.Fatal(err)
				}
				contents := map[string]map[string]any{}
				var testSpan map[string]any
				for _, event := range events {
					c := event["content"].(map[string]any)
					if event["type"] == "test" {
						testSpan = c
					}
					if event["type"] == "span" {
						contents[c["name"].(string)] = c
					}
				}
				root, child := contents["sdk.root"], contents["sdk.child.final"]
				if root == nil || child == nil || root["parent_id"] != testSpan["span_id"] || child["parent_id"] != root["span_id"] || root["trace_id"] != testSpan["trace_id"] || child["trace_id"] != testSpan["trace_id"] {
					t.Fatalf("copy hierarchy: root=%v child=%v test=%v", root, child, testSpan)
				}
				meta := child["meta"].(map[string]any)
				reused := root["span_id"] == uint64(101) && child["span_id"] == uint64(202)
				fresh := root["span_id"] != uint64(101) && child["span_id"] != uint64(202)
				if apmSpans && !fresh || !apmSpans && !reused {
					t.Fatalf("copy IDs: root=%v child=%v", root["span_id"], child["span_id"])
				}
				if _, diagnosed := meta["apm.span_id"]; diagnosed != apmSpans {
					t.Fatalf("APM identity of the copy: %v", meta)
				}
				metrics := child["metrics"].(map[string]any)
				if child["resource"] != "final resource" || child["error"] != uint64(1) || child["duration"] != int64(1000000000) || meta["custom.tag"] != "final value" || meta["error.message"] != "operation error" || metrics["custom.metric"] != float64(42) {
					t.Fatalf("final SDK fields lost: %v", child)
				}
				if _, exists := meta["_dd.parent_id"]; exists {
					t.Fatal("CI copy contains APM reparenting metadata")
				}
				for _, original := range apm {
					if original["span_id"] == uint64(202) && original["meta"].(map[string]any)["_dd.parent_id"] != "0123456789abcdef" {
						t.Fatal("original APM reparenting metadata changed")
					}
				}
			})
		}
	}
	// A debug line explains the spans that the SDK does not send.
	_, _, output := runSDKMirrorOutput(t, dir, bin, "TestSDKMirror", false, true, "DD_TRACE_DEBUG=true")
	if !strings.Contains(output, "2 SDK spans not sent to APM: their copies report to Test Optimization") {
		t.Fatal("no debug line for the withheld spans", output)
	}
}

const sdkMirrorContextSource = `package fixture_test

import (
	"context"
	"fmt"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/stretchr/testify/suite"
	"go.uber.org/goleak"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
)

func startMirrorSDK(t *testing.T) {
	if err := tracer.Start(tracer.WithAgentAddr(os.Getenv("POC_APM_ADDR")), tracer.WithLogStartup(false), tracer.WithSpanPool(true)); err != nil {
		t.Fatal(err)
	}
}
func TestSDKMirrorScopes(t *testing.T) {
	startMirrorSDK(t)
	t.Cleanup(tracer.Stop)
	for i := range 4 {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			t.Parallel()
			native := t.Context()
			ctx := context.WithValue(native, "client-value", i)
			t.Cleanup(func() {
				if native.Err() != context.Canceled {
					t.Error("native cancellation lost")
				}
			})
			root, next := tracer.StartSpanFromContext(ctx, "scope.root", tracer.WithSpanID(uint64(1000+i)), tracer.Tag("scope.test", t.Name()))
			if next.Value("client-value") != i {
				t.Fatal("client context value lost")
			}
			active, ok := tracer.SpanFromContext(next)
			if !ok || active != root {
				t.Fatal("SDK context changed")
			}
			child := root.StartChild("scope.child", tracer.WithSpanID(uint64(2000+i)), tracer.Tag("scope.test", t.Name()))
			explicit := tracer.StartSpan("scope.explicit", tracer.ChildOf(root.Context()), tracer.WithSpanID(uint64(3000+i)), tracer.Tag("scope.test", t.Name()))
			moved := tracer.ContextWithSpan(context.Background(), root)
			detached, _ := tracer.StartSpanFromContext(tracer.ContextWithSpan(moved, nil), "scope.detached", tracer.WithSpanID(uint64(4000+i)), tracer.Tag("scope.test", t.Name()))
			var wg sync.WaitGroup
			for range 8 {
				wg.Go(func() { child.Finish() })
			}
			wg.Wait()
			explicit.Finish()
			detached.Finish()
			root.Finish()
		})
	}
}
func TestSDKMirrorPool(t *testing.T) {
	startMirrorSDK(t)
	defer tracer.Stop()
	root, ctx := tracer.StartSpanFromContext(t.Context(), "pool.root", tracer.WithSpanID(5001), tracer.Tag("pool.retained", "original"))
	root.Finish()
	tracer.Flush()
	for i := range 32 {
		outside := tracer.StartSpan("pool.outside", tracer.WithSpanID(uint64(6000+i)), tracer.Tag("pool.retained", "new"))
		outside.Finish()
	}
	child, _ := tracer.StartSpanFromContext(ctx, "pool.child", tracer.WithSpanID(5002))
	child.Finish()
	tracer.Flush()
}

var mirrorAttempt atomic.Int32

func TestSDKMirrorRetry(t *testing.T) {
	startMirrorSDK(t)
	defer tracer.Stop()
	span, _ := tracer.StartSpanFromContext(t.Context(), "retry.copy", tracer.Tag("retry.attempt", mirrorAttempt.Add(1)))
	span.Finish()
	tracer.Flush()
	if mirrorAttempt.Load() == 1 {
		t.Fatal("retry fixture")
	}
}
func TestSDKMirrorGoleak(t *testing.T) {
	// Verify Mini's internal goroutines before starting the independent SDK.
	goleak.VerifyNone(t)
	startMirrorSDK(t)
	defer tracer.Stop()
	span, _ := tracer.StartSpanFromContext(t.Context(), "goleak.copy")
	span.Finish()
}

type mirrorSuite struct{ suite.Suite }

func (s *mirrorSuite) TestCopy() {
	span, _ := tracer.StartSpanFromContext(s.T().Context(), "testify.copy")
	span.Finish()
}
func TestSDKMirrorSuite(t *testing.T) {
	startMirrorSDK(t)
	defer tracer.Stop()
	suite.Run(t, new(mirrorSuite))
}
func FuzzSDKMirror(f *testing.F) {
	if err := tracer.Start(tracer.WithAgentAddr(os.Getenv("POC_APM_ADDR")), tracer.WithLogStartup(false)); err != nil {
		f.Fatal(err)
	}
	defer tracer.Stop()
	f.Add(1)
	f.Add(2)
	f.Fuzz(func(t *testing.T, n int) {
		ctx := t.Context()
		t.Cleanup(func() {
			if ctx.Err() != context.Canceled {
				t.Error("seed cancellation lost")
			}
		})
		span, _ := tracer.StartSpanFromContext(ctx, fmt.Sprint("fuzz.copy.", n))
		span.Finish()
	})
}
// The test calls a service that starts its own SDK span from the headers it
// receives, as a remote service would.
func TestSDKMirrorHTTP(t *testing.T) {
	startMirrorSDK(t)
	defer tracer.Stop()
	origin := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin <- r.Header.Get("x-datadog-origin")
		parent, err := tracer.Extract(tracer.HTTPHeadersCarrier(r.Header))
		if err != nil {
			t.Error(err)
			return
		}
		tracer.StartSpan("http.server", tracer.ChildOf(parent)).Finish()
	}))
	defer server.Close()
	client, ctx := tracer.StartSpanFromContext(t.Context(), "http.client")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := tracer.Inject(client.Context(), tracer.HTTPHeadersCarrier(request.Header)); err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	client.Finish()
	if got := <-origin; got != os.Getenv("POC_EXPECT_ORIGIN") {
		t.Fatalf("x-datadog-origin: %q", got)
	}
	server.Close()
	tracer.Flush()
}
// A baggage-only parent leaves the SDK span a root of its own trace.
func TestSDKMirrorBaggage(t *testing.T) {
	startMirrorSDK(t)
	defer tracer.Stop()
	parent, err := tracer.Extract(tracer.TextMapCarrier{"baggage": "tenant=qa"})
	if err != nil || parent == nil {
		t.Fatal(parent, err)
	}
	span, _ := tracer.StartSpanFromContext(t.Context(), "baggage.root", tracer.ChildOf(parent), tracer.WithSpanID(7001))
	span.Finish()
	tracer.Flush()
}
func BenchmarkSDKMirror(b *testing.B) {
	if err := tracer.Start(tracer.WithAgentAddr(os.Getenv("POC_APM_ADDR")), tracer.WithLogStartup(false)); err != nil {
		b.Fatal(err)
	}
	defer tracer.Stop()
	for i := 0; i < b.N; i++ {
		span, _ := tracer.StartSpanFromContext(b.Context(), "benchmark.copy")
		span.Finish()
	}
}
`

func TestMiniSDKMirrorContextMatrix(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	if err := os.WriteFile(filepath.Join(dir, "sdk_context_test.go"), []byte(sdkMirrorContextSource), 0600); err != nil {
		t.Fatal(err)
	}
	out, stderr, code := command(t, dir, testEnv(), "go", "mod", "edit", "-require=go.uber.org/goleak@v1.3.0")
	if code != 0 {
		t.Fatal(out, stderr)
	}
	for _, sdk := range sdkRuntimeVersions(t) {
		t.Run(sdk, func(t *testing.T) {
			if sdk != sdkVersion {
				out, stderr, code = command(t, dir, testEnv(), "go", "mod", "edit", "-replace=github.com/DataDog/dd-trace-go/v2=github.com/DataDog/dd-trace-go/v2@"+sdk)
				if code != 0 {
					t.Fatal(out, stderr)
				}
			}
			builds := []struct {
				name  string
				flags []string
			}{{"plain", nil}}
			if sdk == sdkVersion {
				builds = append(builds, struct {
					name  string
					flags []string
				}{"race-cover", []string{"-race", "-covermode=atomic", "-coverpkg=./...,testing,github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"}})
				if reference := os.Getenv("ORCHESTRION_BIN"); reference != "" {
					configureReferenceFixture(t, dir)
					builds = append(builds, struct {
						name  string
						flags []string
					}{"orchestrion", []string{"-toolexec=" + quoteToolArgument(t, reference) + " toolexec"}})
				}
			}
			for _, build := range builds {
				t.Run(build.name, func(t *testing.T) {
					bin := filepath.Join(t.TempDir(), executableName("context.test"))
					args := append([]string{"test", "-mod=mod", "-c", "-o", bin}, build.flags...)
					out, stderr, code := command(t, dir, testEnv(), driver, append(args, ".")...)
					if code != 0 {
						t.Fatal(out, stderr)
					}
					if strings.Contains(out+stderr, "also go to APM") {
						t.Fatal("the SDK lacks adoption's hooks", out, stderr)
					}
					for _, deferred := range []bool{false, true} {
						for _, apmSpans := range []bool{false, true} {
							mode := sdkMirrorMode(apmSpans)
							for _, agentless := range []bool{false, true} {
								t.Run(fmt.Sprintf("deferred=%t/agentless=%t/apm=%t", deferred, agentless, apmSpans), func(t *testing.T) {
									events, apm := runSDKMirror(t, dir, bin, "TestSDKMirrorScopes", deferred, agentless, mode)
									assertSDKMirrorScopes(t, events, apm, apmSpans)
								})
							}
							// Spans outside tests still go to APM.
							sent := 32
							if apmSpans {
								sent = 34
							}
							events, apm := runSDKMirror(t, dir, bin, "TestSDKMirrorPool", deferred, true, mode)
							if len(apm) != sent {
								t.Fatalf("APM pool spans: %d", len(apm))
							}
							copies := mirrorContents(events)
							if len(copies) != 2 || copies["pool.root"]["meta"].(map[string]any)["pool.retained"] != "original" || copies["pool.child"]["parent_id"] != copies["pool.root"]["span_id"] {
								t.Fatal("SDK pool corrupted copies", copies)
							}
							assertSDKMirrorWorkloads(t, dir, bin, deferred, apmSpans)
							assertSDKMirrorHTTP(t, dir, bin, deferred, apmSpans)
							assertSDKMirrorBaggage(t, dir, bin, deferred, apmSpans)
						}
						events, _ := runSDKMirror(t, dir, bin, "TestSDKMirrorRetry", deferred, true, "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=1", "DD_CIVISIBILITY_RETRY_EXECUTION_MODE=in_process")
						counts, err := countCIEvents(events)
						if err != nil || counts.Tests != 2 || counts.Spans != 2 {
							t.Fatal("retry copies", counts, err)
						}
						tests := map[any]bool{}
						for _, event := range events {
							if event["type"] == "test" {
								tests[event["content"].(map[string]any)["span_id"]] = true
							}
						}
						parents := map[any]bool{}
						for _, event := range events {
							if event["type"] == "span" {
								parent := event["content"].(map[string]any)["parent_id"]
								if !tests[parent] {
									t.Fatal("retry copy lost its attempt")
								}
								parents[parent] = true
							}
						}
						if len(parents) != 2 {
							t.Fatal("retry copies shared a test identity")
						}
						events, _ = runSDKMirror(t, dir, bin, "TestSDKMirrorGoleak", deferred, true)
						if counts, err := countCIEvents(events); err != nil || counts.Spans != 1 {
							t.Fatal(counts, err)
						}
					}
				})
			}
		})
	}
}

// Testify, fuzz seeds and benchmarks copy their SDK spans, which go to APM
// only with DD_CIVISIBILITY_APM_SPANS_ENABLED=true.
func assertSDKMirrorWorkloads(t *testing.T, dir, bin string, deferred, apmSpans bool) {
	t.Helper()
	for _, workload := range []struct {
		name         string
		tests, spans int
	}{{"TestSDKMirrorSuite", 2, 1}, {"FuzzSDKMirror", 3, 2}, {"BenchmarkSDKMirror", 1, 1}} {
		events, apm := runSDKMirror(t, dir, bin, workload.name, deferred, true, sdkMirrorMode(apmSpans))
		sent := 0
		if apmSpans {
			sent = workload.spans
		}
		counts, err := countCIEvents(events)
		if err != nil || counts.Tests != workload.tests || counts.Spans != workload.spans || len(apm) != sent {
			t.Fatal(workload.name, counts, len(apm), err)
		}
		if err := validateEventGraph(events); err != nil {
			t.Fatal(err)
		}
	}
}

// The service that the test calls receives the test's trace and the
// ciapp-test origin: its span, sent to APM, names the client span's copy as
// its parent. The client span itself is not sent.
func assertSDKMirrorHTTP(t *testing.T, dir, bin string, deferred, apmSpans bool) {
	t.Helper()
	origin := "ciapp-test"
	if apmSpans {
		origin = ""
	}
	events, apm := runSDKMirror(t, dir, bin, "TestSDKMirrorHTTP", deferred, true, sdkMirrorMode(apmSpans), "POC_EXPECT_ORIGIN="+origin)
	var test map[string]any
	var copies []map[string]any
	for _, event := range events {
		c := event["content"].(map[string]any)
		switch event["type"] {
		case "test":
			test = c
		case "span":
			copies = append(copies, c)
		}
	}
	if test == nil || len(copies) != 1 || copies[0]["parent_id"] != test["span_id"] {
		t.Fatal("HTTP client copy", test, copies)
	}
	spans := map[string]map[string]any{}
	for _, span := range apm {
		spans[span["name"].(string)] = span
	}
	server := spans["http.server"]
	if apmSpans {
		client := spans["http.client"]
		if len(apm) != 2 || client == nil || server == nil || server["parent_id"] != client["span_id"] || server["trace_id"] != client["trace_id"] {
			t.Fatal("APM HTTP spans", apm)
		}
		return
	}
	client := copies[0]
	if len(apm) != 1 || server == nil || server["trace_id"] != test["trace_id"] || server["parent_id"] != client["span_id"] || server["meta"].(map[string]any)["_dd.origin"] != "ciapp-test" {
		t.Fatal("the server span did not join the test's trace", apm, client, test)
	}
}

// An SDK root under a baggage-only parent joins the test's trace like any
// other root.
func assertSDKMirrorBaggage(t *testing.T, dir, bin string, deferred, apmSpans bool) {
	t.Helper()
	events, apm := runSDKMirror(t, dir, bin, "TestSDKMirrorBaggage", deferred, true, sdkMirrorMode(apmSpans))
	var test map[string]any
	for _, event := range events {
		if event["type"] == "test" {
			test = event["content"].(map[string]any)
		}
	}
	copy := mirrorContents(events)["baggage.root"]
	if test == nil || copy == nil || copy["parent_id"] != test["span_id"] || copy["trace_id"] != test["trace_id"] {
		t.Fatal("baggage copy", copy, test)
	}
	if apmSpans {
		if len(apm) != 1 || apm[0]["span_id"] != uint64(7001) || copy["span_id"] == uint64(7001) {
			t.Fatal("baggage root with APM spans", apm, copy)
		}
		return
	}
	if len(apm) != 0 || copy["span_id"] != uint64(7001) {
		t.Fatal("baggage root sent to APM or not adopted", apm, copy)
	}
}

// sdkMirrorMode is the environment of a run in one of the two modes.
func sdkMirrorMode(apmSpans bool) string {
	return fmt.Sprintf("DD_CIVISIBILITY_APM_SPANS_ENABLED=%t", apmSpans)
}

func mirrorContents(events []map[string]any) map[string]map[string]any {
	result := map[string]map[string]any{}
	for _, event := range events {
		if event["type"] == "span" {
			c := event["content"].(map[string]any)
			result[c["name"].(string)] = c
		}
	}
	return result
}

func assertSDKMirrorScopes(t *testing.T, events, apm []map[string]any, apmSpans bool) {
	t.Helper()
	sent := 0
	if apmSpans {
		sent = 16
	}
	counts, err := countCIEvents(events)
	if err != nil || counts.Tests != 5 || counts.Spans != 16 || len(apm) != sent {
		t.Fatal(counts, len(apm), err)
	}
	if err := validateEventGraph(events); err != nil {
		t.Fatal(err)
	}
	tests := map[string]map[string]any{}
	spans := map[string]map[string]map[string]any{}
	for _, event := range events {
		c := event["content"].(map[string]any)
		meta := c["meta"].(map[string]any)
		if event["type"] == "test" {
			tests[meta["test.name"].(string)] = c
		}
		if event["type"] == "span" {
			name := meta["scope.test"].(string)
			if spans[name] == nil {
				spans[name] = map[string]map[string]any{}
			}
			spans[name][c["name"].(string)] = c
		}
	}
	for name, copy := range spans {
		test := tests[name]
		root := copy["scope.root"]
		if test == nil || root["parent_id"] != test["span_id"] || root["trace_id"] != test["trace_id"] {
			t.Fatal("parallel scope crossed", name, copy, test)
		}
		for _, child := range []string{"scope.child", "scope.explicit"} {
			if copy[child]["parent_id"] != root["span_id"] {
				t.Fatal("SDK child escaped test", name, child)
			}
		}
		if copy["scope.detached"]["parent_id"] != test["span_id"] {
			t.Fatal("detach lost current test")
		}
		if apmSpans {
			continue
		}
		// The copies have the SDK span IDs: 1000+i for the root, then 2000+i,
		// 3000+i and 4000+i.
		id, _ := root["span_id"].(uint64)
		for offset, name := range []string{"scope.child", "scope.explicit", "scope.detached"} {
			if id < 1000 || id >= 1004 || copy[name]["span_id"] != id+uint64(offset+1)*1000 {
				t.Fatal("copy IDs differ from the SDK's", name, copy)
			}
		}
	}
	for _, s := range apm {
		id := s["span_id"].(uint64)
		want := uint64(0)
		if s["name"] == "scope.child" || s["name"] == "scope.explicit" {
			want = 1000 + id%1000
		}
		if parent, _ := s["parent_id"].(uint64); parent != want {
			t.Fatal("original APM parent changed", s)
		}
	}
}

func runSDKMirror(t *testing.T, dir, bin, name string, deferred, agentless bool, extra ...string) ([]map[string]any, []map[string]any) {
	t.Helper()
	events, apm, _ := runSDKMirrorOutput(t, dir, bin, name, deferred, agentless, extra...)
	return events, apm
}

// runSDKMirrorOutput also returns the test binary's output.
func runSDKMirrorOutput(t *testing.T, dir, bin, name string, deferred, agentless bool, extra ...string) ([]map[string]any, []map[string]any, string) {
	t.Helper()
	receiver := &parityReceiver{side: map[string][][]byte{}, requests: map[string]int{}}
	var mu sync.Mutex
	var apm []map[string]any
	server := newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0.4/traces" {
			receiver.handler(w, r)
			return
		}
		data, err := decodedRequestBody(r)
		if err != nil {
			receiver.fail(err)
			w.WriteHeader(400)
			return
		}
		decoded, err := decodeMsgpack(data)
		if err != nil {
			receiver.fail(err)
			w.WriteHeader(400)
			return
		}
		mu.Lock()
		for _, trace := range decoded.([]any) {
			for _, span := range trace.([]any) {
				apm = append(apm, span.(map[string]any))
			}
		}
		mu.Unlock()
		fmt.Fprint(w, `{"rate_by_service":{}}`)
	}))
	defer server.Close()
	env := testEnv("DD_CIVISIBILITY_ENABLED=true", fmt.Sprintf("DD_CIVISIBILITY_AGENTLESS_ENABLED=%t", agentless), fmt.Sprintf("DD_CIVISIBILITY_DEFERRED_DELIVERY=%t", deferred), "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL, "DD_TRACE_AGENT_URL="+server.URL, "DD_API_KEY=fixture", "POC_APM_ADDR="+strings.TrimPrefix(server.URL, "http://"), "DD_TRACE_AGENT_PROTOCOL_VERSION=0.4")
	args := []string{"-test.run=^" + name + "$", "-test.parallel=4"}
	if strings.HasPrefix(name, "Benchmark") {
		args = []string{"-test.run=^$", "-test.bench=^" + name + "$", "-test.benchtime=1x"}
	}
	out, stderr, code := command(t, dir, append(env, extra...), bin, args...)
	server.Close()
	if code != 0 {
		t.Fatal(out, stderr)
	}
	receiver.mu.Lock()
	defer receiver.mu.Unlock()
	if len(receiver.failures) != 0 {
		t.Fatal(receiver.failures)
	}
	return receiver.events, apm, out + stderr
}

// Binding Mini's context to a benchmark happens outside its measurement: an
// empty benchmark reports no allocations with the SDK mirror active, as
// without ddto. With -benchtime=1x, the first run is the measured one.
func TestMiniSDKMirrorBenchmarkMeasuresNoBinding(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	source := sdkMirrorContextSource + `
func BenchmarkSDKMirrorEmpty(b *testing.B) {
	for range b.N {
	}
}

func BenchmarkSDKMirrorEmptyParent(b *testing.B) {
	b.Run("child", func(b *testing.B) {
		for range b.N {
		}
	})
}
`
	if err := os.WriteFile(filepath.Join(dir, "sdk_context_test.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	if out, stderr, code := command(t, dir, testEnv(), "go", "mod", "edit", "-require=go.uber.org/goleak@v1.3.0"); code != 0 {
		t.Fatal(out, stderr)
	}
	bin := filepath.Join(t.TempDir(), executableName("context.test"))
	if out, stderr, code := command(t, dir, testEnv(), driver, "test", "-mod=mod", "-c", "-o", bin, "."); code != 0 {
		t.Fatal(out, stderr)
	}
	receiver := &capture{}
	server := newServer(http.HandlerFunc(receiver.handler))
	defer server.Close()
	env := testEnv("DD_CIVISIBILITY_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL, "DD_TRACE_AGENT_URL="+server.URL, "DD_API_KEY=fixture")
	out, stderr, code := command(t, dir, env, bin, "-test.run=^$", "-test.bench=^BenchmarkSDKMirrorEmpty", "-test.benchtime=1x", "-test.benchmem")
	if code != 0 {
		t.Fatal(out, stderr)
	}
	lines := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "BenchmarkSDKMirrorEmpty") {
			lines++
			if !strings.Contains(line, " 0 B/op") || !strings.Contains(line, " 0 allocs/op") {
				t.Errorf("measured Mini's binding: %s", line)
			}
		}
	}
	if lines != 2 {
		t.Fatalf("benchmark lines: %d\n%s", lines, out)
	}
}
