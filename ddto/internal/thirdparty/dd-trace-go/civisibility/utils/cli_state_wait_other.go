// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

//go:build !windows

package utils

// transientStateFileError reports whether opening a state file failed only
// for a moment. Outside Windows, a rename replaces a file at once.
func transientStateFileError(error) bool { return false }
