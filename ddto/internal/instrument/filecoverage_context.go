package instrument

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
)

// Context file coverage is ddto's --experimental-file-coverage=context. Each
// goroutine carries a context, the set of tests and singleflight calls that
// it works for, with one bit per instrumented file. A site sets its file's
// bit in the current goroutine's context. The edits below give the runtime
// the context field and its propagation:
//   - a goroutine starts with its creator's context;
//   - a channel keeps the union of its senders' contexts, which a receive
//     adds to the receiver's;
//   - a timer runs, or delivers, with its creator's context, and
//     context.AfterFunc's function with its registrant's;
//   - a read from a socket, pipe or terminal leaves the goroutine without a
//     context: its hits then go to every running test, like a goroutine that
//     never had one;
//   - a singleflight call's function runs for the call too, and a caller that
//     joins the call gets what it ran.
//
// Mini owns the contexts. The runtime keeps pointers and calls Mini's hooks
// outside every runtime lock, only for goroutines that have a context; before
// the first test starts, none has one.

// contextRuntimeAPI is what TransformContextRuntime and ContextRuntimeHook use
// in the runtime.
var contextRuntimeAPI = []string{
	"field g.labels unsafe.Pointer",
	"field g.m *m",
	"field m.curg *g",
	"field m.locks int32",
	"field hchan.lock mutex",
	"field scase.c *hchan",
	"field timer.isChan bool",
	"func getg() *g",
	"func add(unsafe.Pointer, uintptr) unsafe.Pointer",
	"func newproc1(*funcval, *g, uintptr, bool, waitReason) *g",
	"func gdestroy(*g)",
	"func chansend(*hchan, unsafe.Pointer, bool, uintptr) bool",
	"func chanrecv(*hchan, unsafe.Pointer, bool) (bool, bool)",
	"func selectgo(*scase, *uint16, *uintptr, int, int, bool) (int, bool)",
	"func newTimer(int64, int64, func(any, uintptr, int64), any, *hchan) *timeTimer",
	"func (*timer) unlockAndRun(int64, *synctestBubble)",
	"func efaceOf(*any) *eface",
	"field eface.data unsafe.Pointer",
}

