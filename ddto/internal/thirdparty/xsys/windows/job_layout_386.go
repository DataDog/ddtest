// Copyright 2011 The Go Authors. All rights reserved.
// Adapted from golang.org/x/sys v0.47.0; BSD license in ../LICENSE.

//go:build windows && 386

package windows

type JOBOBJECT_BASIC_LIMIT_INFORMATION struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
	_                       uint32 // pad to 8 byte boundary
}
