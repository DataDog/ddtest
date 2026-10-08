package minitracer

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/citransport"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/bazel"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/msgp/msgp"
)

func TestCIByteBatchingAndCompression(t *testing.T) {
	for _, agentless := range []bool{false, true} {
		t.Run(map[bool]string{false: "agent", true: "agentless"}[agentless], func(t *testing.T) {
			var count, requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				var reader io.Reader = r.Body
				if agentless {
					gz, err := gzip.NewReader(r.Body)
					if err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					defer gz.Close()
					reader = gz
				}
				data, err := io.ReadAll(reader)
				if err != nil {
					t.Error(err)
					return
				}
				if len(data) > citransport.TestCycleMaxPayloadBytes {
					t.Errorf("oversized body: %d", len(data))
				}
				var envelope testCyclePayload
				var events ciEvents
				if err = msgp.Decode(bytes.NewReader(data), &envelope); err != nil {
					t.Error(err)
					return
				}
				if err = msgp.Decode(bytes.NewReader(envelope.Events), &events); err != nil {
					t.Error(err)
					return
				}
				count.Add(int32(len(events)))
				for _, e := range events {
					if e.Content.Meta["version"] != "service-v3" || len(e.Content.Meta["large"]) != 1<<20 {
						t.Error("lost event attributes")
					}
				}
				w.WriteHeader(202)
			}))
			defer server.Close()
			c, err := New(Config{ServiceVersion: "service-v3", MaxEvents: 100000, Transport: citransport.Config{Endpoint: server.URL, Agentless: agentless, APIKey: "fixture"}})
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 8; i++ {
				s, _ := c.StartSpan(context.Background(), "test", SpanType("test"), Tag("large", strings.Repeat("a", 1<<20)))
				s.Finish()
			}
			if err = c.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			if count.Load() != 8 || requests.Load() < 4 || c.DroppedEvents() != 0 {
				t.Fatalf("events=%d batches=%d dropped=%d", count.Load(), requests.Load(), c.DroppedEvents())
			}
		})
	}
}

func TestLargeBatchFailureRetainedAndOversizedEventRejected(t *testing.T) {
	var failing atomic.Bool
	failing.Store(true)
	var delivered atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failing.Load() {
			w.WriteHeader(401)
			return
		}
		var envelope testCyclePayload
		var events ciEvents
		if err := msgp.Decode(r.Body, &envelope); err != nil {
			t.Error(err)
			return
		}
		if err := msgp.Decode(bytes.NewReader(envelope.Events), &events); err != nil {
			t.Error(err)
			return
		}
		delivered.Add(int32(len(events)))
		w.WriteHeader(202)
	}))
	defer server.Close()
	c, err := New(Config{Transport: citransport.Config{Endpoint: server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	span, _ := c.StartSpan(context.Background(), "test", SpanType("test"), Tag("large", strings.Repeat("a", 3<<20)))
	span.Finish()
	// The byte threshold seals the batch; delivery fails in the background.
	waitFor(t, func() bool { return c.LastError() != nil })
	c.mu.Lock()
	retained := len(c.ready) == 1 && len(c.ready[0].events) == 1 && c.ready[0].bytes >= 3<<20 && len(c.events) == 0
	c.mu.Unlock()
	if !retained {
		t.Fatal("failed batch not retained")
	}
	tooLarge, _ := c.StartSpan(context.Background(), "test", SpanType("test"), Tag("large", strings.Repeat("a", citransport.TestCycleMaxPayloadBytes)))
	tooLarge.Finish()
	if c.DroppedEvents() != 1 {
		t.Fatal("oversized event accepted")
	}
	failing.Store(false)
	if err = c.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if delivered.Load() != 1 {
		t.Fatal("retained batch lost or duplicated")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.queuedBytes != 0 || len(c.ready) != 0 {
		t.Fatal("byte count not reset")
	}
}

// waitFor polls a condition set by background delivery.
func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestBazelNativeEventFilesAndWriteFailure(t *testing.T) {
	recorder := &log.RecordLogger{}
	t.Cleanup(log.UseLogger(recorder))
	level := log.GetLevel()
	t.Cleanup(func() { log.SetLevel(level) })
	log.SetLevel(log.LevelDebug)
	t.Setenv(bazel.PayloadsInFilesEnv, "true")
	t.Setenv(bazel.UndeclaredOutputsDirEnv, t.TempDir())
	bazel.ResetForTesting()
	t.Cleanup(bazel.ResetForTesting)
	c, err := New(Config{Transport: citransport.Config{Agentless: true}})
	if err != nil {
		t.Fatal(err)
	}
	s, _ := c.StartSpan(context.Background(), "test", SpanType("test"), Tag("test_session_id", "10"), Tag("test.status", "pass"))
	s.Finish()
	if err = c.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	paths, err := filepath.Glob(filepath.Join(os.Getenv(bazel.UndeclaredOutputsDirEnv), "payloads", "tests", "*.json"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("files %v: %v", paths, err)
	}
	raw, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err = json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	e := payload["events"].([]any)[0].(map[string]any)
	content := e["content"].(map[string]any)
	if e["type"] != "test" || e["version"] != float64(2) || content["test_session_id"] != float64(10) {
		t.Fatalf("wrong event: %v", e)
	}
	t.Setenv(bazel.UndeclaredOutputsDirEnv, "")
	bazel.ResetForTesting()
	c, err = New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	s, _ = c.StartSpan(context.Background(), "test", SpanType("test"))
	s.Finish()
	if err = c.Close(context.Background()); err == nil || c.LastError() == nil {
		t.Fatal("write failure suppressed")
	}
	lines := strings.Join(recorder.Logs(), "\n")
	if strings.Contains(lines, "test-cycle: request finished") {
		t.Fatal("Bazel files reported an HTTP attempt")
	}
	for _, status := range []string{"ok", "error"} {
		found := false
		for _, line := range recorder.Logs() {
			if strings.Contains(line, "send finished duration=") && strings.Contains(line, "mode=files") && strings.HasSuffix(line, "attempts=0 status="+status) {
				found = true
			}
		}
		if !found {
			t.Errorf("missing Bazel result %s: %s", status, lines)
		}
	}
}
