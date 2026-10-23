//go:build linux || darwin

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"syscall"
)

func lockWorkflowFile(file *os.File) error {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err == unix.EWOULDBLOCK {
		return errWorkflowBusy
	}
	return err
}

// workflowLockParent is fixed, not TMPDIR: invocations of one checkout with
// different TMPDIR values must exclude each other, because recovery scans the
// shared process-owner storage under /tmp (processSocketBase) and the
// repository's probe records. Tests point it at a private directory.
var workflowLockParent = "/tmp"

func workflowLockDirectory(string) string {
	return filepath.Join(workflowLockParent, fmt.Sprintf("ddtest-workflow-locks-%d", os.Getuid()))
}
func ownedByCurrentUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Getuid())
}
func privateDirectoryPermissions(info os.FileInfo) bool { return info.Mode().Perm() == 0700 }
func openWorkflowFile(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}
func releaseWorkflowFile(file *os.File) error {
	current, err := os.Lstat(file.Name())
	info, statErr := file.Stat()
	if err == nil && statErr == nil && os.SameFile(info, current) {
		err = os.Remove(file.Name())
	}
	if os.IsNotExist(err) {
		err = nil
	}
	return errors.Join(err, statErr, file.Close())
}
