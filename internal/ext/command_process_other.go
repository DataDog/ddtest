//go:build js || plan9 || wasip1

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

func signalCommand(cmd *exec.Cmd, signal os.Signal) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	return cmd.Process.Signal(signal)
}
