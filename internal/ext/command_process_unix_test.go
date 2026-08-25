//go:build darwin || linux

package ext

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestDefaultCommandExecutor_CombinedOutput_CancellationTerminatesDescendants(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	executor := &DefaultCommandExecutor{}
	result := make(chan error, 1)
	started := time.Now()
	go func() {
		_, err := executor.CombinedOutput(ctx, "sh", []string{
			"-c",
			`sleep 30 & child=$!; echo "$child" > "$DDTEST_CHILD_PID_FILE"; wait`,
		}, map[string]string{"DDTEST_CHILD_PID_FILE": pidFile})
		result <- err
	}()

	childPID := waitForChildPID(t, pidFile)
	cancel()

	select {
	case err := <-result:
		if err == nil {
			t.Fatal("expected cancellation error")
		}
		if elapsed := time.Since(started); elapsed >= 2*time.Second {
			t.Fatalf("command cancellation took %v; descendant likely kept an output pipe open", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("command did not return after cancellation")
	}

	waitForProcessExit(t, childPID)
}

func TestDefaultCommandExecutor_CombinedOutput_CancellationIsBoundedForDetachedDescendant(t *testing.T) {
	setsidPath, err := exec.LookPath("setsid")
	if err != nil {
		t.Skip("setsid is required to test a detached descendant")
	}

	pidFile := filepath.Join(t.TempDir(), "detached-child.pid")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	result := make(chan error, 1)
	go func() {
		_, err := (&DefaultCommandExecutor{}).CombinedOutput(ctx, "sh", []string{
			"-c",
			`"$DDTEST_SETSID" sh -c 'echo "$$" > "$DDTEST_CHILD_PID_FILE"; exec sleep 30' & wait`,
		}, map[string]string{
			"DDTEST_SETSID":         setsidPath,
			"DDTEST_CHILD_PID_FILE": pidFile,
		})
		result <- err
	}()

	childPID := waitForChildPID(t, pidFile)
	t.Cleanup(func() { killProcess(t, childPID) })
	cancelledAt := time.Now()
	cancel()

	select {
	case err := <-result:
		if err == nil {
			t.Fatal("expected cancellation error")
		}
		assertBoundedPipeWait(t, time.Since(cancelledAt))
	case <-time.After(3 * commandWaitDelay):
		t.Fatal("command did not return after cancelling a wrapper with a detached descendant")
	}

	assertProcessRunning(t, childPID)
}

func TestDefaultCommandExecutor_CombinedOutput_ExitIsBoundedWhenDescendantKeepsPipesOpen(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	started := time.Now()
	_, err := (&DefaultCommandExecutor{}).CombinedOutput(
		context.Background(),
		"sh",
		[]string{"-c", `sleep 30 & echo "$!" > "$DDTEST_CHILD_PID_FILE"`},
		map[string]string{"DDTEST_CHILD_PID_FILE": pidFile},
	)
	if !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("expected ErrWaitDelay, got %v", err)
	}
	assertBoundedPipeWait(t, time.Since(started))

	childPID := waitForChildPID(t, pidFile)
	t.Cleanup(func() { killProcess(t, childPID) })
	assertProcessRunning(t, childPID)
}

func assertBoundedPipeWait(t *testing.T, elapsed time.Duration) {
	t.Helper()
	if elapsed < commandWaitDelay/2 {
		t.Fatalf("command returned after %v; descendant did not exercise WaitDelay", elapsed)
	}
	if elapsed >= 3*commandWaitDelay {
		t.Fatalf("command returned after %v; WaitDelay did not bound the inherited pipe wait", elapsed)
	}
}

func waitForChildPID(t *testing.T, pidFile string) int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		contents, err := os.ReadFile(pidFile)
		if err == nil {
			pidText := strings.TrimSpace(string(contents))
			if pidText == "" {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			pid, parseErr := strconv.Atoi(pidText)
			if parseErr != nil {
				t.Fatalf("parse child PID: %v", parseErr)
			}
			return pid
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read child PID: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("child process did not start")
	return 0
}

func waitForProcessExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		running, err := processIsRunning(pid)
		if err != nil {
			t.Fatalf("check child process %d: %v", pid, err)
		}
		if !running {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("child process %d was still running after cancellation", pid)
}

func assertProcessRunning(t *testing.T, pid int) {
	t.Helper()
	running, err := processIsRunning(pid)
	if err != nil {
		t.Fatalf("detached child process %d was not running: %v", pid, err)
	}
	if !running {
		t.Fatalf("detached child process %d was not running", pid)
	}
}

func processIsRunning(pid int) (bool, error) {
	err := syscall.Kill(pid, 0)
	if errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if runtime.GOOS != "linux" {
		return true, nil
	}

	stat, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	closingParenthesis := strings.LastIndex(string(stat), ") ")
	if closingParenthesis == -1 || closingParenthesis+2 >= len(stat) {
		return false, errors.New("unexpected process stat format")
	}
	return stat[closingParenthesis+2] != 'Z', nil
}

func killProcess(t *testing.T, pid int) {
	t.Helper()
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatalf("find detached child process %d: %v", pid, err)
	}
	if err := process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("kill detached child process %d: %v", pid, err)
	}
	waitForProcessExit(t, pid)
}
