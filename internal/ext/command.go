package ext

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

const commandWaitDelay = time.Second
const commandTerminationGracePeriod = 5 * time.Second

type CommandExecutor interface {
	CombinedOutput(ctx context.Context, name string, args []string, envMap map[string]string) ([]byte, error)
	Run(ctx context.Context, name string, args []string, envMap map[string]string) error
}

type signalNotifier interface {
	Notify(chan<- os.Signal, ...os.Signal)
	Stop(chan<- os.Signal)
}

type osSignalNotifier struct{}

func (n osSignalNotifier) Notify(c chan<- os.Signal, signals ...os.Signal) {
	signal.Notify(c, signals...)
}

func (n osSignalNotifier) Stop(c chan<- os.Signal) {
	signal.Stop(c)
}

type DefaultCommandExecutor struct {
	signalNotifier         signalNotifier
	terminationGracePeriod time.Duration
}

func (e *DefaultCommandExecutor) notifier() signalNotifier {
	if e.signalNotifier != nil {
		return e.signalNotifier
	}

	return osSignalNotifier{}
}

func (e *DefaultCommandExecutor) gracePeriod() time.Duration {
	if e.terminationGracePeriod > 0 {
		return e.terminationGracePeriod
	}

	return commandTerminationGracePeriod
}

type signalCancellation interface {
	Signal() os.Signal
}

func contextCancellationSignal(ctx context.Context) os.Signal {
	var cancellation signalCancellation
	if errors.As(context.Cause(ctx), &cancellation) {
		return cancellation.Signal()
	}

	return syscall.SIGTERM
}

// applyEnvMap applies environment variables from envMap to the command
func applyEnvMap(cmd *exec.Cmd, envMap map[string]string) {
	if len(envMap) > 0 {
		cmd.Env = os.Environ()
		for key, value := range envMap {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
}

func prepareCommand(cmd *exec.Cmd) {
	configureCommandProcess(cmd)
	// Bound the time spent waiting for inherited output pipes after cancellation.
	// A detached descendant can otherwise keep CombinedOutput blocked indefinitely.
	cmd.WaitDelay = commandWaitDelay
}

func (e *DefaultCommandExecutor) CombinedOutput(ctx context.Context, name string, args []string, envMap map[string]string) ([]byte, error) {
	// no-dd-sa:go-security/command-injection
	cmd := exec.CommandContext(ctx, name, args...)
	applyEnvMap(cmd, envMap)
	prepareCommand(cmd)

	return cmd.CombinedOutput()
}

func (e *DefaultCommandExecutor) Output(ctx context.Context, name string, args []string, envMap map[string]string) ([]byte, []byte, error) {
	// no-dd-sa:go-security/command-injection
	cmd := exec.CommandContext(ctx, name, args...)
	applyEnvMap(cmd, envMap)
	prepareCommand(cmd)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func (e *DefaultCommandExecutor) Run(ctx context.Context, name string, args []string, envMap map[string]string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	// no-dd-sa:go-security/command-injection
	cmd := exec.Command(name, args...)
	applyEnvMap(cmd, envMap)
	configureCommandProcessGroup(cmd)

	// Stream test output while keeping execution non-interactive. With a nil
	// Stdin, os/exec connects the command to the null device, so accidental
	// reads receive EOF instead of blocking on a pipe or background terminal.
	cmd.Stdin = nil
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Set up signal forwarding for common termination signals used by CI systems
	// SIGTERM - standard graceful termination (most common in CI)
	// SIGINT - interrupt/user cancellation
	// SIGHUP - hangup/connection loss
	// SIGQUIT - quit signal
	sigChan := make(chan os.Signal, 1)
	notifier := e.notifier()
	notifier.Notify(sigChan, commandSignals()...)
	defer notifier.Stop(sigChan)
	// Close the gap between the initial context check and process startup. Once
	// signals are registered, any cancellation racing Start is retained in
	// sigChan and forwarded after the process is running.
	if err := ctx.Err(); err != nil {
		return err
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		return err
	}

	// Wait for command completion in a goroutine
	errChan := make(chan error, 1)
	go func() {
		errChan <- cmd.Wait()
	}()

	ctxDone := ctx.Done()
	var graceTimer *time.Timer
	var graceExpired <-chan time.Time
	terminationRequested := false
	signalCount := 0
	requestTermination := func(sig os.Signal) {
		terminationRequested = true
		_ = signalCommand(cmd, sig)
		graceTimer = time.NewTimer(e.gracePeriod())
		graceExpired = graceTimer.C
	}
	forceTermination := func() {
		_ = signalCommand(cmd, os.Kill)
	}
	defer func() {
		if graceTimer != nil {
			graceTimer.Stop()
		}
	}()

	// Gracefully forward the first cancellation request. A second signal or an
	// expired grace period forcefully terminates the wrapper.
	for {
		select {
		case sig := <-sigChan:
			signalCount++
			if signalCount > 1 {
				forceTermination()
				continue
			}
			if !terminationRequested {
				requestTermination(sig)
			}
		case <-ctxDone:
			ctxDone = nil
			if !terminationRequested {
				requestTermination(contextCancellationSignal(ctx))
			}
		case <-graceExpired:
			graceExpired = nil
			forceTermination()
		case err := <-errChan:
			// Command finished
			// A wrapper can exit after handling the graceful signal while one of
			// its ordinary descendants remains alive in the process group. Do not
			// leave those descendants behind once cancellation has been requested.
			if terminationRequested || ctx.Err() != nil {
				forceTermination()
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
	}
}
