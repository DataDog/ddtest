// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

//go:build unix && !linux

package net

// processEnded cannot tell a zombie from a running process on this platform:
// a zombie owner then costs its waiters the wait for a running one.
func processEnded(int) bool {
	return false
}
