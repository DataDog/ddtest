package instrument

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
)

// SignalHook is an overlay file for package os/signal. It gives Mini a Notify
// whose channel receives a signal only when no other channel wants it: when
// the signal would otherwise end the process. Mini then sends its events and
// ends the process with the same signal, and a test that handles SIGTERM or
// SIGINT itself, or the fuzzing coordinator's SIGINT, keeps the process alive
// as under go test.
//
// TransformSignal edits process, the only place that delivers signals to
// channels, to ask __dd_civisibility_signalWantedElsewhere about Mini's
// channel, in handlers.m and in handlers.stopping while Stop removes it. The
// answer uses handlers' state under the lock that process already holds, so a
// channel that the test stops right after its delivery still counts. Channels
// that Stop is removing still receive the signal, as in process, and count
// too. Mini's channel is known before Notify registers it and until Stop has
// removed it.
//
// Reset removes a signal from every channel, Mini's included, and returns it
// to its default action. TransformSignal gives Reset's cancel the action
// __dd_civisibility_resetSignal instead of disableSignal: for a signal that
// Mini holds, it registers Mini's channel again under the same lock, so the
// signal never ends the process without reaching Mini first. Ignore's cancel
// gets __dd_civisibility_ignoreSignal instead of ignoreSignal: Mini stops
// holding a signal that the test ignores, which then stays ignored as without
// ddto, also after a later Notify, Stop or Reset.
//
// The hook sets Mini's variables instead of calling Mini: os/signal
// initializes before Mini, and a Mini function that coverage instruments
// would record its counters under package ID zero if it ran first. Mini's
// channel and the signals it wants change under handlers' lock.
const SignalHook = `package signal

import (
	"os"
	_ "unsafe"
)

//go:linkname __dd_civisibility_signalNotify github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/integrations.signalNotify
var __dd_civisibility_signalNotify func(chan<- os.Signal, chan<- int, ...os.Signal) []os.Signal

//go:linkname __dd_civisibility_signalStop github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/integrations.signalStop
var __dd_civisibility_signalStop func(chan<- os.Signal)

// Mini's channel, the signals it holds and those that the process ignores
// for good, all guarded by handlers' lock. While Stop removes Mini's channel,
// it is __dd_civisibility_stopping instead, which Reset does not hold again.
var (
	__dd_civisibility_signals  chan<- os.Signal
	__dd_civisibility_stopping chan<- os.Signal
	__dd_civisibility_observed chan<- int // signals that reached other channels, for Mini's debug log
	__dd_civisibility_wanted   [numSig]bool
	__dd_civisibility_ignored  [numSig]bool
)

func init() {
	// A process that started with SIGINT ignored keeps it ignored by the
	// operating system after a Notify and Stop, although Ignored then reports
	// false. os/signal initializes before any code that could call Notify.
	if n := signum(os.Interrupt); n >= 0 && n < numSig && signalIgnored(n) {
		__dd_civisibility_ignored[n] = true
	}
	__dd_civisibility_signalNotify = func(c chan<- os.Signal, observed chan<- int, sig ...os.Signal) []os.Signal {
		// A signal that the test ignored before Mini started can stay ignored
		// by the operating system although Ignored reports false.
		var keep []os.Signal
		var wanted [numSig]bool
		handlers.Lock()
		for _, s := range sig {
			if n := signum(s); n >= 0 && n < numSig && !__dd_civisibility_ignored[n] {
				keep = append(keep, s)
				wanted[n] = true
			}
		}
		if len(keep) != 0 {
			// Known before Notify registers c, so that a signal that arrives
			// meanwhile reaches c only when no other channel wants it.
			__dd_civisibility_signals, __dd_civisibility_observed, __dd_civisibility_wanted = c, observed, wanted
		}
		handlers.Unlock()
		if len(keep) == 0 {
			return nil
		}
		// Notify creates handlers.m and starts the signal loop, which
		// __dd_civisibility_hold relies on.
		Notify(c, keep...)
		handlers.Lock()
		// A Reset before Notify, without handlers.m, could not hold them.
		for n := range wanted {
			if wanted[n] {
				__dd_civisibility_hold(n)
			}
		}
		handlers.Unlock()
		return keep
	}
	__dd_civisibility_signalStop = func(c chan<- os.Signal) {
		handlers.Lock()
		if __dd_civisibility_signals == c {
			__dd_civisibility_signals, __dd_civisibility_stopping, __dd_civisibility_wanted = nil, c, [numSig]bool{}
		}
		handlers.Unlock()
		// Stop can still deliver to c, from handlers.stopping.
		Stop(c)
		handlers.Lock()
		if __dd_civisibility_stopping == c {
			__dd_civisibility_stopping, __dd_civisibility_observed = nil, nil
		}
		handlers.Unlock()
	}
}

// __dd_civisibility_signalWantedElsewhere reports whether c is Mini's channel
// and another channel wants signal n. process calls it holding handlers' lock.
// Mini learns, without blocking, of a signal that arrived while other
// channels were registered for it, whether or not their buffers took it.
func __dd_civisibility_signalWantedElsewhere(c chan<- os.Signal, n int) bool {
	if c != __dd_civisibility_signals && c != __dd_civisibility_stopping {
		return false
	}
	elsewhere := false
	for other, h := range handlers.m {
		elsewhere = elsewhere || other != c && h.want(n)
	}
	for _, d := range handlers.stopping {
		elsewhere = elsewhere || d.c != c && d.h.want(n)
	}
	if elsewhere {
		select {
		case __dd_civisibility_observed <- n:
		default:
		}
	}
	return elsewhere
}

// __dd_civisibility_hold makes Mini's channel want signal n, as Notify does,
// if Mini registered n and the test has not ignored it since, and reports
// whether it did. The caller holds handlers' lock.
func __dd_civisibility_hold(n int) bool {
	c := __dd_civisibility_signals
	if c == nil || handlers.m == nil || n < 0 || n >= numSig || !__dd_civisibility_wanted[n] || __dd_civisibility_ignored[n] {
		return false
	}
	h := handlers.m[c]
	if h == nil {
		h = new(handler)
		handlers.m[c] = h
	}
	if !h.want(n) {
		h.set(n)
		handlers.ref[n]++
	}
	enableSignal(n)
	return true
}

// __dd_civisibility_resetSignal is Reset's action for signal n, which cancel
// calls holding handlers' lock after removing n from every channel. It
// disables n, as Reset does, unless Mini holds n: then Mini's channel wants
// it again, and n stays enabled throughout.
func __dd_civisibility_resetSignal(n int) {
	if !__dd_civisibility_hold(n) {
		disableSignal(n)
	}
}

// __dd_civisibility_ignoreSignal is Ignore's action for signal n, which
// cancel calls holding handlers' lock after removing n from every channel.
// Mini stops holding n for good.
func __dd_civisibility_ignoreSignal(n int) {
	if n >= 0 && n < numSig {
		__dd_civisibility_ignored[n] = true
	}
	ignoreSignal(n)
}
`

