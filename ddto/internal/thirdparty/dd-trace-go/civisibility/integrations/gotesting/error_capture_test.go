// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/integrations"
)

// errorfLike calls the hook as testing.common.Errorf does, one frame below
// the test.
func errorfLike(tb testing.TB, errType, message string) string {
	return instrumentCaptureFormattedError(tb, errType, message, 0)
}

//go:noinline
func atStackDepth(depth int, fn func()) {
	if depth == 0 {
		fn()
		return
	}
	atStackDepth(depth-1, fn)
}

func enableCIVisibilityForTest(t testing.TB) {
	previous := atomic.LoadInt32(&ciVisibilityEnabledValue)
	atomic.StoreInt32(&ciVisibilityEnabledValue, 1)
	t.Cleanup(func() { atomic.StoreInt32(&ciVisibilityEnabledValue, previous) })
}

// The first formatted error of a test is kept with the stack of its caller;
// later errors change nothing.
func TestCaptureFormattedErrorKeepsFirstError(t *testing.T) {
	enableCIVisibilityForTest(t)
	tb := &testing.T{}
	execMeta := createTestMetadata(tb, nil)
	defer deleteTestMetadata(tb)

	if got := errorfLike(tb, "Error", "first\n"); got != "first\n" {
		t.Fatalf("formatted text changed to %q", got)
	}
	first := execMeta.processRetryError.Load()
	if first == nil || first.Type != "Error" || first.Message != "first" ||
		!strings.Contains(first.Stack, "TestCaptureFormattedErrorKeepsFirstError") {
		t.Fatalf("first error %#v", first)
	}
	for _, message := range []string{"second\n", "third\n"} {
		if got := errorfLike(tb, "Fatal", message); got != message {
			t.Fatalf("formatted text changed to %q", got)
		}
	}
	if execMeta.processRetryError.Load() != first {
		t.Fatal("a later error replaced the first")
	}
}

// Concurrent first errors still record exactly one, which never changes.
func TestCaptureFormattedErrorConcurrentFirstWins(t *testing.T) {
	enableCIVisibilityForTest(t)
	tb := &testing.T{}
	execMeta := createTestMetadata(tb, nil)
	defer deleteTestMetadata(tb)

	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errorfLike(tb, "Error", "concurrent\n")
		}()
	}
	wg.Wait()
	first := execMeta.processRetryError.Load()
	if first == nil || first.Message != "concurrent" || first.Stack == "" {
		t.Fatalf("first error %#v", first)
	}
	errorfLike(tb, "Error", "late\n")
	if execMeta.processRetryError.Load() != first {
		t.Fatal("a later error replaced the first")
	}
}

// A process-retry child records the first error of its owner the same way.
func TestRecordProcessRetryChildErrorInfoKeepsFirstError(t *testing.T) {
	owner, child := &testing.T{}, &testing.T{}
	ownerMeta := createTestMetadata(owner, nil)
	defer deleteTestMetadata(owner)
	childMeta := createTestMetadata(child, nil)
	defer deleteTestMetadata(child)
	childMeta.processRetryOwner = ownerMeta

	recordProcessRetryChildErrorInfo(child, "Error", "first", 0)
	first := ownerMeta.processRetryError.Load()
	if first == nil || first.Message != "first" || !strings.Contains(first.Stack, "TestRecordProcessRetryChildErrorInfoKeepsFirstError") {
		t.Fatalf("first error %#v", first)
	}
	recordProcessRetryChildErrorInfo(child, "Fatal", "second", 0)
	if ownerMeta.processRetryError.Load() != first || childMeta.processRetryError.Load() != nil {
		t.Fatal("the first owner error changed or moved")
	}
}

type errorCaptureStubTest struct {
	integrations.Test
	errors int
}

func (s *errorCaptureStubTest) Name() string { return "stub" }

func (s *errorCaptureStubTest) SetError(...integrations.ErrorOption) { s.errors++ }

func (s *errorCaptureStubTest) Close(integrations.TestResultStatus, ...integrations.TestCloseOption) {
}

// instrumentSetErrorInfo reuses the stack recorded with a formatted error
// instead of capturing another one. Each frame of a captured stack allocates.
func TestSetErrorInfoCapturesStackOnlyWithoutFormattedError(t *testing.T) {
	enableCIVisibilityForTest(t)
	run := func(formatted bool) float64 {
		stub := &errorCaptureStubTest{}
		allocs := testing.AllocsPerRun(50, func() {
			tb := &testing.T{}
			execMeta := createTestMetadata(tb, nil)
			execMeta.test = stub
			if formatted {
				execMeta.processRetryError.Store(&processRetryErrorInfo{Type: "Error", Message: "m", Stack: "recorded"})
			}
			atStackDepth(20, func() { instrumentSetErrorInfo(tb, "Fail", "failed test", 0) })
			deleteTestMetadata(tb)
		})
		if stub.errors == 0 {
			t.Fatal("no error was set")
		}
		return allocs
	}
	captured, reused := run(false), run(true)
	if reused+20 > captured {
		t.Fatalf("formatted error allocated %.0f times, a captured stack %.0f", reused, captured)
	}
}

// BenchmarkCaptureFormattedErrorRepeated is a failing test's second and
// later t.Errorf calls.
func BenchmarkCaptureFormattedErrorRepeated(b *testing.B) {
	enableCIVisibilityForTest(b)
	tb := &testing.T{}
	createTestMetadata(tb, nil)
	defer deleteTestMetadata(tb)
	errorfLike(tb, "Error", "first\n")
	b.ReportAllocs()
	for b.Loop() {
		atStackDepth(10, func() { errorfLike(tb, "Error", "again\n") })
	}
}
