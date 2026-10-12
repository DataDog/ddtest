// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/minitracer"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/constants"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/integrations"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/ddtrace/ext"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/locking"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"
)

// fuzzEventQueue belongs to one instrumentation claim. Admission can run in
// parallel seeds; normal draining starts only after native M.Run returns. Fatal
// shutdown drains completed executions before the session's close actions.
type fuzzEventQueue struct {
	mu             locking.Mutex
	events         []*fuzzTestEvent // +checklocks:mu
	finished       bool             // +checklocks:mu
	nativeFinished bool             // +checklocks:mu
}

type fuzzTestEvent struct {
	native          testing.TB
	metadata        *testExecutionMetadata
	test            integrations.Test
	suite           integrations.TestSuite
	module          integrations.TestModule
	finishTime      time.Time
	failed, skipped bool // raw outcome captured before Test Management masks it
	closeContainers bool
}

func (q *fuzzEventQueue) add(event fuzzTestEvent) *fuzzTestEvent {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.finished {
		// A fatal close may overlap a seed completing. Preserve its captured
		// outcome rather than adding references to an already drained queue.
		event.finish(q.nativeFinished)
		minitracer.Flush()
		if event.closeContainers {
			checkModuleAndSuite(event.module, event.suite)
		}
		return &event
	}
	q.events = append(q.events, &event)
	return &event
}

// complete freezes a root's raw outcome before native Test Management masking.
// Roots are admitted before calling F.Fuzz so a fatal seed also closes its root.
func (q *fuzzEventQueue) complete(event *fuzzTestEvent, failed, skipped bool, finishTime time.Time) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.finished {
		return
	}
	event.failed = failed
	event.skipped = skipped
	event.finishTime = finishTime
}

func (q *fuzzEventQueue) finish() {
	q.drain(false)
}

// finishAfterNativeRun is the only path allowed to read native durations. Go
// writes them without common.mu; M.Run returning provides the required barrier.
func (q *fuzzEventQueue) finishAfterNativeRun() {
	q.drain(true)
}

func (q *fuzzEventQueue) drain(nativeFinished bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.finished {
		return
	}
	q.finished = true
	q.nativeFinished = nativeFinished
	for i := range q.events {
		q.events[i].finish(nativeFinished)
		// CI Flush drains the tracer's accepted chunks before returning. Keep
		// each burst below its 1,000-entry shared queue; no sleeps or worker
		// scheduling assumptions are needed for a large deferred corpus.
		if (i+1)%256 == 0 || i == len(q.events)-1 {
			minitracer.Flush()
		}
	}
	// Close containers only after every queued test event has been emitted.
	for i := range q.events {
		if q.events[i].closeContainers {
			checkModuleAndSuite(q.events[i].module, q.events[i].suite)
		}
		q.events[i] = nil
	}
	q.events = nil
}

// nativeResult reads Go's stored result, never Failed's process-wide race check.
// Fatal shutdown can read an active root's already-propagated seed failure, but
// must not read duration: Go writes it without holding common.mu. timed reports
// whether duration is Go's.
func (e *fuzzTestEvent) nativeResult(nativeFinished bool) (failed, skipped bool, duration time.Duration, timed bool) {
	var t *testing.T
	switch native := e.native.(type) {
	case *testing.T:
		t = native
	case *testing.F:
		t = (*testing.T)(unsafe.Pointer(native))
	default:
		return
	}
	layout := getTestingInternalsLayout()
	var base unsafe.Pointer
	if native, ok := e.native.(*testing.F); ok {
		base = fuzzCommonBase(native, layout)
	} else {
		base = commonBaseForTest(t, layout)
	}
	if base != nil && allAvailable(layout.common.mu, layout.common.failed, layout.common.skipped, layout.common.duration) {
		mu := fieldPtr[sync.RWMutex](base, layout.common.mu)
		mu.RLock()
		failed = *fieldPtr[bool](base, layout.common.failed)
		skipped = *fieldPtr[bool](base, layout.common.skipped)
		mu.RUnlock()
		// common.duration is written without mu. Only M.Run's completion barrier
		// permits this read; fatal drains keep the independently captured finish time.
		if nativeFinished {
			duration, timed = *fieldPtr[time.Duration](base, layout.common.duration), true
		}
		runtime.KeepAlive(e.native)
		return
	}
	fields := getTestPrivateFields(t)
	if fields == nil || fields.mu == nil {
		log.Debug("civisibility: native fuzz result layout unavailable; retaining captured outcome and finish time")
		return
	}
	fields.mu.RLock()
	if fields.failed != nil {
		failed = *fields.failed
	}
	if fields.skipped != nil {
		skipped = *fields.skipped
	}
	fields.mu.RUnlock()
	if nativeFinished {
		durationPtr, err := getFieldPointerFromWithType(e.native, "duration", reflect.TypeFor[time.Duration]())
		if err != nil {
			log.Debug("civisibility: native fuzz duration field unavailable; retaining captured finish time")
			return
		}
		duration, timed = *(*time.Duration)(durationPtr), true
	}
	return
}

func (e *fuzzTestEvent) finish(nativeFinished bool) {
	failed, skipped, duration, timed := e.nativeResult(nativeFinished)
	failed = failed || e.failed
	// A managed pass/failure is changed to native skip by design. That skip
	// must not replace the raw event status captured before masking.
	skipped = e.skipped || skipped && !e.metadata.isQuarantined && !e.metadata.isDisabled
	finishFuzzTestEvent(failed, skipped, e.metadata, e.test, e.suite, e.module, fuzzFinishTime(e.test.StartTime(), e.finishTime, duration, timed))
}

// fuzzFinishTime ends an event at its start plus the duration that Go
// printed, when Go's duration is known, even if it is zero; Go measures it on
// its own clock and excludes the wait for parallel seeds. Otherwise it keeps
// the finish time captured by the wrapper, or now when a seed panic aborted
// its root before the wrapper completed.
func fuzzFinishTime(start, captured time.Time, duration time.Duration, timed bool) time.Time {
	if timed {
		return start.Add(max(duration, 0))
	}
	if captured.IsZero() {
		return time.Now()
	}
	return captured
}

func finishFuzzTestEvent(failed, skipped bool, execMeta *testExecutionMetadata, test integrations.Test, suite integrations.TestSuite, module integrations.TestModule, finishTime time.Time) {
	status := integrations.ResultStatusPass
	if failed {
		status = integrations.ResultStatusFail
		if captured := execMeta.processRetryError.Load(); captured != nil {
			test.SetError(integrations.WithErrorInfo(captured.Type, captured.Message, captured.Stack))
		} else if execMeta.panicData != nil {
			test.SetError(integrations.WithErrorInfo("panic", fmt.Sprint(execMeta.panicData), execMeta.panicStacktrace))
		} else {
			test.SetTag(ext.Error, true)
		}
		suite.SetTag(ext.Error, true)
		module.SetTag(ext.Error, true)
	} else if skipped {
		status = integrations.ResultStatusSkip
	}
	test.SetTag(constants.TestFinalStatus, calculateFinalStatus(!failed && !skipped, failed, skipped, execMeta.isQuarantined, execMeta.isDisabled, execMeta.isAttemptToFix))
	reason := execMeta.skipReason
	if reason == "" {
		if captured := execMeta.processRetrySkipReason.Load(); captured != nil {
			reason = *captured
		}
	}
	test.Close(status, integrations.WithTestFinishTime(finishTime), integrations.WithTestSkipReason(reason))
}
