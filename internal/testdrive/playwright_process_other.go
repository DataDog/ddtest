//go:build !(aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris)

package testdrive

import "fmt"

func stopPlaywrightProcess(pid int) error {
	return fmt.Errorf("automatic cleanup of detached Playwright server %d is not supported on this OS", pid)
}
