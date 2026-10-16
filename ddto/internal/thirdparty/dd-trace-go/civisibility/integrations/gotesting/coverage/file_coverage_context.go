package coverage

import (
	"math/bits"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"
)

// Context file coverage is ddto's --experimental-file-coverage=context. Each
// goroutine carries a context, the tests and singleflight calls (its sinks)
// that it works for, with one bit per instrumented file, which a site sets
// when it runs. ddto's edits of the runtime give a goroutine its creator's
// context and add a channel's senders to its receivers.
//
// A goroutine that reads a socket, pipe or terminal may work for anyone from
// then on, as may one that never had a context. It has no context, and its
// sites set bits in the window of the test that started last, which every
// test that runs meanwhile reads. It keeps the calls that it works for in a
// call set, which its sends carry on: such a call leaks to windows, and its
// joiners read the windows since it leaked.
//
// A test's files are those of every context that held it, or held a call
// that it joined, and of every window since it started or since such a call
// leaked. A call that lost track of its work, because a context or a union
// held too many sinks, is saturated: its joiners report every file.
//
// The runtime and the banks reach these variables through linkname. The
// runtime initializes fileCoverageUnknown and the two state functions, and
// calls the hooks, which this package sets before a test can start.
var (
	fileCoverageWords       uint64
	fileCoverageBanksV2     unsafe.Pointer
	fileCoverageUnknown     unsafe.Pointer
	fileCoverageGetState    func() (unsafe.Pointer, unsafe.Pointer)
	fileCoverageSetState    func(unsafe.Pointer, unsafe.Pointer)
	fileCoverageSendHook    func(slot *uintptr, state unsafe.Pointer) (sent unsafe.Pointer)
	fileCoverageReceiveHook func(union uintptr, ctx, calls unsafe.Pointer) (nextCtx, nextCalls, seen unsafe.Pointer)
	fileCoverageIOHook      func(ctx unsafe.Pointer) (calls unsafe.Pointer)
)

// fileCoverageContextBankVersion is the layout of fileCoverageContextBank,
// which ddto's generated code repeats
// (instrument.FileCoverageContextBankVersion).
const fileCoverageContextBankVersion = 2

// fileCoverageContextBank is one package variant's record. Its files' bits
// start at word *base; prefix is the package's symbol prefix, and path the
// import path that -trimpath gives its directory.
type fileCoverageContextBank struct {
	next    unsafe.Pointer
	version uintptr
	base    *uintptr
	names   *string
	n       uintptr
	always  *string
	nAlways uintptr
	prefix  string
	path    string
}

// maxContextSinks bounds a context, a call set and a channel's union.
const maxContextSinks = 32

// A contextSink is a test or a singleflight call.
type contextSink struct {
	id   uint64
	call bool
	// epoch is the current window's when a test starts.
	epoch uint64
	// dead: a test after its capture; a call once retired, when no live test
	// and no call in flight reaches it through joins.
	dead    bool
	retired bool
	// A call leaks once a goroutine without a context works for it, from the
	// window leakyEpoch on. It is saturated once a context or a union with
	// too many sinks dropped it. unknownJoined: a goroutine without a
	// context joined it.
	leaky         bool
	leakyEpoch    uint64
	saturated     bool
	unknownJoined bool
	// joined are the calls that this sink joined, and contexts those that
	// hold it.
	joined   []*contextSink
	contexts []*fileContext
}

// A fileContext is an interned set of sinks. A goroutine's context has file
// bits, and the goroutine points at their first word. A call set holds the
// calls of a goroutine without a context, and its one word only names it.
type fileContext struct {
	bits    []uint64
	sinks   []*contextSink
	callSet bool
	key     string
}

func (c *fileContext) pointer() unsafe.Pointer {
	if c == nil {
		return nil
	}
	return unsafe.Pointer(&c.bits[0])
}

type fileWindow struct {
	epoch uint64
	bits  []uint64
}

type contextState struct {
	once      sync.Once
	available bool
	// ready is set once the banks are read, after packages initialized: the
	// singleflight edits do nothing before.
	ready atomic.Bool

	// Set once, read without the lock.
	words   int
	names   []string
	always  []string
	files   map[string]int
	symbols map[string]bool

	mu      sync.Mutex
	byBits  map[uintptr]*fileContext
	byKey   map[string]*fileContext
	tests   map[uint64]*contextSink
	calls   map[*contextSink]bool
	windows []fileWindow
	epoch   uint64
	nextID  uint64
	// void holds no live sink; overflow stands for a channel union with more
	// live sinks than a context can hold.
	void, overflow *fileContext
	stats          struct{ merges, walks, incomplete, overflows, io, leaky int }
}

