//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd || windows)

package runner

import (
	"errors"
	"os"
)

// Without file locks, vendor workspaces stay in each run's own directory.
func tryLockFile(*os.File, bool) error { return errors.ErrUnsupported }
