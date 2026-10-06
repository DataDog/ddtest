package internal

import (
	"testing"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/cidelivery"
)

// Deferred tickers run at idle checkpoints, but no more often than their
// interval, so a serial suite does not flush telemetry once per test.
func TestDeferredTickerWaitsForItsInterval(t *testing.T) {
	t.Setenv(cidelivery.DeferredEnv, "true")
	ticks := 0
	ticker := NewTicker(func() { ticks++ }, Range[time.Duration]{Min: time.Millisecond, Max: 50 * time.Millisecond})
	defer ticker.Stop()
	for range 10 {
		ticker.tickIfDue()
	}
	if ticks != 0 {
		t.Fatalf("checkpoints ticked before the interval: %d", ticks)
	}
	time.Sleep(60 * time.Millisecond)
	ticker.tickIfDue()
	ticker.tickIfDue()
	if ticks != 1 {
		t.Fatalf("ticks after one interval: %d", ticks)
	}
	ticker.CanIncreaseSpeed() // Shorter interval, as the periodic ticker supports.
	time.Sleep(30 * time.Millisecond)
	ticker.tickIfDue()
	if ticks != 2 {
		t.Fatalf("ticks after a faster interval: %d", ticks)
	}
}
