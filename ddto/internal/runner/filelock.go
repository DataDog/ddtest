package runner

import (
	"context"
	"errors"
	"os"
	"time"
)

// errLockBusy reports that another open file holds a conflicting lock.
var errLockBusy = errors.New("file lock is held by another process")

// waitForSharedLock retries a nonblocking request instead of waiting in the
// kernel, so cancellation or an interrupt can stop preparation. Pruners hold
// the exclusive lock only while they remove a workspace.
func waitForSharedLock(ctx context.Context, file *os.File) error {
	delay := time.Millisecond
	for {
		err := tryLockFile(file, false)
		if !errors.Is(err, errLockBusy) {
			return err
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		delay = min(2*delay, 50*time.Millisecond)
	}
}
