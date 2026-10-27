// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package workspace

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ErrBusy reports that another ddtest invocation holds the repository lock.
var ErrBusy = errors.New("another ddtest workflow is active")

// OS locks are released after a crash. Unix releases remove the file while
// holding its lock; acquisition verifies the pathname still names that inode.
// Windows keeps one stable file per checkout because open locks cannot be unlinked.
type Lock struct {
	*os.File
	once sync.Once
	err  error
}

// Close releases the lock; closing an earlier lock never releases a later one.
func (lock *Lock) Close() error {
	lock.once.Do(func() { lock.err = releaseLockFile(lock.File) })
	return lock.err
}

// AcquireLock excludes other ddtest invocations for the same checkout. It
// returns ErrBusy while another invocation holds the lock.
func AcquireLock(root string) (*Lock, error) {
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	directory := lockDirectory(canonical)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	if !PrivateOwnedDirectory(info) {
		return nil, fmt.Errorf("workflow lock storage must be a private directory owned by the current user")
	}
	path := filepath.Join(directory, fmt.Sprintf("%x.lock", sha256.Sum256([]byte(canonical))))
	for range 8 {
		file, err := openLockFile(path)
		if err != nil {
			return nil, err
		}
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || !OwnedByCurrentUser(info) {
			_ = file.Close()
			return nil, fmt.Errorf("workflow lock must be a regular file owned by the current user")
		}
		if err := lockFile(file); err != nil {
			_ = file.Close()
			return nil, err
		}
		current, err := os.Lstat(path)
		if err == nil && os.SameFile(info, current) {
			return &Lock{File: file}, nil
		}
		_ = file.Close()
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("workflow lock changed repeatedly; retry after other invocations finish")
}

// PrivateOwnedDirectory reports a real directory only the current user can use.
func PrivateOwnedDirectory(info os.FileInfo) bool {
	return info.IsDir() && info.Mode()&os.ModeSymlink == 0 && privateDirectoryPermissions(info) && OwnedByCurrentUser(info)
}
