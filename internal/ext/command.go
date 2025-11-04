package ext

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

type CommandExecutor interface {
	CombinedOutput(ctx context.Context, cmd *exec.Cmd) ([]byte, error)
	Run(ctx context.Context, cmd *exec.Cmd) error
}

type DefaultCommandExecutor struct{}

func (e *DefaultCommandExecutor) CombinedOutput(ctx context.Context, cmd *exec.Cmd) ([]byte, error) {
	// Set up buffers to capture stdout and stderr
	var outputBuf bytes.Buffer
	cmd.Stdout = &outputBuf
	cmd.Stderr = &outputBuf

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	errChan := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		errChan <- err
	}()

	// Wait for either context cancellation or command completion
	select {
	case <-ctx.Done():
		// Context was cancelled, kill the process
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-errChan
		return nil, errors.New("command cancelled by context")
	case err := <-errChan:
		return outputBuf.Bytes(), err
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

	// Set up signal handling for SIGINT and SIGTERM only
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
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
