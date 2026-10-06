//go:build !windows

package runner

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// fakeGo installs a shell script named go first in PATH.
func fakeGo(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go"), []byte("#!/bin/sh\n"+script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestRunGoForwardsSignalsAndKeepsExitStatus(t *testing.T) {
	// A handled interrupt keeps the child's own exit status.
	fakeGo(t, "trap 'kill $!; exit 7' INT\nsleep 30 >/dev/null 2>&1 &\nwait\n")
	signals := make(chan os.Signal, 1)
	result := make(chan int, 1)
	go func() {
		result <- runGo(context.Background(), t.TempDir(), []string{"test"}, nil, signals, nil, io.Discard, io.Discard)
	}()
	time.Sleep(200 * time.Millisecond)
	signals <- os.Interrupt
	if code := <-result; code != 7 {
		t.Fatalf("interrupt exit=%d, want the child's 7", code)
	}

	// A child killed by a signal is reported with the shell convention.
	fakeGo(t, "exec sleep 30\n")
	go func() {
		result <- runGo(context.Background(), t.TempDir(), []string{"test"}, nil, signals, nil, io.Discard, io.Discard)
	}()
	time.Sleep(200 * time.Millisecond)
	signals <- syscall.SIGTERM
	if code := <-result; code != 128+int(syscall.SIGTERM) {
		t.Fatalf("terminated exit=%d", code)
	}

	// Context cancellation interrupts first instead of killing.
	fakeGo(t, "trap 'kill $!; exit 3' INT\nsleep 30 >/dev/null 2>&1 &\nwait\n")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		result <- runGo(ctx, t.TempDir(), []string{"test"}, nil, nil, nil, io.Discard, io.Discard)
	}()
	time.Sleep(200 * time.Millisecond)
	cancel()
	if code := <-result; code != 3 {
		t.Fatalf("canceled exit=%d, want graceful 3", code)
	}
}

func TestSignalDuringPreparationStopsAndCleansUp(t *testing.T) {
	// go list never answers; only the signal can end preparation.
	fakeGo(t, "exec sleep 30\n")
	signals := make(chan os.Signal, 1)
	go func() {
		time.Sleep(200 * time.Millisecond)
		signals <- syscall.SIGTERM
	}()
	start := time.Now()
	_, interrupted, err := prepareInterruptibly(context.Background(), t.TempDir(), options{packages: []string{"."}}, Mini, signals, io.Discard)
	if interrupted != syscall.SIGTERM || err == nil || time.Since(start) > 10*time.Second {
		t.Fatalf("interrupted=%v err=%v after %s", interrupted, err, time.Since(start))
	}
	if code := interruptedStatus(interrupted); code != 128+int(syscall.SIGTERM) {
		t.Fatalf("status=%d", code)
	}
}
