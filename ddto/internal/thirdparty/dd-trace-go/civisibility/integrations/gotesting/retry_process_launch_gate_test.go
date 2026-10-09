// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/testassert/require"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/constants"
)

func TestRunProcessRetryAttemptRechecksCancellationAfterLaunchGateWait(t *testing.T) {
	for _, expired := range []bool{false, true} {
		name := "cancel_after_gate_wait"
		if expired {
			name = "deadline_before_gate_wait"
		}
		t.Run(name, func(t *testing.T) {
			resetProcessRetryLimiterForTesting(t)
			restoreLaunchGate := resetProcessRetryLaunchGateForTesting(t)
			defer restoreLaunchGate()
			releaseGate := sync.OnceFunc(holdProcessRetryLaunchGateForTesting(t))

			ctx, cancel := context.WithCancel(context.Background())
			gateWaitEntered := make(chan struct{})
			allowGateWait := make(chan struct{})
			resumeGateWait := sync.OnceFunc(func() { close(allowGateWait) })
			startContext := &processRetryBlockingDoneContext{
				Context: ctx,
				entered: gateWaitEntered,
				release: allowGateWait,
			}
			base := time.Unix(1_700_000_000, 0)
			timeout := make(chan time.Time)
			if expired {
				close(timeout)
			}
			startCalls := atomic.Int32{}
			baseline := &processRetryLaunchBaseline{
				hooks: processRetryRunnerHooks{
					command:     exec.Command,
					prepareTree: noopProcessRetryTree,
					startAndWait: func(*exec.Cmd) (<-chan error, error) {
						startCalls.Add(1)
						return nil, errors.New("unexpected child start")
					},
					releaseTree: noopProcessRetryTree,
					now:         func() time.Time { return base },
					// Keep timer delivery consistent with the frozen clock.
					newTimer: func(time.Duration) processRetryTimer {
						return &processRetryStaticTimer{ch: timeout}
					},
				},
				executable:       os.Args[0],
				workingDirectory: ".",
				timeout:          time.Second,
				timeoutSet:       true,
			}
			attemptResult := make(chan processRetryAttemptResult, 1)
			attemptFinished := make(chan struct{})
			defer func() {
				cancel()
				releaseGate()
				resumeGateWait()
				<-attemptFinished
			}()
			go func() {
				defer close(attemptFinished)
				attempt := runProcessRetryAttemptWithBaseline(startContext, processRetryChildConfig{
					TestName:    "TestCancellationAfterLaunchGateWait",
					Attempt:     1,
					RetryReason: constants.AutoTestRetriesRetryReason,
				}, time.Time{}, false, baseline)
				if attempt.Cleanup != nil {
					attempt.Cleanup()
				}
				attemptResult <- attempt
			}()

			var attempt processRetryAttemptResult
			select {
			case <-gateWaitEntered:
				require.False(t, expired, "expired attempt must not reach the launch gate")
				// Done blocks and returns nil: only reopening the gate wakes
				// the launch loop, which must then recheck context cancellation.
				cancel()
				releaseGate()
				resumeGateWait()
				attempt = <-attemptResult
			case attempt = <-attemptResult:
				require.True(t, expired, "attempt returned before waiting at the launch gate: %v", attempt.Err)
			}
			require.True(t, attempt.SetupFailure)
			require.Equal(t, expired, attempt.TimedOut)
			if expired {
				require.ErrorIs(t, attempt.Err, context.DeadlineExceeded)
			} else {
				require.ErrorIs(t, attempt.Err, errProcessRetryLaunchCanceled)
				require.ErrorIs(t, attempt.Err, context.Canceled)
			}
			require.Zero(t, startCalls.Load())
		})
	}
}

func resetProcessRetryLaunchGateForTesting(t testing.TB) func() {
	t.Helper()
	processRetryLaunchGate.mu.Lock()
	oldDisabled := processRetryLaunchGate.disabled.Load()
	oldReaping := processRetryLaunchGate.reaping
	oldLaunching := processRetryLaunchGate.launching
	oldActiveGroups := processRetryLaunchGate.activeGroups
	oldActiveChildren := processRetryLaunchGate.activeChildren
	oldShuttingDown := processRetryLaunchGate.shuttingDown.Load()
	oldShutdown := processRetryLaunchGate.shutdown
	oldChanged := processRetryLaunchGate.changed
	oldWaiters := processRetryLaunchGate.waiters
	processRetryLaunchGate.disabled.Store(false)
	processRetryLaunchGate.reaping = 0
	processRetryLaunchGate.launching = 0
	processRetryLaunchGate.activeGroups = 0
	processRetryLaunchGate.activeChildren = 0
	processRetryLaunchGate.shuttingDown.Store(false)
	processRetryLaunchGate.shutdown = make(chan struct{})
	processRetryLaunchGate.changed = make(chan struct{})
	processRetryLaunchGate.waiters = 0
	processRetryLaunchGate.mu.Unlock()
	return func() {
		processRetryLaunchGate.mu.Lock()
		processRetryLaunchGate.disabled.Store(oldDisabled)
		processRetryLaunchGate.reaping = oldReaping
		processRetryLaunchGate.launching = oldLaunching
		processRetryLaunchGate.activeGroups = oldActiveGroups
		processRetryLaunchGate.activeChildren = oldActiveChildren
		processRetryLaunchGate.shuttingDown.Store(oldShuttingDown)
		processRetryLaunchGate.shutdown = oldShutdown
		processRetryLaunchGate.changed = oldChanged
		processRetryLaunchGate.waiters = oldWaiters
		processRetryLaunchGate.mu.Unlock()
	}
}

func resetProcessRetryLimiterForTesting(t testing.TB) {
	t.Helper()
	old := globalProcessRetryLimiter.Swap(&processRetryLimiter{})
	t.Cleanup(func() {
		globalProcessRetryLimiter.Store(old)
	})
}

func holdProcessRetryLaunchGateForTesting(t *testing.T) func() {
	t.Helper()
	reapWaitEntered := make(chan struct{}, 1)
	reapTimeout := make(chan time.Time)
	waitCh := make(chan error, 1)
	reapResult := make(chan error, 1)
	go func() {
		reapResult <- waitForProcessRetryReapAfterKill(processRetryRunnerHooks{
			after: func(time.Duration) <-chan time.Time {
				reapWaitEntered <- struct{}{}
				return reapTimeout
			},
		}, waitCh, &processRetryAttemptResult{})
	}()
	<-reapWaitEntered

	return func() {
		waitCh <- nil
		require.NoError(t, <-reapResult)
	}
}

type processRetryStaticTimer struct {
	ch <-chan time.Time
}

func (t *processRetryStaticTimer) C() <-chan time.Time { return t.ch }
func (t *processRetryStaticTimer) Stop() bool          { return true }

type processRetryBlockingDoneContext struct {
	context.Context
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *processRetryBlockingDoneContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	<-c.release
	return nil
}