// ContextRuntimeHook is the runtime file that context file coverage adds, with
// mini as the import path of Mini's coverage package:
//   - DdtoWord, which sites call, returns the word of the current goroutine's
//     context that holds a file's bit. A goroutine without a context gets
//     Mini's current window, which every running test reads; before Mini sets
//     one, a scratch array that nobody reads;
//   - DdtoState and DdtoSetState read and set the current goroutine's
//     context and, for a goroutine without one, the singleflight calls that
//     it works for. The context and net/http edits use them; Mini gets them
//     through its variables, which these initialize, so that it compiles
//     without them;
//   - the channel, timer and poll hooks that the edits call. They call Mini
//     only from a user goroutine that holds no runtime lock, and, but for a
//     receive, that has a context or calls. The goroutine caches the last union that
//     it knows its context already includes, for sends and for receives, so
//     that a channel used by one goroutine calls Mini once. Mini returns what
//     the goroutine caches as a pointer to a context that it keeps, so that
//     the goroutine keeps it alive and its address cannot come back as
//     another context's.
func ContextRuntimeHook(mini string) string {
	return strings.ReplaceAll(strings.ReplaceAll(`package runtime

import (
	"internal/abi"
	"internal/runtime/atomic"
	"unsafe"
)

var __ddto_scratch [WORDS]uint64

//go:linkname __ddto_unknown MINI.fileCoverageUnknown
var __ddto_unknown = unsafe.Pointer(&__ddto_scratch)

//go:linkname __ddto_sendHook MINI.fileCoverageSendHook
var __ddto_sendHook func(slot *uintptr, state unsafe.Pointer) (sent unsafe.Pointer)

//go:linkname __ddto_receiveHook MINI.fileCoverageReceiveHook
var __ddto_receiveHook func(union uintptr, ctx, calls unsafe.Pointer) (nextCtx, nextCalls, seen unsafe.Pointer)

//go:linkname __ddto_ioHook MINI.fileCoverageIOHook
var __ddto_ioHook func(ctx unsafe.Pointer) (calls unsafe.Pointer)

//go:linkname __ddto_getState MINI.fileCoverageGetState
var __ddto_getState = DdtoState

//go:linkname __ddto_setState MINI.fileCoverageSetState
var __ddto_setState = DdtoSetState

// DdtoWord returns the word that holds bit i%64 of word i/64 of the current
// goroutine's file set.
func DdtoWord(i uintptr) *uint64 {
	c := getg().ddtoCtx
	if c == nil {
		c = atomic.Loadp(unsafe.Pointer(&__ddto_unknown))
	}
	return (*uint64)(add(c, i*8))
}

// DdtoState returns the current goroutine's context, and, for a goroutine
// without one, the singleflight calls that it works for.
func DdtoState() (ctx, calls unsafe.Pointer) {
	gp := getg()
	return gp.ddtoCtx, gp.ddtoCalls
}

// DdtoSetState sets the current goroutine's context and calls.
func DdtoSetState(ctx, calls unsafe.Pointer) {
	gp := getg()
	gp.ddtoCtx, gp.ddtoCalls, gp.ddtoSent, gp.ddtoSeen = ctx, calls, nil, nil
}

// __ddto_hookable reports whether Mini's hooks may run on gp: a user
// goroutine that holds no runtime lock.
func __ddto_hookable(gp *g) bool {
	return gp == gp.m.curg && gp.m.locks == 0
}

// __ddto_send adds the sender's context, or its calls, to c's union before c
// holds the value.
func __ddto_send(c *hchan) {
	if c == nil {
		return
	}
	gp := getg()
	state := gp.ddtoCtx
	if state == nil {
		state = gp.ddtoCalls
	}
	if state == nil || !__ddto_hookable(gp) {
		return
	}
	if union := atomic.Loaduintptr(&c.ddto); union == uintptr(state) || union != 0 && union == uintptr(gp.ddtoSent) {
		return
	}
	if hook := __ddto_sendHook; hook != nil {
		gp.ddtoSent = hook(&c.ddto, state)
	}
}

// __ddto_receive adds c's union to the receiver's context once a value was
// received. A receiver without a context calls Mini too: it takes the
// union's singleflight calls.
func __ddto_receive(c *hchan) {
	gp := getg()
	if !__ddto_hookable(gp) {
		return
	}
	ctx := gp.ddtoCtx
	union := atomic.Loaduintptr(&c.ddto)
	if union == 0 || union == uintptr(ctx) || union == uintptr(gp.ddtoSeen) {
		return
	}
	if hook := __ddto_receiveHook; hook != nil {
		nextCtx, nextCalls, seen := hook(union, ctx, gp.ddtoCalls)
		if nextCtx != ctx || nextCalls != gp.ddtoCalls {
			gp.ddtoCtx, gp.ddtoCalls, gp.ddtoSent = nextCtx, nextCalls, nil
		}
		gp.ddtoSeen = seen
	}
}

// __ddto_timerChannel adds the timer creator's context to the timer's
// channel: c, or, for an asynchronous timer channel (asynctimerchan=1), the
// channel that the timer function gets as its argument.
func __ddto_timerChannel(c *hchan, arg any) {
	if c == nil {
		if e := efaceOf(&arg); e._type != nil && e._type.Kind() == abi.Chan {
			c = (*hchan)(e.data)
		}
	}
	__ddto_send(c)
}

// __ddto_selectSend adds the sender's context to every send case's union: the
// case that sends is not known yet.
func __ddto_selectSend(cas0 *scase, nsends int) {
	if gp := getg(); gp.ddtoCtx == nil && gp.ddtoCalls == nil || nsends == 0 {
		return
	}
	cases := (*[1 << 16]scase)(unsafe.Pointer(cas0))[:nsends:nsends]
	for i := range cases {
		__ddto_send(cases[i].c)
	}
}

// __ddto_pollRead leaves the current goroutine without a context before it
// reads a socket, pipe or terminal: what it does next may be for whoever
// wrote, so its files go to every running test. It keeps the singleflight
// calls that it works for, which Mini returns.
//
//go:linkname __ddto_pollRead internal/poll.__ddto_pollRead
func __ddto_pollRead() {
	gp := getg()
	ctx := gp.ddtoCtx
	if ctx == nil || gp != gp.m.curg {
		return
	}
	calls := unsafe.Pointer(nil)
	if hook := __ddto_ioHook; hook != nil && gp.m.locks == 0 {
		calls = hook(ctx)
	}
	gp.ddtoCtx, gp.ddtoCalls, gp.ddtoSent, gp.ddtoSeen = nil, calls, nil, nil
}
`, "MINI", mini), "WORDS", strconv.Itoa(FileCoverageContextWords))
}

