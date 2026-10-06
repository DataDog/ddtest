package cidelivery

import (
	"context"
	"sync"
	"time"
)

// A pending leak check blocks new sends before waiting for active sends.
// State changes wake waiters through a channel, so sends can honor deadlines.
var sendGate struct {
	checks  sync.Mutex
	mu      sync.Mutex
	active  int
	paused  bool
	changed chan struct{}
}

func sendGateChangedLocked() {
	if sendGate.changed != nil {
		close(sendGate.changed)
		sendGate.changed = nil
	}
}
func sendGateWaitLocked() <-chan struct{} {
	if sendGate.changed == nil {
		sendGate.changed = make(chan struct{})
	}
	return sendGate.changed
}

// backgroundWork tracks CI start-up goroutines that are neither senders nor
// named workers, such as the repository upload and its git subprocesses.
var backgroundWork struct {
	sync.Mutex
	active int
	idle   chan struct{} // Closed whenever active returns to zero.
}

// backgroundWaitLimit matches the SDK's own wait for the repository upload.
const backgroundWaitLimit = time.Minute

// TrackBackground registers CI work that a leak check must wait for. Call it
// before starting the goroutine; the returned function is idempotent.
func TrackBackground() func() {
	backgroundWork.Lock()
	if backgroundWork.active == 0 {
		backgroundWork.idle = make(chan struct{})
	}
	backgroundWork.active++
	backgroundWork.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			backgroundWork.Lock()
			backgroundWork.active--
			if backgroundWork.active == 0 {
				close(backgroundWork.idle)
			}
			backgroundWork.Unlock()
		})
	}
}

// waitForBackground reports whether tracked work finished within the limit.
func waitForBackground(limit time.Duration) bool {
	backgroundWork.Lock()
	active, idle := backgroundWork.active, backgroundWork.idle
	backgroundWork.Unlock()
	if active == 0 {
		return true
	}
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case <-idle:
		return true
	case <-timer.C:
		return false
	}
}

var connections struct {
	sync.Mutex
	closers map[*connectionCloser]struct{}
}

type connectionCloser struct{ close func() }

// BeginSend and EndSend bracket only CI network delivery. A leak check waits
// for existing sends, then pauses new ones until goleak has taken its snapshots.
// The named waiting frame lets the shim distinguish our senders from user work.
func BeginSend() { _ = BeginSendContext(context.Background()) }

// BeginSendContext waits for leak-check admission without ignoring cancellation.
// Only a successful call acquires a send slot and must be paired with EndSend.
func BeginSendContext(ctx context.Context) error {
	sendGate.mu.Lock()
	defer sendGate.mu.Unlock()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !sendGate.paused {
			sendGate.active++
			return nil
		}
		changed := sendGateWaitLocked()
		sendGate.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
		}
		sendGate.mu.Lock()
	}
}
func EndSend() {
	sendGate.mu.Lock()
	sendGate.active--
	if sendGate.active == 0 {
		sendGateChangedLocked()
	}
	sendGate.mu.Unlock()
}

func pauseSends() func() {
	sendGate.checks.Lock()
	sendGate.mu.Lock()
	sendGate.paused = true
	for sendGate.active != 0 {
		changed := sendGateWaitLocked()
		sendGate.mu.Unlock()
		<-changed
		sendGate.mu.Lock()
	}
	sendGate.mu.Unlock()
	return func() {
		sendGate.mu.Lock()
		sendGate.paused = false
		sendGateChangedLocked()
		sendGate.mu.Unlock()
		sendGate.checks.Unlock()
	}
}

func RegisterConnectionCloser(close func()) func() {
	entry := &connectionCloser{close: close}
	connections.Lock()
	if connections.closers == nil {
		connections.closers = make(map[*connectionCloser]struct{})
	}
	connections.closers[entry] = struct{}{}
	connections.Unlock()
	return func() {
		connections.Lock()
		delete(connections.closers, entry)
		connections.Unlock()
	}
}

// PrepareLeakCheck leaves goroutine discovery to goleak. Closing only owned CI
// connections allows its normal retry loop to observe HTTP workers terminating;
// no net/http function or goroutine snapshot is added to an ignore filter.
func PrepareLeakCheck() func() {
	// Tracked start-up work, such as the repository upload, would otherwise be
	// reported as a leak while it runs git or waits between requests.
	waitForBackground(backgroundWaitLimit)
	resume := pauseSends()
	connections.Lock()
	closers := make([]func(), 0, len(connections.closers))
	for entry := range connections.closers {
		closers = append(closers, entry.close)
	}
	connections.Unlock()
	for _, close := range closers {
		close()
	}
	return resume
}
