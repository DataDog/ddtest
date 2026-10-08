package minitracer

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/cidelivery"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/citransport"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils/telemetry"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/msgp/msgp"
)

// Ordinary delivery runs up to maxConcurrentSends background senders, like the
// SDK's concurrent flushes, so intake latency does not cap event throughput.
// maxPendingBatches bounds the sealed batches waiting for or in delivery.
const (
	maxConcurrentSends = 8
	maxPendingBatches  = 8
)

// Background retries after a failed delivery start no sooner than this backoff,
// which doubles up to maxRetryBackoff while failures continue.
const (
	minRetryBackoff = time.Second
	maxRetryBackoff = 10 * time.Second
)

var (
	errClientClosed = errors.New("event finished after CI client closed")
	errOversized    = errors.New("CI event exceeds the test-cycle payload limit")
	errQueueFull    = errors.New("CI event queue is full while intake delivery is failing")
)

// readyBatch is a sealed intake payload: events no longer receive additions.
type readyBatch struct {
	events []*ciEvent
	bytes  int
}

// add never performs network I/O. A full batch is sealed for the background
// senders in ordinary mode, or for the next idle checkpoint in deferred mode.
//
// When ordinary delivery reaches maxPendingBatches, the finishing test waits
// for a sender while the intake accepts payloads, as the SDK's writer does, so
// a slow intake loses no events. After a failed delivery and until the next
// success, events beyond the bound are rejected instead: an unavailable intake
// cannot stall tests. Deferred delivery never waits; its batches stay queued
// until an idle checkpoint.
func (c *Client) add(event *ciEvent) {
	size := eventSize(event)
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed && size+c.envelopeBytes > citransport.TestCycleMaxPayloadBytes {
		c.rejectLocked(errOversized)
		return
	}
	for {
		if c.closed {
			c.rejectLocked(errClientClosed)
			return
		}
		if !c.openBatchFullLocked(size) || c.sealLocked() {
			break
		}
		c.startSendersLocked()
		if c.failing {
			c.rejectLocked(errQueueFull)
			return
		}
		c.space.Wait()
	}
	telemetry.EventsEnqueueForSerialization()
	c.events = append(c.events, event)
	c.queuedBytes += size
	if len(c.events) >= c.maxEvents || c.queuedBytes+c.envelopeBytes >= citransport.TestCycleFlushBytes {
		c.sealLocked()
	}
	c.startSendersLocked()
}

func (c *Client) openBatchFullLocked(size int) bool {
	return len(c.events) > 0 && (len(c.events) >= c.maxEvents || c.queuedBytes+size+c.envelopeBytes > citransport.TestCycleFlushBytes)
}

// sealLocked moves the open batch to the ready queue. Ordinary mode keeps that
// queue bounded; deferred mode keeps every batch until an idle checkpoint.
func (c *Client) sealLocked() bool {
	if len(c.events) == 0 {
		return true
	}
	if !c.deferUntilIdle && len(c.ready)+c.inflight >= maxPendingBatches {
		return false
	}
	c.sealAllLocked()
	return true
}

// sealAllLocked seals the open batch for an explicit flush or closure, even
// when ordinary delivery has reached its pending bound.
func (c *Client) sealAllLocked() {
	if len(c.events) == 0 {
		return
	}
	c.ready = append(c.ready, readyBatch{events: c.events, bytes: c.queuedBytes})
	c.events, c.spare = c.spare, nil
	c.queuedBytes = 0
}

// startSendersLocked starts ordinary-mode senders for sealed batches that no
// running sender is about to take, up to maxConcurrentSends, unless a failure
// backoff is pending. Deferred mode delivers only at idle checkpoints.
func (c *Client) startSendersLocked() {
	if c.deferUntilIdle || c.closed || c.senders >= maxConcurrentSends || c.senders-c.sendersBusy >= len(c.ready) || c.backoffLocked() {
		return
	}
	for c.senders < maxConcurrentSends && c.senders-c.sendersBusy < len(c.ready) {
		c.senders++
		go c.deliverInBackground()
	}
}

// backoffLocked reports a pending retry delay after a failed delivery.
func (c *Client) backoffLocked() bool {
	return !c.nextRetry.IsZero() && time.Now().Before(c.nextRetry)
}

// deliverInBackground is one ordinary-mode sender. It sends one sealed batch at
// a time, each bounded by the flush timeout, and encodes into its own buffer.
// The goleak shim recognizes this frame. A failure returns the batch to the
// queue and starts the backoff, which ends this sender.
func (c *Client) deliverInBackground() {
	c.mu.Lock()
	buffer := c.takeBufferLocked()
	for !c.closed && len(c.ready) > 0 && !c.backoffLocked() {
		batch := c.popReadyLocked()
		c.sendersBusy++
		c.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
		err := c.sendBatch(ctx, buffer, batch)
		cancel()
		c.mu.Lock()
		c.sendersBusy--
		c.completeLocked(batch, err)
	}
	c.senders--
	c.returnBufferLocked(buffer)
	c.mu.Unlock()
}

