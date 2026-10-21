package ext

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCombinedOutputInDirectoryAndConfiguredTimeout(t *testing.T) {
	if mode := os.Getenv("DDTEST_DIRECTORY_HELPER"); mode != "" {
		if mode == "wait" {
			time.Sleep(time.Minute)
			return
		}
		directory, err := os.Getwd()
		if err != nil {
			os.Exit(3)
		}
		fmt.Print(directory + "|" + os.Getenv("DDTEST_DIRECTORY_VALUE"))
		return
	}
	directory := t.TempDir()
	executor := &DefaultCommandExecutor{Timeout: 5 * time.Second}
	output, err := executor.CombinedOutputInDirectory(context.Background(), directory, os.Args[0], []string{"-test.run=^TestCombinedOutputInDirectoryAndConfiguredTimeout$"}, map[string]string{"DDTEST_DIRECTORY_HELPER": "cwd", "DDTEST_DIRECTORY_VALUE": "overridden"})
	if err != nil {
		t.Fatalf("directory invocation: %v: %s", err, output)
	}
	canonical, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if canonical == directory {
		t.Fatal("executor changed parent working directory")
	}
	expected, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output), expected+"|overridden") {
		t.Fatalf("wrong directory/environment: %s", output)
	}
	executor.Timeout = 200 * time.Millisecond
	for _, run := range []func() error{
		func() error {
			_, err := executor.CombinedOutput(context.Background(), os.Args[0], []string{"-test.run=^TestCombinedOutputInDirectoryAndConfiguredTimeout$"}, map[string]string{"DDTEST_DIRECTORY_HELPER": "wait"})
			return err
		},
		func() error {
			_, _, err := executor.Output(context.Background(), os.Args[0], []string{"-test.run=^TestCombinedOutputInDirectoryAndConfiguredTimeout$"}, map[string]string{"DDTEST_DIRECTORY_HELPER": "wait"})
			return err
		},
		func() error {
			return executor.Run(context.Background(), os.Args[0], []string{"-test.run=^TestCombinedOutputInDirectoryAndConfiguredTimeout$"}, map[string]string{"DDTEST_DIRECTORY_HELPER": "wait"})
		},
	} {
		started := time.Now()
		err := run()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("want deadline exceeded, got %v", err)
		}
		if time.Since(started) > 2*time.Second {
			t.Fatal("deadline did not bound command")
		}
	}
}
