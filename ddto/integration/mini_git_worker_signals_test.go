//go:build unix

package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The terminal's signals reach the worker too, not only ddto: the worker
// stops its upload as for a stop line, without leaving pack files, and still
// closes the telemetry application once go test has exited. These are the
// signals that ddto forwards to go test; Windows cannot send them to another
// process.
func TestMiniGitWorkerStopsOnInterrupt(t *testing.T) {
	dir, driver, _ := gitStateFixture(t)
	for _, sig := range []os.Signal{os.Interrupt, syscall.SIGHUP, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			agent := &telemetryAgent{}
			sending := make(chan struct{}, 1)
			server := newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/packfile") {
					_, _ = io.Copy(io.Discard, r.Body)
					select {
					case sending <- struct{}{}:
					default:
					}
					<-r.Context().Done()
					return
				}
				agent.handler(w, r)
			}))
			defer server.Close()
			temp, state := t.TempDir(), t.TempDir()
			if err := os.WriteFile(filepath.Join(state, "used"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(driver, "ci-git-worker", state, "cli-runtime", "1m")
			cmd.Dir = dir
			cmd.Env = append(testEnv("DD_CIVISIBILITY_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=false", "DD_TRACE_AGENT_URL="+server.URL,
				"DD_INSTRUMENTATION_TELEMETRY_ENABLED=true", "DD_CIVISIBILITY_GIT_UPLOAD_ENABLED=true", "DD_GIT_REPOSITORY_URL=", "DD_GIT_COMMIT_SHA=",
				"TMPDIR="+temp), "PWD="+dir)
			input, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			if _, err := io.WriteString(input, "proceed go1.99.0\n"); err != nil {
				t.Fatal(err)
			}
			select {
			case <-sending:
			case <-time.After(time.Minute):
				t.Fatalf("the worker never sent its pack file: %s", stderr.String())
			}
			if err := cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			var upload struct{ Error string }
			for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(20 * time.Millisecond) {
				if data, err := os.ReadFile(filepath.Join(state, "upload.json")); err == nil {
					if err := json.Unmarshal(data, &upload); err != nil {
						t.Fatal(err)
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("the signalled worker recorded no upload: %s", stderr.String())
				}
			}
			if upload.Error != "ddto stopped the repository upload: signal" {
				t.Fatalf("upload %+v", upload)
			}
			if _, err := io.WriteString(input, "exited\n"); err != nil {
				t.Fatal(err)
			}
			_ = input.Close()
			if err := cmd.Wait(); err != nil {
				t.Fatalf("worker: %v %s", err, stderr.String())
			}
			for _, place := range []string{temp, filepath.Join(dir, ".git")} {
				entries, err := os.ReadDir(place)
				if err != nil {
					t.Fatal(err)
				}
				for _, entry := range entries {
					if strings.HasPrefix(entry.Name(), ".dd-pack-objects") {
						t.Errorf("the signalled upload left %s in %s", entry.Name(), place)
					}
				}
			}
			s := summarizeTelemetry(t, agent.take())
			if len(s.closings) != 1 || s.logs["civisibility: CLI repository upload stopped|reason:signal"] == 0 {
				t.Fatalf("telemetry closings %v, logs %v", s.closings, s.logs)
			}
		})
	}
}