// FileCoverageContextWords bounds the words of every context bank of a
// build: the runtime's scratch array, where sites set bits before the first
// test starts, has that many.
const FileCoverageContextWords = 1 << 16

// TransformContextRuntime checks that the runtime, given as the sources that
// the build compiles, has the shape that its edits expect, and returns the
// files that it edits:
//   - g gains the context and its two caches, after its pprof labels; hchan
//     gains its senders' union, and timer its creator's context;
//   - newproc1 gives a user goroutine its creator's context, where it gives
//     it the creator's pprof labels, and gdestroy clears it;
//   - chansend calls the send hook first, selectgo for every send case
//     first; chanrecv calls the receive hook before it returns a received
//     value, and selectgo before it returns a received case;
//   - newTimer records its creator's context, and adds it to the channel's
//     union; unlockAndRun runs a timer function with that context.
//
// The edits stay on their lines, so the runtime keeps its positions.
func TransformContextRuntime(files map[string][]byte) (map[string][]byte, error) {
	if err := declarationsError(files, contextRuntimeAPI, "the runtime lacks %s"); err != nil {
		return nil, err
	}
	return editFiles(files, "runtime", func(fs *token.FileSet, file *ast.File, add func(at int, text string), found map[string]int) {
		offset := func(p token.Pos) int { return fs.Position(p).Offset }
		for _, decl := range file.Decls {
			switch decl := decl.(type) {
			case *ast.GenDecl:
				for _, spec := range decl.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					st, ok := ts.Type.(*ast.StructType)
					if !ok || len(st.Fields.List) == 0 {
						continue
					}
					switch ts.Name.Name {
					case "g":
						for _, field := range st.Fields.List {
							if len(field.Names) == 1 && field.Names[0].Name == "labels" {
								add(offset(field.Type.End()), "; ddtoCtx, ddtoCalls, ddtoSent, ddtoSeen unsafe.Pointer")
								found["g"]++
							}
						}
					case "hchan":
						add(offset(st.Fields.List[len(st.Fields.List)-1].End()), "; ddto uintptr")
						found["hchan"]++
					case "timer":
						add(offset(st.Fields.List[len(st.Fields.List)-1].End()), "; ddto, ddtoCalls unsafe.Pointer")
						found["timer"]++
					}
				}
			case *ast.FuncDecl:
				if decl.Body == nil {
					continue
				}
				params := paramNames(decl.Type)
				switch name := funcName(decl); name {
				case "newproc1":
					ast.Inspect(decl.Body, func(n ast.Node) bool {
						stmt, ok := n.(*ast.IfStmt)
						if !ok || stmt.Else == nil || !isCall(stmt.Cond, "isSystemGoroutine") {
							return true
						}
						if block, ok := stmt.Else.(*ast.BlockStmt); ok && len(params) > 1 {
							add(offset(block.Lbrace)+1, " newg.ddtoCtx, newg.ddtoCalls = "+params[1]+".ddtoCtx, "+params[1]+".ddtoCalls;")
							found[name]++
						}
						return false
					})
				case "gdestroy":
					ast.Inspect(decl.Body, func(n ast.Node) bool {
						stmt, ok := n.(*ast.AssignStmt)
						if !ok || len(stmt.Lhs) != 1 || len(stmt.Rhs) != 1 || !isIdent(stmt.Rhs[0], "nil") {
							return true
						}
						if sel, ok := stmt.Lhs[0].(*ast.SelectorExpr); ok && sel.Sel.Name == "labels" {
							gp := render(sel.X)
							add(offset(stmt.End()), "; "+gp+".ddtoCtx, "+gp+".ddtoCalls, "+gp+".ddtoSent, "+gp+".ddtoSeen = nil, nil, nil, nil")
							found[name]++
						}
						return false
					})
				case "chansend":
					add(offset(decl.Body.Lbrace)+1, " __ddto_send("+params[0]+");")
					found[name]++
				case "chanrecv":
					ast.Inspect(decl.Body, func(n ast.Node) bool {
						if _, ok := n.(*ast.FuncLit); ok {
							return false
						}
						ret, ok := n.(*ast.ReturnStmt)
						if !ok || len(ret.Results) != 2 || !isIdent(ret.Results[0], "true") {
							return true
						}
						switch received := ret.Results[1].(type) {
						case *ast.Ident:
							if received.Name == "true" {
								add(offset(ret.Pos()), "__ddto_receive("+params[0]+"); ")
								found[name]++
							} else if received.Name != "false" {
								add(offset(ret.Pos()), "if "+received.Name+" { __ddto_receive("+params[0]+") }; ")
								found[name]++
							}
						}
						return true
					})
				case "selectgo":
					add(offset(decl.Body.Lbrace)+1, " __ddto_selectSend("+params[0]+", "+params[3]+");")
					ast.Inspect(decl.Body, func(n ast.Node) bool {
						if _, ok := n.(*ast.FuncLit); ok {
							return false
						}
						ret, ok := n.(*ast.ReturnStmt)
						if !ok || len(ret.Results) != 2 {
							return true
						}
						chosen, ok1 := ret.Results[0].(*ast.Ident)
						received, ok2 := ret.Results[1].(*ast.Ident)
						if ok1 && ok2 && received.Name != "false" {
							add(offset(ret.Pos()), "if "+received.Name+" { __ddto_receive((*[1 << 16]scase)(unsafe.Pointer("+params[0]+"))["+chosen.Name+"].c) }; ")
							found[name]++
						}
						return true
					})
				case "newTimer":
					for _, stmt := range decl.Body.List {
						assign, ok := stmt.(*ast.AssignStmt)
						if !ok || assign.Tok != token.DEFINE || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
							continue
						}
						if call, ok := assign.Rhs[0].(*ast.CallExpr); ok && isIdent(call.Fun, "new") && len(call.Args) == 1 && isIdent(call.Args[0], "timeTimer") {
							t := render(assign.Lhs[0])
							add(offset(stmt.End()), "; "+t+".timer.ddto, "+t+".timer.ddtoCalls = getg().ddtoCtx, getg().ddtoCalls; __ddto_timerChannel("+params[4]+", "+params[3]+")")
							found[name]++
						}
					}
				case "(*timer) unlockAndRun":
					t := decl.Recv.List[0].Names[0].Name
					ast.Inspect(decl.Body, func(n ast.Node) bool {
						stmt, ok := n.(*ast.ExprStmt)
						if !ok {
							return true
						}
						if call, ok := stmt.X.(*ast.CallExpr); ok && isIdent(call.Fun, "f") && len(call.Args) == 3 {
							start, end := offset(stmt.Pos()), offset(stmt.End())
							add(start, "if !"+t+".isChan { __ddto_g := getg(); __ddto_ctx, __ddto_calls := __ddto_g.ddtoCtx, __ddto_g.ddtoCalls; __ddto_g.ddtoCtx, __ddto_g.ddtoCalls = "+t+".ddto, "+t+".ddtoCalls; ")
							add(end, "; __ddto_g.ddtoCtx, __ddto_g.ddtoCalls = __ddto_ctx, __ddto_calls } else { "+render(call)+" }")
							found[name]++
						}
						return true
					})
				}
			}
		}
	}, map[string]int{"g": 1, "hchan": 1, "timer": 1, "newproc1": 1, "gdestroy": 1, "chansend": 1, "chanrecv": 3, "selectgo": 1, "newTimer": 1, "(*timer) unlockAndRun": 1})
}