// signalHookAPI is every declaration of package os/signal that SignalHook
// uses, rendered as sourceDeclarations renders them. A toolchain that lacks
// one, or declares it otherwise, builds without the hook.
var signalHookAPI = []string{
	"embed handlers sync.Mutex",
	"field handlers.m map[chan<- os.Signal]*handler",
	"field handlers.ref [numSig]int64",
	"field handlers.stopping []stopping",
	"field stopping.c chan<- os.Signal",
	"field stopping.h *handler",
	"func (*handler) want(int) bool",
	"func (*handler) set(int)",
	"func Notify(chan<- os.Signal, ...os.Signal)",
	"func Stop(chan<- os.Signal)",
	"func cancel([]os.Signal, func(int))",
	"func enableSignal(int)",
	"func disableSignal(int)",
	"func signum(os.Signal) int",
	"func ignoreSignal(int)",
	"func signalIgnored(int) bool",
	"const numSig",
}

// signalRuntimeAPI is what SignalHook's linknames name in Mini's integrations
// package, rendered as sourceDeclarations renders it.
var signalRuntimeAPI = []string{
	"var signalNotify func(chan<- os.Signal, chan<- int, ...os.Signal) []os.Signal",
	"var signalStop func(chan<- os.Signal)",
}

// TransformSignal checks that package os/signal, given as the sources that the
// build compiles, declares what SignalHook uses, and returns its file whose
// process asks the hook about Mini's channel, and whose Reset and Ignore give
// cancel the hook's actions. The edits stay on their lines, so os/signal keeps
// its positions.
func TransformSignal(files map[string][]byte) (map[string][]byte, error) {
	if err := declarationsError(files, signalHookAPI, "os/signal lacks %s"); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	deliveries, cancels := map[string]int{}, map[string]int{}
	output := map[string][]byte{}
	for _, name := range names {
		src := files[name]
		if bytes.Contains(src, []byte("__dd_civisibility_")) {
			return nil, fmt.Errorf("%s: already instrumented", name)
		}
		fs := token.NewFileSet()
		file, err := parser.ParseFile(fs, name, src, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		text := func(n ast.Node) string {
			return string(src[fs.Position(n.Pos()).Offset:fs.Position(n.End()).Offset])
		}
		var edits []edit
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Body == nil {
				continue
			}
			switch fn.Name.Name {
			case "process":
				for _, d := range processDeliveries(fn.Body) {
					edits = append(edits, edit{fs.Position(d.call.Pos()).Offset, fs.Position(d.call.End()).Offset,
						fmt.Sprintf("%s && !__dd_civisibility_signalWantedElsewhere(%s, %s)", text(d.call), d.channel, text(d.signal))})
					deliveries[d.list]++
				}
			case "Reset", "Ignore":
				native, hook := "disableSignal", "__dd_civisibility_resetSignal"
				if fn.Name.Name == "Ignore" {
					native, hook = "ignoreSignal", "__dd_civisibility_ignoreSignal"
				}
				if action := cancelAction(fn, native); action != nil {
					edits = append(edits, edit{fs.Position(action.Pos()).Offset, fs.Position(action.End()).Offset, hook})
					cancels[fn.Name.Name]++
				}
			}
		}
		if len(edits) == 0 {
			continue
		}
		sort.Slice(edits, func(i, j int) bool { return edits[i].start < edits[j].start })
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
	switch {
	case deliveries["m"] != 1 || deliveries["stopping"] != 1:
		return nil, fmt.Errorf("os/signal's process delivers to handlers.m in %d places and to handlers.stopping in %d that TransformSignal recognizes, want 1 each", deliveries["m"], deliveries["stopping"])
	case cancels["Reset"] != 1 || cancels["Ignore"] != 1:
		return nil, fmt.Errorf("os/signal declares %d Reset and %d Ignore functions that TransformSignal recognizes, want 1 each", cancels["Reset"], cancels["Ignore"])
	}
	return output, nil
}

// signalDelivery is process's check that a channel of handlers.m, or of
// handlers.stopping, wants the signal: the condition of
// "for c, h := range handlers.m { if h.want(n) {", or of
// "for _, d := range handlers.stopping { if d.h.want(n) {".
type signalDelivery struct {
	call    *ast.CallExpr
	channel string
	signal  ast.Expr
	// list is m or stopping.
	list string
}

// processDeliveries returns the conditions that guard a delivery to a channel
// of handlers.m or handlers.stopping in process's body.
func processDeliveries(body *ast.BlockStmt) []signalDelivery {
	var found []signalDelivery
	ast.Inspect(body, func(n ast.Node) bool {
		loop, ok := n.(*ast.RangeStmt)
		if !ok {
			return true
		}
		over, ok := loop.X.(*ast.SelectorExpr)
		if !ok || over.Sel.Name != "m" && over.Sel.Name != "stopping" {
			return true
		}
		if owner, ok := over.X.(*ast.Ident); !ok || owner.Name != "handlers" {
			return true
		}
		value, ok := loop.Value.(*ast.Ident)
		if !ok || len(loop.Body.List) == 0 {
			return true
		}
		check, ok := loop.Body.List[0].(*ast.IfStmt)
		if !ok || check.Init != nil {
			return true
		}
		call, ok := check.Cond.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return true
		}
		want, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || want.Sel.Name != "want" {
			return true
		}
		var channel string
		switch over.Sel.Name {
		case "m":
			key, ok := loop.Key.(*ast.Ident)
			if !ok {
				return true
			}
			if id, ok := want.X.(*ast.Ident); !ok || id.Name != value.Name {
				return true
			}
			channel = key.Name
		default:
			field, ok := want.X.(*ast.SelectorExpr)
			if !ok || field.Sel.Name != "h" {
				return true
			}
			if id, ok := field.X.(*ast.Ident); !ok || id.Name != value.Name {
				return true
			}
			channel = value.Name + ".c"
		}
		found = append(found, signalDelivery{call: call, channel: channel, signal: call.Args[0], list: over.Sel.Name})
		return true
	})
	return found
}

