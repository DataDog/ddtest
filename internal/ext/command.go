package ext

import "os/exec"

type CommandExecutor interface {
	CombinedOutput(cmd *exec.Cmd) ([]byte, error)
}

type DefaultCommandExecutor struct{}

func (e *DefaultCommandExecutor) CombinedOutput(cmd *exec.Cmd) ([]byte, error) {
	return cmd.CombinedOutput()
}
