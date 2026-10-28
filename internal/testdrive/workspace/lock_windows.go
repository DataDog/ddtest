// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package workspace

import (
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
)

func lockFile(file *os.File) error {
	err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
	if err == windows.ERROR_LOCK_VIOLATION {
		return ErrBusy
	}
	return err
}

func lockDirectory(root string) string {
	return filepath.Join(root, ".testoptimization", ".workflow-locks")
}
func OwnedByCurrentUser(os.FileInfo) bool          { return true } // Access is inherited from the user's checkout/temporary directory.
func privateDirectoryPermissions(os.FileInfo) bool { return true } // Unix mode bits do not describe Windows ACLs.
func openLockFile(path string) (*os.File, error) {
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, os.ErrPermission
	}
	return os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
}
func releaseLockFile(file *os.File) error { return file.Close() }
