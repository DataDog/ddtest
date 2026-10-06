package runner

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Observe real Go output before its module download is allowed to finish.
// This catches buffering that a check of the final command output would miss.
func TestRuntimeDownloadProgress(t *testing.T) {
	for _, outcome := range []string{"success", "failure", "cancel"} {
		t.Run(outcome, func(t *testing.T) {
			const version = "v1.2.3"
			module := "module " + miniModule + "\n\ngo 1.26.0\n"
			var archive bytes.Buffer
			zw := zip.NewWriter(&archive)
			for name, contents := range map[string]string{"go.mod": module, "testopt/testopt.go": "package testopt\n"} {
				w, err := zw.Create(miniModule + "@" + version + "/" + name)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := io.WriteString(w, contents); err != nil {
					t.Fatal(err)
				}
			}
			if err := zw.Close(); err != nil {
				t.Fatal(err)
			}
			blocked, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				prefix := "/" + miniModule + "/@v/"
				switch r.URL.Path {
				case prefix + version + ".info":
					fmt.Fprintf(w, `{"Version":%q,"Time":"2026-10-06T00:00:00Z"}`, version)
				case prefix + version + ".mod":
					io.WriteString(w, module)
				case prefix + "list":
					fmt.Fprintln(w, version)
				case prefix + version + ".zip":
					once.Do(func() { close(blocked) })
					<-release
					if outcome == "failure" {
						http.Error(w, "diagnostic download failure", http.StatusNotFound)
						return
					}
					w.Write(archive.Bytes())
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			// Release handlers before Close, including assertion-failure paths.
			var unblock sync.Once
			defer unblock.Do(func() { close(release) })
			cache := t.TempDir()
			// Go makes downloaded modules read-only, including their directories.
			// Restore write access only in this test's cache before TempDir cleanup.
			t.Cleanup(func() {
				if err := filepath.WalkDir(cache, func(path string, _ fs.DirEntry, err error) error {
					if err != nil {
						return err
					}
					return os.Chmod(path, 0700)
				}); err != nil {
					t.Error(err)
				}
			})
			t.Setenv("GOMODCACHE", cache)
			t.Setenv("GOPROXY", server.URL)
			t.Setenv("GONOPROXY", "none")
			t.Setenv("GOSUMDB", "off")
			t.Setenv("GOWORK", "off")
			t.Setenv("GOTOOLCHAIN", "local")
			dir := t.TempDir()
			modfile := filepath.Join(dir, "go.mod")
			if err := os.WriteFile(modfile, []byte("module example.com/client\n\ngo 1.26.0\n"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			progress := &downloadProgress{seen: make(chan struct{}, 1)}
			done := make(chan error, 1)
			go func() {
				// No local source: resolve exactly the CLI's published version.
				done <- requireMini(ctx, dir, modfile, "", version, progress)
			}()
			select {
			case <-blocked:
			case err := <-done:
				t.Fatalf("download never started: %v\n%s", err, progress)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			select {
			case <-progress.seen:
			case <-ctx.Done():
				t.Fatalf("download output was buffered until completion: %s", progress)
			}
			if !strings.Contains(progress.String(), "ddtest: preparing runtime with go get "+miniModule+"@"+version) {
				t.Fatalf("missing initial status: %s", progress)
			}
			if outcome == "cancel" {
				cancel()
			} else {
				unblock.Do(func() { close(release) })
			}
			select {
			case err := <-done:
				if (err == nil) != (outcome == "success") {
					t.Fatalf("outcome=%s err=%v\n%s", outcome, err, progress)
				}
				if outcome == "failure" && (!strings.Contains(progress.String(), "404 Not Found") || strings.Contains(err.Error(), "404 Not Found")) {
					t.Fatalf("error must be streamed without duplication: err=%v\n%s", err, progress)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("go get did not finish after cancellation or download completion")
			}
		})
	}
}

type downloadProgress struct {
	mu   sync.Mutex
	data bytes.Buffer
	seen chan struct{}
}

func (p *downloadProgress) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	n, err := p.data.Write(data)
	if strings.Contains(p.data.String(), "go: downloading ") {
		select {
		case p.seen <- struct{}{}:
		default:
		}
	}
	return n, err
}

func (p *downloadProgress) String() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.data.String()
}
