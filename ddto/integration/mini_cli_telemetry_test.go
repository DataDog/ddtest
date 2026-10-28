package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// telemetryMessage is one telemetry payload, alone or in a message-batch.
type telemetryMessage struct {
	Type    string          `json:"request_type"`
	Payload json.RawMessage `json:"payload"`
}

// telemetryRequest is a telemetry request as the Agent receives it.
type telemetryRequest struct {
	Type        string `json:"request_type"`
	RuntimeID   string `json:"runtime_id"`
	SeqID       int64  `json:"seq_id"`
	Application struct {
		ServiceName     string `json:"service_name"`
		LanguageVersion string `json:"language_version"`
	} `json:"application"`
	Payload     json.RawMessage `json:"payload"`
	session     string
	rootSession string
}

func (r telemetryRequest) messages(t *testing.T) []telemetryMessage {
	t.Helper()
	if r.Type != "message-batch" {
		return []telemetryMessage{{Type: r.Type, Payload: r.Payload}}
	}
	var batch []telemetryMessage
	if err := json.Unmarshal(r.Payload, &batch); err != nil {
		t.Fatal(err)
	}
	return batch
}

// telemetryAgent is a fake Agent that records telemetry requests and answers
// the CI Visibility requests that a test process makes.
type telemetryAgent struct {
	mu       sync.Mutex
	requests []telemetryRequest
	// reject answers 403 to telemetry requests that contain it.
	reject atomic.Pointer[string]
}

func (a *telemetryAgent) handler(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	switch {
	case strings.Contains(r.URL.Path, "telemetry"):
		var request telemetryRequest
		if err := json.Unmarshal(body, &request); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		request.session, request.rootSession = r.Header.Get("DD-Session-ID"), r.Header.Get("DD-Root-Session-ID")
		a.mu.Lock()
		a.requests = append(a.requests, request)
		a.mu.Unlock()
		if reject := a.reject.Load(); reject != nil && bytes.Contains(body, []byte(*reject)) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	case strings.HasSuffix(r.URL.Path, "/setting"):
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"id":"poc","type":"ci_app_test_service_libraries_settings","attributes":{"require_git":false,"impacted_tests_enabled":false,"early_flake_detection":{"enabled":false},"test_management":{"enabled":false}}}}`)
	case strings.HasSuffix(r.URL.Path, "/search_commits"):
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[]}`)
	default:
		w.WriteHeader(http.StatusAccepted)
	}
}

// take returns and forgets the recorded requests.
func (a *telemetryAgent) take() []telemetryRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	requests := a.requests
	a.requests = nil
	return requests
}

// telemetrySummary is what a run's telemetry requests reported.
type telemetrySummary struct {
	runtimeIDs map[string]bool
	starts     []int64 // seq_id of each request with app-started
	closings   []int64 // seq_id of each request with app-closing
	seqIDs     []int64
	counts     map[string]float64 // CI count metrics by name
	logs       map[string]int     // log message and tags, with their counts
}

func summarizeTelemetry(t *testing.T, requests []telemetryRequest) telemetrySummary {
	t.Helper()
	s := telemetrySummary{runtimeIDs: map[string]bool{}, counts: map[string]float64{}, logs: map[string]int{}}
	for _, request := range requests {
		s.runtimeIDs[request.RuntimeID] = true
		s.seqIDs = append(s.seqIDs, request.SeqID)
		if request.session != request.RuntimeID || (request.rootSession != "" && request.rootSession == request.session) {
			t.Errorf("request %d: session %q, root %q, runtime %q", request.SeqID, request.session, request.rootSession, request.RuntimeID)
		}
		for _, message := range request.messages(t) {
			switch message.Type {
			case "app-started":
				s.starts = append(s.starts, request.SeqID)
			case "app-closing":
				s.closings = append(s.closings, request.SeqID)
			case "generate-metrics":
				var metrics struct {
					Series []struct {
						Metric string   `json:"metric"`
						Type   string   `json:"type"`
						Points [][2]any `json:"points"`
					} `json:"series"`
				}
				if err := json.Unmarshal(message.Payload, &metrics); err != nil {
					t.Fatal(err)
				}
				for _, series := range metrics.Series {
					for _, point := range series.Points {
						value, _ := point[1].(float64)
						s.counts[series.Metric] += value
					}
				}
			case "logs":
				var logs struct {
					Logs []struct {
						Message string `json:"message"`
						Tags    string `json:"tags"`
						Count   int    `json:"count"`
					} `json:"logs"`
				}
				if err := json.Unmarshal(message.Payload, &logs); err != nil {
					t.Fatal(err)
				}
				for _, log := range logs.Logs {
					s.logs[log.Message+"|"+log.Tags] += max(log.Count, 1)
				}
			}
		}
	}
	slices.Sort(s.seqIDs)
	return s
}