var contextCoverage contextState

// contextCoverageAvailable reports registered context banks of the known
// layout, with the runtime's edits.
func contextCoverageAvailable() bool {
	c := &contextCoverage
	c.once.Do(func() {
		if fileCoverageGetState == nil || fileCoverageSetState == nil || atomic.LoadPointer(&fileCoverageUnknown) == nil {
			return
		}
		banks := 0
		for p := atomic.LoadPointer(&fileCoverageBanksV2); p != nil; p = (*fileCoverageContextBank)(p).next {
			if bank := (*fileCoverageContextBank)(p); bank.version != fileCoverageContextBankVersion {
				log.Debug("civisibility.cov: context file coverage off: bank layout %d, want %d", bank.version, fileCoverageContextBankVersion)
				return
			}
			banks++
		}
		if banks == 0 {
			return
		}
		c.setup()
		c.available = true
		log.Debug("civisibility.cov: per-test context file coverage: true [banks:%d words:%d]", banks, c.words)
	})
	return c.available
}

// setup indexes the banks' files. Every package has initialized, so every
// bank has reserved its words.
func (c *contextState) setup() {
	c.words = max(int(atomic.LoadUint64(&fileCoverageWords)), 1)
	c.names = make([]string, c.words*64)
	c.files, c.symbols = map[string]int{}, map[string]bool{}
	for p := atomic.LoadPointer(&fileCoverageBanksV2); p != nil; p = (*fileCoverageContextBank)(p).next {
		bank := (*fileCoverageContextBank)(p)
		c.symbols[bank.prefix] = true
		if bank.n > 0 {
			for k, name := range unsafe.Slice(bank.names, bank.n) {
				i := int(*bank.base)*64 + k
				c.names[i] = name
				c.files[filepath.ToSlash(name)] = i
				// -trimpath gives a file the import path of its directory.
				c.files[bank.path+"/"+filepath.Base(name)] = i
			}
		}
		if bank.nAlways > 0 {
			c.always = append(c.always, unsafe.Slice(bank.always, bank.nAlways)...)
		}
	}
	c.byBits, c.byKey = map[uintptr]*fileContext{}, map[string]*fileContext{}
	c.tests, c.calls = map[uint64]*contextSink{}, map[*contextSink]bool{}
	c.void = c.intern(nil, false)
	c.overflow = &fileContext{bits: make([]uint64, 1), key: "overflow"}
	c.byBits[uintptr(c.overflow.pointer())] = c.overflow
	fileCoverageSendHook, fileCoverageReceiveHook, fileCoverageIOHook = contextSend, contextReceive, contextIO
	c.ready.Store(true)
}

// beginContextCoverage starts a test: a new window becomes current, and the
// test's goroutine gets the context of the test alone. One lock orders the
// epochs and the windows' publication.
func beginContextCoverage() uint64 {
	c := &contextCoverage
	c.mu.Lock()
	c.epoch++
	window := fileWindow{c.epoch, make([]uint64, c.words)}
	c.windows = append(c.windows, window)
	atomic.StorePointer(&fileCoverageUnknown, unsafe.Pointer(&window.bits[0]))
	c.nextID++
	test := &contextSink{id: c.nextID, epoch: c.epoch}
	c.tests[c.epoch] = test
	ctx := c.intern([]*contextSink{test}, false)
	c.mu.Unlock()
	fileCoverageSetState(ctx.pointer(), nil)
	return window.epoch
}

