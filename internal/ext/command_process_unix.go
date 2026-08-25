//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package ext

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func configureCommandProcess(cmd *exec.Cmd) {
	configureCommandProcessGroup(cmd)
	cmd.Cancel = func() error {
		return signalCommand(cmd, os.Kill)
	}
}

func configureCommandProcessGroup(cmd *exec.Cmd) {
	// Start each command in its own process group so cancellation signals can
	// terminate framework wrappers and their ordinary descendants together.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func commandSignals() []os.Signal {
	return []os.Signal{syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP, syscall.SIGQUIT}
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
