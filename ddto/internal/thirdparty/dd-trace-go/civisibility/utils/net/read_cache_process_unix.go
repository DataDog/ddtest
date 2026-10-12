// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

//go:build unix

package net

import (
	"errors"
	"syscall"
)

// processAlive reports whether the process pid runs, and whether that is
// known: a signal 0 checks for the process without signaling it, and EPERM
// means that it exists under another user. A process that ended but that its
// parent has not reaped also accepts the signal; processEnded tells it apart
// where the platform can.
func processAlive(pid int) (alive bool, known bool) {
	if pid <= 0 {
		return false, false
	}
	switch err := syscall.Kill(pid, 0); {
	case err == nil, errors.Is(err, syscall.EPERM):
		return !processEnded(pid), true
	case errors.Is(err, syscall.ESRCH):
		return false, true
	}
	return false, false
}
