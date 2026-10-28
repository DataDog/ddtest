// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

package utils

import (
	"errors"
	"syscall"
)

// transientStateFileError reports whether opening a state file failed only
// because another process holds it without sharing it, for a moment: the CLI
// writes each file under a temporary name and renames it, and the rename, or
// a scanner that opens the new file, can hold it as a reader opens it.
func transientStateFileError(err error) bool {
	return errors.Is(err, errorSharingViolation)
}

// errorSharingViolation is Windows' ERROR_SHARING_VIOLATION, which package
// syscall does not name.
const errorSharingViolation syscall.Errno = 32
