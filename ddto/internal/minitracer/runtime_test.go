package minitracer

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/cidelivery"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/citransport"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"
)

// Stop flushes aggregated error logs like dd-trace-go's tracer.Stop, so a
// delivery failure or lost-event report at exit is printed, not discarded.
func TestStopFlushesAggregatedErrorLogs(t *testing.T) {
	recorder := &log.RecordLogger{}
	defer log.UseLogger(recorder)()
	log.Error("mini stop flush check %d", 7)
	Stop()
	for _, line := range recorder.Logs() {
		if strings.Contains(line, "mini stop flush check 7") {
			return
		}
	}
	t.Fatalf("aggregated error not flushed at Stop: %q", recorder.Logs())
}

// Per-test admission, coverage processing and the CI writers ask cidelivery
// for the mode. It must stay the runtime client's even if a test changes the
// environment variable, and follow the environment again after Stop.
func TestStartFixesDeliveryModeUntilStop(t *testing.T) {
	previous := active.Load()
	t.Cleanup(func() { active.Store(previous) })
	t.Setenv("DD_TRACE_AGENT_URL", "http://127.0.0.1:9")
	for _, deferred := range []bool{false, true} {
		t.Run(fmt.Sprintf("deferred=%t", deferred), func(t *testing.T) {
			t.Setenv(cidelivery.DeferredEnv, strconv.FormatBool(deferred))
			Start()
			client := active.Load()
			if client == nil || client.deferUntilIdle != deferred {
				t.Fatal("runtime client did not start in the selected mode")
			}
			t.Setenv(cidelivery.DeferredEnv, strconv.FormatBool(!deferred))
			if cidelivery.Enabled() != deferred {
				t.Fatal("a later environment change overrode the client's mode")
			}
			Stop()
			if cidelivery.Enabled() != !deferred {
				t.Fatal("mode stayed fixed after Stop")
			}
		})
	}
}

// A client started while another is still closing reads the environment, and
// the earlier client's release leaves the newer mode fixed.
func TestRestartKeepsNewerDeliveryMode(t *testing.T) {
	previous := active.Load()
	t.Cleanup(func() { active.Store(previous) })
	t.Setenv("DD_TRACE_AGENT_URL", "http://127.0.0.1:9")
	t.Setenv(cidelivery.DeferredEnv, "false")
	Start()
	earlier := active.Load()
	t.Setenv(cidelivery.DeferredEnv, "true")
	Start()
	later := active.Load()
	if earlier == nil || later == nil || later == earlier || !later.deferUntilIdle {
		t.Fatal("restarted client did not read the environment")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = earlier.Close(ctx)
	earlier.releaseMode()
	t.Setenv(cidelivery.DeferredEnv, "false")
	if !cidelivery.Enabled() {
		t.Fatal("an earlier client's release replaced the newer client's mode")
	}
	Stop()
	if cidelivery.Enabled() {
		t.Fatal("mode stayed fixed after Stop")
	}
}

func TestRuntimeDeliveryDiagnostics(t *testing.T) {
	for _, code := range []int{http.StatusAccepted, http.StatusUnauthorized} {
		for _, debug := range []bool{false, true} {
			t.Run(fmt.Sprintf("http=%d/debug=%t", code, debug), func(t *testing.T) {
				recorder := &log.RecordLogger{}
				t.Cleanup(log.UseLogger(recorder))
				level := log.GetLevel()
				t.Cleanup(func() { log.SetLevel(level) })
				log.SetLevel(log.LevelWarn)
				if debug {
					log.SetLevel(log.LevelDebug)
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }))
				t.Cleanup(server.Close)
				client, err := New(Config{Transport: citransport.Config{Endpoint: server.URL, Attempts: 1}})
				if err != nil {
					t.Fatal(err)
				}
				previous := active.Swap(client)
				t.Cleanup(func() { Stop(); active.Store(previous) })
				span, _ := StartSpanFromContext(context.Background(), "test", SpanType("test"))
				span.Finish()
				Flush()
				Stop()
				lines := strings.Join(recorder.Logs(), "\n")
				if !debug {
					if strings.Contains(lines, "ci mini tracer:") {
						t.Fatal("debug-disabled runtime logged timings")
					}
					return
				}
				status := "ok"
				if code == http.StatusUnauthorized {
					status = "error"
				}
				for _, operation := range []string{"flush", "close"} {
					marker := "ci mini tracer: " + operation + " finished duration="
					found := false
					for _, line := range recorder.Logs() {
						if strings.Contains(line, marker) && strings.HasSuffix(line, "status="+status) {
							found = true
						}
					}
					if !found {
						t.Errorf("missing %s result %s: %s", operation, status, lines)
					}
				}
			})
		}
	}
}
