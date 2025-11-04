package ext

import (
	"context"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestDefaultCommandExecutor_CombinedOutput_Success(t *testing.T) {
	executor := &DefaultCommandExecutor{}

	// Test with echo command (available on all Unix systems)
	output, err := executor.CombinedOutput(context.Background(), "echo", []string{"hello world"}, nil)

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
	output, err := executor.CombinedOutput(context.Background(), "ls", []string{"-1", "."}, nil)

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
	output, err := executor.CombinedOutput(context.Background(), "ls", []string{"/nonexistent/directory/path"}, nil)

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

	// Test with true command (should return immediately with no output)
	output, err := executor.CombinedOutput(context.Background(), "true", []string{}, nil)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Should be empty output
	if len(output) != 0 {
		t.Errorf("expected empty output, got %q", string(output))
	}
}

func TestDefaultCommandExecutor_CombinedOutput_WithEnvMap(t *testing.T) {
	executor := &DefaultCommandExecutor{}

	// Test with environment variable
	envMap := map[string]string{
		"TEST_VAR": "test_value",
	}
	output, err := executor.CombinedOutput(context.Background(), "sh", []string{"-c", "echo $TEST_VAR"}, envMap)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expected := "test_value\n"
	actual := string(output)
	if actual != expected {
		t.Errorf("expected %q, got %q", expected, actual)
	}
}

func TestDefaultCommandExecutor_Run_Success(t *testing.T) {
	executor := &DefaultCommandExecutor{}

	// Test with simple echo command
	err := executor.Run(context.Background(), "echo", []string{"test"}, nil)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestDefaultCommandExecutor_Run_CommandFailure(t *testing.T) {
	executor := &DefaultCommandExecutor{}

	// Test with a command that will fail
	err := executor.Run(context.Background(), "ls", []string{"/nonexistent/directory/path"}, nil)

	if err == nil {
		t.Error("expected error for nonexistent directory")
	}
}

func TestDefaultCommandExecutor_Run_WithEnvMap(t *testing.T) {
	executor := &DefaultCommandExecutor{}

	// Test with environment variable
	envMap := map[string]string{
		"TEST_VAR": "test_value",
	}
	err := executor.Run(context.Background(), "sh", []string{"-c", "test \"$TEST_VAR\" = \"test_value\""}, envMap)

	if err != nil {
		t.Fatalf("expected no error (environment variable should be set), got %v", err)
	}
}

func TestDefaultCommandExecutor_Run_ProcessCompletesNormally(t *testing.T) {
	executor := &DefaultCommandExecutor{}

	// Test that normal completion works
	start := time.Now()
	err := executor.Run(context.Background(), "sleep", []string{"0.1"}, nil)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Should complete quickly
	if duration > 2*time.Second {
		t.Errorf("process took too long: %v", duration)
	}
}

func TestDefaultCommandExecutor_Run_ContextCancellation(t *testing.T) {
	executor := &DefaultCommandExecutor{}

	// Create a context that we can cancel
	ctx, cancel := context.WithCancel(context.Background())

	// Cancel the context after a short delay
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	// Run a long-running command - it should be cancelled by context
	err := executor.Run(ctx, "sleep", []string{"30"}, nil)

	// The process should have been cancelled
	if err == nil {
		t.Fatal("expected error from cancelled process")
	}

	t.Logf("Process correctly cancelled with: %v", err)
}

func TestDefaultCommandExecutor_CombinedOutput_ContextCancellation(t *testing.T) {
	executor := &DefaultCommandExecutor{}

	// Create a context that we can cancel
	ctx, cancel := context.WithCancel(context.Background())

	// Cancel the context after a short delay
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	// Run a long-running command - it should be cancelled by context
	output, err := executor.CombinedOutput(ctx, "sleep", []string{"30"}, nil)

	// The process should have been cancelled
	if err == nil {
		t.Fatal("expected error from cancelled process")
	}

	// Output should be empty on cancellation (we didn't capture anything)
	if len(output) > 0 {
		t.Logf("Got output on cancellation: %q", string(output))
	}

	t.Logf("Process correctly cancelled with: %v", err)
}

func TestDefaultCommandExecutor_CombinedOutput_ContextTimeout(t *testing.T) {
	executor := &DefaultCommandExecutor{}

	// Create a context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Run a long-running command - it should timeout
	output, err := executor.CombinedOutput(ctx, "sleep", []string{"30"}, nil)

	// The process should have timed out
	if err == nil {
		t.Fatal("expected error from timed out process")
	}

	// Output should be empty on timeout
	if len(output) > 0 {
		t.Logf("Got output on timeout: %q", string(output))
	}

	t.Logf("Process correctly timed out with: %v", err)
}

func TestDefaultCommandExecutor_Run_ContextTimeout(t *testing.T) {
	executor := &DefaultCommandExecutor{}

	// Create a context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Run a long-running command - it should timeout
	err := executor.Run(ctx, "sleep", []string{"30"}, nil)

	// The process should have timed out
	if err == nil {
		t.Fatal("expected error from timed out process")
	}

	t.Logf("Process correctly timed out with: %v", err)
}

func TestDefaultCommandExecutor_Run_SignalForwarding(t *testing.T) {
	executor := &DefaultCommandExecutor{}

	// Send SIGINT to the test process itself after a delay
	// The executor should forward this to the child sleep process
	go func() {
		time.Sleep(200 * time.Millisecond)
		pid := os.Getpid()
		process, err := os.FindProcess(pid)
		if err != nil {
			t.Errorf("failed to find test process: %v", err)
			return
		}
		if err := process.Signal(syscall.SIGINT); err != nil {
			t.Errorf("failed to send SIGINT to test process: %v", err)
		}
	}()

	// Run a long-running command - it should be interrupted by the signal forwarding
	err := executor.Run(context.Background(), "sleep", []string{"30"}, nil)

	// The process should have been interrupted
	if err == nil {
		t.Fatal("expected error from interrupted process")
	}

	t.Logf("Process correctly interrupted with: %v", err)
}

func TestDefaultCommandExecutor_Run_SignalForwardingSIGTERM(t *testing.T) {
	executor := &DefaultCommandExecutor{}

	// Send SIGTERM to the test process itself after a delay
	// The executor should forward this to the child sleep process
	go func() {
		time.Sleep(200 * time.Millisecond)
		pid := os.Getpid()
		process, err := os.FindProcess(pid)
		if err != nil {
			t.Errorf("failed to find test process: %v", err)
			return
		}
		if err := process.Signal(syscall.SIGTERM); err != nil {
			t.Errorf("failed to send SIGTERM to test process: %v", err)
		}
	}()

	// Run a long-running command - it should be terminated by the signal forwarding
	err := executor.Run(context.Background(), "sleep", []string{"30"}, nil)

	// The process should have been terminated
	if err == nil {
		t.Fatal("expected error from terminated process")
	}

	t.Logf("Process correctly terminated with: %v", err)
}