// ContextPollHook is the file that context file coverage adds to
// internal/poll: the runtime provides the function.
const ContextPollHook = "package poll\n\nfunc __ddto_pollRead()\n"

// TransformContextPoll edits internal/poll, given as the sources that the
// build compiles, so that readLock calls the runtime's poll hook before a read
// of a descriptor that the poller watches: a socket, pipe or terminal, not a
// regular file.
func TransformContextPoll(files map[string][]byte) (map[string][]byte, error) {
	if err := declarationsError(files, []string{"func (*FD) readLock() error", "field FD.pd pollDesc", "func (*pollDesc) pollable() bool"}, "internal/poll lacks %s"); err != nil {
		return nil, err
	}
	return editFiles(files, "internal/poll", func(fs *token.FileSet, file *ast.File, add func(int, string), found map[string]int) {
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil && funcName(fn) == "(*FD) readLock" {
				fd := fn.Recv.List[0].Names[0].Name
				add(fs.Position(fn.Body.Lbrace).Offset+1, " if "+fd+".pd.pollable() { __ddto_pollRead() };")
				found["readLock"]++
			}
		}
	}, map[string]int{"readLock": 1})
}

// ContextCaptureHook is the file that context file coverage adds to context
// and net/http, named name: __ddto_capture returns a function that runs with
// the context of the goroutine that registered it.
func ContextCaptureHook(name string) string {
	return "package " + name + contextCaptureHook
}

