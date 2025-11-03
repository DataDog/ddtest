package ext

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
)

type CommandExecutor interface {
	CombinedOutput(ctx context.Context, cmd *exec.Cmd) ([]byte, error)
	Run(ctx context.Context, cmd *exec.Cmd) error
}

type DefaultCommandExecutor struct{}

func (e *DefaultCommandExecutor) CombinedOutput(ctx context.Context, cmd *exec.Cmd) ([]byte, error) {
	// Create a channel to receive the result
	type result struct {
		output []byte
		err    error
	}
	resultChan := make(chan result, 1)

	// Run the command in a goroutine
	go func() {
		output, err := cmd.CombinedOutput()
		resultChan <- result{output: output, err: err}
	}()

	// Wait for either context cancellation or command completion
	select {
	case <-ctx.Done():
		// Context was cancelled, kill the process
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		// Wait for the goroutine to finish
		<-resultChan
		return nil, errors.New("command cancelled by context")
	case res := <-resultChan:
		return res.output, res.err
	}
}

func (e *DefaultCommandExecutor) Run(ctx context.Context, cmd *exec.Cmd) error {
	// Connect command's stdin/stdout/stderr to parent's stdin/stdout/stderr for proper streaming
	// stdin is needed even for non-interactive commands because some gems (like reline) check terminal properties
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Start the command
	if err := cmd.Start(); err != nil {
		return err
	}

	// Set up signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan)
	defer signal.Stop(sigChan)

	// Wait for the command to finish in a goroutine
	errChan := make(chan error, 1)
	go func() {
		errChan <- cmd.Wait()
	}()

	// Single select loop to handle context cancellation, signals, and command completion
	for {
		select {
		case <-ctx.Done():
			// Context was cancelled, kill the process
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			<-errChan // Wait for the command to finish
			return errors.New("command cancelled by context")
		case sig := <-sigChan:
			// Forward the signal to the process
			if cmd.Process != nil {
				_ = cmd.Process.Signal(sig)
			}
		case err := <-errChan:
			// Command finished
			return err
		}
	}
}