// cancelAction returns the action of fn's only statement,
// "cancel(sig, native)" with fn's variadic os.Signal parameter, as in Reset and
// Ignore, or nil.
func cancelAction(fn *ast.FuncDecl, native string) *ast.Ident {
	params := fn.Type.Params.List
	if len(params) != 1 || len(params[0].Names) != 1 || render(params[0].Type) != "...os.Signal" || len(fn.Body.List) != 1 {
		return nil
	}
	stmt, ok := fn.Body.List[0].(*ast.ExprStmt)
	if !ok {
		return nil
	}
	call, ok := stmt.X.(*ast.CallExpr)
	if !ok || len(call.Args) != 2 || call.Ellipsis.IsValid() {
		return nil
	}
	cancel, ok := call.Fun.(*ast.Ident)
	param, paramOK := call.Args[0].(*ast.Ident)
	action, actionOK := call.Args[1].(*ast.Ident)
	if !ok || !paramOK || !actionOK || cancel.Name != "cancel" || param.Name != params[0].Names[0].Name || action.Name != native {
		return nil
	}
	return action
}

// SignalHookRuntimeError reports why the selected runtime's integrations
// package, given as the sources that the build compiles, cannot receive
// SignalHook's registration, or nil.
func SignalHookRuntimeError(sources map[string][]byte) error {
	return declarationsError(sources, signalRuntimeAPI, "the runtime's integrations package lacks %s")
}
