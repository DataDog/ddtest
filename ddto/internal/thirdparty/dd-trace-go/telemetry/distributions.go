// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package telemetry

import (
	"fmt"
	"sync"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/telemetry/internal"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/telemetry/internal/knownmetrics"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/telemetry/internal/transport"
)

type distributions struct {
	store         sync.Map // metricKey -> *distribution
	initMu        sync.Mutex
	pool          *internal.SyncPool[[]float64]
	queueSize     internal.Range[int]
	skipAllowlist bool // Debugging feature to skip the allowlist of known metrics
}

// LoadOrStore returns a MetricHandle for the given distribution metric. If the metric key does not exist, it will be created.
func (d *distributions) LoadOrStore(namespace Namespace, name string, tags []string) MetricHandle {
	kind := transport.DistMetric
	key := newMetricKey(namespace, kind, name, tags)
	if handle, ok := d.store.Load(key); ok {
		return handle.(*distribution)
	}
	d.initMu.Lock()
	handle, loaded := d.store.Load(key)
	if !loaded {
		handle = &distribution{
			key:    key,
			values: internal.NewRingQueueWithPool[float64](d.queueSize, d.pool),
		}
		d.store.Store(key, handle)
	}
	d.initMu.Unlock()

	if !loaded && !d.skipAllowlist { // The metric is new: validate and log issues about it
		if err := validateMetricKey(namespace, kind, name, tags); err != nil {
			log.Warn("telemetry: %s", err.Error())
		}
	}

	return handle.(*distribution)
}

func (d *distributions) Payload() transport.Payload {
	var series []transport.DistributionSeries
	d.store.Range(func(_, value any) bool {
		if payload := value.(*distribution).payload(); payload.Namespace != "" {
			series = append(series, payload)
		}
		return true
	})

	if len(series) == 0 {
		return nil
	}

	return transport.Distributions{Series: series, SkipAllowlist: d.skipAllowlist}
}

type distribution struct {
	key    metricKey
	values *internal.RingQueue[float64]

	logLoss sync.Once
}

func (d *distribution) Submit(value float64) {
	if !d.values.Enqueue(value) {
		d.logLoss.Do(func() {
			log.Debug("telemetry: distribution %q is losing values because the buffer is full", d.key.name)
			Log(NewRecord(LogWarn, fmt.Sprintf("telemetry: distribution %s is losing values because the buffer is full", d.key)), WithStacktrace())
		})
	}
}

func (d *distribution) Get() float64 {
	return d.values.ReversePeek()
}

func (d *distribution) payload() transport.DistributionSeries {
	if d.values.IsEmpty() {
		return transport.DistributionSeries{}
	}

	return transport.DistributionSeries{
		Metric:    d.key.name,
		Namespace: d.key.namespace,
		Tags:      d.key.SplitTags(),
		Common:    knownmetrics.IsCommonMetric(d.key.namespace, d.key.kind, d.key.name),
		Points:    d.values.Flush(),
	}
}
