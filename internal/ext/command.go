package ext

import "os/exec"

type CommandExecutor interface {
	CombinedOutput(cmd *exec.Cmd) ([]byte, error)
	Run(cmd *exec.Cmd) error
}

type DefaultCommandExecutor struct{}

func (e *DefaultCommandExecutor) CombinedOutput(cmd *exec.Cmd) ([]byte, error) {
	return cmd.CombinedOutput()
}

func (e *DefaultCommandExecutor) Run(cmd *exec.Cmd) error {
	return cmd.Run()
}
