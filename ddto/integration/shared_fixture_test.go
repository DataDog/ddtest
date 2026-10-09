package integration

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"testing"
	"time"
)

// Only immutable compilation inputs and binaries are shared. runParityCase
// still owns a fresh receiver, child process and retry state for every case.
type parityFixture struct {
	dir, sdk, mini, oracle string
	base, head             string
}

type sharedParityFixture struct {
	once    sync.Once
	fixture *parityFixture
}

var (
	sharedTestingParity sharedParityFixture
	sharedTestifyParity sharedParityFixture
	sharedParityRoot    string
	sharedDriverBuild   struct {
		once      sync.Once
		path, err string
	}
)

// sharedDriver builds ddto once per test process. The driver is an
// immutable build of this checkout that tests only execute, so every fixture
// can share it instead of linking it again. TestMain owns its directory.
func sharedDriver(t *testing.T, root string) string {
	t.Helper()
	sharedDriverBuild.once.Do(func() {
		dir, err := os.MkdirTemp(sharedParityRoot, "driver-")
		if err != nil {
			sharedDriverBuild.err = err.Error()
			return
		}
		bin := filepath.Join(dir, executableName("ddto"))
		// Like a user's build, ddto keeps the checkout's VCS information.
		out, stderr, code, _, err := runCommand(root, testEnv("GOFLAGS="), "go", "build", "-o", bin, "./cmd/ddto")
		switch {
		case err != nil:
			sharedDriverBuild.err = err.Error()
		case code != 0:
			sharedDriverBuild.err = out + "\n" + stderr
		default:
			sharedDriverBuild.path = bin
		}
	})
	if sharedDriverBuild.path == "" {
		t.Fatalf("build driver: %s", sharedDriverBuild.err)
	}
	return sharedDriverBuild.path
}

// The first consumer's t.TempDir would disappear before the deferred matrix
// starts. TestMain owns these directories through all selected tests and -count
// repetitions. A filtered deferred-only invocation initializes its own build.
func TestMain(m *testing.M) {
	var err error
	sharedParityRoot, err = os.MkdirTemp("", "ddto-parity-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create shared parity workspace:", err)
		os.Exit(1)
	}
	code := m.Run()
	if err := removeSharedParityWorkspace(sharedParityRoot); err != nil {
		fmt.Fprintln(os.Stderr, "clean shared parity workspace:", err)
		code = 1
	}
	os.Exit(code)
}

// Like testing.TempDir, allow Windows handles a short time to close. Removing
// Orchestrion's URL file requests shutdown, but its log can remain open until
// the daemon exits. A persistent lock or any other error still fails cleanup.
func removeSharedParityWorkspace(path string) error {
	const retryWindow = 2 * time.Second
	const retryInterval = 10 * time.Millisecond
	err := os.RemoveAll(path)
	if err == nil || runtime.GOOS != "windows" {
		return err
	}
	deadline := time.Now().Add(retryWindow)
	for {
		// Windows ERROR_ACCESS_DENIED (5) and ERROR_SHARING_VIOLATION (32)
		// are the same transient errors retried by testing.TempDir.
		retryable := errors.Is(err, syscall.Errno(5)) || errors.Is(err, syscall.Errno(32))
		if !retryable || time.Now().Add(retryInterval).After(deadline) {
			return err
		}
		time.Sleep(retryInterval)
		err = os.RemoveAll(path)
	}
}

func (s *sharedParityFixture) get(t *testing.T, name string, build func(*testing.T, func() string) *parityFixture) *parityFixture {
	t.Helper()
	s.once.Do(func() {
		start := time.Now()
		tempDir := func() string {
			t.Helper()
			dir, err := os.MkdirTemp(sharedParityRoot, "")
			if err != nil {
				t.Fatal(err)
			}
			return dir
		}
		s.fixture = build(t, tempDir)
		t.Logf("shared %s fixture built once in %s", name, time.Since(start))
	})
	if s.fixture == nil {
		// sync.Once also completes when the builder calls Fatal/Goexit. Never
		// let a later test reuse an incomplete fixture or hide the first failure.
		t.Fatalf("shared %s fixture unavailable after a failed build", name)
	}
	return s.fixture
}
