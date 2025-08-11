package ext

import (
	"os/exec"
	"strings"
	"testing"
)

func TestDefaultCommandExecutor_CombinedOutput_Success(t *testing.T) {
	executor := &DefaultCommandExecutor{}

	// Test with echo command (available on all Unix systems)
	cmd := exec.Command("echo", "hello world")
	output, err := executor.CombinedOutput(cmd)

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
	output, err := executor.CombinedOutput(cmd)

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
	output, err := executor.CombinedOutput(cmd)

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

	output, err := executor.CombinedOutput(cmd)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Should be empty output
	if len(output) != 0 {
		t.Errorf("expected empty output, got %q", string(output))
	}
}
