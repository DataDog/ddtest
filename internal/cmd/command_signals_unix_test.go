//go:build darwin || linux

package cmd

import (
	"context"
	"errors"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestCommandSignalContextHandlesUnixCancellationSignals(t *testing.T) {
	for _, sig := range []os.Signal{syscall.SIGHUP, syscall.SIGQUIT} {
		t.Run(sig.String(), func(t *testing.T) {
			ctx, stop := commandSignalContext(context.Background())
			defer stop()

			process, err := os.FindProcess(os.Getpid())
			if err != nil {
				t.Fatalf("find current process: %v", err)
			}
			if err := process.Signal(sig); err != nil {
				t.Fatalf("send %s: %v", sig, err)
			}

			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
				t.Fatalf("context was not cancelled by %s", sig)
			}

			var cancellation commandSignalCancellation
			if !errors.As(context.Cause(ctx), &cancellation) {
				t.Fatalf("context cause = %v, want commandSignalCancellation", context.Cause(ctx))
			}
			if cancellation.Signal() != sig {
				t.Fatalf("cancellation signal = %v, want %v", cancellation.Signal(), sig)
			}
		})
	}
}
