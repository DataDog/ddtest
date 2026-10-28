//go:build unix

package runner

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// go test can write -coverprofile to a FIFO, which ddto does not read back:
// opening it would wait for another writer.
func TestProfileCoverageSkipsFIFO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coverage.fifo")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skip("no FIFO:", err)
	}
	done := make(chan bool, 1)
	go func() {
		_, ok := profileCoverage(path)
		done <- ok
	}()
	select {
	case ok := <-done:
		if ok {
			t.Fatal("coverage from a FIFO")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("profileCoverage waited on a FIFO")
	}
}
