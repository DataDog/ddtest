package coverage

import (
	"runtime"
	"slices"
	"sync/atomic"
	"testing"
	"unsafe"
)

// contextHarness stands in for the runtime's edits: one goroutine, whose
// context and call set the test switches, and the scratch window.
type contextHarness struct {
	t              *testing.T
	current, calls unsafe.Pointer
	names          []string
	base           uintptr
	scratch        [8]uint64
}

// withContextCoverage registers one context bank of names and makes context
// coverage available, restoring the process's state afterwards.
func withContextCoverage(t *testing.T, names ...string) *contextHarness {
	t.Helper()
	h := &contextHarness{t: t, names: names}
	saved := struct {
		words          uint64
		banks, unknown unsafe.Pointer
		get            func() (unsafe.Pointer, unsafe.Pointer)
		set            func(unsafe.Pointer, unsafe.Pointer)
		send           func(*uintptr, unsafe.Pointer) unsafe.Pointer
		receive        func(uintptr, unsafe.Pointer, unsafe.Pointer) (unsafe.Pointer, unsafe.Pointer, unsafe.Pointer)
		io             func(unsafe.Pointer) unsafe.Pointer
		uploadEnabled  bool
		previousMode   string
	}{fileCoverageWords, fileCoverageBanksV2, fileCoverageUnknown, fileCoverageGetState, fileCoverageSetState, fileCoverageSendHook, fileCoverageReceiveHook, fileCoverageIOHook, coverageUploadEnabled, mode}
	t.Cleanup(func() {
		fileCoverageWords, fileCoverageBanksV2, fileCoverageUnknown = saved.words, saved.banks, saved.unknown
		fileCoverageGetState, fileCoverageSetState = saved.get, saved.set
		fileCoverageSendHook, fileCoverageReceiveHook, fileCoverageIOHook = saved.send, saved.receive, saved.io
		coverageUploadEnabled, mode = saved.uploadEnabled, saved.previousMode
		contextCoverage = contextState{}
	})
	contextCoverage = contextState{}
	fileCoverageWords = uint64(len(names)+63) / 64
	fileCoverageUnknown = unsafe.Pointer(&h.scratch[0])
	fileCoverageGetState = func() (unsafe.Pointer, unsafe.Pointer) { return h.current, h.calls }
	fileCoverageSetState = func(ctx, calls unsafe.Pointer) { h.current, h.calls = ctx, calls }
	bank := &fileCoverageContextBank{version: fileCoverageContextBankVersion, base: &h.base, n: uintptr(len(names)), prefix: "example.com/p", path: "example.com/p"}
	if len(names) > 0 {
		bank.names = &names[0]
	}
	fileCoverageBanksV2 = unsafe.Pointer(bank)
	coverageUploadEnabled, mode = true, ""
	if !FileCoverageActive() || !contextCoverageAvailable() {
		t.Fatal("context file coverage is not active with a bank")
	}
	return h
}

// hit sets file i's bit in the current goroutine's file set, as a site does.
func (h *contextHarness) hit(i int) {
	c := h.current
	if c == nil {
		c = atomic.LoadPointer(&fileCoverageUnknown)
	}
	setBit(unsafe.Slice((*uint64)(c), contextCoverage.words), i)
}

// send and receive run the runtime's hooks for a channel's union.
func (h *contextHarness) send(slot *uintptr) {
	state := h.current
	if state == nil {
		state = h.calls
	}
	if state != nil {
		fileCoverageSendHook(slot, state)
	}
}

func (h *contextHarness) receive(slot *uintptr) {
	if *slot != 0 {
		h.current, h.calls, _ = fileCoverageReceiveHook(*slot, h.current, h.calls)
	}
}

// in runs f as a goroutine that starts with ctx and no call set, and returns
// its context afterwards.
func (h *contextHarness) in(ctx unsafe.Pointer, f func()) unsafe.Pointer {
	ctx, _ = h.inState(ctx, nil, f)
	return ctx
}

// inState runs f as a goroutine that starts with ctx and calls, and returns
// its context and call set afterwards.
func (h *contextHarness) inState(ctx, calls unsafe.Pointer, f func()) (unsafe.Pointer, unsafe.Pointer) {
	savedCtx, savedCalls := h.current, h.calls
	h.current, h.calls = ctx, calls
	f()
	ctx, calls = h.current, h.calls
	h.current, h.calls = savedCtx, savedCalls
	return ctx, calls
}

func (h *contextHarness) files(t *testing.T, epoch uint64) []string {
	t.Helper()
	got := scanContextCoverage(epoch)
	slices.Sort(got)
	return got
}

