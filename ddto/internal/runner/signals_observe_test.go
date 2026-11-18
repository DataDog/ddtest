package runner

import (
	"os"
	"sync"
	"syscall"
	"testing"
)

// From the moment that finish can see the first signal, and so skip the
// map's upload, the run's status is the signal's: a run whose upload a
// signal skips or cancels ends with that signal, not with go test's status.
func TestObserveSignalsStatusOnceSeen(t *testing.T) {
	between, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	original := betweenSignalSteps
	betweenSignalSteps = func() {
		close(between)
		<-release
	}
	t.Cleanup(func() { betweenSignalSteps = original })
	in := make(chan os.Signal, 4)
	signaled := &runSignals{done: make(chan struct{})}
	out := observeSignals(in, signaled)
	in <- syscall.SIGTERM
	<-between
	if !signaled.seen.Load() {
		t.Fatal("the signal is not seen")
	}
	// finish skips the upload now and the run ends: its status.
	status := make(chan int, 1)
	go func() { status <- signaled.status(out, 0, nil) }()
	once.Do(func() { close(release) })
	if got := <-status; got != interruptedStatus(syscall.SIGTERM) {
		t.Fatalf("status %d once the signal is seen", got)
	}
}

// A signal that go test took, as runGo does, leaves go test's status.
func TestObserveSignalsConsumedByGoTest(t *testing.T) {
	in := make(chan os.Signal, 4)
	signaled := &runSignals{done: make(chan struct{})}
	out := observeSignals(in, signaled)
	in <- syscall.SIGTERM
	<-out
	<-signaled.done
	if got := signaled.status(out, 3, nil); got != 3 {
		t.Fatalf("status %d, want go test's 3", got)
	}
	if got := (&runSignals{done: make(chan struct{})}).status(make(chan os.Signal), 4, nil); got != 4 {
		t.Fatalf("status %d without a signal", got)
	}
}
