package minitracer

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
