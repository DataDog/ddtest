// Package cidelivery coordinates opt-in delivery between instrumented tests.
// It starts no goroutines. A checkpoint owns admission until all queued work
// completes; tests already admitted may continue to run in parallel.
package cidelivery

import (
	"context"
	"os"
	"strconv"
	"sync"
)

const DeferredEnv = "DD_CIVISIBILITY_DEFERRED_DELIVERY"

func Enabled() bool {
	v, _ := strconv.ParseBool(os.Getenv(DeferredEnv))
	return v
}

// Coordinator keeps delivery outside test bodies and their cleanup callbacks.
// Buffered work may grow for the lifetime of a parallel group: waiting for an
// idle checkpoint from inside that group would deadlock Go's test scheduler.
type Coordinator struct {
	admission contextMutex
	active    int
	mu        sync.Mutex
	callbacks []*idleCallback
	pending   []func()
}

type idleCallback struct{ run func() }

var process Coordinator

// Begin waits for an idle delivery checkpoint, then admits a test. Release is
// idempotent because retry cleanup and native cleanup may share a registration.
func Begin() func() { return process.begin() }

func (c *Coordinator) begin() func() {
	c.admission.Lock()
	if c.active == 0 {
		c.drain()
	}
	c.active++
	c.admission.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			c.admission.Lock()
			defer c.admission.Unlock()
			c.active--
			if c.active == 0 {
				c.drain()
			}
		})
	}
}

// Register installs a synchronous checkpoint callback. Removing a callback
// does not wait for a callback already captured by a running checkpoint.
func Register(run func()) func() { return process.register(run) }

func (c *Coordinator) register(run func()) func() {
	entry := &idleCallback{run: run}
	c.mu.Lock()
	c.callbacks = append(c.callbacks, entry)
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		entry.run = nil
		c.mu.Unlock()
	}
}

// Queue transfers ownership of a closed coverage/log batch until a checkpoint.
// The work itself must release that batch, including on delivery failure.
func Queue(run func()) {
	process.queue(run)
}

func (c *Coordinator) queue(run func()) {
	c.mu.Lock()
	c.pending = append(c.pending, run)
	c.mu.Unlock()
}

// Checkpoint drains only when no test is admitted. Explicit flush calls during
// a test leave delivery to the next checkpoint rather than waiting on the test.
func Checkpoint() {
	process.admission.Lock()
	defer process.admission.Unlock()
	if process.active == 0 {
		process.drain()
	}
}

// Shutdown drains even while a test is unwinding through a terminal panic or
// signal. It preserves final delivery and lets writer shutdown join its batches.
func Shutdown() {
	process.admission.Lock()
	defer process.admission.Unlock()
	process.drain()
}

// RunIfIdle serializes an explicit client flush with test admission. A skipped
// flush is picked up by the client's registered checkpoint callback.
func RunIfIdle(ctx context.Context, run func() error) error {
	return process.runIfIdle(ctx, run)
}

func (c *Coordinator) runIfIdle(ctx context.Context, run func() error) error {
	if err := c.admission.lock(ctx); err != nil {
		return err
	}
	defer c.admission.Unlock()
	if c.active != 0 {
		return nil
	}
	return run()
}

func (c *Coordinator) drain() {
	c.mu.Lock()
	var callbacks []func()
	kept := c.callbacks[:0]
	for _, entry := range c.callbacks {
		if entry.run != nil {
			callbacks = append(callbacks, entry.run)
			kept = append(kept, entry)
		}
	}
	clear(c.callbacks[len(kept):])
	c.callbacks = kept
	c.mu.Unlock()
	for _, run := range callbacks {
		run()
	}
	for {
		c.mu.Lock()
		work := c.pending
		c.pending = nil
		c.mu.Unlock()
		if len(work) == 0 {
			return
		}
		for _, run := range work {
			run()
		}
	}
}
