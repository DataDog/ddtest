//go:build unix

package integration

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/toolwrap"
)

// A signal while ddto waits to rebuild its wrapper executable, which another
// run still uses, stops ddto before go test starts, with the signal's status.
func TestToolWrapperWaitStopsOnSignal(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	cache := t.TempDir()
	env := testEnv(append(isolatedUserCache(t, cache), "DD_CIVISIBILITY_ENABLED=false", "DD_TRACE_DEBUG=true")...)
	args := []string{"test", "--runtime=mini", "-count=1", "-coverpkg=testing,./...", "."}
	if out, stderr, code := command(t, dir, env, driver, args...); code != 0 {
		t.Fatal(out, stderr)
	}
	parent := filepath.Join(userCacheIn(cache), "ddto", "toolwraps")
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	key := ""
	for _, entry := range entries {
		if entry.IsDir() {
			key = entry.Name()
		}
	}
	executable := filepath.Join(parent, key, toolwrap.Executable)
	if err := os.Remove(executable); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("stale"), 0700); err != nil {
		t.Fatal(err)
	}
	// Another run uses the stale executable.
	lock, err := os.OpenFile(filepath.Join(parent, key+".lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_SH); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(driver, args...)
	cmd.Dir, cmd.Env = dir, env
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waiting := make(chan struct{})
	var log strings.Builder
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			log.WriteString(scanner.Text() + "\n")
			if strings.Contains(scanner.Text(), "tool wrapper state=stale") {
				close(waiting)
				for scanner.Scan() {
				}
				return
			}
		}
	}()
	select {
	case <-waiting:
	case <-time.After(2 * time.Minute):
		_ = cmd.Process.Kill()
		t.Fatalf("ddto never waited for the stale wrapper:\n%s", log.String())
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case err := <-exited:
		// ddto re-raises the signal after cleanup, or exits with its status
		// where the signal is ignored.
		var status syscall.WaitStatus
		if exit, ok := err.(*exec.ExitError); ok {
			status, _ = exit.Sys().(syscall.WaitStatus)
		}
		if err == nil || !(status.Signaled() && status.Signal() == syscall.SIGINT || status.Exited() && status.ExitStatus() == 128+int(syscall.SIGINT)) {
			t.Fatalf("exit %v, want SIGINT or status %d", err, 128+int(syscall.SIGINT))
		}
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("the signal did not stop the wait")
	}
	if stdout.Len() != 0 {
		t.Fatalf("go test ran:\n%s", stdout.String())
	}
}
