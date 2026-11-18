package runner

import (
	"os"
	"sync"
	"syscall"
	"testing"
)

// Once the first signal cancels the map's upload, statusAfterSignal finds
// it: the signal is in the channel before done closes, so a run that the
// cancellation ends returns the signal's status, not go test's; and from
// before the signal goes on, finish sees it, so no upload starts.
func TestObserveSignalsBeforeCancel(t *testing.T) {
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
	// Between its steps, whatever the signal cancels finds it.
	if !signaled.seen.Load() || len(out) != 1 {
		t.Fatalf("seen %t, %d signals passed on", signaled.seen.Load(), len(out))
	}
	select {
	case <-signaled.done:
		t.Fatal("done closed before the signal was passed on")
	default:
	}
	once.Do(func() { close(release) })
	<-signaled.done
	if status := statusAfterSignal(out, 0, nil); status != interruptedStatus(syscall.SIGTERM) {
		t.Fatalf("status %d after the cancellation", status)
	}
}
