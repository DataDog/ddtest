package integrations

import (
	"reflect"
	"runtime/debug"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"
)

// A crash ends the process without running exit hooks or deferred calls, so
// CI Visibility never closes and Mini's buffered events are lost. ddto's
// overlays call CloseForCrash where Go code still runs before two of them:
// testing's -timeout alarm, and, with ddto's --panic-hook, an unrecovered
// panic once the panicking goroutine has run its deferred calls.
var (
	// crashing is set by the first CloseForCrash: the process ends with that
	// crash once it returns.
	crashing atomic.Bool
	// crashClosed closes when the first CloseForCrash has closed CI Visibility.
	crashClosed = make(chan struct{})

	// unhandledPanic is read by the runtime's gopanic, which ddto's
	// --panic-hook edits, before it prints an unrecovered panic. The runtime
	// declares the same variable through a linkname, so it has no
	// initializer: this package's init sets it.
	unhandledPanic func(any)
)

func init() { unhandledPanic = closeForUnhandledPanic }

// closeForUnhandledPanic closes CI Visibility before an unrecovered panic
// ends the process. A panic in a test's own goroutine has already closed it.
func closeForUnhandledPanic(value any) {
	CloseForCrash("panic", "panic: "+panicValueText(value), string(debug.Stack()))
}

// panicValueText renders an unrecovered panic's value without calling any of
// its methods: the runtime has already called its Error or String method,
// and calls nothing else. Values of other kinds show their type, as the
// runtime prints their type and address.
func panicValueText(value any) string {
	if value == nil {
		return "nil"
	}
	v := reflect.ValueOf(value)
	var text string
	switch v.Kind() {
	case reflect.String:
		text = strconv.Quote(v.String())
		if v.Type() == reflect.TypeFor[string]() {
			return v.String()
		}
	case reflect.Bool:
		text = strconv.FormatBool(v.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		text = strconv.FormatInt(v.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		text = strconv.FormatUint(v.Uint(), 10)
	case reflect.Float32, reflect.Float64:
		text = strconv.FormatFloat(v.Float(), 'g', -1, 64)
	case reflect.Complex64, reflect.Complex128:
		text = strconv.FormatComplex(v.Complex(), 'g', -1, 128)
	default:
		return "(" + v.Type().String() + ")"
	}
	if v.Type().Name() == v.Kind().String() {
		return text
	}
	return v.Type().String() + "(" + text + ")"
}

// failOpenTests fails, with errType, message and stack, every test whose
// event is still open before a crash ends the process: the tests that the
// crash interrupts, including the parents of running subtests, which Go's
// -timeout lists too. Quarantined and disabled tests keep the final status
// skip, as when they fail otherwise. It returns how many it failed.
func failOpenTests(errType, message, stack string) int {
	failed := 0
	openTests.Range(func(key, _ any) bool {
		if key.(*tslvTest).failForCrash(errType, message, stack) {
			failed++
		}
		return true
	})
	return failed
}

// CloseForCrash fails the running tests with errType, message and stack and
// closes CI Visibility, at most forcedExitTimeout, before a crash ends the
// process. A crash in another goroutine meanwhile waits for that close, and
// os.Exit waits for the crash: the process ends with the crash's status, as
// without Mini. Other goroutines keep running meanwhile.
func CloseForCrash(errType, message, stack string) {
	if !crashing.CompareAndSwap(false, true) {
		log.Debug("civisibility: another crash (%s) while CI Visibility closes for the first; waiting for that close", errType)
		waitForCrashClose()
		return
	}
	defer close(crashClosed)
	state := civisibility.GetState()
	if state != civisibility.StateInitialized && state != civisibility.StateExiting {
		log.Debug("civisibility: the process crashes (%s) with CI Visibility %s; nothing to close: %s", errType, stateName(state), firstLine(message))
		return
	}
	log.Debug("civisibility: the process crashes (%s); failing the running tests and closing CI Visibility first: %s\n%s", errType, message, stack)
	started := time.Now()
	finished := closeWithinTimeout("before a crash", func() {
		if civisibility.GetState() == civisibility.StateInitialized {
			log.Debug("civisibility: failed %d running tests before the crash", failOpenTests(errType, message, stack))
		}
	})
	if finished {
		log.Debug("civisibility: closed CI Visibility before the crash duration=%s", time.Since(started))
	} else {
		log.Error("civisibility: closing CI Visibility before a crash did not finish within %s; crashing without it", forcedExitTimeout)
	}
	// Errors are otherwise printed once a minute; the process ends now.
	log.Flush()
}

// waitForCrashClose waits, at most forcedExitTimeout, for the first
// CloseForCrash to close CI Visibility.
func waitForCrashClose() {
	select {
	case <-crashClosed:
	case <-time.After(forcedExitTimeout):
	}
}

// holdForCrash blocks a goroutine about to end the process, as os.Exit's,
// while a crash closes CI Visibility: the crash then ends the process with its
// own status. Without it, os.Exit(0) after the tests could win, and a crashed
// test binary would exit 0.
func holdForCrash(what string) {
	if !crashing.Load() {
		return
	}
	log.Debug("civisibility: %s while the process crashes; the crash ends the process", what)
	log.Flush()
	waitForCrashClose()
	select {}
}

// firstLine returns s up to its first line break.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// stateName names a CI Visibility state for debug logs.
func stateName(state civisibility.State) string {
	switch state {
	case civisibility.StateInitializing:
		return "initializing"
	case civisibility.StateInitialized:
		return "initialized"
	case civisibility.StateExiting:
		return "exiting"
	case civisibility.StateExited:
		return "exited"
	}
	return "uninitialized"
}
