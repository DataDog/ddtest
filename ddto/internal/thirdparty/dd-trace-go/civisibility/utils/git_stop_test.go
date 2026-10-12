// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

package utils

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils/telemetry"
)

// TestFakeGitWaitsForInterruptHelperProcess is a git command that runs until
// it is interrupted, recording that it started and that it was interrupted.
func TestFakeGitWaitsForInterruptHelperProcess(t *testing.T) {
	marker := os.Getenv("DD_TEST_FAKE_GIT_INTERRUPT")
	if marker == "" {
		t.Skip("helper process")
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM)
	_ = os.WriteFile(marker, []byte("started\n"), 0o600)
	<-signals
	if file, err := os.OpenFile(marker, os.O_APPEND|os.O_WRONLY, 0); err == nil {
		_, _ = file.WriteString("interrupted\n")
		_ = file.Close()
	}
	os.Exit(0)
}

// When ddto's Git worker stops, a running git command is interrupted, so git
// can remove its lock files, rather than killed; later commands fail at once.
func TestGitCommandStopsWithInterrupt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows cannot interrupt another process")
	}
	resetGitCommandCachesForTesting(t)
	marker := filepath.Join(t.TempDir(), "signal")
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, args := gitExecutable, gitExecutableArgs
	gitExecutable, gitExecutableArgs = testBinary, []string{"-test.run=^TestFakeGitWaitsForInterruptHelperProcess$", "--"}
	t.Cleanup(func() { gitExecutable, gitExecutableArgs = executable, args })
	t.Setenv("DD_TEST_FAKE_GIT_INTERRUPT", marker)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	SetGitCommandContext(ctx)
	t.Cleanup(func() { SetGitCommandContext(context.Background()) })

	done := make(chan error, 1)
	go func() {
		_, err := execGit(telemetry.NotSpecifiedCommandsType, "fetch")
		done <- err
	}()
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(10 * time.Millisecond) {
		if data, _ := os.ReadFile(marker); strings.HasPrefix(string(data), "started") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the git command never started")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(gitStopDelay + 30*time.Second):
		t.Fatal("the stopped git command did not return")
	}
	if data, _ := os.ReadFile(marker); string(data) != "started\ninterrupted\n" {
		t.Fatalf("git was not interrupted before it was killed: %q", data)
	}
	started := time.Now()
	if _, err := execGit(telemetry.NotSpecifiedCommandsType, "status"); err == nil || time.Since(started) > time.Second {
		t.Fatalf("a git command after the stop ran: %v after %s", err, time.Since(started))
	}
}
