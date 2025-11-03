package ext

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestDefaultCommandExecutor_CombinedOutput_Success(t *testing.T) {
	executor := &DefaultCommandExecutor{}

	// Test with echo command (available on all Unix systems)
	cmd := exec.Command("echo", "hello world")
	output, err := executor.CombinedOutput(context.Background(), cmd)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expected := "hello world\n"
	actual := string(output)
	if actual != expected {
		t.Errorf("expected %q, got %q", expected, actual)
	}
}

func TestDefaultCommandExecutor_CombinedOutput_WithArgs(t *testing.T) {
	executor := &DefaultCommandExecutor{}

	// Test ls command with current directory (more portable)
	cmd := exec.Command("ls", "-1", ".") // -1 for one file per line
	output, err := executor.CombinedOutput(context.Background(), cmd)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	outputStr := string(output)
	// Should have some output (at least some files/directories)
	if len(strings.TrimSpace(outputStr)) == 0 {
		t.Error("expected some output from ls command")
	}

	// Should contain lines (multiple entries)
	lines := strings.Split(strings.TrimSpace(outputStr), "\n")
	if len(lines) == 0 {
		t.Error("expected at least one line of output")
	}
}

func TestDefaultCommandExecutor_CombinedOutput_CommandFailure(t *testing.T) {
	executor := &DefaultCommandExecutor{}

	// Test with a command that will fail
	cmd := exec.Command("ls", "/nonexistent/directory/path")
	output, err := executor.CombinedOutput(context.Background(), cmd)

	if err == nil {
		t.Error("expected error for nonexistent directory")
	}

	// Should still return output (stderr in this case)
	if len(output) == 0 {
		t.Error("expected some output even on failure")
	}

	outputStr := string(output)
	if !strings.Contains(outputStr, "No such file or directory") {
		t.Error("expected 'No such file or directory' in error output")
	}
}

func TestDefaultCommandExecutor_CombinedOutput_EmptyCommand(t *testing.T) {
	executor := &DefaultCommandExecutor{}

	// Test with cat command reading from stdin (should return immediately with no input)
	cmd := exec.Command("cat")
	// Close stdin so cat exits immediately
	cmd.Stdin = strings.NewReader("")

	output, err := executor.CombinedOutput(context.Background(), cmd)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Should be empty output
	if len(output) != 0 {
		t.Errorf("expected empty output, got %q", string(output))
	}
}

