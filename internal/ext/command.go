package ext

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

type CommandExecutor interface {
	CombinedOutput(ctx context.Context, name string, args []string, envMap map[string]string) ([]byte, error)
	Run(ctx context.Context, name string, args []string, envMap map[string]string) error
}

type DefaultCommandExecutor struct{}

// applyEnvMap applies environment variables from envMap to the command
func applyEnvMap(cmd *exec.Cmd, envMap map[string]string) {
	if len(envMap) > 0 {
		cmd.Env = os.Environ()
		for key, value := range envMap {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
}

func (e *DefaultCommandExecutor) CombinedOutput(ctx context.Context, name string, args []string, envMap map[string]string) ([]byte, error) {
	// no-dd-sa:go-security/command-injection
	cmd := exec.CommandContext(ctx, name, args...)
	applyEnvMap(cmd, envMap)

	return cmd.CombinedOutput()
}

func (e *DefaultCommandExecutor) Run(ctx context.Context, name string, args []string, envMap map[string]string) error {
	// no-dd-sa:go-security/command-injection
	cmd := exec.CommandContext(ctx, name, args...)
	applyEnvMap(cmd, envMap)

	// Connect command's stdin/stdout/stderr to parent's stdin/stdout/stderr for proper streaming
	// stdin is needed even for non-interactive commands because some gems (like reline) check terminal properties
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Start the command
	if err := cmd.Start(); err != nil {
		return err
	}

	// Set up signal forwarding for common termination signals used by CI systems
	// SIGTERM - standard graceful termination (most common in CI)
	// SIGINT - interrupt/user cancellation
	// SIGHUP - hangup/connection loss
	// SIGQUIT - quit signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP, syscall.SIGQUIT)
	defer signal.Stop(sigChan)

	// Wait for command completion in a goroutine
	errChan := make(chan error, 1)
	go func() {
		errChan <- cmd.Wait()
	}()

	// Wait for either signals or command completion
	for {
		select {
		case sig := <-sigChan:
			// Forward the signal to the child process
			if cmd.Process != nil {
				_ = cmd.Process.Signal(sig)
			}
		case err := <-errChan:
			// Command finished
			return err
		}
	}
}
