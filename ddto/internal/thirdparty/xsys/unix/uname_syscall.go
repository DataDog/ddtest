//go:build linux || aix

package unix

import "syscall"

// ReadKernelInfo reads the platform kernel identity without changing its formatting.
func ReadKernelInfo() (KernelInfo, error) {
	var uts syscall.Utsname
	if err := syscall.Uname(&uts); err != nil {
		return KernelInfo{}, err
	}
	return KernelInfo{utsString(uts.Sysname[:]), utsString(uts.Release[:]), utsString(uts.Version[:])}, nil
}