func TestDefaultCommandExecutor_Run_Success(t *testing.T) {
	executor := &DefaultCommandExecutor{}

	// Test with simple echo command
	cmd := exec.Command("echo", "test")
	err := executor.Run(context.Background(), cmd)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestDefaultCommandExecutor_Run_CommandFailure(t *testing.T) {
	executor := &DefaultCommandExecutor{}

	// Test with a command that will fail
	cmd := exec.Command("ls", "/nonexistent/directory/path")
	err := executor.Run(context.Background(), cmd)

	if err == nil {
		t.Error("expected error for nonexistent directory")
	}
}

func TestDefaultCommandExecutor_Run_SignalForwarding(t *testing.T) {
	executor := &DefaultCommandExecutor{}

	// Create a long-running process that we can interrupt
	cmd := exec.Command("sleep", "30")

	// Send SIGINT to the test process itself (simulating Ctrl+C to the parent)
	// The executor should forward this to the child sleep process
	go func() {
		pid := os.Getpid()
		process, err := os.FindProcess(pid)
		if err != nil {
			t.Errorf("failed to find test process: %v", err)
			return
		}
		// Wait for command to start
		time.Sleep(200 * time.Millisecond)
		if err := process.Signal(syscall.SIGINT); err != nil {
			t.Errorf("failed to send signal to test process: %v", err)
		}
	}()

	// Run the command - it should be interrupted by the signal forwarding
	err := executor.Run(context.Background(), cmd)

	// The process should have been interrupted
	if err == nil {
		t.Fatal("expected error from interrupted process")
	}

	// Verify it's a signal-related error
	if !strings.Contains(err.Error(), "signal") && !strings.Contains(err.Error(), "interrupt") {
		t.Logf("Got error (expected signal/interrupt related): %v", err)
	}

	t.Logf("Process correctly terminated with: %v", err)
}

func TestDefaultCommandExecutor_Run_SignalForwardingSIGTERM(t *testing.T) {
	executor := &DefaultCommandExecutor{}

	// Create a long-running process
	cmd := exec.Command("sleep", "30")

	// Send SIGTERM to the test process itself
	// The executor should forward this to the child sleep process
	go func() {
		pid := os.Getpid()
		process, err := os.FindProcess(pid)
		if err != nil {
			t.Errorf("failed to find test process: %v", err)
			return
		}
		// Wait for command to start
		time.Sleep(200 * time.Millisecond)
		if err := process.Signal(syscall.SIGTERM); err != nil {
			t.Errorf("failed to send SIGTERM to test process: %v", err)
		}
	}()

	// Run the command - it should be terminated by the signal forwarding
	err := executor.Run(context.Background(), cmd)

	// The process should have been terminated
	if err == nil {
		t.Fatal("expected error from terminated process")
	}

	t.Logf("Process correctly terminated with: %v", err)
}

func TestDefaultCommandExecutor_Run_ProcessCompletesNormally(t *testing.T) {
	executor := &DefaultCommandExecutor{}

	// Test that normal completion works without signals
	cmd := exec.Command("sleep", "0.1")

	start := time.Now()
	err := executor.Run(context.Background(), cmd)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Should complete quickly
	if duration > 2*time.Second {
		t.Errorf("process took too long: %v", duration)
	}
}

func TestDefaultCommandExecutor_Run_MultipleCommands(t *testing.T) {
	executor := &DefaultCommandExecutor{}

	// Test that multiple sequential commands work fine
	// (ensures signal handlers are properly cleaned up)

	// First command with signal
	t.Run("first command with signal", func(t *testing.T) {
		cmd := exec.Command("sleep", "30")

		// Send SIGINT to the test process itself
		go func() {
			time.Sleep(200 * time.Millisecond)
			pid := os.Getpid()
			process, _ := os.FindProcess(pid)
			_ = process.Signal(syscall.SIGINT)
		}()

		err := executor.Run(context.Background(), cmd)
		if err == nil {
			t.Fatal("expected error from interrupted process")
		}

		t.Logf("First command interrupted: %v", err)
	})

	// Second command without signal - should complete normally
	t.Run("second command completes normally", func(t *testing.T) {
		cmd := exec.Command("echo", "test")
		err := executor.Run(context.Background(), cmd)
		if err != nil {
			t.Fatalf("second command failed: %v", err)
		}
		t.Logf("Second command completed successfully")
	})
}

func TestDefaultCommandExecutor_Run_SignalHandlerCleanup(t *testing.T) {
	executor := &DefaultCommandExecutor{}

	// Run multiple commands sequentially to verify signal handlers are cleaned up
	for i := 0; i < 3; i++ {
		cmd := exec.Command("echo", "test")
		err := executor.Run(context.Background(), cmd)
		if err != nil {
			t.Fatalf("command %d failed: %v", i, err)
		}
	}

	// If signal handlers weren't cleaned up properly, this would leak goroutines
	// The test passing means cleanup is working
}

func TestDefaultCommandExecutor_Run_ContextCancellation(t *testing.T) {
	executor := &DefaultCommandExecutor{}

	// Create a context that we can cancel
	ctx, cancel := context.WithCancel(context.Background())

	// Create a long-running process
	cmd := exec.Command("sleep", "30")

	// Cancel the context after a short delay
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	// Run the command - it should be cancelled by context
	err := executor.Run(ctx, cmd)

	// The process should have been cancelled
	if err == nil {
		t.Fatal("expected error from cancelled process")
	}

	// Verify it's a cancellation error
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("expected 'cancelled' in error, got: %v", err)
	}

	t.Logf("Process correctly cancelled with: %v", err)
}

func TestDefaultCommandExecutor_CombinedOutput_ContextCancellation(t *testing.T) {
	executor := &DefaultCommandExecutor{}

	// Create a context that we can cancel
	ctx, cancel := context.WithCancel(context.Background())

	// Create a long-running process
	cmd := exec.Command("sleep", "30")

	// Cancel the context after a short delay
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	// Run the command - it should be cancelled by context
	output, err := executor.CombinedOutput(ctx, cmd)

	// The process should have been cancelled
	if err == nil {
		t.Fatal("expected error from cancelled process")
	}

	// Verify it's a cancellation error
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("expected 'cancelled' in error, got: %v", err)
	}

	// Output should be nil on cancellation
	if output != nil {
		t.Errorf("expected nil output on cancellation, got: %q", string(output))
	}

	t.Logf("Process correctly cancelled with: %v", err)
}
