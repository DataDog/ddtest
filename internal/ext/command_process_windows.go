//go:build windows

package ext

import (
	"os"
	"os/exec"
)

func configureCommandProcess(cmd *exec.Cmd) {}

func configureCommandProcessGroup(cmd *exec.Cmd) {}

func commandSignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}

func signalCommand(cmd *exec.Cmd, _ os.Signal) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	// Windows cannot deliver Unix-style graceful signals through os.Process;
	// every signal except os.Kill returns syscall.EWINDOWS. Kill immediately
	// instead of waiting through a grace period that cannot succeed.
	return cmd.Process.Kill()
}
