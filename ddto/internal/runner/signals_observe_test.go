package runner

import (
	"os"
	"sync"
	"syscall"
	"testing"
)

// The relay records the first signal before it passes it on: once finish can
// see it, and so skip the map's upload, the run's status is the signal's,
// not go test's.
func TestObserveSignalsStatusOnceSeen(t *testing.T) {
	held, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	original := betweenSignalSteps
	betweenSignalSteps = func() {
		close(held)
		<-release
	}
	t.Cleanup(func() { betweenSignalSteps = original })
	in := make(chan os.Signal, 4)
	signaled := &runSignals{done: make(chan struct{})}
	out := (*gitWorker)(nil).relaySignals(in, signaled)
	in <- syscall.SIGTERM
	<-held
	if signaled.seen.Load() || len(out) != 0 {
		t.Fatalf("held before it records the signal, seen %t and %d passed on", signaled.seen.Load(), len(out))
	}
	once.Do(func() { close(release) })
	<-signaled.done
	if !signaled.seen.Load() {
		t.Fatal("done closed before the signal was seen")
	}
	// finish skips the upload, and the run ends: its status.
	if got := signaled.status(out, 0, nil); got != interruptedStatus(syscall.SIGTERM) {
		t.Fatalf("status %d once the signal is seen", got)
	}
}

// A signal that go test took, as runGo does, leaves go test's status.
func TestObserveSignalsConsumedByGoTest(t *testing.T) {
	in := make(chan os.Signal, 4)
	signaled := &runSignals{done: make(chan struct{})}
	out := (*gitWorker)(nil).relaySignals(in, signaled)
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
