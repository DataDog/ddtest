// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package logs

import (
	"sync"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/cidelivery"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils/net"
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

// logsWriter is responsible for writing logs to the agentless endpoint.
type logsWriter struct {
	client   net.Client     // http client
	payload  *logsPayload   // Encodes and buffers events in JSON format.
	climit   chan struct{}  // Limits the number of concurrent outgoing connections.
	wg       sync.WaitGroup // Waits for all uploads to finish.
	mu       sync.Mutex     // Guards payload rotation, stopped state, and upload reservations.
	stopped  bool           // Prevents new entries and reservations after shutdown starts.
	deferred bool
}

// newLogsWriter creates a new instance of logsWriter.
func newLogsWriter() *logsWriter {
	log.Debug("logsWriter: creating logs writer instance")
	writer := &logsWriter{
		client:   net.NewClientForLogs(),
		payload:  newLogsPayload(),
		climit:   make(chan struct{}, concurrentConnectionLimit),
		deferred: cidelivery.Enabled(),
	}
	// Deferred mode queues only full payloads for idle checkpoints; the partial
	// payload waits for stop, so a serial suite does not send one per test.
	return writer
}

func (w *logsWriter) add(entry *logEntry) bool {
	var payloadToFlush *logsPayload

	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return false
	}
	if err := w.payload.push(entry); err != nil {
		log.Error("logsWriter: Error encoding JSON: %s", err.Error())
		w.mu.Unlock()
		return false
	}
	if w.payload.size() > agentlessPayloadSizeLimit {
		payloadToFlush = w.rotateAndReserveLocked()
	}
	w.mu.Unlock()

	if payloadToFlush != nil {
		w.startUpload(payloadToFlush)
	}
	return true
}

func (w *logsWriter) stop() {
	log.Debug("logsWriter: stopping writer")
	w.mu.Lock()
	var payloadToFlush *logsPayload
	if !w.stopped {
		w.stopped = true
		payloadToFlush = w.rotateAndReserveLocked()
	}
	w.mu.Unlock()

	if payloadToFlush != nil {
		w.startUpload(payloadToFlush)
	}
	if w.deferred {
		// Shutdown can run before a panicking test releases admission.
		cidelivery.Shutdown()
	}
	w.wg.Wait()
	if closer, ok := w.client.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

func (w *logsWriter) flush() {
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return
	}
	payloadToFlush := w.rotateAndReserveLocked()
	w.mu.Unlock()

	if payloadToFlush != nil {
		w.startUpload(payloadToFlush)
	}
}

// rotateAndReserveLocked swaps the active payload and reserves an upload.
// w.mu must be held by the caller.
func (w *logsWriter) rotateAndReserveLocked() *logsPayload {
	if w.payload.itemCount() == 0 {
		return nil
	}

	oldp := w.payload
	w.payload = newLogsPayload()
	w.wg.Add(1)
	return oldp
}

// startUpload transfers the reserved batch to either the idle queue or the
// existing asynchronous sender. In both modes sendPayload releases ownership.
func (w *logsWriter) startUpload(oldp *logsPayload) {
	if w.deferred {
		cidelivery.Queue(func() { w.sendPayload(oldp) })
		return
	}
	go w.sendPayload(oldp)
}

func (w *logsWriter) sendPayload(p *logsPayload) {
	w.climit <- struct{}{}
	defer func() {
		p.clear()
		<-w.climit
		w.wg.Done()
	}()
	size, count := p.size(), p.itemCount()
	log.Debug("logsWriter: sending payload: size: %d logs entries: %d\n", size, count)
	if err := w.client.SendLogs(p); err != nil {
		log.Error("logsWriter: failure sending logs data data: %s", err.Error())
	}
}
