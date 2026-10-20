//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package testdrive

import (
	"errors"
	"fmt"
	"syscall"
	"time"
)

func waitOwnedProcessExit(pid int) error {
	return waitOwnedProcessExitWithSignal(pid, syscall.Kill, 10*time.Second)
}

func waitOwnedProcessExitWithSignal(pid int, signal func(int, syscall.Signal) error, timeout time.Duration) error {
	// Watchman was stopped through its private socket. A reused PID may make
	// this check inconclusive, but must never cause another process to be killed.
	deadline := time.Now().Add(timeout)
	for {
		if errors.Is(signal(pid, 0), syscall.ESRCH) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("cannot verify exit of owned process %d", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
