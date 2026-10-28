package workspace

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
)

func cloneFile(source, target string) (bool, error) {
	in, err := os.Open(source)
	if err != nil {
		return false, err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return false, err
	}
	cloneErr := unix.IoctlFileClone(int(out.Fd()), int(in.Fd()))
	closeErr := out.Close()
	if cloneErr == nil {
		return true, closeErr
	}
	if err := os.Remove(target); err != nil {
		return false, errors.Join(cloneErr, closeErr, err)
	}
	return false, closeErr
}

// FreeSpace reports the bytes available to the current user at path.
func FreeSpace(path string) (uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return stat.Bavail * uint64(stat.Bsize), nil
}