// scanContextCoverage captures the test that began at epoch: the files of
// every context that held it, or held a call that it joined, of the windows
// since it began, or since such a call leaked to them, and the files that
// every test reports; every file if such a call is saturated. The test's
// goroutine is left without a context.
func scanContextCoverage(epoch uint64) []string {
	c := &contextCoverage
	fileCoverageSetState(nil, nil)
	c.mu.Lock()
	test := c.tests[epoch]
	if test == nil {
		c.mu.Unlock()
		return nil
	}
	delete(c.tests, epoch)
	files := c.sinkFiles(c.closure([]*contextSink{test}), test.epoch)
	c.kill(test)
	c.prune()
	stats := c.stats
	contexts := len(c.byKey)
	c.mu.Unlock()
	var names []string
	for w, word := range files {
		for ; word != 0; word &= word - 1 {
			if name := c.names[w*64+bits.TrailingZeros64(word)]; name != "" {
				names = append(names, name)
			}
		}
	}
	log.Debug("civisibility.cov: context coverage [epoch:%d files:%d contexts:%d merges:%d walks:%d incomplete:%d overflows:%d io:%d leaky:%d]",
		epoch, len(names), contexts, stats.merges, stats.walks, stats.incomplete, stats.overflows, stats.io, stats.leaky)
	return append(names, c.always...)
}

// sinkFiles returns the bits of sinks' contexts and of the windows since
// from, or since a leaky call among sinks leaked; every bit for a saturated
// call.
func (c *contextState) sinkFiles(sinks []*contextSink, from uint64) []uint64 {
	files := make([]uint64, c.words)
	for _, sink := range sinks {
		if sink.saturated {
			for i := range files {
				files[i] = ^uint64(0)
			}
			return files
		}
		for _, ctx := range sink.contexts {
			if !ctx.callSet {
				orBits(files, ctx.bits)
			}
		}
		if sink.leaky {
			from = min(from, sink.leakyEpoch)
		}
	}
	for _, window := range c.windows {
		if window.epoch >= from {
			orBits(files, window.bits)
		}
	}
	return files
}

// closure returns sinks and the calls that they joined, through every level.
func (c *contextState) closure(sinks []*contextSink) []*contextSink {
	seen := map[*contextSink]bool{}
	var out []*contextSink
	for _, sink := range sinks {
		if !seen[sink] {
			seen[sink] = true
			out = append(out, sink)
		}
	}
	for i := 0; i < len(out); i++ {
		for _, call := range out[i].joined {
			if !seen[call] {
				seen[call] = true
				out = append(out, call)
			}
		}
	}
	return out
}

// kill marks sink dead and drops the contexts whose sinks are all dead. A
// goroutine that still points at one keeps it alive; nobody reads its bits.
func (c *contextState) kill(sink *contextSink) {
	sink.dead = true
	delete(c.calls, sink)
	for _, ctx := range sink.contexts {
		if !slices.ContainsFunc(ctx.sinks, func(s *contextSink) bool { return !s.dead }) {
			delete(c.byBits, uintptr(ctx.pointer()))
			delete(c.byKey, ctx.key)
		}
	}
}

// prune kills the retired calls that no live test and no call in flight
// reaches, and drops the windows that no live test or leaky call reads. The
// current window stays.
func (c *contextState) prune() {
	var roots []*contextSink
	for _, test := range c.tests {
		roots = append(roots, test)
	}
	for call := range c.calls {
		if !call.retired {
			roots = append(roots, call)
		}
	}
	needed := map[*contextSink]bool{}
	for _, sink := range c.closure(roots) {
		needed[sink] = true
	}
	from := c.epoch
	for call := range c.calls {
		if call.retired && !needed[call] {
			c.kill(call)
		} else if call.leaky {
			from = min(from, call.leakyEpoch)
		}
	}
	for _, test := range c.tests {
		from = min(from, test.epoch)
	}
	i := 0
	for i < len(c.windows) && c.windows[i].epoch < from {
		i++
	}
	c.windows = slices.Delete(c.windows, 0, i)
}

// intern returns the context, or the call set, of sinks, sorted by id.
func (c *contextState) intern(sinks []*contextSink, callSet bool) *fileContext {
	var key strings.Builder
	if callSet {
		key.WriteString("calls:")
	}
	for _, sink := range sinks {
		key.WriteString(strconv.FormatUint(sink.id, 36))
		key.WriteByte(',')
	}
	if ctx := c.byKey[key.String()]; ctx != nil {
		return ctx
	}
	words := c.words
	if callSet {
		words = 1
	}
	ctx := &fileContext{bits: make([]uint64, words), sinks: sinks, callSet: callSet, key: key.String()}
	c.byKey[ctx.key], c.byBits[uintptr(ctx.pointer())] = ctx, ctx
	for _, sink := range sinks {
		sink.contexts = append(sink.contexts, ctx)
	}
	return ctx
}

