package gotesting

import (
	"sync"
	_ "unsafe" // go:linkname
)

// testingParallelStop records the end of a parallel test in testing's own
// accounting, as testing's tRunner does. ddtest's testing overlay registers it
// while testing initializes, when the toolchain keeps that accounting; without
// the overlay it stays nil.
var testingParallelStop func()

// registerTestingParallelStop is called by ddtest's testing overlay.
//
//go:linkname registerTestingParallelStop
func registerTestingParallelStop(stop func()) { testingParallelStop = stop }

// sharedParallelLeaseAttempts holds attempts that went parallel after an
// earlier attempt had moved the logical test's parallel lease to the original
// test. Each entry is removed when its attempt publishes its result.
var sharedParallelLeaseAttempts sync.Map // *retryAttemptRoot -> struct{}

// releaseSharedParallelLease runs when an attempt goes parallel after the
// original already holds the lease. It returns the scheduler slot the attempt
// took, as before. testing also counted the attempt as a started parallel
// test, but only the original's tRunner records an end, for the attempt that
// moved the lease; this attempt records its own end when it publishes.
func releaseSharedParallelLease(r *retryAttemptRoot) {
	testingTestStateRelease(getTestState(r.test))
	sharedParallelLeaseAttempts.Store(r, struct{}{})
}

// publishRetryAttemptResult hands an attempt's result to its caller, after
// recording the end of a parallel attempt that shared the lease, as tRunner
// does before releasing its caller. Otherwise testing.AllocsPerRun panics in
// every later test of the binary.
func publishRetryAttemptResult(attempt *retryAttemptRoot, resultCh chan<- retryAttemptResult, result retryAttemptResult) {
	if _, shared := sharedParallelLeaseAttempts.LoadAndDelete(attempt); shared && testingParallelStop != nil {
		testingParallelStop()
	}
	resultCh <- result
}