const contextCaptureHook = `

import "runtime"

func __ddto_capture(f func()) func() {
	ctx, calls := runtime.DdtoState()
	return func() {
		runtime.DdtoSetState(ctx, calls)
		f()
	}
}
`

// TransformContextContext edits context, given as the sources that the build
// compiles, so that AfterFunc's function runs with its registrant's context:
// the goroutine that cancels starts the one that runs it.
func TransformContextContext(files map[string][]byte) (map[string][]byte, error) {
	if err := declarationsError(files, []string{"func AfterFunc(Context, func()) (func() bool)", "field afterFuncCtx.f func()"}, "context lacks %s"); err != nil {
		return nil, err
	}
	return editFiles(files, "context", func(fs *token.FileSet, file *ast.File, add func(int, string), found map[string]int) {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || funcName(fn) != "AfterFunc" {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				lit, ok := n.(*ast.CompositeLit)
				if !ok || !isIdent(lit.Type, "afterFuncCtx") {
					return true
				}
				for _, elt := range lit.Elts {
					if kv, ok := elt.(*ast.KeyValueExpr); ok && isIdent(kv.Key, "f") {
						add(fs.Position(kv.Value.Pos()).Offset, "__ddto_capture(")
						add(fs.Position(kv.Value.End()).Offset, ")")
						found["AfterFunc"]++
					}
				}
				return false
			})
		}
	}, map[string]int{"AfterFunc": 1})
}

// ContextSingleflightHook is the file that context file coverage adds to a
// singleflight package named name, with mini as the import path of Mini's
// coverage package, which implements the functions. They take the address of
// the call's sink, which Mini creates on first use.
func ContextSingleflightHook(name, mini string) string {
	return strings.ReplaceAll(strings.ReplaceAll(`package NAME

import "unsafe"

//go:linkname __ddto_enter MINI.fileCoverageEnter
func __ddto_enter(sink *unsafe.Pointer)

//go:linkname __ddto_leave MINI.fileCoverageLeave
func __ddto_leave(sink *unsafe.Pointer)

//go:linkname __ddto_join MINI.fileCoverageJoin
func __ddto_join(sink *unsafe.Pointer)

//go:linkname __ddto_complete MINI.fileCoverageComplete
func __ddto_complete(sink *unsafe.Pointer)
`, "NAME", name), "MINI", mini)
}

// TransformContextHTTP edits net/http, given as the sources that the build
// compiles, so that a function that RegisterOnShutdown registers runs with
// its registrant's context: Shutdown's caller starts the goroutine that runs
// it.
func TransformContextHTTP(files map[string][]byte) (map[string][]byte, error) {
	if err := declarationsError(files, []string{"func (*Server) RegisterOnShutdown(func())", "field Server.onShutdown []func()"}, "net/http lacks %s"); err != nil {
		return nil, err
	}
	return editFiles(files, "net/http", func(fs *token.FileSet, file *ast.File, add func(int, string), found map[string]int) {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || funcName(fn) != "(*Server) RegisterOnShutdown" {
				continue
			}
			f := paramNames(fn.Type)[0]
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || !isIdent(call.Fun, "append") || len(call.Args) != 2 || !isIdent(call.Args[1], f) {
					return true
				}
				add(fs.Position(call.Args[1].Pos()).Offset, "__ddto_capture(")
				add(fs.Position(call.Args[1].End()).Offset, ")")
				found["RegisterOnShutdown"]++
				return false
			})
		}
	}, map[string]int{"RegisterOnShutdown": 1})
}

