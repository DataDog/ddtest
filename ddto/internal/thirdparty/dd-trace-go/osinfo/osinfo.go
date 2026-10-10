// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package osinfo

import (
	"runtime"
	"sync"
)

// Modified by the platform's detect function to provide OS-specific information
var (
	osName        = runtime.GOOS
	osVersion     = "unknown"
	arch          = runtime.GOARCH
	kernelName    = "unknown"
	kernelRelease = "unknown"
	kernelVersion = "unknown"

	// detectOnce runs detect on first use instead of at package initialization,
	// so a process that never reports OS metadata reads no files and starts
	// no process for it.
	detectOnce sync.Once
)

// load fills the OS metadata once.
func load() { detectOnce.Do(detect) }

// OSName returns the name of the operating system, including the distribution
// for Linux when possible.
func OSName() string {
	// call out to OS-specific implementation
	load()
	return osName
}

// OSVersion returns the operating system release, e.g. major/minor version
// number and build ID.
func OSVersion() string {
	// call out to OS-specific implementation
	load()
	return osVersion
}

// Architecture returns the architecture of the operating system.
func Architecture() string {
	return arch
}

// KernelName returns the name of the kernel.
func KernelName() string {
	load()
	return kernelName
}

// KernelRelease returns the release of the kernel.
func KernelRelease() string {
	load()
	return kernelRelease
}

// KernelVersion returns the version of the kernel.
func KernelVersion() string {
	load()
	return kernelVersion
}
