// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

package utils

import (
	"io/fs"
	"testing"
	"time"
)

// A state file that another process holds without sharing it, as during the
// CLI's rename, refuses to open with a sharing violation for a moment.
// WaitCLIGitStateFile waits for it instead of reporting it unavailable.
func TestWaitCLIGitStateFileWaitsForASharedFile(t *testing.T) {
	dir := t.TempDir()
	if err := WriteCLIGitStateFile(dir, CLIGitUploadFile, CLIGitUpload{Bytes: 7}); err != nil {
		t.Fatal(err)
	}
	held := 2
	t.Cleanup(func() { readCLIGitStateFile = ReadCLIGitStateFile })
	readCLIGitStateFile = func(dir, name string, value any) error {
		if held > 0 {
			held--
			return &fs.PathError{Op: "open", Path: name, Err: errorSharingViolation}
		}
		return ReadCLIGitStateFile(dir, name, value)
	}
	var upload CLIGitUpload
	if err := WaitCLIGitStateFile(dir, CLIGitUploadFile, &upload, 10*time.Second); err != nil || upload.Bytes != 7 || held != 0 {
		t.Fatalf("upload %+v, %v, %d holds left", upload, err, held)
	}
}
