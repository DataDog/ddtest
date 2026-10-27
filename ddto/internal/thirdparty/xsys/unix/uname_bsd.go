// Copyright 2009 The Go Authors. All rights reserved.
// Fixed-buffer Uname queries adapted from x/sys v0.47.0; BSD license in ../LICENSE.

//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package unix

import (
	"runtime"
	"syscall"
	"unsafe"
)

// ReadKernelInfo reads the platform kernel identity without changing its formatting.
func ReadKernelInfo() (KernelInfo, error) { return kernelInfoFromSysctl(readKernelField, runtime.GOOS) }

// Read once into the caller's fixed buffer, retaining partial bytes on ENOMEM.
// OpenBSD's public Syscall6 routes SYS___SYSCTL through its libc compatibility
// stub; the other targets expose the same native syscall through Syscall6.
func readKernelField(key string, buffer []byte) error {
	mib, ok := kernelFieldMIB(key)
	if !ok || len(buffer) == 0 {
		return syscall.EINVAL
	}
	n := uintptr(len(buffer))
	_, _, errno := syscall.Syscall6(syscall.SYS___SYSCTL,
		uintptr(unsafe.Pointer(&mib[0])), uintptr(len(mib)),
		uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&n)), 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}
