// Copyright 2014 The Go Authors. All rights reserved.
// Adapted from golang.org/x/sys v0.47.0; BSD license in ../LICENSE.

//go:build solaris

package unix

import (
	"syscall"
	"unsafe"
)

// Solaris uname uses the same libc/runtime trampoline as the pinned x/sys.
//
//go:cgo_import_dynamic libc_uname uname "libc.so"
//go:linkname procUname libc_uname
var procUname uintptr

func rawSysvicall6(trap, nargs, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err syscall.Errno)

type solarisUtsname struct{ Sysname, Nodename, Release, Version, Machine [257]byte }

// ReadKernelInfo reads the platform kernel identity without changing its formatting.
func ReadKernelInfo() (KernelInfo, error) {
	var uts solarisUtsname
	_, _, err := rawSysvicall6(uintptr(unsafe.Pointer(&procUname)), 1, uintptr(unsafe.Pointer(&uts)), 0, 0, 0, 0, 0)
	if err != 0 {
		return KernelInfo{}, err
	}
	return KernelInfo{utsString(uts.Sysname[:]), utsString(uts.Release[:]), utsString(uts.Version[:])}, nil
}
