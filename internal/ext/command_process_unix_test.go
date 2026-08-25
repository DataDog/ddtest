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

type manualSignalNotifier struct {
	registered chan chan<- os.Signal
}

func (n *manualSignalNotifier) Notify(c chan<- os.Signal, _ ...os.Signal) {
	n.registered <- c
}

func (n *manualSignalNotifier) Stop(chan<- os.Signal) {}

type testSignalCancellation struct {
	signal os.Signal
}

func (c testSignalCancellation) Error() string {
	return "test signal cancellation"
}

func (c testSignalCancellation) Signal() os.Signal {
	return c.signal
}

func TestDefaultCommandExecutor_Run_ContextAndSignalCancellationShareGracePeriod(t *testing.T) {
	readyFile := filepath.Join(t.TempDir(), "ready")
	handledFile := filepath.Join(t.TempDir(), "handled")
	ctx, cancel := context.WithCancelCause(context.Background())
	notifier := &manualSignalNotifier{registered: make(chan chan<- os.Signal, 1)}
	executor := &DefaultCommandExecutor{
		signalNotifier:         notifier,
		terminationGracePeriod: 2 * time.Second,
	}
	result := make(chan error, 1)
	go func() {
		result <- executor.Run(ctx, "sh", []string{
			"-c",
			`trap 'echo handled > "$DDTEST_HANDLED_FILE"' TERM; echo ready > "$DDTEST_READY_FILE"; while :; do sleep 0.05; done`,
		}, map[string]string{
			"DDTEST_READY_FILE":   readyFile,
			"DDTEST_HANDLED_FILE": handledFile,
		})
	}()

	signalChannel := <-notifier.registered
	waitForFile(t, readyFile)
	cancel(testSignalCancellation{signal: syscall.SIGTERM})
	// The executor also receives the OS signal that caused root cancellation.
	// It is the same first cancellation request, not a force-kill request.
	signalChannel <- syscall.SIGTERM
	waitForFile(t, handledFile)
	select {
	case err := <-result:
		t.Fatalf("duplicate first-signal delivery skipped the grace period: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	signalChannel <- syscall.SIGTERM
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("expected context cancellation error")
		}
	case <-time.After(time.Second):
		t.Fatal("second signal did not force termination")
	}
}

func TestDefaultCommandExecutor_Run_AllowsGracefulSignalHandling(t *testing.T) {
	readyFile := filepath.Join(t.TempDir(), "ready")
	handledFile := filepath.Join(t.TempDir(), "handled")
	notifier := &manualSignalNotifier{registered: make(chan chan<- os.Signal, 1)}
	executor := &DefaultCommandExecutor{
		signalNotifier:         notifier,
		terminationGracePeriod: 2 * time.Second,
	}
	result := make(chan error, 1)
	go func() {
		result <- executor.Run(context.Background(), "sh", []string{
			"-c",
			`trap 'echo handled > "$DDTEST_HANDLED_FILE"; exit 0' TERM; echo ready > "$DDTEST_READY_FILE"; while :; do sleep 0.05; done`,
		}, map[string]string{
			"DDTEST_READY_FILE":   readyFile,
			"DDTEST_HANDLED_FILE": handledFile,
		})
	}()

	signalChannel := <-notifier.registered
	waitForFile(t, readyFile)
	signalChannel <- syscall.SIGTERM

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("gracefully handled command returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("command did not exit after gracefully handling SIGTERM")
	}
	waitForFile(t, handledFile)
}