// callSetOf returns the call set of a goroutine without a context that works
// for sinks' calls, which leak from now on; nil without calls. With too many
// calls, it keeps none, and they saturate.
func (c *contextState) callSetOf(sinks []*contextSink) *fileContext {
	kept := calls(sinks)
	switch {
	case len(kept) == 0:
		return nil
	case len(kept) > maxContextSinks:
		c.stats.overflows++
		saturate(kept)
		return nil
	}
	c.markLeaky(kept)
	return c.intern(kept, true)
}

// lookup returns the context at p, or nil for a context that was dropped, or
// that never was one. Channels keep unions as integers, which keep nothing
// alive; a dropped context's address can come back as another context's,
// which only adds that context's sinks.
func (c *contextState) lookup(p uintptr) *fileContext {
	if p == 0 {
		return nil
	}
	return c.byBits[p]
}

// live returns ctx's live sinks; nil for nil.
func live(ctx *fileContext) []*contextSink {
	if ctx == nil {
		return nil
	}
	var out []*contextSink
	for _, sink := range ctx.sinks {
		if !sink.dead {
			out = append(out, sink)
		}
	}
	return out
}

// calls returns the calls among sinks.
func calls(sinks []*contextSink) []*contextSink {
	return slices.DeleteFunc(slices.Clone(sinks), func(s *contextSink) bool { return !s.call })
}

// union merges two lists of sinks sorted by id.
func union(a, b []*contextSink) []*contextSink {
	out := make([]*contextSink, 0, len(a)+len(b))
	for len(a) > 0 || len(b) > 0 {
		switch {
		case len(b) == 0 || len(a) > 0 && a[0].id < b[0].id:
			out, a = append(out, a[0]), a[1:]
		case len(a) == 0 || b[0].id < a[0].id:
			out, b = append(out, b[0]), b[1:]
		default:
			out, a, b = append(out, a[0]), a[1:], b[1:]
		}
	}
	return out
}

// markLeaky marks the calls among sinks leaky: what runs for them from now on
// may go to windows.
func (c *contextState) markLeaky(sinks []*contextSink) {
	for _, sink := range sinks {
		if sink.call && !sink.leaky && !sink.dead {
			sink.leaky, sink.leakyEpoch = true, c.epoch
			c.stats.leaky++
		}
	}
}

// saturate marks the calls among sinks saturated: what runs for them can no
// longer be told.
func saturate(sinks []*contextSink) {
	for _, sink := range sinks {
		if sink.call {
			sink.saturated = true
		}
	}
}

// subscribe makes sinks join call, but call itself.
func subscribe(sinks []*contextSink, call *contextSink) {
	for _, sink := range sinks {
		if sink != call && !slices.Contains(sink.joined, call) {
			sink.joined = append(sink.joined, call)
		}
	}
}

// currentWindow returns the bits that goroutines without a context set.
func (c *contextState) currentWindow() []uint64 {
	if n := len(c.windows); n > 0 {
		return c.windows[n-1].bits
	}
	return nil
}

// contextSend adds the live sinks of the sender's context, or of its call
// set, to a channel's union before the channel holds the sent value, and
// returns the union, which the sender's goroutine caches.
func contextSend(slot *uintptr, state unsafe.Pointer) unsafe.Pointer {
	c := &contextCoverage
	c.mu.Lock()
	defer c.mu.Unlock()
	sender := live(c.lookup(uintptr(state)))
	current := c.lookup(atomic.LoadUintptr(slot))
	if current == c.overflow {
		// The receivers lose the sender's calls.
		saturate(sender)
		return current.pointer()
	}
	existing := live(current)
	merged := union(existing, sender)
	if current != nil && len(merged) == len(existing) {
		return current.pointer()
	}
	next := c.void
	if len(merged) > maxContextSinks {
		c.stats.overflows++
		saturate(merged)
		next = c.overflow
	} else if len(merged) > 0 {
		next = c.intern(merged, false)
	}
	atomic.StoreUintptr(slot, uintptr(next.pointer()))
	return next.pointer()
}

