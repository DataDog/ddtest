package runner

import (
	"os"
	"syscall"
	"unsafe"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/xsys/windows"
)

var procLockFileEx = windows.NewLazySystemDLL("kernel32.dll").NewProc("LockFileEx")

const (
	lockfileFailImmediately = 0x1
	lockfileExclusiveLock   = 0x2
	errorLockViolation      = syscall.Errno(33)
)

// tryLockFile locks the file's first byte without waiting. Windows locks
// belong to the handle, so separate opens conflict even within one process;
// closing releases.
func tryLockFile(file *os.File, exclusive bool) error {
	flags := uintptr(lockfileFailImmediately)
	if exclusive {
		flags |= lockfileExclusiveLock
	}
	overlapped := new(syscall.Overlapped)
	r1, _, err := syscall.SyscallN(procLockFileEx.Addr(), file.Fd(), flags, 0, 1, 0, uintptr(unsafe.Pointer(overlapped)))
	if r1 != 0 {
		return nil
	}
	if err == errorLockViolation {
		return errLockBusy
	}
	return err
}
