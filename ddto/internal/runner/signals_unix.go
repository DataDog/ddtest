//go:build !windows

package runner

import (
	"os"
	"os/signal"
	"syscall"
)

// forwardedSignals reach go test even when only ddto received them.
var forwardedSignals = []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}

// signalExitCode reports a child killed by a signal with the shell's 128+N
// convention, so Exit can terminate ddto with the same signal.
func signalExitCode(state *os.ProcessState) (int, bool) {
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal()), true
	}
	return 0, false
}

func interruptedStatus(s os.Signal) int {
	if number, ok := s.(syscall.Signal); ok {
		return 128 + int(number)
	}
	return 1
}

func interruptProcess(p *os.Process) error { return p.Signal(os.Interrupt) }

// Exit ends ddto like its go child: a 128+N status from signalExitCode
// re-raises signal N after cleanup, so callers observe the same termination.
func Exit(code int) {
	if code > 128 && code <= 128+64 {
		number := syscall.Signal(code - 128)
		signal.Reset(number)
		_ = syscall.Kill(syscall.Getpid(), number)
	}
	os.Exit(code)
}
