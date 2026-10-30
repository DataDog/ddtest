package leak

import (
	"testing"
	"time"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func TestNoLeak(t *testing.T) { time.Sleep(10 * time.Millisecond) }

var block = make(chan struct{})

func TestLeaks(t *testing.T) {
	go func() { <-block }()
}
