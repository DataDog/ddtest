//go:build windows

package integration

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestSharedParityWorkspaceWaitsForOpenFile(t *testing.T) {
	dir, file := openSharedParityFile(t)
	if err := os.RemoveAll(dir); !errors.Is(err, syscall.Errno(32)) {
		t.Fatalf("open file did not reproduce a Windows sharing violation: %v", err)
	}
	closed := make(chan error, 1)
	go func() {
		time.Sleep(25 * time.Millisecond)
		closed <- file.Close()
	}()
	if err := removeSharedParityWorkspace(dir); err != nil {
		t.Fatal("cleanup did not wait for the handle to close:", err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("workspace remains after cleanup: %v", err)
	}
}

func TestSharedParityWorkspaceRejectsPersistentLock(t *testing.T) {
	dir, file := openSharedParityFile(t)
	if err := removeSharedParityWorkspace(dir); !errors.Is(err, syscall.Errno(32)) {
		t.Fatalf("persistent sharing violation was hidden: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := removeSharedParityWorkspace(dir); err != nil {
		t.Fatal(err)
	}
}

func openSharedParityFile(t *testing.T) (string, *os.File) {
	t.Helper()
	dir := t.TempDir()
	file, err := os.Create(filepath.Join(dir, ".orchestrion-jobserver.stderr.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return dir, file
}
