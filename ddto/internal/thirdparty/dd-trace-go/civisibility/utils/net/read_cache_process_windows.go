// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

package net

import (
	"errors"
	"syscall"
)

// processQueryLimitedInformation is PROCESS_QUERY_LIMITED_INFORMATION.
const processQueryLimitedInformation = 0x1000

// processAlive reports whether the process pid runs, and whether that is
// known: a process that cannot be opened for lack of access exists, and a
// zero wait on its handle tells whether it ended, whatever its exit code.
func processAlive(pid int) (alive bool, known bool) {
	if pid <= 0 {
		return false, false
	}
	handle, err := syscall.OpenProcess(processQueryLimitedInformation|syscall.SYNCHRONIZE, false, uint32(pid))
	switch {
	case errors.Is(err, syscall.ERROR_ACCESS_DENIED):
		return true, true
	case errors.Is(err, syscall.Errno(87)): // ERROR_INVALID_PARAMETER: no such process
		return false, true
	case err != nil:
		return false, false
	}
	defer syscall.CloseHandle(handle)
	switch event, err := syscall.WaitForSingleObject(handle, 0); {
	case err != nil:
		return false, false
	case event == syscall.WAIT_OBJECT_0:
		return false, true
	case event == syscall.WAIT_TIMEOUT:
		return true, true
	}
	return false, false
}
