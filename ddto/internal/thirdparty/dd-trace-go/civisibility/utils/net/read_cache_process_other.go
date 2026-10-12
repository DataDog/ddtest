// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

//go:build !unix && !windows

package net

// processAlive cannot tell whether a process runs on this platform.
func processAlive(int) (alive bool, known bool) {
	return false, false
}
