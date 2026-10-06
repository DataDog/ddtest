package minitracer

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/cidelivery"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/citransport"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils/telemetry"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/msgp/msgp"
)

// maxReadyBatches bounds ordinary delivery: sealed batches waiting for intake
// plus the open batch. Beyond that, new events are rejected instead of waiting.
const maxReadyBatches = 4

// Background retries after a failed delivery start no sooner than this backoff,
// which doubles up to maxRetryBackoff while failures continue.
const (
	minRetryBackoff = time.Second
	maxRetryBackoff = 10 * time.Second
)

var (
	errClientClosed = errors.New("event finished after CI client closed")
	errOversized    = errors.New("CI event exceeds the test-cycle payload limit")
	errQueueFull    = errors.New("CI event queue is full while intake delivery is failing or slow")
)

// readyBatch is a sealed intake payload: events no longer receive additions.
type readyBatch struct {
	events []*ciEvent
	bytes  int
}

// add never performs network I/O, so a slow or unreachable intake cannot delay
// the finishing test. A full batch is sealed and handed to one background
// sender in ordinary mode, or to the next idle checkpoint in deferred mode.
func (c *Client) add(event *ciEvent) {
	size := eventSize(event)
	c.mu.Lock()
	switch {
	case c.closed:
		c.rejectLocked(errClientClosed)
	case size+c.envelopeBytes > citransport.TestCycleMaxPayloadBytes:
		c.rejectLocked(errOversized)
	case c.openBatchFullLocked(size) && !c.sealLocked():
		c.rejectLocked(errQueueFull)
	default:
		telemetry.EventsEnqueueForSerialization()
		c.events = append(c.events, event)
		c.queuedBytes += size
		if len(c.events) >= c.maxEvents || c.queuedBytes+c.envelopeBytes >= citransport.TestCycleFlushBytes {
			c.sealLocked()
		}
	}
	start := c.backgroundDueLocked()
	c.mu.Unlock()
	if start {
		go c.deliverInBackground()
	}
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
	if !c.deferUntilIdle && len(c.ready)+c.inflight >= maxReadyBatches {
		return false
	}
	c.sealAllLocked()
	return true
}

// sealAllLocked seals the open batch for an explicit flush or closure, even
// when ordinary delivery has reached its ready-queue bound.
func (c *Client) sealAllLocked() {
	if len(c.events) == 0 {
		return
	}
	c.ready = append(c.ready, readyBatch{events: c.events, bytes: c.queuedBytes})
	c.events, c.spare = c.spare, nil
	c.queuedBytes = 0
}

// backgroundDueLocked starts ordinary delivery when sealed batches wait, no
// sender runs, and any failure backoff has elapsed.
func (c *Client) backgroundDueLocked() bool {
	if c.deferUntilIdle || c.closed || c.sending || len(c.ready) == 0 || time.Now().Before(c.nextRetry) {
		return false
	}
	c.sending = true
	return true
}

// deliverInBackground sends sealed batches one at a time, each bounded by the
// flush timeout. A failure keeps its batch first in line for the next attempt.
func (c *Client) deliverInBackground() {
	for {
		err := c.deliverReady(context.Background())
		c.mu.Lock()
		if err != nil {
			c.retryBackoff = min(max(2*c.retryBackoff, minRetryBackoff), maxRetryBackoff)
			c.nextRetry = time.Now().Add(c.retryBackoff)
		}
		again := err == nil && !c.closed && len(c.ready) > 0
		if !again {
			c.sending = false
		}
		c.mu.Unlock()
		if !again {
			return
		}
	}
}

// deliverReady sends every sealed batch, waiting for any delivery in progress.
func (c *Client) deliverReady(ctx context.Context) error {
	c.sendMu <- struct{}{}
	defer c.release()
	return c.drainLocked(ctx, true)
}

// drainLocked delivers sealed batches in order; the caller holds the delivery
// token. With perBatchTimeout each batch gets its own flush timeout. A failed
// batch returns to the front while the client is open; a closed client
// abandons it, counting one dropped payload.
func (c *Client) drainLocked(ctx context.Context, perBatchTimeout bool) error {
	for {
		c.mu.Lock()
		if len(c.ready) == 0 {
			c.mu.Unlock()
			return nil
		}
		batch := c.ready[0]
		c.ready[0] = readyBatch{}
		c.ready = c.ready[1:]
		c.inflight++ // Still counts toward the bound until delivery ends.
		c.mu.Unlock()
		sendCtx, cancel := ctx, context.CancelFunc(func() {})
		if perBatchTimeout {
			sendCtx, cancel = context.WithTimeout(ctx, c.timeout)
		}
		err := c.sendBatch(sendCtx, batch)
		cancel()
		c.mu.Lock()
		c.inflight--
		if err != nil {
			c.lastErr = err
			if c.closed {
				c.discardBatchLocked(batch.events)
			} else {
				c.ready = append([]readyBatch{batch}, c.ready...)
			}
			c.mu.Unlock()
			return err
		}
		clear(batch.events)
		if c.spare == nil {
			c.spare = batch.events[:0] // Reuse delivered storage for a later batch.
		}
		// Intake accepts payloads again: background delivery need not wait.
		c.retryBackoff, c.nextRetry = 0, time.Time{}
		c.mu.Unlock()
	}
}

// sendBatch encodes into the reusable payload owned by the delivery token.
func (c *Client) sendBatch(ctx context.Context, batch readyBatch) error {
	serializationStart := time.Now()
	c.payload.Reset()
	c.payload.Grow(batch.bytes + c.envelopeBytes)
	metadata, wireEvents := prepareCommonMetadata(c.metadata, batch.events)
	err := msgp.Encode(&c.payload, &testCycleBatch{Version: 1, Metadata: metadata, Events: wireEvents})
	if err == nil {
		telemetry.EndpointPayloadEventsCount(telemetry.TestCycleEndpointType, float64(len(batch.events)))
		telemetry.EndpointPayloadBytes(telemetry.TestCycleEndpointType, float64(c.payload.Len()))
		telemetry.EndpointEventsSerializationMs(telemetry.TestCycleEndpointType, float64(time.Since(serializationStart).Milliseconds()))
		err = c.transport.Send(ctx, c.payload.Bytes())
	}
	if c.payload.Cap() > citransport.TestCycleFlushBytes {
		c.payload = bytes.Buffer{}
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
		err := cidelivery.RunIfIdle(func() error { ran = true; return c.flush(ctx) })
		if !ran {
			// The next checkpoint also delivers the open batch.
			c.mu.Lock()
			c.flushRequested = true
			c.mu.Unlock()
		}
		return err
	}
	return c.flush(ctx)
}

func (c *Client) flush(ctx context.Context) error {
	if err := c.acquire(ctx); err != nil {
		return err
	}
	defer c.release()
	c.mu.Lock()
	c.sealAllLocked()
	c.mu.Unlock()
	return c.drainLocked(ctx, false)
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
// payload.
func (c *Client) Close(ctx context.Context) error {
	defer c.removeConnectionCloser()
	if c.removeIdleFlush != nil {
		c.removeIdleFlush()
	}
	c.mu.Lock()
	c.closed = true
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
	c.transport.CloseIdleConnections()
	return err
}

// LastError returns the most recent delivery or rejection error.
func (c *Client) LastError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastErr
}