// TransformContextTesting edits testing, given as the sources that the build
// compiles, so that the parallel tests' semaphore carries no context:
// release, which a test with subtests calls before its cleanups and so before
// its coverage is captured, sends to a waiting test without the sender's.
func TransformContextTesting(files map[string][]byte) (map[string][]byte, error) {
	if err := declarationsError(files, []string{"func (*testState) release()", "field testState.startParallel chan bool"}, "testing lacks %s"); err != nil {
		return nil, err
	}
	return editFiles(files, "testing", func(fs *token.FileSet, file *ast.File, add func(int, string), found map[string]int) {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || funcName(fn) != "(*testState) release" {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				send, ok := n.(*ast.SendStmt)
				if !ok {
					return true
				}
				if sel, ok := send.Chan.(*ast.SelectorExpr); ok && sel.Sel.Name == "startParallel" {
					add(fs.Position(send.Pos()).Offset, "__ddto_ctx, __ddto_calls := runtime.DdtoState(); runtime.DdtoSetState(nil, nil); ")
					add(fs.Position(send.End()).Offset, "; runtime.DdtoSetState(__ddto_ctx, __ddto_calls)")
					found["release"]++
				}
				return true
			})
		}
	}, map[string]int{"release": 1})
}

// TransformContextSingleflight edits a singleflight package, either
// golang.org/x/sync/singleflight or std's internal/singleflight, given as the
// sources that the build compiles:
//   - call gains its sink;
//   - doCall runs fn with the call's sink added to the leader's context, and
//     completes the call's sink right before the call's WaitGroup is done,
//     under the group's mutex: no later caller can join it, and no caller
//     returns before it completes;
//   - a caller that finds the call in flight, in Do or DoChan, joins it
//     before it releases the group's mutex.
func TransformContextSingleflight(files map[string][]byte) (map[string][]byte, error) {
	if err := declarationsError(files, []string{"field call.wg sync.WaitGroup", "field call.dups int"}, "singleflight lacks %s"); err != nil {
		return nil, err
	}
	return editFiles(files, "singleflight", func(fs *token.FileSet, file *ast.File, add func(int, string), found map[string]int) {
		offset := func(p token.Pos) int { return fs.Position(p).Offset }
		edited := false
		for _, decl := range file.Decls {
			switch decl := decl.(type) {
			case *ast.GenDecl:
				for _, spec := range decl.Specs {
					if ts, ok := spec.(*ast.TypeSpec); ok && ts.Name.Name == "call" {
						if st, ok := ts.Type.(*ast.StructType); ok && len(st.Fields.List) > 0 {
							add(offset(st.Fields.List[len(st.Fields.List)-1].End()), "; __ddto __ddto_unsafe.Pointer")
							found["call"]++
							edited = true
						}
					}
				}
			case *ast.FuncDecl:
				if decl.Body == nil {
					continue
				}
				switch name := funcName(decl); name {
				case "(*Group) Do", "(*Group) DoChan":
					for _, stmt := range decl.Body.List {
						ifStmt, ok := stmt.(*ast.IfStmt)
						if !ok || ifStmt.Init == nil || !isIdent(ifStmt.Cond, "ok") {
							continue
						}
						init, ok := ifStmt.Init.(*ast.AssignStmt)
						if !ok || len(init.Lhs) != 2 {
							continue
						}
						c := render(init.Lhs[0])
						for _, inner := range ifStmt.Body.List {
							if expr, ok := inner.(*ast.ExprStmt); ok && isMethodCall(expr.X, "Unlock") {
								add(offset(inner.Pos()), "__ddto_join(&"+c+".__ddto); ")
								found[name]++
								edited = true
								break
							}
						}
					}
				case "(*Group) doCall":
					c := paramNames(decl.Type)[0]
					ast.Inspect(decl.Body, func(n ast.Node) bool {
						switch stmt := n.(type) {
						case *ast.AssignStmt:
							if len(stmt.Rhs) == 1 && len(stmt.Lhs) == 2 && isCallOf(stmt.Rhs[0], "fn") {
								add(offset(stmt.Pos()), "__ddto_enter(&"+c+".__ddto); ")
								add(offset(stmt.End()), "; __ddto_leave(&"+c+".__ddto)")
								found["fn"]++
								edited = true
							}
						case *ast.ExprStmt:
							if call, ok := stmt.X.(*ast.CallExpr); ok && render(call.Fun) == c+".wg.Done" {
								add(offset(stmt.Pos()), "__ddto_complete(&"+c+".__ddto); ")
								found["Done"]++
								edited = true
							}
						}
						return true
					})
				}
			}
		}
		if edited {
			add(offset(file.Name.End()), "; import __ddto_unsafe \"unsafe\"")
		}
	}, map[string]int{"call": 1, "(*Group) Do": 1, "(*Group) DoChan": 1, "fn": 1, "Done": 1})
}

