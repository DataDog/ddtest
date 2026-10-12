//go:build !linux && !windows

package runner

import (
	"fmt"
	"os"
	"syscall"
)

// The BSD libcs' execvp (gen/exec.c in FreeBSD and Apple's Libc) searches
// defaultPath, _PATH_DEFPATH, without PATH.
const defaultPath = "/usr/bin:/bin"

// searchName is nil: the BSD libcs search PATH for any name.
func searchName(string) error {
	return nil
}

// entryPath returns the candidate that the libc tries for file in a PATH
// entry: ./file in an empty entry, the current directory. The libc skips an
// entry whose candidate is too long for its buffer, of PATH_MAX, with this
// message.
func entryPath(dir, file string) (string, bool) {
	if dir == "" {
		dir = "."
	}
	if len(dir)+len(file)+2 > 1024 {
		fmt.Fprintf(os.Stderr, "execvP: %s: path too long\n", dir)
		return "", false
	}
	return dir + "/" + file, true
}

// searchNext reports whether the libc goes on to the next PATH entry after
// err, and whether err reports a candidate that could not run. It takes
// EACCES for the candidate's only if the candidate exists, not for a
// directory that cannot be searched.
func searchNext(candidate string, err error) (next, denied bool) {
	switch err {
	case syscall.E2BIG, syscall.ENOMEM, syscall.ETXTBSY:
		return false, false
	case syscall.ELOOP, syscall.ENAMETOOLONG, syscall.ENOENT, syscall.ENOTDIR:
		return true, false
	}
	if _, statErr := os.Stat(candidate); statErr != nil {
		return true, false
	}
	return err == syscall.EACCES, err == syscall.EACCES
}

// notFound is the libc's error when no candidate started.
func notFound(error) error {
	return syscall.ENOENT
}
