package testdrive

import (
	"fmt"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCopyRetriesWhenTheHostRunsOutOfFiles(t *testing.T) {
	delays := descriptorRetryDelays
	descriptorRetryDelays = []time.Duration{0, 0}
	t.Cleanup(func() { descriptorRetryDelays = delays })

	calls := 0
	require.NoError(t, copyWithDescriptorRetry(func() error {
		calls++
		if calls == 1 {
			return fmt.Errorf("open x: %w", syscall.ENFILE)
		}
		return nil
	}))
	require.Equal(t, 2, calls, "one transient exhaustion is retried")

	calls = 0
	err := copyWithDescriptorRetry(func() error { calls++; return fmt.Errorf("open x: %w", syscall.EMFILE) })
	require.True(t, fileDescriptorsExhausted(err))
	require.Equal(t, 3, calls, "persistent exhaustion still surfaces after the retries")

	calls = 0
	require.Error(t, copyWithDescriptorRetry(func() error { calls++; return syscall.EACCES }))
	require.Equal(t, 1, calls, "other errors are not retried")
}
