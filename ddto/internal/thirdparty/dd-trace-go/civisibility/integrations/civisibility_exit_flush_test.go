// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package integrations

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	tracer "github.com/tonyredondo/dd-ci-testing-poc/internal/minitracer"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility"
)

// Exit delivers events finished after session close through the tracer's
// final close, and a failing intake sees one delivery's retries, not two.
func TestExitCiVisibilityDeliversRemainingEventsOnce(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		requests int
	}{
		{name: "accepted", status: http.StatusAccepted, requests: 1},
		// citransport makes three attempts for a retryable status.
		{name: "unavailable", status: http.StatusServiceUnavailable, requests: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetCIVisibilityBootstrapStateForTesting()
			t.Cleanup(restoreCIVisibilityBootstrapForTesting)

			var mu sync.Mutex
			var bodies [][]byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/citestcycle") {
					body, _ := io.ReadAll(r.Body)
					mu.Lock()
					bodies = append(bodies, body)
					mu.Unlock()
				}
				w.WriteHeader(tc.status)
			}))
			t.Cleanup(server.Close)
			t.Setenv("DD_CIVISIBILITY_AGENTLESS_ENABLED", "false")
			t.Setenv("DD_TRACE_AGENT_URL", server.URL)
			t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "false")

			tracer.Start()
			span, _ := tracer.StartSpanFromContext(context.Background(), "late.event", tracer.SpanType("test"))
			span.Finish()

			civisibility.SetState(civisibility.StateInitialized)
			ExitCiVisibility()

			mu.Lock()
			defer mu.Unlock()
			if len(bodies) != tc.requests {
				t.Fatalf("test-cycle requests = %d, want %d", len(bodies), tc.requests)
			}
			for _, body := range bodies {
				if !strings.Contains(string(body), "late.event") {
					t.Fatalf("request does not carry the late event: %q", body)
				}
			}
		})
	}
}
