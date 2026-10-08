package integration

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// SDK parentage must stay unchanged while independent copies reach CI intake.
func TestMiniSDKSpanMirror(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	source := `package fixture_test

import (
	"context"
	"errors"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/tonyredondo/dd-ci-testing-poc/propagation"
	"os"
	"testing"
	"time"
)

func TestSDKMirror(t *testing.T) {
	if err := tracer.Start(tracer.WithAgentAddr(os.Getenv("POC_APM_ADDR")), tracer.WithLogStartup(false)); err != nil {
		t.Fatal(err)
	}
	defer tracer.Stop()
	started := time.Unix(1700000000, 0)
	root, ctx := tracer.StartSpanFromContext(t.Context(), "sdk.root", tracer.WithSpanID(101), tracer.StartTime(started))
	child, childCtx := tracer.StartSpanFromContext(ctx, "sdk.child", tracer.WithSpanID(202), tracer.StartTime(started))
	carrier := tracer.TextMapCarrier{}
	if err := tracer.Inject(child.Context(), carrier); err != nil {
		t.Fatal(err)
	}
	if carrier["x-datadog-parent-id"] != "202" || carrier["x-datadog-trace-id"] != "101" {
		t.Fatal("APM propagation changed", carrier)
	}
	portable, ok := propagation.FromContext(childCtx)
	if !ok || portable.SpanID == 202 || portable.TraceID == child.Context().TraceIDBytes() {
		t.Fatal("copy identity was not independent")
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
	for _, deferred := range []bool{false, true} {
		t.Run(fmt.Sprintf("deferred=%t", deferred), func(t *testing.T) {
			events, apm := runSDKMirror(t, dir, bin, "TestSDKMirror", deferred, true)
			if len(apm) != 3 {
				t.Fatalf("original APM spans: %v", apm)
			}
			for _, span := range apm {
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
			if root["span_id"] == uint64(101) || child["span_id"] == uint64(202) {
				t.Fatal("copies reused SDK identities")
			}
			meta := child["meta"].(map[string]any)
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

const sdkMirrorContextSource = `package fixture_test

import (
	"context"
	"fmt"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/stretchr/testify/suite"
	"go.uber.org/goleak"
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
	for _, sdk := range []string{sdkVersion, "v2.11.0-rc.2"} {
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
					for _, deferred := range []bool{false, true} {
						for _, agentless := range []bool{false, true} {
							t.Run(fmt.Sprintf("deferred=%t/agentless=%t", deferred, agentless), func(t *testing.T) {
								events, apm := runSDKMirror(t, dir, bin, "TestSDKMirrorScopes", deferred, agentless)
								assertSDKMirrorScopes(t, events, apm)
							})
						}
						events, apm := runSDKMirror(t, dir, bin, "TestSDKMirrorPool", deferred, true)
						if len(apm) != 34 {
							t.Fatalf("APM pool spans: %d", len(apm))
						}
						copies := mirrorContents(events)
						if len(copies) != 2 || copies["pool.root"]["meta"].(map[string]any)["pool.retained"] != "original" || copies["pool.child"]["parent_id"] != copies["pool.root"]["span_id"] {
							t.Fatal("SDK pool corrupted copies", copies)
						}
						events, _ = runSDKMirror(t, dir, bin, "TestSDKMirrorRetry", deferred, true, "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=1", "DD_CIVISIBILITY_RETRY_EXECUTION_MODE=in_process")
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
						for _, workload := range []struct {
							name         string
							tests, spans int
						}{{"TestSDKMirrorSuite", 2, 1}, {"FuzzSDKMirror", 3, 2}, {"BenchmarkSDKMirror", 1, 1}} {
							events, apm = runSDKMirror(t, dir, bin, workload.name, deferred, true)
							counts, err := countCIEvents(events)
							if err != nil || counts.Tests != workload.tests || counts.Spans != workload.spans || len(apm) != workload.spans {
								t.Fatal(workload.name, counts, len(apm), err)
							}
							if err := validateEventGraph(events); err != nil {
								t.Fatal(err)
							}
						}
					}
				})
			}
		})
	}
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

func assertSDKMirrorScopes(t *testing.T, events, apm []map[string]any) {
	t.Helper()
	counts, err := countCIEvents(events)
	if err != nil || counts.Tests != 5 || counts.Spans != 16 || len(apm) != 16 {
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
	receiver := &parityReceiver{side: map[string][][]byte{}, requests: map[string]int{}}
	var mu sync.Mutex
	var apm []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	return receiver.events, apm
}