func TestContextCoverageAttributesToEachTest(t *testing.T) {
	h := withContextCoverage(t, "/repo/a.go", "/repo/b.go", "/repo/c.go")
	a := beginContextCoverage()
	ctxA := h.current
	b := beginContextCoverage()
	ctxB := h.current
	h.in(ctxA, func() { h.hit(0) })
	h.in(ctxB, func() { h.hit(1) })
	// A goroutine without a context runs for every running test.
	h.in(nil, func() { h.hit(2) })
	if got := h.files(t, a); !slices.Equal(got, []string{"/repo/a.go", "/repo/c.go"}) {
		t.Errorf("A: %v", got)
	}
	if got := h.files(t, b); !slices.Equal(got, []string{"/repo/b.go", "/repo/c.go"}) {
		t.Errorf("B: %v", got)
	}
}

func TestContextCoverageMergesChannelUnions(t *testing.T) {
	h := withContextCoverage(t, "/repo/a.go", "/repo/b.go")
	a := beginContextCoverage()
	ctxA := h.current
	b := beginContextCoverage()
	ctxB := h.current
	var slot uintptr
	h.in(ctxB, func() { h.send(&slot) })
	// A receives B's work: what A runs next counts for both.
	merged := h.in(ctxA, func() {
		h.receive(&slot)
		h.hit(1)
	})
	if merged == ctxA {
		t.Fatal("the receive kept A's context")
	}
	if got := h.files(t, a); !slices.Equal(got, []string{"/repo/b.go"}) {
		t.Errorf("A: %v", got)
	}
	if got := h.files(t, b); !slices.Equal(got, []string{"/repo/b.go"}) {
		t.Errorf("B: %v", got)
	}
	// A receive that adds nothing keeps the context.
	h.in(ctxA, func() {
		var own uintptr
		h.send(&own)
		h.receive(&own)
		if h.current != ctxA {
			t.Error("a receive of A's own union changed A's context")
		}
	})
}

// A call's work reaches its joiners through a worker without a context and a
// second hop to a worker of another test.
func TestContextCoverageKeepsCallsAcrossUnknownWorkers(t *testing.T) {
	h := withContextCoverage(t, "/repo/f.go", "/repo/g.go")
	a := beginContextCoverage()
	ctxA := h.current
	d := beginContextCoverage()
	ctxD := h.current
	var call unsafe.Pointer
	var toWorker, toV uintptr
	leader := h.in(ctxA, func() {
		fileCoverageEnter(&call)
		h.send(&toWorker)
	})
	// W, started by TestMain, receives the call's work and hands it on.
	_, workerCalls := h.inState(nil, nil, func() {
		h.receive(&toWorker)
		h.send(&toV)
	})
	if workerCalls == nil {
		t.Fatal("the worker without a context dropped the call")
	}
	h.in(ctxD, func() {
		h.receive(&toV)
		h.hit(0)
	})
	h.in(leader, func() { fileCoverageLeave(&call) })
	b := beginContextCoverage()
	h.in(h.current, func() { fileCoverageJoin(&call) })
	h.in(leader, func() { fileCoverageComplete(&call) })
	if got := h.files(t, b); !slices.Contains(got, "/repo/f.go") {
		t.Errorf("the joiner lacks the call's file from the second hop: %v", got)
	}
	// The leader's test led the call: the work was for it too.
	if got := h.files(t, a); !slices.Contains(got, "/repo/f.go") {
		t.Errorf("the leader's test lacks the call's file from the second hop: %v", got)
	}
	h.files(t, d)
}

func TestContextCoverageIOKeepsCalls(t *testing.T) {
	h := withContextCoverage(t, "/repo/f.go")
	beginContextCoverage()
	var call unsafe.Pointer
	ctx, callSet := h.inState(h.current, nil, func() {
		fileCoverageEnter(&call)
		h.current, h.calls = nil, fileCoverageIOHook(h.current)
	})
	if set := contextCoverage.lookup(uintptr(callSet)); ctx != nil || set == nil || !set.callSet || len(set.sinks) != 1 || !set.sinks[0].call || !set.sinks[0].leaky {
		t.Fatalf("after IO: context %v, call set %+v", ctx, set)
	}
	// Without calls, the goroutine loses its context.
	if next := h.in(nil, func() {}); next != nil {
		t.Fatal("a goroutine without a context gained one")
	}
	beginContextCoverage()
	if calls := fileCoverageIOHook(h.current); calls != nil {
		t.Fatal("IO kept a test in a call set")
	}
}

