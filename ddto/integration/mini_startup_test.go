package integration

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Exercise the real startup path: neither settings nor telemetry may hold up
// the other's request, and neither may outlive admission of the first test.
func TestMiniStartupOverlapsSettingsAndTelemetry(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	writeBuildFixture(t, dir, map[string]string{"sample_test.go": `package fixture_test
import ("os"; "testing")
func TestStarted(t *testing.T) { if err := os.WriteFile(os.Getenv("DDTEST_STARTED_FILE"), []byte("started"), 0600); err != nil { t.Fatal(err) } }
`})
	binary := filepath.Join(t.TempDir(), "startup.test")
	if out, stderr, code := command(t, dir, testEnv(), driver, "test", "--runtime=mini", "-race", "-c", "-o", binary); code != 0 {
		t.Fatal(out, stderr)
	}
	for _, deferred := range []bool{false, true} {
		for _, scenario := range []string{"settings-first", "telemetry-first", "settings-error", "telemetry-error"} {
			t.Run(fmt.Sprintf("deferred=%t/%s", deferred, scenario), func(t *testing.T) {
				settingsSeen, telemetrySeen := make(chan struct{}), make(chan struct{})
				settingsRelease, telemetryRelease := make(chan struct{}), make(chan struct{})
				var settingsOnce, telemetryOnce, settingsUnblock, telemetryUnblock sync.Once
				releaseSettings := func() { settingsUnblock.Do(func() { close(settingsRelease) }) }
				releaseTelemetry := func() { telemetryUnblock.Do(func() { close(telemetryRelease) }) }
				var mu sync.Mutex
				var types []string
				receiver := &capture{}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.Contains(r.URL.Path, "apmtelemetry") {
						var reader io.Reader = r.Body
						if r.Header.Get("Content-Encoding") == "gzip" {
							gz, err := gzip.NewReader(r.Body)
							if err != nil {
								t.Error(err)
								w.WriteHeader(400)
								return
							}
							defer gz.Close()
							reader = gz
						}
						var payload struct {
							Type string `json:"request_type"`
						}
						if err := json.NewDecoder(reader).Decode(&payload); err != nil {
							t.Error(err)
						}
						mu.Lock()
						types = append(types, payload.Type)
						n := len(types)
						mu.Unlock()
						telemetryOnce.Do(func() { close(telemetrySeen) })
						<-telemetryRelease
						if scenario == "telemetry-error" && n == 1 {
							w.WriteHeader(503)
						} else {
							w.WriteHeader(202)
						}
						return
					}
					if strings.HasSuffix(r.URL.Path, "/setting") {
						settingsOnce.Do(func() { close(settingsSeen) })
						<-settingsRelease
						if scenario == "settings-error" {
							w.WriteHeader(400)
							return
						}
					}
					receiver.handler(w, r)
				}))
				defer server.Close()
				defer releaseSettings()
				defer releaseTelemetry()
				ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
				defer cancel()
				marker := filepath.Join(t.TempDir(), "started")
				cmd := exec.CommandContext(ctx, binary, "-test.run=^TestStarted$", "-test.count=1")
				cmd.Dir = dir
				cmd.Env = testEnv("DD_TRACE_DEBUG=true", "DD_CIVISIBILITY_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=false", "DD_TRACE_AGENT_URL="+server.URL, "DD_INSTRUMENTATION_TELEMETRY_ENABLED=true", fmt.Sprintf("DD_CIVISIBILITY_DEFERRED_DELIVERY=%t", deferred), "DDTEST_STARTED_FILE="+marker, "XDG_CACHE_HOME="+t.TempDir())
				var stdout, stderr bytes.Buffer
				cmd.Stdout = &stdout
				cmd.Stderr = &stderr
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				done := make(chan struct{})
				var runErr error
				go func() { runErr = cmd.Wait(); close(done) }()
				// Reap even when an assertion fails; handlers are released before waiting.
				defer func() { releaseSettings(); releaseTelemetry(); cancel(); <-done }()
				for _, seen := range []<-chan struct{}{telemetrySeen, settingsSeen} {
					select {
					case <-seen:
					case <-done:
						t.Fatalf("early exit: %v\n%s", runErr, &stderr)
					case <-ctx.Done():
						t.Fatal("startup requests did not overlap")
					}
				}
				if scenario == "telemetry-first" {
					releaseTelemetry()
				} else {
					releaseSettings()
				}
				select {
				case <-done:
					t.Fatalf("test process exited while startup was blocked: %v", runErr)
				case <-time.After(100 * time.Millisecond):
				}
				if _, err := os.Stat(marker); !os.IsNotExist(err) {
					t.Fatalf("test entered before startup completed: %v", err)
				}
				releaseSettings()
				releaseTelemetry()
				<-done
				if runErr != nil {
					t.Fatalf("%v\n%s\n%s", runErr, &stdout, &stderr)
				}
				for _, marker := range []string{"runtime bootstrap finished duration=", "settings initialization finished duration=", "telemetry: request finished type=app-started duration=", "ciVisibilityHttpClient: request finished path=", "test-cycle: request finished host=", "test-cycle: send finished duration=", "ci mini tracer: flush finished duration=", "ci mini tracer: close finished duration=", "civisibility: session close finished duration=", "civisibility: shutdown finished duration=", "civisibility: telemetry stop finished duration="} {
					if !strings.Contains(stderr.String(), marker) {
						t.Errorf("missing timing %q: %s", marker, &stderr)
					}
				}
				if _, err := os.Stat(marker); err != nil {
					t.Fatalf("test never ran: %v", err)
				}
				mu.Lock()
				defer mu.Unlock()
				if len(types) == 0 || types[0] != "app-started" {
					t.Fatalf("telemetry order: %v", types)
				}
				receiver.mu.Lock()
				defer receiver.mu.Unlock()
				if len(receiver.failures) != 0 || len(receiver.events) != 4 {
					t.Fatalf("events=%d failures=%v", len(receiver.events), receiver.failures)
				}
			})
		}
	}
}