// popReadyLocked takes the oldest sealed batch; it still counts toward the
// pending bound until delivery completes.
func (c *Client) popReadyLocked() readyBatch {
	batch := c.ready[0]
	c.ready[0] = readyBatch{}
	c.ready = c.ready[1:]
	c.inflight++
	return batch
}

// completeLocked ends one delivery attempt. A failed batch returns to the front
// of the queue while the client is open; a closed client abandons it, counting
// one dropped payload. Any success clears the failure state and the backoff.
func (c *Client) completeLocked(batch readyBatch, err error) {
	c.inflight--
	if c.inflight == 0 && c.inflightDone != nil {
		close(c.inflightDone)
		c.inflightDone = nil
	}
	if err != nil {
		c.lastErr = err
		c.failing = true
		c.retryBackoff = min(max(2*c.retryBackoff, minRetryBackoff), maxRetryBackoff)
		c.nextRetry = time.Now().Add(c.retryBackoff)
		if c.closed {
			c.terminalFailures++
			c.terminalErr = err
			c.discardBatchLocked(batch.events)
		} else {
			c.ready = append([]readyBatch{batch}, c.ready...)
		}
	} else {
		clear(batch.events)
		if c.spare == nil {
			c.spare = batch.events[:0] // Reuse delivered storage for a later batch.
		}
		c.failing = false
		c.retryBackoff, c.nextRetry = 0, time.Time{}
	}
	// Waiting finishers recheck the bound, the failure state and closure.
	c.space.Broadcast()
}

// takeBufferLocked and returnBufferLocked reuse encoding buffers between the
// background senders. Unusually large buffers are released instead.
func (c *Client) takeBufferLocked() *bytes.Buffer {
	if n := len(c.buffers); n > 0 {
		buffer := c.buffers[n-1]
		c.buffers[n-1] = nil
		c.buffers = c.buffers[:n-1]
		return buffer
	}
	return new(bytes.Buffer)
}

func (c *Client) returnBufferLocked(buffer *bytes.Buffer) {
	if buffer.Cap() == 0 || buffer.Cap() > citransport.TestCycleFlushBytes {
		return
	}
	buffer.Reset()
	c.buffers = append(c.buffers, buffer)
}

// deliverReady sends every sealed batch at an idle checkpoint, waiting for any
// explicit flush in progress. Deferred mode has no background senders.
func (c *Client) deliverReady(ctx context.Context) error {
	c.sendMu <- struct{}{}
	defer c.release()
	return c.drainLocked(ctx, true)
}

// drain is the state shared by the senders of one drainLocked call.
type drain struct {
	ctx             context.Context
	perBatchTimeout bool
	err             error // First failure, guarded by Client.mu.
}

// drainLocked delivers sealed batches, oldest first; the caller holds the
// delivery token, which owns c.payload. With perBatchTimeout each batch gets
// its own flush timeout. No batch starts after the first failure, which is
// returned.
//
// Deferred mode has no background senders, so the caller sends together with
// up to maxConcurrentSends-1 drainWorker goroutines, all finished before it
// returns: no delivery goroutine outlives a checkpoint, Flush or Close.
// Payloads sent together can arrive in any order.
func (c *Client) drainLocked(ctx context.Context, perBatchTimeout bool) error {
	d := &drain{ctx: ctx, perBatchTimeout: perBatchTimeout}
	var workers sync.WaitGroup
	if c.deferUntilIdle {
		c.mu.Lock()
		extra := min(len(c.ready), maxConcurrentSends) - 1
		c.mu.Unlock()
		for i := 0; i < extra; i++ {
			workers.Add(1)
			go c.drainWorker(d, &workers)
		}
	}
	c.drainBatches(d, &c.payload)
	workers.Wait()
	return d.err
}

// drainWorker is an additional deferred-mode sender of one drain, with its own
// encoding buffer. The goleak shim recognizes this frame.
func (c *Client) drainWorker(d *drain, done *sync.WaitGroup) {
	defer done.Done()
	c.mu.Lock()
	buffer := c.takeBufferLocked()
	c.mu.Unlock()
	c.drainBatches(d, buffer)
	c.mu.Lock()
	c.returnBufferLocked(buffer)
	c.mu.Unlock()
}

// drainBatches sends sealed batches with the given buffer until the queue is
// empty or a delivery of the same drain has failed.
func (c *Client) drainBatches(d *drain, payload *bytes.Buffer) {
	for {
		c.mu.Lock()
		if len(c.ready) == 0 || d.err != nil {
			c.mu.Unlock()
			return
		}
		batch := c.popReadyLocked()
		c.mu.Unlock()
		sendCtx, cancel := d.ctx, context.CancelFunc(func() {})
		if d.perBatchTimeout {
			sendCtx, cancel = context.WithTimeout(d.ctx, c.timeout)
		}
		err := c.sendBatch(sendCtx, payload, batch)
		cancel()
		c.mu.Lock()
		c.completeLocked(batch, err)
		if err != nil && d.err == nil {
			d.err = err
		}
		c.mu.Unlock()
	}
}

