// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package coverage

import (
	"sync"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/cidelivery"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils/net"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils/telemetry"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"
)

// Constants defining the payload size limits for agentless mode.
const (
	// agentlessPayloadMaxLimit is the maximum payload size allowed, indicating the
	// maximum size of the package that the intake can receive.
	agentlessPayloadMaxLimit = 50 * 1024 * 1024 // 5 MB

	// agentlessPayloadSizeLimit specifies the maximum allowed size of the payload before
	// it triggers a flush to the transport.
	agentlessPayloadSizeLimit = agentlessPayloadMaxLimit / 2

	// concurrentConnectionLimit specifies the maximum number of concurrent outgoing
	// connections allowed.
	concurrentConnectionLimit = 100
)

type coverageWriter struct {
	client   net.Client       // http client
	payload  *coveragePayload // Encodes and buffers events in msgpack format.
	climit   chan struct{}    // Limits the number of concurrent outgoing connections.
	wg       sync.WaitGroup   // Waits for all uploads to finish.
	mu       sync.Mutex       // Guards payload rotation between add and flush.
	deferred bool
}

func newCoverageWriter() *coverageWriter {
	log.Debug("coverageWriter: creating trace writer instance")
	writer := &coverageWriter{
		client:   net.NewClientForCodeCoverage(),
		payload:  newCoveragePayload(),
		climit:   make(chan struct{}, concurrentConnectionLimit),
		deferred: cidelivery.Enabled(),
	}
	// Deferred mode queues only full payloads for idle checkpoints; the partial
	// payload waits for stop, so a serial suite does not send one per test.
	return writer
}

func (w *coverageWriter) add(coverage *testCoverage) {
	telemetry.EventsEnqueueForSerialization()
	ciTestCoverage := newCiTestCoverageData(coverage)
	var payloadToFlush *coveragePayload

	w.mu.Lock()
	if err := w.payload.push(ciTestCoverage); err != nil {
		log.Error("coverageWriter: Error encoding msgpack: %s", err.Error())
	}
	if w.payload.size() > agentlessPayloadSizeLimit {
		payloadToFlush = w.rotatePayloadLocked()
	}
	w.mu.Unlock()

	if payloadToFlush != nil {
		w.flushPayload(payloadToFlush)
	}
}

func (w *coverageWriter) stop() {
	log.Debug("coverageWriter: stopping writer")
	w.flush()
	if w.deferred {
		cidelivery.Checkpoint()
	}
	w.wg.Wait()
	if closer, ok := w.client.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

func (w *coverageWriter) flush() {
	w.mu.Lock()
	payloadToFlush := w.rotatePayloadLocked()
	w.mu.Unlock()

	if payloadToFlush != nil {
		w.flushPayload(payloadToFlush)
	}
}

// rotatePayloadLocked swaps out the current payload while w.mu is held.
func (w *coverageWriter) rotatePayloadLocked() *coveragePayload {
	if w.payload.itemCount() == 0 {
		return nil
	}

	oldp := w.payload
	w.payload = newCoveragePayload()
	return oldp
}

// flushPayload reserves ownership before queueing. Deferred work acquires its
// connection permit only at the checkpoint, so a large parallel group cannot
// deadlock after filling the asynchronous connection limit.
func (w *coverageWriter) flushPayload(oldp *coveragePayload) {
	w.wg.Add(1)
	if w.deferred {
		cidelivery.Queue(func() {
			w.climit <- struct{}{}
			w.sendPayload(oldp)
		})
		return
	}
	w.climit <- struct{}{}
	go w.sendPayload(oldp)
}

func (w *coverageWriter) sendPayload(p *coveragePayload) {
	defer func() {
		p.clear()
		<-w.climit
		w.wg.Done()
	}()
	size, count := p.size(), p.itemCount()
	log.Debug("coverageWriter: sending payload: size: %d events: %d\n", size, count)
	buf, err := p.getBuffer()
	if err != nil {
		log.Error("coverageWriter: failure getting coverage data: %s", err.Error())
		return
	}
	telemetry.CodeCoverageFiles(float64(p.itemCount()))
	if err := w.client.SendCoveragePayload(buf); err != nil {
		log.Error("coverageWriter: failure sending coverage data: %s", err.Error())
	}
}
