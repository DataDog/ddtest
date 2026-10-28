package runner

import "syscall"

// glibc's execvp (posix/execvpe.c) searches defaultPath, confstr(_CS_PATH),
// without PATH.
const defaultPath = "/bin:/usr/bin"

// searchName is glibc's error for a name that it does not search PATH for:
// one longer than NAME_MAX.
func searchName(file string) error {
	if len(file) > 255 {
		return syscall.ENAMETOOLONG
	}
	return nil
}

// entryPath returns the candidate that glibc tries for file in a PATH entry:
// file itself in an empty entry, the current directory. glibc skips an entry
// too long for its buffer, of Linux's PATH_MAX.
func entryPath(dir, file string) (string, bool) {
	if len(dir) >= 4096 {
		return "", false
	}
	if dir == "" {
		return file, true
	}
	return dir + "/" + file, true
}

// searchNext reports whether glibc goes on to the next PATH entry after err,
// and whether err reports a candidate that could not run.
func searchNext(_ string, err error) (next, denied bool) {
	switch err {
	case syscall.EACCES:
		return true, true
	case syscall.ENOENT, syscall.ESTALE, syscall.ENOTDIR, syscall.ENODEV, syscall.ETIMEDOUT:
		return true, false
	}
	return false, false
}

// notFound is glibc's error when no candidate started: the last one's.
func notFound(last error) error {
	return last
}
