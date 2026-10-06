package internal

import (
	"testing"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/cidelivery"
)

func TestStoppedTickerCannotRestart(t *testing.T) {
	t.Setenv(cidelivery.DeferredEnv, "false")
	ticker := NewTicker(func() {}, Range[time.Duration]{Min: time.Millisecond, Max: 2 * time.Millisecond})
	ticker.Stop()
	ticker.Stop()
	// The worker has exited, so a restarted timer would fill its channel.
	ticker.CanIncreaseSpeed()
	ticker.CanDecreaseSpeed()
	time.Sleep(30 * time.Millisecond)
	select {
	case <-ticker.ticker.C:
		t.Fatal("a speed change restarted a stopped ticker")
	default:
	}
}
