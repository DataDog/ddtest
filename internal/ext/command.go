package ext

import (
	"os"
	"os/exec"
	"os/signal"
)

type CommandExecutor interface {
	CombinedOutput(cmd *exec.Cmd) ([]byte, error)
	Run(cmd *exec.Cmd) error
}

type DefaultCommandExecutor struct{}

func (e *DefaultCommandExecutor) CombinedOutput(cmd *exec.Cmd) ([]byte, error) {
	return cmd.CombinedOutput()
}

func (e *DefaultCommandExecutor) Run(cmd *exec.Cmd) error {
	// Start the command
	if err := cmd.Start(); err != nil {
		return err
	}

	// Create a channel to signal when the command finishes
	done := make(chan struct{})
	defer close(done)

	// Set up signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan)
	defer signal.Stop(sigChan)

	// Start a goroutine to forward signals to the process
	go func() {
		select {
		case sig := <-sigChan:
			// Forward the signal to the process
			if cmd.Process != nil {
				_ = cmd.Process.Signal(sig)
			}
		case <-done:
			return
		}
	}()

	// Wait for the command to finish
	err := cmd.Wait()

	return err
}
