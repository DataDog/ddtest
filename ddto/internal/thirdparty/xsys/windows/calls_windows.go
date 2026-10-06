// Copyright 2011 The Go Authors. All rights reserved.
// Adapted from golang.org/x/sys v0.47.0; BSD license in ../LICENSE.

//go:build windows

// Package windows implements only the native operations required by CI retries.
package windows

import (
	"syscall"
	"unsafe"
)

type Handle = syscall.Handle
type SecurityAttributes = syscall.SecurityAttributes

const (
	CREATE_SUSPENDED                   = 0x00000004
	PROCESS_SET_QUOTA                  = 0x0100
	PROCESS_TERMINATE                  = syscall.PROCESS_TERMINATE
	THREAD_SUSPEND_RESUME              = 0x0002
	TH32CS_SNAPTHREAD                  = syscall.TH32CS_SNAPTHREAD
	JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE = 0x00002000
	JobObjectExtendedLimitInformation  = 9
	ERROR_NO_MORE_FILES                = syscall.ERROR_NO_MORE_FILES
)

func CloseHandle(handle Handle) error { return syscall.CloseHandle(handle) }
func OpenProcess(access uint32, inherit bool, pid uint32) (Handle, error) {
	return syscall.OpenProcess(access, inherit, pid)
}
func CreateToolhelp32Snapshot(flags uint32, pid uint32) (Handle, error) {
	return syscall.CreateToolhelp32Snapshot(flags, pid)
}

func errnoErr(err syscall.Errno) error {
	if err == 0 {
		return syscall.EINVAL
	}
	return err
}

var kernel32 = NewLazySystemDLL("kernel32.dll")
var (
	procAssignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
	procCreateJobObjectW         = kernel32.NewProc("CreateJobObjectW")
	procOpenThread               = kernel32.NewProc("OpenThread")
	procResumeThread             = kernel32.NewProc("ResumeThread")
	procSetInformationJobObject  = kernel32.NewProc("SetInformationJobObject")
	procTerminateJobObject       = kernel32.NewProc("TerminateJobObject")
	procThread32First            = kernel32.NewProc("Thread32First")
	procThread32Next             = kernel32.NewProc("Thread32Next")
)

type ThreadEntry32 struct {
	Size           uint32
	Usage          uint32
	ThreadID       uint32
	OwnerProcessID uint32
	BasePri        int32
	DeltaPri       int32
	Flags          uint32
}

type IO_COUNTERS struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

type JOBOBJECT_EXTENDED_LIMIT_INFORMATION struct {
	BasicLimitInformation JOBOBJECT_BASIC_LIMIT_INFORMATION
	IoInfo                IO_COUNTERS
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

func AssignProcessToJobObject(job Handle, process Handle) (err error) {
	r1, _, e1 := syscall.SyscallN(procAssignProcessToJobObject.Addr(), uintptr(job), uintptr(process))
	if r1 == 0 {
		err = errnoErr(e1)
	}
	return
}

func CreateJobObject(jobAttr *SecurityAttributes, name *uint16) (handle Handle, err error) {
	r0, _, e1 := syscall.SyscallN(procCreateJobObjectW.Addr(), uintptr(unsafe.Pointer(jobAttr)), uintptr(unsafe.Pointer(name)))
	handle = Handle(r0)
	if handle == 0 {
		err = errnoErr(e1)
	}
	return
}

func OpenThread(desiredAccess uint32, inheritHandle bool, threadId uint32) (handle Handle, err error) {
	var _p0 uint32
	if inheritHandle {
		_p0 = 1
	}
	r0, _, e1 := syscall.SyscallN(procOpenThread.Addr(), uintptr(desiredAccess), uintptr(_p0), uintptr(threadId))
	handle = Handle(r0)
	if handle == 0 {
		err = errnoErr(e1)
	}
	return
}

func ResumeThread(thread Handle) (ret uint32, err error) {
	r0, _, e1 := syscall.SyscallN(procResumeThread.Addr(), uintptr(thread))
	ret = uint32(r0)
	if ret == 0xffffffff {
		err = errnoErr(e1)
	}
	return
}

func SetInformationJobObject(job Handle, JobObjectInformationClass uint32, JobObjectInformation uintptr, JobObjectInformationLength uint32) (ret int, err error) {
	r0, _, e1 := syscall.SyscallN(procSetInformationJobObject.Addr(), uintptr(job), uintptr(JobObjectInformationClass), uintptr(JobObjectInformation), uintptr(JobObjectInformationLength))
	ret = int(r0)
	if ret == 0 {
		err = errnoErr(e1)
	}
	return
}

func TerminateJobObject(job Handle, exitCode uint32) (err error) {
	r1, _, e1 := syscall.SyscallN(procTerminateJobObject.Addr(), uintptr(job), uintptr(exitCode))
	if r1 == 0 {
		err = errnoErr(e1)
	}
	return
}

func Thread32First(snapshot Handle, threadEntry *ThreadEntry32) (err error) {
	r1, _, e1 := syscall.SyscallN(procThread32First.Addr(), uintptr(snapshot), uintptr(unsafe.Pointer(threadEntry)))
	if r1 == 0 {
		err = errnoErr(e1)
	}
	return
}

func Thread32Next(snapshot Handle, threadEntry *ThreadEntry32) (err error) {
	r1, _, e1 := syscall.SyscallN(procThread32Next.Addr(), uintptr(snapshot), uintptr(unsafe.Pointer(threadEntry)))
	if r1 == 0 {
		err = errnoErr(e1)
	}
	return
}
