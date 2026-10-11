package githubjob

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Only regular Worker logs are read: a directory fails to read, and a FIFO
// without a writer would block the scan.
func TestFromDiagnosticsSkipsNonRegularFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "Worker_3.log"), 0o700); err != nil {
		t.Fatal(err)
	}
	if !makeFIFO(t, filepath.Join(dir, "Worker_2.log")) {
		t.Log("FIFOs are unavailable on this platform")
	}
	if err := os.WriteFile(filepath.Join(dir, "Worker_1.log"), []byte(`{"job":{"d":[{"k":"check_run_id","v":42}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "Worker_1.log"), old, old); err != nil {
		t.Fatal(err)
	}
	type result struct {
		id string
		ok bool
	}
	done := make(chan result, 1)
	go func() {
		id, ok := FromDiagnostics([]string{dir}, t.Logf)
		done <- result{id, ok}
	}()
	select {
	case got := <-done:
		if !got.ok || got.id != "42" {
			t.Fatalf("job ID %q, %t", got.id, got.ok)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("scan blocked on a non-regular file")
	}
}
