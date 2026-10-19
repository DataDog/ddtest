//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package testdrive

import (
	"errors"
	"fmt"
	"syscall"
	"time"
)

func stopPlaywrightProcess(pid int) error {
	return stopPlaywrightProcessWithSignal(pid, syscall.Kill)
}

func stopPlaywrightProcessWithSignal(pid int, signal func(int, syscall.Signal) error) error {
	termErr := signal(pid, syscall.SIGTERM)
	if termErr != nil {
		if errors.Is(termErr, syscall.ESRCH) {
			return nil
		}
		if !errors.Is(termErr, syscall.EPERM) {
			return termErr
		}
	}
	for range 20 {
		if errors.Is(signal(pid, 0), syscall.ESRCH) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	// A process group may be finishing its own teardown. A denied signal is
	// acceptable only when disappearance was observed, never merely on EPERM.
	if termErr != nil {
		return termErr
	}
	err := signal(pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	if err != nil {
		return err
	}
	for range 20 {
		if errors.Is(signal(pid, 0), syscall.ESRCH) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("owned process %d is still alive after termination", pid)
}