// waitInflight waits until no batch is being delivered, typically by the
// background senders.
func (c *Client) waitInflight(ctx context.Context) error {
	c.mu.Lock()
	if c.inflight == 0 {
		c.mu.Unlock()
		return nil
	}
	if c.inflightDone == nil {
		c.inflightDone = make(chan struct{})
	}
	done := c.inflightDone
	c.mu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// sendBatch encodes into the given buffer, which the caller owns exclusively.
func (c *Client) sendBatch(ctx context.Context, payload *bytes.Buffer, batch readyBatch) error {
	serializationStart := time.Now()
	payload.Reset()
	payload.Grow(batch.bytes + c.envelopeBytes)
	metadata, wireEvents := prepareCommonMetadata(c.metadata, batch.events)
	err := msgp.Encode(payload, &testCycleBatch{Version: 1, Metadata: metadata, Events: wireEvents})
	if err == nil {
		telemetry.EndpointPayloadEventsCount(telemetry.TestCycleEndpointType, float64(len(batch.events)))
		telemetry.EndpointPayloadBytes(telemetry.TestCycleEndpointType, float64(payload.Len()))
		telemetry.EndpointEventsSerializationMs(telemetry.TestCycleEndpointType, float64(time.Since(serializationStart).Milliseconds()))
		err = c.transport.Send(ctx, payload.Bytes())
	}
	if payload.Cap() > citransport.TestCycleFlushBytes {
		*payload = bytes.Buffer{}
	}
	return err
}

func (c *Client) rejectLocked(err error) {
	c.lastErr = err
	c.dropped++
}

// DroppedEvents reports rejected events and events abandoned at final closure.
// This event count is separate from the payload-drop telemetry counter.
func (c *Client) DroppedEvents() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dropped
}

// Flush delivers every buffered event, including the open batch, and waits for
// the result. A failed batch stays queued while the client is open. In
// deferred mode an active test leaves delivery to its next idle checkpoint; it
// must not wait for itself.
func (c *Client) Flush(ctx context.Context) error {
	if c.deferUntilIdle {
		if err := ctx.Err(); err != nil {
			return err
		}
		ran := false
		err := cidelivery.RunIfIdle(ctx, func() error { ran = true; return c.flush(ctx) })
		if !ran && err == nil {
			// The next checkpoint also delivers the open batch.
			c.mu.Lock()
			c.flushRequested = true
			c.mu.Unlock()
		}
		return err
	}
	return c.flush(ctx)
}

// flush seals the open batch and delivers every sealed batch, including those
// the background senders still hold: a sender that fails returns its batch to
// the queue, which is delivered again here.
func (c *Client) flush(ctx context.Context) error {
	if err := c.acquire(ctx); err != nil {
		return err
	}
	defer c.release()
	c.mu.Lock()
	c.sealAllLocked()
	c.space.Broadcast() // The open batch is empty again.
	c.mu.Unlock()
	for {
		if err := c.drainLocked(ctx, false); err != nil {
			return err
		}
		if err := c.waitInflight(ctx); err != nil {
			return err
		}
		c.mu.Lock()
		pending := len(c.ready)
		c.mu.Unlock()
		if pending == 0 {
			return nil
		}
	}
}

// discardBatchLocked records one abandoned payload, regardless of its event
// count. The caller holds mu; retryable flush failures never call this method.
func (c *Client) discardBatchLocked(batch []*ciEvent) {
	if len(batch) == 0 {
		return
	}
	c.dropped += uint64(len(batch))
	telemetry.EndpointPayloadDropped(telemetry.TestCycleEndpointType)
	clear(batch)
}

// Close seals the client, performs a final flush and releases connections.
// An unsuccessful final flush abandons each remaining payload once. Earlier
// Flush failures remain retryable; a closed client cannot resend an abandoned
// payload. A delivery already in flight at entry is part of this final flush;
// its terminal error is returned too. Later empty closes do not replay it.
func (c *Client) Close(ctx context.Context) error {
	defer c.removeConnectionCloser()
	if c.removeIdleFlush != nil {
		c.removeIdleFlush()
	}
	c.mu.Lock()
	previousFailures := c.terminalFailures
	c.closed = true
	c.space.Broadcast() // Waiting finishers reject their events.
	c.mu.Unlock()
	// Closure is terminal, including panic/signal shutdown. The runtime drains
	// its idle coordinator first; explicit clients still own final delivery.
	err := c.flush(ctx)
	if err != nil {
		c.mu.Lock()
		// If acquiring the token was canceled, queued batches still need to be
		// abandoned. A batch in flight belongs to its sender, which abandons it.
		c.sealAllLocked()
		for _, batch := range c.ready {
			c.discardBatchLocked(batch.events)
		}
		c.ready, c.events, c.queuedBytes = nil, nil, 0
		c.lastErr = err
		c.mu.Unlock()
	}
	c.mu.Lock()
	if err == nil && c.terminalFailures != previousFailures {
		err = c.terminalErr
	}
	c.mu.Unlock()
	c.transport.CloseIdleConnections()
	return err
}

// LastError returns the most recent delivery or rejection error.
func (c *Client) LastError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastErr
}
