package runner

import (
	"os"
	"syscall"
)

// Windows delivers console interrupts to every attached process; forwarding is
// a no-op there, but handling them keeps ddtest alive to clean up its plan.
var forwardedSignals = []os.Signal{os.Interrupt, syscall.SIGTERM}

func signalExitCode(*os.ProcessState) (int, bool) { return 0, false }

func interruptedStatus(os.Signal) int { return 1 }

func interruptProcess(p *os.Process) error { return p.Kill() }

// Exit retains the child's native exit code.
func Exit(code int) { os.Exit(code) }