// contextReceive returns the receiver's state once it received a value from
// a channel with union, and the union, which the receiver's goroutine
// caches. A receiver with a context adds the union's live sinks to it; one
// without a context adds the union's calls to its call set. When the
// receiver gains sinks, the files of its stack go where its next hits do:
// the rest of each frame's statement runs for them before the next site.
func contextReceive(u uintptr, ctx, callSet unsafe.Pointer) (unsafe.Pointer, unsafe.Pointer, unsafe.Pointer) {
	c := &contextCoverage
	c.mu.Lock()
	incoming := c.lookup(u)
	if incoming == nil {
		c.mu.Unlock()
		return ctx, callSet, nil
	}
	var next, nextCalls *fileContext
	var target []uint64
	if ctx == nil {
		mine := live(c.lookup(uintptr(callSet)))
		merged := union(mine, calls(live(incoming)))
		if len(merged) == len(mine) {
			c.mu.Unlock()
			return nil, callSet, incoming.pointer()
		}
		nextCalls, target = c.callSetOf(merged), c.currentWindow()
	} else {
		mine := live(c.lookup(uintptr(ctx)))
		switch merged := union(mine, live(incoming)); {
		case incoming == c.overflow:
			// The union's calls saturated when it overflowed; the receiver
			// may now work for any of its tests.
			nextCalls, target = c.callSetOf(mine), c.currentWindow()
		case len(merged) == len(mine):
			c.mu.Unlock()
			return ctx, nil, incoming.pointer()
		case len(merged) > maxContextSinks:
			// Every running test reads the windows; the calls go on.
			c.stats.overflows++
			nextCalls, target = c.callSetOf(merged), c.currentWindow()
		default:
			next = c.intern(merged, false)
			target = next.bits
		}
	}
	c.stats.merges++
	c.mu.Unlock()
	c.repair(target)
	return next.pointer(), nextCalls.pointer(), incoming.pointer()
}

// contextIO returns the call set of a goroutine that loses ctx before it
// reads a socket, pipe or terminal: it may work for anyone from then on, so
// its stack's files go to the window, and its calls leak.
func contextIO(ctx unsafe.Pointer) unsafe.Pointer {
	c := &contextCoverage
	c.mu.Lock()
	c.stats.io++
	callSet := c.callSetOf(live(c.lookup(uintptr(ctx))))
	target := c.currentWindow()
	c.mu.Unlock()
	c.repair(target)
	return callSet.pointer()
}

// repair sets the bits of the files of every frame of the calling goroutine's
// stack in target, or of every file when it cannot tell them all: the walk
// stops before the goroutine's first frame, or a frame of an instrumented
// package maps to no file.
func (c *contextState) repair(target []uint64) {
	if target == nil {
		return
	}
	c.count(&c.stats.walks)
	pcs := make([]uintptr, 64)
	for {
		n := runtime.Callers(1, pcs)
		if n < len(pcs) {
			pcs = pcs[:n]
			break
		}
		if len(pcs) >= 1<<16 {
			c.credit(target)
			return
		}
		pcs = make([]uintptr, 4*len(pcs))
	}
	frames := runtime.CallersFrames(pcs)
	complete := false
	for {
		frame, more := frames.Next()
		if i, ok := c.files[filepath.ToSlash(frame.File)]; ok {
			setBit(target, i)
		} else if c.symbols[symbolPrefix(frame.Function)] {
			c.credit(target)
			return
		}
		complete = frame.Function == "runtime.goexit"
		if !more {
			break
		}
	}
	if !complete {
		c.credit(target)
	}
}

func (c *contextState) count(stat *int) {
	c.mu.Lock()
	*stat++
	c.mu.Unlock()
}

// credit sets every file's bit in target.
func (c *contextState) credit(target []uint64) {
	c.count(&c.stats.incomplete)
	for i := range target {
		atomic.StoreUint64(&target[i], ^uint64(0))
	}
}

// symbolPrefix returns the package part of a function's symbol name, as the
// linker escapes it.
func symbolPrefix(function string) string {
	if i := strings.IndexByte(function, '['); i >= 0 {
		function = function[:i]
	}
	slash := strings.LastIndexByte(function, '/')
	if dot := strings.IndexByte(function[slash+1:], '.'); dot >= 0 {
		return function[:slash+1+dot]
	}
	return function
}

// The singleflight edits call these with the address of a call's sink, which
// the first of them creates. The group's mutex is held for fileCoverageJoin
// and fileCoverageComplete. Before the banks are read, while packages
// initialize, they do nothing.

