//go:build darwin || linux

package ext

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func configureCommandProcess(cmd *exec.Cmd) {
	// Start each command in its own process group so context cancellation also
	// terminates framework wrappers and their descendants.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return signalCommand(cmd, os.Kill)
	}
}

func signalCommand(cmd *exec.Cmd, signal os.Signal) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}

	sysSignal, ok := signal.(syscall.Signal)
	if !ok {
		return cmd.Process.Signal(signal)
	}

	if err := syscall.Kill(-cmd.Process.Pid, sysSignal); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	return nil
}