func TestContextCoverageOverflow(t *testing.T) {
	h := withContextCoverage(t, "/repo/a.go")
	var slot uintptr
	var epochs []uint64
	for range maxContextSinks + 1 {
		epochs = append(epochs, beginContextCoverage())
		h.send(&slot)
	}
	if contextCoverage.lookup(slot) != contextCoverage.overflow {
		t.Fatal("the union did not overflow")
	}
	ctx := h.in(h.current, func() { h.receive(&slot) })
	if ctx != nil {
		t.Fatal("the receiver of an overflowed union kept a context")
	}
	h.in(nil, func() { h.hit(0) })
	for _, epoch := range epochs {
		if got := h.files(t, epoch); !slices.Contains(got, "/repo/a.go") {
			t.Errorf("test %d lacks the file of an overflowed union's receiver: %v", epoch, got)
		}
	}
}

// Contexts and windows of finished tests are dropped, so a long run keeps
// what its running tests need.
func TestContextCoverageRetention(t *testing.T) {
	h := withContextCoverage(t, "/repo/a.go")
	var slot uintptr
	for range 1000 {
		epoch := beginContextCoverage()
		h.send(&slot)
		h.hit(0)
		h.files(t, epoch)
	}
	if n, w := len(contextCoverage.byKey), len(contextCoverage.windows); n > 3 || w > 1 {
		t.Fatalf("after 1000 tests: %d contexts, %d windows", n, w)
	}
}

// A call that joins itself, or two calls that join each other, are dropped
// once retired and no live test reaches them.
func TestContextCoverageReclaimsCallCycles(t *testing.T) {
	h := withContextCoverage(t, "/repo/a.go")
	epoch := beginContextCoverage()
	var c, d unsafe.Pointer
	h.in(h.current, func() {
		fileCoverageEnter(&c)
		fileCoverageJoin(&c)
		fileCoverageEnter(&d)
		fileCoverageJoin(&c)
		fileCoverageLeave(&d)
		fileCoverageJoin(&d)
		fileCoverageLeave(&c)
	})
	fileCoverageComplete(&c)
	fileCoverageComplete(&d)
	h.files(t, epoch)
	if n := len(contextCoverage.calls); n != 0 {
		t.Fatalf("%d calls left after their test finished", n)
	}
}

// A joiner that may work for anyone gets the call's files in the window
// before the call's waiters return.
func TestContextCoverageUnknownJoiner(t *testing.T) {
	h := withContextCoverage(t, "/repo/f.go")
	a := beginContextCoverage()
	var call unsafe.Pointer
	leader := h.in(h.current, func() { fileCoverageEnter(&call) })
	b := beginContextCoverage()
	h.in(nil, func() { fileCoverageJoin(&call) })
	h.in(leader, func() {
		h.hit(0)
		fileCoverageLeave(&call)
	})
	fileCoverageComplete(&call)
	if got := h.files(t, b); !slices.Contains(got, "/repo/f.go") {
		t.Errorf("a test running while an unknown goroutine joined lacks the call's file: %v", got)
	}
	h.files(t, a)
}

// A merge credits the files of the receiver's stack: here, this file's,
// which a bank names. A frame of an instrumented package whose file maps to
// none credits every file.
func TestContextCoverageRepairsTheStack(t *testing.T) {
	_, self, _, _ := runtime.Caller(0)
	h := withContextCoverage(t, self, "/repo/other.go")
	a := beginContextCoverage()
	ctxA := h.current
	b := beginContextCoverage()
	var slot uintptr
	h.send(&slot)
	h.in(ctxA, func() { h.receive(&slot) })
	if got := h.files(t, b); !slices.Equal(got, []string{self}) {
		t.Errorf("B after A received its work: %v", got)
	}
	h.files(t, a)
	contextCoverage.symbols[symbolPrefix(funcName(t))] = true
	delete(contextCoverage.files, self)
	target := make([]uint64, contextCoverage.words)
	contextCoverage.repair(target)
	if target[0] != ^uint64(0) {
		t.Errorf("an unmapped frame of an instrumented package credited %#x", target[0])
	}
}

func funcName(t *testing.T) string {
	pc, _, _, _ := runtime.Caller(1)
	return runtime.FuncForPC(pc).Name()
}

func TestSymbolPrefix(t *testing.T) {
	for function, want := range map[string]string{
		"example.com/m.v2/dot%2epkg_test.TestWhere": "example.com/m.v2/dot%2epkg_test",
		"example.com/p.(*T).M":                      "example.com/p",
		"example.com/p.F[...]":                      "example.com/p",
		"example.com/p.F.func1":                     "example.com/p",
		"runtime.goexit":                            "runtime",
		"example.com/p.G[go.shape.struct { x.T }]":  "example.com/p",
	} {
		if got := symbolPrefix(function); got != want {
			t.Errorf("symbolPrefix(%q) = %q, want %q", function, got, want)
		}
	}
}

