package citransport

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/telemetry"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/telemetry/telemetrytest"
)

type diagnosticRoundTripper func(*http.Request) (*http.Response, error)

func (f diagnosticRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func recordTransportDebug(t *testing.T, enabled bool) *log.RecordLogger {
	t.Helper()
	recorder := &log.RecordLogger{}
	t.Cleanup(log.UseLogger(recorder))
	level := log.GetLevel()
	t.Cleanup(func() { log.SetLevel(level) })
	log.SetLevel(log.LevelWarn)
	if enabled {
		log.SetLevel(log.LevelDebug)
	}
	return recorder
}

func TestSendDebugDiagnostics(t *testing.T) {
	for _, debug := range []bool{false, true} {
		for _, agentless := range []bool{false, true} {
			for _, tc := range []struct {
				name   string
				codes  []int
				status string
			}{
				{"success", []int{202}, "ok"},
				{"transient", []int{503, 202}, "ok"},
				{"retry-after", []int{429, 202}, "ok"},
				{"exhausted", []int{503, 503}, "error"},
				{"permanent", []int{401}, "error"},
				{"network", []int{0, 0}, "error"},
			} {
				t.Run(fmt.Sprintf("debug=%t/agentless=%t/%s", debug, agentless, tc.name), func(t *testing.T) {
					recorder := recordTransportDebug(t, debug)
					calls := 0
					client := &http.Client{Transport: diagnosticRoundTripper(func(r *http.Request) (*http.Response, error) {
						defer r.Body.Close()
						var reader io.Reader = r.Body
						if agentless {
							gz, err := gzip.NewReader(r.Body)
							if err != nil {
								t.Fatal(err)
							}
							defer gz.Close()
							reader = gz
							if r.Header.Get("dd-api-key") != "private-key" {
								t.Error("lost API key")
							}
						}
						body, err := io.ReadAll(reader)
						if err != nil || string(body) != "private-payload" {
							t.Error("changed request payload", err)
						}
						code := tc.codes[calls]
						calls++
						if code == 0 {
							return nil, errors.New("private-network-error")
						}
						return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader("private-response")), Header: http.Header{"Retry-After": {"0"}}}, nil
					})}
					transport, err := New(Config{Endpoint: "http://fixture.invalid/api/v2/citestcycle?token=private-query", Agentless: agentless, APIKey: "private-key", HTTPClient: client, Attempts: len(tc.codes), RetryDelay: time.Millisecond})
					if err != nil {
						t.Fatal(err)
					}
					err = transport.Send(context.Background(), []byte("private-payload"))
					if (err == nil) != (tc.status == "ok") || calls != len(tc.codes) {
						t.Fatalf("err=%v calls=%d", err, calls)
					}
					lines := strings.Join(recorder.Logs(), "\n")
					if !debug {
						if strings.Contains(lines, "test-cycle:") {
							t.Fatal("debug-disabled delivery logged diagnostics")
						}
						return
					}
					if strings.Count(lines, "test-cycle: request finished") != calls {
						t.Fatalf("wrong attempt accounting: %s", lines)
					}
					for i, code := range tc.codes {
						want := fmt.Sprintf("status_code=%d network_error=%t retry=%t", code, code == 0, i+1 < len(tc.codes))
						if !strings.Contains(lines, want) {
							t.Errorf("missing %q: %s", want, lines)
						}
					}
					for _, want := range []string{"host=fixture.invalid path=/api/v2/citestcycle", "duration=", fmt.Sprintf("attempts=%d status=%s", calls, tc.status), fmt.Sprintf("gzip=%t", agentless)} {
						if !strings.Contains(lines, want) {
							t.Errorf("missing %q: %s", want, lines)
						}
					}
					if strings.Contains(lines, "private-") || strings.Contains(lines, "token=") {
						t.Fatalf("diagnostics leaked request or error data: %s", lines)
					}
				})
			}
		}
	}
}

type diagnosticResponseBody struct {
	entered chan struct{}
	release chan struct{}
	closed  bool
}

func (b *diagnosticResponseBody) Read([]byte) (int, error) {
	close(b.entered)
	<-b.release
	return 0, io.EOF
}
func (b *diagnosticResponseBody) Close() error { b.closed = true; return nil }

func TestRequestDiagnosticWaitsForResponseClose(t *testing.T) {
	recorder := recordTransportDebug(t, true)
	metrics := &telemetrytest.RecordClient{}
	t.Cleanup(telemetry.MockClient(metrics))
	body := &diagnosticResponseBody{entered: make(chan struct{}), release: make(chan struct{})}
	client := &http.Client{Transport: diagnosticRoundTripper(func(r *http.Request) (*http.Response, error) {
		_ = r.Body.Close()
		return &http.Response{StatusCode: 202, Body: body}, nil
	})}
	transport, err := New(Config{Endpoint: "http://fixture.invalid/api/v2/citestcycle", HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- transport.Send(context.Background(), []byte("payload")) }()
	released, joined := false, false
	defer func() {
		if !released {
			close(body.release)
		}
		if !joined {
			<-done
		}
	}()
	select {
	case <-body.entered:
	case <-time.After(time.Second):
		t.Fatal("response consumption never began")
	}
	heldSince := time.Now()
	// The body handshake happens after metric submission. Until release there
	// are no further metric writes, so inspecting this recorder map is safe.
	var metric *telemetrytest.RecordMetricHandle
	for key, handle := range metrics.Metrics {
		if key.Name == "endpoint_payload.requests_ms" && key.Namespace == telemetry.NamespaceCIVisibility {
			metric = handle
		}
	}
	if metric == nil {
		t.Fatal("request duration metric missing before EOF")
	}
	headerDuration := metric.Get()
	for _, line := range recorder.Logs() {
		if strings.Contains(line, "finished") {
			t.Fatalf("completion logged before response EOF/close: %s", line)
		}
	}
	heldFor := time.Since(heldSince)
	close(body.release)
	released = true
	err = <-done
	joined = true
	if err != nil || !body.closed {
		t.Fatalf("response close incomplete: err=%v closed=%t", err, body.closed)
	}
	if metric.Get() != headerDuration {
		t.Fatal("debug timing changed header-latency metric")
	}
	lines := recorder.Logs()
	finished := 0
	for _, line := range lines {
		if !strings.Contains(line, "finished") {
			continue
		}
		finished++
		for _, field := range strings.Fields(line) {
			if strings.HasPrefix(field, "duration=") {
				duration, err := time.ParseDuration(strings.TrimPrefix(field, "duration="))
				if err != nil || duration < heldFor {
					t.Errorf("completion duration excludes response wait: %s", line)
				}
			}
		}
	}
	if finished != 2 {
		t.Fatalf("missing request/payload completion: %v", lines)
	}
}

func TestSendDebugEarlyErrors(t *testing.T) {
	recorder := recordTransportDebug(t, true)
	transport, err := New(Config{Endpoint: "http://fixture.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := transport.Send(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := transport.Send(context.Background(), make([]byte, TestCycleMaxPayloadBytes+1)); err == nil {
		t.Fatal("oversized payload accepted")
	}
	lines := strings.Join(recorder.Logs(), "\n")
	for _, want := range []string{"attempts=0 status=canceled", "attempts=0 status=error"} {
		if !strings.Contains(lines, want) {
			t.Errorf("missing %q: %s", want, lines)
		}
	}
	if strings.Contains(lines, "request finished") {
		t.Fatal("early failure reported an HTTP attempt")
	}
}
