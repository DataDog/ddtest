//go:build unix

package githubjob

import (
	"syscall"
	"testing"
)

func makeFIFO(t *testing.T, path string) bool {
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	return true
}