func TestDefaultCommandExecutor_Run_SecondSignalForcesTermination(t *testing.T) {
	readyFile := filepath.Join(t.TempDir(), "ready")
	handledFile := filepath.Join(t.TempDir(), "handled")
	notifier := &manualSignalNotifier{registered: make(chan chan<- os.Signal, 1)}
	executor := &DefaultCommandExecutor{
		signalNotifier:         notifier,
		terminationGracePeriod: 5 * time.Second,
	}
	result := make(chan error, 1)
	go func() {
		result <- executor.Run(context.Background(), "sh", []string{
			"-c",
			`trap 'echo handled > "$DDTEST_HANDLED_FILE"' TERM; echo ready > "$DDTEST_READY_FILE"; while :; do sleep 0.05; done`,
		}, map[string]string{
			"DDTEST_READY_FILE":   readyFile,
			"DDTEST_HANDLED_FILE": handledFile,
		})
	}()

	signalChannel := <-notifier.registered
	waitForFile(t, readyFile)
	signalChannel <- syscall.SIGTERM
	waitForFile(t, handledFile)
	forcedAt := time.Now()
	signalChannel <- syscall.SIGTERM

	select {
	case err := <-result:
		if err == nil {
			t.Fatal("expected forced termination error")
		}
		if elapsed := time.Since(forcedAt); elapsed >= time.Second {
			t.Fatalf("second signal took %v to force termination", elapsed)
		}
	case <-time.After(time.Second):
		t.Fatal("second signal did not force termination")
	}
}

func TestDefaultCommandExecutor_Run_GracePeriodForcesTermination(t *testing.T) {
	readyFile := filepath.Join(t.TempDir(), "ready")
	childPIDFile := filepath.Join(t.TempDir(), "child.pid")
	notifier := &manualSignalNotifier{registered: make(chan chan<- os.Signal, 1)}
	gracePeriod := 150 * time.Millisecond
	executor := &DefaultCommandExecutor{
		signalNotifier:         notifier,
		terminationGracePeriod: gracePeriod,
	}
	result := make(chan error, 1)
	go func() {
		result <- executor.Run(context.Background(), "sh", []string{
			"-c",
			`trap '' TERM; sh -c 'trap "" TERM; echo "$$" > "$DDTEST_CHILD_PID_FILE"; while :; do sleep 0.05; done' & echo ready > "$DDTEST_READY_FILE"; wait`,
		}, map[string]string{
			"DDTEST_READY_FILE":     readyFile,
			"DDTEST_CHILD_PID_FILE": childPIDFile,
		})
	}()

	signalChannel := <-notifier.registered
	waitForFile(t, readyFile)
	childPID := waitForChildPID(t, childPIDFile)
	signalledAt := time.Now()
	signalChannel <- syscall.SIGTERM

	select {
	case err := <-result:
		if err == nil {
			t.Fatal("expected forced termination error")
		}
		if elapsed := time.Since(signalledAt); elapsed < gracePeriod/2 {
			t.Fatalf("command was killed before its grace period: %v", elapsed)
		}
	case <-time.After(time.Second):
		t.Fatal("command was not killed after its grace period")
	}
	waitForProcessExit(t, childPID)
}

func TestDefaultCommandExecutor_Run_CancellationCleansUpDescendantsAfterWrapperExits(t *testing.T) {
	readyFile := filepath.Join(t.TempDir(), "ready")
	childPIDFile := filepath.Join(t.TempDir(), "child.pid")
	notifier := &manualSignalNotifier{registered: make(chan chan<- os.Signal, 1)}
	executor := &DefaultCommandExecutor{
		signalNotifier:         notifier,
		terminationGracePeriod: 2 * time.Second,
	}
	result := make(chan error, 1)
	go func() {
		result <- executor.Run(context.Background(), "sh", []string{
			"-c",
			`trap 'exit 0' TERM; sh -c 'trap "" TERM; echo "$$" > "$DDTEST_CHILD_PID_FILE"; while :; do sleep 0.05; done' & echo ready > "$DDTEST_READY_FILE"; wait`,
		}, map[string]string{
			"DDTEST_READY_FILE":     readyFile,
			"DDTEST_CHILD_PID_FILE": childPIDFile,
		})
	}()

	signalChannel := <-notifier.registered
	waitForFile(t, readyFile)
	childPID := waitForChildPID(t, childPIDFile)
	signalChannel <- syscall.SIGTERM

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("gracefully handled command returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("command did not exit after gracefully handling SIGTERM")
	}

	waitForProcessExit(t, childPID)
}

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
	// A descendant that creates a new session is intentionally outside DDTest's
	// process-tree cancellation contract. WaitDelay must still bound our exit.
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

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stat %s: %v", path, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("file %s was not created", path)
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
