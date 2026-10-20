// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var errWorkflowBusy = errors.New("another ddtest workflow is active")

// OS locks are released even after a crash. The empty coordination file lives
// outside the checkout and is reusable; never unlink a potentially locked inode.
func acquireWorkflowLock(root string) (*os.File, error) {
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	directory := filepath.Join(os.TempDir(), fmt.Sprintf("ddtest-workflow-locks-%d", os.Getuid()))
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("workflow lock storage is not a real directory")
	}
	path := filepath.Join(directory, fmt.Sprintf("%x.lock", sha256.Sum256([]byte(canonical))))
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := lockWorkflowFile(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}