// A joiner without a context, which runs for every test, gets the windows
// that a leaky call's work went to before the joiner's test started.
func TestContextCoverageUnknownJoinerGetsLeakyWindows(t *testing.T) {
	h := withContextCoverage(t, "/repo/f.go")
	a := beginContextCoverage()
	var call unsafe.Pointer
	_, leader := h.inState(nil, nil, func() { fileCoverageEnter(&call) })
	h.inState(nil, leader, func() { h.hit(0) })
	h.files(t, a)
	b := beginContextCoverage()
	h.inState(nil, nil, func() { fileCoverageJoin(&call) })
	h.inState(nil, leader, func() {
		fileCoverageLeave(&call)
		fileCoverageComplete(&call)
	})
	if got := h.files(t, b); !slices.Contains(got, "/repo/f.go") {
		t.Errorf("a test running while an unknown goroutine joined lacks the leaked work: %v", got)
	}
}

// Calls sent to an overflowed union saturate: their joiners report every file.
func TestContextCoverageOverflowSaturates(t *testing.T) {
	h := withContextCoverage(t, "/repo/a.go", "/repo/b.go")
	var slot uintptr
	var epochs []uint64
	for range maxContextSinks + 1 {
		epochs = append(epochs, beginContextCoverage())
		h.send(&slot)
	}
	var call unsafe.Pointer
	leader := h.in(h.current, func() {
		fileCoverageEnter(&call)
		h.send(&slot)
	})
	joiner := beginContextCoverage()
	h.in(h.current, func() { fileCoverageJoin(&call) })
	h.in(leader, func() {
		fileCoverageLeave(&call)
		fileCoverageComplete(&call)
	})
	if got := h.files(t, joiner); len(got) != 2 {
		t.Errorf("the joiner of a saturated call reports %v", got)
	}
	for _, epoch := range epochs {
		h.files(t, epoch)
	}
}

// Singleflight before the banks are read, as in a package's init, changes
// nothing.
func TestContextCoverageSingleflightBeforeReady(t *testing.T) {
	h := withContextCoverage(t, "/repo/a.go")
	contextCoverage.ready.Store(false)
	var call unsafe.Pointer
	fileCoverageEnter(&call)
	fileCoverageJoin(&call)
	fileCoverageLeave(&call)
	fileCoverageComplete(&call)
	if call != nil || h.current != nil || h.calls != nil {
		t.Fatalf("before the banks were read: call %v, state %v %v", call, h.current, h.calls)
	}
}

// A call's function that joins another call in flight gives that call's
// files to its own joiners too.
func TestContextCoverageNestedCalls(t *testing.T) {
	h := withContextCoverage(t, "/repo/outer.go", "/repo/inner.go")
	a := beginContextCoverage()
	var outer, inner unsafe.Pointer
	innerLeader := h.in(h.current, func() {
		fileCoverageEnter(&inner)
		h.hit(1)
	})
	b := beginContextCoverage()
	h.in(h.current, func() {
		fileCoverageEnter(&outer)
		h.hit(0)
		fileCoverageJoin(&inner)
		fileCoverageLeave(&outer)
	})
	c := beginContextCoverage()
	h.in(h.current, func() { fileCoverageJoin(&outer) })
	h.in(innerLeader, func() {
		fileCoverageLeave(&inner)
		fileCoverageComplete(&inner)
	})
	fileCoverageComplete(&outer)
	if got := h.files(t, c); !slices.Equal(got, []string{"/repo/inner.go", "/repo/outer.go"}) {
		t.Errorf("the outer call's joiner: %v", got)
	}
	h.files(t, a)
	h.files(t, b)
}

// A leader whose function panics never leaves the call: what it runs next
// still counts for the call, a superset, and the joiner gets the function's
// files.
func TestContextCoveragePanickingLeader(t *testing.T) {
	h := withContextCoverage(t, "/repo/f.go", "/repo/after.go")
	a := beginContextCoverage()
	var call unsafe.Pointer
	leader := h.in(h.current, func() {
		fileCoverageEnter(&call)
		h.hit(0)
	})
	b := beginContextCoverage()
	h.in(h.current, func() { fileCoverageJoin(&call) })
	fileCoverageComplete(&call)
	h.in(leader, func() { h.hit(1) })
	if got := h.files(t, b); !slices.Equal(got, []string{"/repo/after.go", "/repo/f.go"}) {
		t.Errorf("the joiner of a call whose leader panicked: %v", got)
	}
	h.files(t, a)
}