// A ddto test run reports its telemetry as one application: ddto's Git
// worker sends app-started and app-closing, and the test processes send
// their metrics under its runtime ID, numbered with its requests. A fully
// cached run sends no telemetry; a test binary run alone reports its own
// application, as before.
func TestMiniCLITelemetry(t *testing.T) {
	dir, driver, bin := gitStateFixture(t)
	addSubPackage(t, dir)
	agent := &telemetryAgent{}
	server := newServer(http.HandlerFunc(agent.handler))
	defer server.Close()
	env := testEnv("DD_CIVISIBILITY_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=false", "DD_TRACE_AGENT_URL="+server.URL,
		"DD_INSTRUMENTATION_TELEMETRY_ENABLED=true", "DD_CIVISIBILITY_GIT_UPLOAD_ENABLED=true", "DD_GIT_REPOSITORY_URL=", "DD_GIT_COMMIT_SHA=")
	run := func(args ...string) string {
		t.Helper()
		out, stderr, code := command(t, dir, env, driver, append([]string{"test", "--runtime=mini", "-run=^Test(Pass|Sub)$"}, args...)...)
		if code != 0 {
			t.Fatalf("ddto test: %d %s %s", code, out, stderr)
		}
		return out
	}

	run(".", "./sub")
	s := summarizeTelemetry(t, agent.take())
	if len(s.runtimeIDs) != 1 {
		t.Fatalf("runtime IDs: %v", s.runtimeIDs)
	}
	if len(s.starts) != 1 || len(s.closings) != 1 || s.starts[0] != 1 || s.closings[0] != s.seqIDs[len(s.seqIDs)-1] {
		t.Fatalf("app-started at %v, app-closing at %v, requests %v", s.starts, s.closings, s.seqIDs)
	}
	for i, seq := range s.seqIDs {
		if seq != int64(i+1) {
			t.Fatalf("seq_ids are not 1..%d: %v", len(s.seqIDs), s.seqIDs)
		}
	}
	// Both test processes report their events; the worker reports its Git
	// work, which they no longer repeat.
	if s.counts["event_created"] == 0 || s.counts["git_requests.search_commits"] != 1 || s.counts["git.command"] == 0 {
		t.Fatalf("metrics: %v", s.counts)
	}
	if len(s.logs) != 0 {
		t.Fatalf("logs: %v", s.logs)
	}
	ids := s.runtimeIDs

	// With every result cached, no test process runs and nothing is sent.
	run("./sub")
	agent.take()
	if out := run("./sub"); !strings.Contains(out, "(cached)") {
		t.Fatalf("second run was not cached:\n%s", out)
	}
	if requests := agent.take(); len(requests) != 0 {
		t.Fatalf("cached run sent telemetry: %+v", requests)
	}

	// When the intake rejects the test processes' requests, the worker
	// reports them in its own.
	reject := `"event_created"`
	agent.reject.Store(&reject)
	run("-count=1", ".", "./sub")
	agent.reject.Store(nil)
	s = summarizeTelemetry(t, agent.take())
	failed := s.logs["civisibility: test process telemetry request failed|status_code:403"]
	if len(s.closings) != 1 || failed < 2 {
		t.Fatalf("rejected test processes: closings %v, logs %v", s.closings, s.logs)
	}
	for id := range s.runtimeIDs {
		if ids[id] {
			t.Fatalf("two invocations reported runtime ID %s", id)
		}
	}

	// A test binary run alone owns its application.
	if out, stderr, code := command(t, dir, env, bin, "-test.run=^TestPass$"); code != 0 {
		t.Fatalf("binary: %d %s %s", code, out, stderr)
	}
	s = summarizeTelemetry(t, agent.take())
	if len(s.runtimeIDs) != 1 || len(s.starts) != 1 || len(s.closings) != 1 || s.counts["git.command"] == 0 {
		t.Fatalf("binary alone: %+v", s)
	}
}