// editFiles parses each of files, collects the edits that visit makes, and
// returns the files that it edits, each with a line directive that keeps the
// positions. found counts the edits of each kind, which must equal want.
func editFiles(files map[string][]byte, what string, visit func(*token.FileSet, *ast.File, func(int, string), map[string]int), want map[string]int) (map[string][]byte, error) {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	output := map[string][]byte{}
	found := map[string]int{}
	for _, name := range names {
		src := files[name]
		if bytes.Contains(src, []byte(fileCoveragePrefix)) {
			return nil, fmt.Errorf("%s: already instrumented", name)
		}
		fs := token.NewFileSet()
		file, err := parser.ParseFile(fs, name, src, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		var edits []edit
		visit(fs, file, func(at int, text string) { edits = append(edits, edit{at, at, text}) }, found)
		if len(edits) == 0 {
			continue
		}
		sort.SliceStable(edits, func(i, j int) bool { return edits[i].start < edits[j].start })
		var buf bytes.Buffer
		buf.WriteString("//line " + name + ":1\n")
		cursor := 0
		for _, e := range edits {
			buf.Write(src[cursor:e.start])
			buf.WriteString(e.text)
			cursor = e.end
		}
		buf.Write(src[cursor:])
		output[name] = buf.Bytes()
	}
	kinds := make([]string, 0, len(want))
	for kind := range want {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	for _, kind := range kinds {
		if found[kind] != want[kind] {
			return nil, fmt.Errorf("%s has %d places of kind %s to edit, want %d", what, found[kind], kind, want[kind])
		}
	}
	return output, nil
}

// funcName returns a function's name as sourceDeclarations renders it, with
// its receiver's type.
func funcName(fn *ast.FuncDecl) string {
	if fn.Recv != nil && len(fn.Recv.List) == 1 {
		return "(" + render(fn.Recv.List[0].Type) + ") " + fn.Name.Name
	}
	return fn.Name.Name
}

// paramNames returns the names of a function's parameters, "_" for unnamed
// ones.
func paramNames(fn *ast.FuncType) []string {
	var names []string
	for _, field := range fn.Params.List {
		if len(field.Names) == 0 {
			names = append(names, "_")
		}
		for _, name := range field.Names {
			names = append(names, name.Name)
		}
	}
	return names
}

func isIdent(expr ast.Expr, name string) bool {
	id, ok := expr.(*ast.Ident)
	return ok && id.Name == name
}

// isCall reports whether expr calls the function name.
func isCall(expr ast.Expr, name string) bool {
	call, ok := expr.(*ast.CallExpr)
	return ok && isIdent(call.Fun, name)
}

// isCallOf reports whether expr calls the function value name without
// arguments, or with the key.
func isCallOf(expr ast.Expr, name string) bool {
	call, ok := expr.(*ast.CallExpr)
	return ok && isIdent(call.Fun, name) && len(call.Args) <= 1
}

// isMethodCall reports whether expr calls a method name without arguments.
func isMethodCall(expr ast.Expr, name string) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == name
}
