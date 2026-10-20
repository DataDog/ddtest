//go:build !(aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris)

package testdrive

import "fmt"

func waitOwnedProcessExit(pid int) error {
	return fmt.Errorf("automatic cleanup of owned process %d is not supported on this OS", pid)
}
