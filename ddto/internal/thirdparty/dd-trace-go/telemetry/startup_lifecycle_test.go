package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/telemetry/internal"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/telemetry/internal/transport"
)

type startupEnvelope struct {
	Type    string          `json:"request_type"`
	Seq     int             `json:"seq_id"`
	Payload json.RawMessage `json:"payload"`
}

// CI initialization calls StartApp before the first test. Its flush must finish
// there, in both delivery modes, so no test waits for or overlaps startup HTTP.
// Each subprocess owns a fresh global client and enabled flag.
func TestStartupTelemetrySendsBeforeTests(t *testing.T) {
	const helperEnv = "DDTO_TELEMETRY_STARTUP_HELPER"
	if os.Getenv(helperEnv) == "" {
		for _, mode := range []string{"false", "true"} {
			for _, scenario := range []string{"startup", "retry", "concurrent-close", "panic"} {
				t.Run("deferred="+mode+"/"+scenario, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
					defer cancel()
					cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStartupTelemetrySendsBeforeTests$", "-test.count=1")
					cmd.Env = append(os.Environ(), helperEnv+"="+scenario, "DD_INSTRUMENTATION_TELEMETRY_ENABLED=true", "DD_CIVISIBILITY_DEFERRED_DELIVERY="+mode)
					if out, err := cmd.CombinedOutput(); err != nil {
						t.Fatalf("startup lifecycle: %v\n%s", err, out)
					}
				})
			}
		}
		return
	}
	scenario := os.Getenv(helperEnv)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	var mu sync.Mutex
	var bodies []startupEnvelope
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var body startupEnvelope
		if err := json.Unmarshal(data, &body); err != nil {
			t.Errorf("telemetry body: %v", err)
		}
		mu.Lock()
		bodies = append(bodies, body)
		n := len(bodies)
		mu.Unlock()
		if n == 1 {
			close(entered)
			<-release
		}
		if scenario == "retry" && n <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	defer unblock()
	httpClient := server.Client()
	httpClient.Timeout = 2 * time.Second
	next, err := NewClient("startup-test", "", "", ClientConfig{
		AgentURL: server.URL, AgentlessURL: server.URL, APIKey: "startup-test-placeholder",
		HTTPClient: httpClient, FlushInterval: internal.Range[time.Duration]{Min: time.Hour, Max: time.Hour},
	})
	if err != nil {
		t.Fatal(err)
	}
	var panicCalls int
	if scenario == "panic" {
		next.AddFlushTicker(func(Client) { panicCalls++; panic("startup callback") })
	}
	next.RegisterAppConfig("startup-config", "initial", OriginEnvVar)
	started := make(chan struct{})
	go func() { StartApp(next); close(started) }()
	if scenario == "panic" {
		<-started
		if !Disabled() || panicCalls != 1 {
			t.Fatalf("failed startup left telemetry enabled: disabled=%t calls=%d", Disabled(), panicCalls)
		}
		StopApp()
		if GlobalClient() != nil {
			t.Fatal("failed startup remained installed after close")
		}
		select {
		case <-entered:
			t.Fatal("failed startup attempted HTTP")
		default:
		}
		return
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("StartApp did not send startup")
	}
	select {
	case <-started:
		t.Fatal("StartApp returned before its startup request completed")
	case <-time.After(10 * time.Millisecond):
	}
	closing := make(chan struct{})
	if scenario == "concurrent-close" {
		go func() { StopApp(); close(closing) }()
		select {
		case <-closing:
			t.Fatal("StopApp returned during startup HTTP")
		case <-time.After(10 * time.Millisecond):
		}
	}
	unblock()
	<-started
	if scenario == "concurrent-close" {
		<-closing
	} else {
		StopApp()
	}
	if GlobalClient() != nil {
		t.Fatal("session close left the client installed")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) < 2 || bodies[0].Type != "app-started" {
		t.Fatalf("startup missing or out of order: %+v", bodies)
	}
	var startup transport.AppStarted
	if err := json.Unmarshal(bodies[0].Payload, &startup); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, config := range startup.Configuration {
		found = found || config.Name == "startup-config" && config.Value == "initial"
	}
	if !found {
		t.Fatalf("startup lost its configuration: %+v", startup.Configuration)
	}
	successes := 0
	for i, body := range bodies {
		if body.Type == "app-started" && (scenario != "retry" || i >= 2) {
			successes++
		}
		if i > 0 && (body.Seq < bodies[i-1].Seq || (body.Seq == bodies[i-1].Seq && body.Type != "app-started")) {
			t.Fatalf("sequence went backwards: %+v", bodies)
		}
	}
	if successes != 1 {
		t.Fatalf("startup successfully delivered %d times: %+v", successes, bodies)
	}
	// This client has metrics/logs disabled, so closing is a standalone event.
	if bodies[len(bodies)-1].Type != "app-closing" {
		t.Fatalf("session close missing: %+v", bodies)
	}
}

// Initial metrics must retain their values and timestamps across later submissions.
func TestStartupFlushRetainsMetricsUntilNextFlush(t *testing.T) {
	for _, outcome := range []string{"success", "retry", "oversized"} {
		t.Run(outcome, func(t *testing.T) {
			var mu sync.Mutex
			var requests []startupEnvelope
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var envelope startupEnvelope
				if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
					t.Error(err)
				}
				mu.Lock()
				requests = append(requests, envelope)
				n := len(requests)
				mu.Unlock()
				if n <= 2 && outcome == "retry" {
					w.WriteHeader(503)
				} else if envelope.Type == "app-started" && outcome == "oversized" {
					w.WriteHeader(413)
				} else {
					w.WriteHeader(202)
				}
			}))
			defer server.Close()
			c, err := NewClient("snapshot", "", "", ClientConfig{HTTPClient: server.Client(), AgentURL: server.URL, AgentlessURL: server.URL, APIKey: "placeholder", MetricsEnabled: true, Debug: true})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			native := c.(*client)
			counter := native.Count(transport.NamespaceCIVisibility, "startup.snapshot", nil)
			counter.Submit(7)
			native.AppStart()
			native.flushStartup()
			mu.Lock()
			firstCount := len(requests)
			firstType := requests[0].Type
			mu.Unlock()
			if firstType != "app-started" || firstCount > 2 {
				t.Fatalf("startup sent metrics: %+v", requests)
			}
			snapshots := native.payloadQueue.Flush()
			if len(snapshots) == 0 {
				t.Fatal("startup did not retain any snapshots")
			}
			expected, err := json.Marshal(snapshots[len(snapshots)-1])
			if err != nil {
				t.Fatal(err)
			}
			native.payloadQueue.Enqueue(snapshots...)
			counter.Submit(3)
			native.AppStop()
			native.Flush()
			mu.Lock()
			defer mu.Unlock()
			found := false
			for _, envelope := range requests[firstCount:] {
				if bytes.Equal(envelope.Payload, expected) {
					found = true
				}
			}
			if !found {
				t.Fatalf("queued metric snapshot changed: %+v", requests)
			}
		})
	}
}