// fileCoverageEnter adds the call's sink to the leader's state while it runs
// the call's function, and makes the leader's sinks join the call: its
// function runs for them.
func fileCoverageEnter(p *unsafe.Pointer) {
	c := &contextCoverage
	if !c.ready.Load() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	call := c.call(p)
	ctx, callSet := fileCoverageGetState()
	if ctx == nil {
		mine := live(c.lookup(uintptr(callSet)))
		subscribe(mine, call)
		fileCoverageSetState(nil, c.callSetOf(union(mine, []*contextSink{call})).pointer())
		return
	}
	mine := live(c.lookup(uintptr(ctx)))
	subscribe(mine, call)
	if merged := union(mine, []*contextSink{call}); len(merged) > maxContextSinks {
		c.stats.overflows++
		fileCoverageSetState(nil, c.callSetOf(merged).pointer())
	} else {
		fileCoverageSetState(c.intern(merged, false).pointer(), nil)
	}
}

// fileCoverageLeave removes the call's sink from the leader's state once the
// call's function returned.
func fileCoverageLeave(p *unsafe.Pointer) {
	c := &contextCoverage
	if !c.ready.Load() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	call := (*contextSink)(atomic.LoadPointer(p))
	ctx, callSet := fileCoverageGetState()
	without := func(state unsafe.Pointer) ([]*contextSink, bool) {
		current := c.lookup(uintptr(state))
		if call == nil || current == nil || !slices.Contains(current.sinks, call) {
			return nil, false
		}
		return slices.DeleteFunc(live(current), func(s *contextSink) bool { return s == call }), true
	}
	if ctx != nil {
		if rest, ok := without(ctx); ok {
			fileCoverageSetState(c.intern(rest, false).pointer(), nil)
		}
	} else if rest, ok := without(callSet); ok {
		fileCoverageSetState(nil, c.callSetOf(rest).pointer())
	}
}

// fileCoverageJoin makes the sinks of a caller that found the call in flight
// join it. A caller without a context makes the call publish its files to
// the window when it completes.
func fileCoverageJoin(p *unsafe.Pointer) {
	c := &contextCoverage
	if !c.ready.Load() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	call := c.call(p)
	ctx, callSet := fileCoverageGetState()
	if ctx == nil {
		call.unknownJoined = true
		subscribe(live(c.lookup(uintptr(callSet))), call)
		return
	}
	subscribe(live(c.lookup(uintptr(ctx))), call)
}

// fileCoverageComplete retires the call before its waiters return. A call
// that a goroutine without a context joined publishes its files to the
// window first.
func fileCoverageComplete(p *unsafe.Pointer) {
	c := &contextCoverage
	if !c.ready.Load() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	call := (*contextSink)(atomic.LoadPointer(p))
	if call == nil || call.retired {
		return
	}
	call.retired = true
	if window := c.currentWindow(); call.unknownJoined && window != nil {
		orBitsAtomic(window, c.sinkFiles(c.closure([]*contextSink{call}), c.epoch))
	}
}

// call returns the call's sink, which it creates on first use. The call
// struct keeps it alive, and the mutex orders its creation.
func (c *contextState) call(p *unsafe.Pointer) *contextSink {
	if sink := (*contextSink)(atomic.LoadPointer(p)); sink != nil {
		return sink
	}
	c.nextID++
	sink := &contextSink{id: c.nextID, call: true, epoch: c.epoch}
	c.calls[sink] = true
	atomic.StorePointer(p, unsafe.Pointer(sink))
	return sink
}

func setBit(bits []uint64, i int) {
	w, bit := &bits[i/64], uint64(1)<<(i%64)
	for {
		old := atomic.LoadUint64(w)
		if old&bit != 0 || atomic.CompareAndSwapUint64(w, old, old|bit) {
			return
		}
	}
}

// orBits adds src's bits, which sites may be setting, to dst, which only the
// caller sees.
func orBits(dst, src []uint64) {
	for i := range dst {
		dst[i] |= atomic.LoadUint64(&src[i])
	}
}

// orBitsAtomic adds src's bits to dst, which sites may be setting too.
func orBitsAtomic(dst, src []uint64) {
	for i := range dst {
		if add := atomic.LoadUint64(&src[i]); add != 0 {
			for {
				old := atomic.LoadUint64(&dst[i])
				if old|add == old || atomic.CompareAndSwapUint64(&dst[i], old, old|add) {
					break
				}
			}
		}
	}
}
