// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package telemetry

import (
	"math"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/telemetry/internal"
)

// noopMetricHandle is a no-op implementation of a metric handle.
type noopMetricHandle struct{}

func (noopMetricHandle) Submit(_ float64) {}

func (noopMetricHandle) Get() float64 {
	return math.NaN()
}

var metricLogLossOnce sync.Once

// swappableMetricHandle is a MetricHandle that holds a pointer to another MetricHandle and a recorder to replay actions done before the actual MetricHandle is set.
type swappableMetricHandle struct {
	ptr      atomic.Pointer[MetricHandle]
	recorder internal.Recorder[MetricHandle]
	maker    func(client Client) MetricHandle
}

func (t *swappableMetricHandle) Submit(value float64) {
	if Disabled() {
		return
	}

	inner := t.ptr.Load()
	if inner == nil || *inner == nil {
		if !t.recorder.Record(func(handle MetricHandle) {
			handle.Submit(value)
		}) {
			metricLogLossOnce.Do(func() {
				msg := "telemetry: metric is losing values because the telemetry client has not been started yet, dropping telemetry data, please start the telemetry client earlier to avoid data loss"
				log.Debug("%s\n", msg)
				Log(NewRecord(LogError, msg), WithStacktrace())
			})
		}
		return
	}

	(*inner).Submit(value)
}

func (t *swappableMetricHandle) Get() float64 {
	inner := t.ptr.Load()
	if inner == nil || *inner == nil {
		return 0
	}

	return (*inner).Get()
}

func (t *swappableMetricHandle) swap(handle MetricHandle) {
	if t.ptr.Swap(&handle) == nil {
		t.recorder.Replay(handle)
	}
}

var _ MetricHandle = (*swappableMetricHandle)(nil)

// BindCount retains a lazily registered counter for a fixed tag combination.
// Submit reuses the global swappable handle, including startup replay and client
// replacement. MockClient's registry reset invalidates the cached registration.
// The constructor owns a copy of tags; callers may reuse or change their slice.
func BindCount(namespace Namespace, name string, tags []string) MetricHandle {
	return &boundCountHandle{namespace: namespace, name: name, tags: slices.Clone(tags)}
}

type boundCountState struct {
	generation uint64
	handle     MetricHandle
}

type boundCountHandle struct {
	namespace Namespace
	name      string
	tags      []string
	mu        sync.Mutex // only initial registration and test resets
	state     atomic.Pointer[boundCountState]
}

func (h *boundCountHandle) handle() MetricHandle {
	if Disabled() {
		return noopMetricHandleInstance
	}
	generation := metricRegistryGeneration.Load()
	if state := h.state.Load(); state != nil && state.generation == generation {
		return state.handle
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	generation = metricRegistryGeneration.Load()
	if state := h.state.Load(); state != nil && state.generation == generation {
		return state.handle
	}
	state := &boundCountState{generation: generation, handle: Count(h.namespace, h.name, slices.Clone(h.tags))}
	h.state.Store(state)
	return state.handle
}

func (h *boundCountHandle) Submit(value float64) { h.handle().Submit(value) }
func (h *boundCountHandle) Get() float64         { return h.handle().Get() }
