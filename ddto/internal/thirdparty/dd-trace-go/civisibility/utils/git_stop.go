// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

package utils

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"sync/atomic"
	"syscall"
	"time"
)

// gitCommandContext stops the git commands of ddto's Git worker when ddto
// asks it to stop. Other processes never set it, and their commands run as
// before.
var gitCommandContext atomic.Pointer[context.Context]

// gitStopDelay is how long a stopped git command has to exit after its
// interrupt, which lets git remove its lock files, before it is killed.
var gitStopDelay = 3 * time.Second

// SetGitCommandContext makes this process's git commands stop when ctx is
// done: a running command receives an interrupt and is killed after
// gitStopDelay, and later commands fail at once. ddto's Git worker sets it
// before its first git command.
func SetGitCommandContext(ctx context.Context) {
	gitCommandContext.Store(&ctx)
}

// newGitCommand creates a git command, stoppable if SetGitCommandContext set
// a context that can be done.
func newGitCommand(args []string) *exec.Cmd {
	ctx := gitCommandContext.Load()
	if ctx == nil || (*ctx).Done() == nil {
		return exec.Command(gitExecutable, args...)
	}
	cmd := exec.CommandContext(*ctx, gitExecutable, args...)
	cmd.Cancel = func() error { return interruptProcess(cmd.Process) }
	cmd.WaitDelay = gitStopDelay
	return cmd
}

// interruptProcess asks a process to exit: SIGTERM, which git handles by
// removing its lock files. Windows has no such signal for another process.
func interruptProcess(process *os.Process) error {
	if runtime.GOOS == "windows" {
		return process.Kill()
	}
	return process.Signal(syscall.SIGTERM)
}
