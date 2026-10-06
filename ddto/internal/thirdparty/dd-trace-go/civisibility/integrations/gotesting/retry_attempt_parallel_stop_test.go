package gotesting

import (
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// testing counts each parallel attempt as a started parallel test, while the
// original's tRunner records one end, for the attempt that moved the lease to
// it. Every later parallel attempt records its own end; sequential attempts
// record nothing.
func TestSharedParallelLeaseAttemptsRecordTheirEnd(t *testing.T) {
	var stops atomic.Int32
	previous := testingParallelStop
	testingParallelStop = func() { stops.Add(1) }
	defer func() { testingParallelStop = previous }()

	t.Run("container", func(container *testing.T) {
		container.Run("attempt", func(original *testing.T) {
			group, reason := newRetryAttemptGroup(original)
			require.Empty(original, reason)
			defer group.retire()
			attempt := func(target func(*testing.T)) {
				root, result, reason := runFreshRetryAttemptInGroup(group, target)
				require.Empty(original, reason)
				require.NotNil(original, root)
				defer root.cancelContexts()
				require.False(original, result.failed)
			}
			parallel := func(local *testing.T) { local.Parallel() }

			attempt(parallel)
			require.Equal(original, int32(0), stops.Load(), "the lease owner's end belongs to the original")
			attempt(parallel)
			require.Equal(original, int32(1), stops.Load())
			attempt(parallel)
			require.Equal(original, int32(2), stops.Load())
		})
	})
	sharedParallelLeaseAttempts.Range(func(key, _ any) bool {
		t.Fatalf("attempt %p kept its shared-lease mark", key)
		return false
	})
}

func TestSequentialAttemptsRecordNoParallelEnd(t *testing.T) {
	var stops atomic.Int32
	previous := testingParallelStop
	testingParallelStop = func() { stops.Add(1) }
	defer func() { testingParallelStop = previous }()

	t.Run("container", func(container *testing.T) {
		container.Run("attempt", func(original *testing.T) {
			group, reason := newRetryAttemptGroup(original)
			require.Empty(original, reason)
			defer group.retire()
			for range 2 {
				root, result, reason := runFreshRetryAttemptInGroup(group, func(*testing.T) {})
				require.Empty(original, reason)
				require.NotNil(original, root)
				root.cancelContexts()
				require.False(original, result.failed)
			}
		})
	})
	require.Equal(t, int32(0), stops.Load())
}
