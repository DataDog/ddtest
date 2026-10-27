//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package runner

import (
	"errors"
	"os"
	"syscall"
)

// tryLockFile takes an advisory lock on the whole file without waiting. Locks
// belong to the open file, so separate opens conflict even within one process;
// closing releases.
func tryLockFile(file *os.File, exclusive bool) error {
	how := syscall.LOCK_SH | syscall.LOCK_NB
	if exclusive {
		how = syscall.LOCK_EX | syscall.LOCK_NB
	}
	for {
		err := syscall.Flock(int(file.Fd()), how)
		switch {
		case errors.Is(err, syscall.EINTR):
			continue
		case errors.Is(err, syscall.EWOULDBLOCK):
			return errLockBusy
		}
		return err
	}
}
