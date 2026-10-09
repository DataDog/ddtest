package runner

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"testing"
)

// TestMain keeps persistent vendor workspaces out of the developer's cache.
// The directory is created on first use: helper processes that re-execute this
// binary exit without returning from m.Run, so they must not create one.
func TestMain(m *testing.M) {
	var (
		once     sync.Once
		cache    string
		cacheErr error
	)
	userCacheDir = func() (string, error) {
		once.Do(func() { cache, cacheErr = os.MkdirTemp("", "ddtest-runner-cache-") })
		return cache, cacheErr
	}
	code := m.Run()
	if cache != "" {
		if err := os.RemoveAll(cache); err != nil {
			fmt.Fprintln(os.Stderr, "remove user cache:", err)
			code = 1
		}
	}
	os.Exit(code)
}

// Helper processes re-execute this binary and exit inside m.Run, so TestMain's
// cleanup never runs for them. They must not create a user cache.
func TestHelperProcessesLeaveNoTemporaryFiles(t *testing.T) {
	temp := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestPackageListHelperProcess$", "--", "success")
	cmd.Env = append(os.Environ(), "DDTEST_PACKAGE_LIST_HELPER=1", "TMPDIR="+temp, "TMP="+temp, "TEMP="+temp)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	if entries, err := os.ReadDir(temp); err != nil || len(entries) != 0 {
		t.Fatalf("helper process left temporary files: %v %v", entries, err)
	}
}
