package runner

import (
	"os"
	"path/filepath"
	"testing"
)

// Removing a vendor workspace's file keeps a read-only vendored file
// read-only: Windows clears the attribute of a removed read-only hard link,
// which the original shares.
func TestVendorWorkspaceKeepsReadOnlyFiles(t *testing.T) {
	dir := t.TempDir()
	original := filepath.Join(dir, "vendored.go")
	if err := os.WriteFile(original, []byte("package vendored\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(original, 0o644) })
	info, err := os.Stat(original)
	if err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(dir, "shared.go")
	if err := shareVendorFile(original, shared, info); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(shared); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(original); err != nil || info.Mode().Perm()&0o200 != 0 {
		t.Fatalf("vendored file after removing its share: %v, %v", info.Mode(), err)
	}
}
