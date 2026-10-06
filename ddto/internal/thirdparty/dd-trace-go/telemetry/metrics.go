// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package telemetry

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/telemetry/internal/knownmetrics"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/telemetry/internal/transport"
)

// metricKey is used as a key in the metrics store hash map.
type metricKey struct {
	namespace Namespace
	kind      transport.MetricType
	name      string
	tags      string
}

func (k metricKey) SplitTags() []string {
	if k.tags == "" {
		return nil
	}
	return strings.Split(k.tags, ",")
}

func (k metricKey) String() string {
	return fmt.Sprintf("%s.%s.%s.%s", k.namespace, k.kind, k.name, k.tags)
}

func validateMetricKey(namespace Namespace, kind transport.MetricType, name string, tags []string) error {
	if len(name) == 0 {
		return fmt.Errorf("metric name with tags %v should be empty", tags)
	}

	if !knownmetrics.IsKnownMetric(namespace, kind, name) {
		return fmt.Errorf("metric name %q of kind %q in namespace %q is not a known metric, please update the list of metric names running ./scripts/gen_known_metrics.sh or check that you wrote the name correctly. "+
			"The metric will still be sent", name, string(kind), namespace)
	}

	for _, tag := range tags {
		if len(tag) == 0 {
			return fmt.Errorf("metric %q should not have empty tags", name)
		}

		if strings.Contains(tag, ",") {
			return fmt.Errorf("metric %q tag %q should not contain commas", name, tag)
		}
	}

	return nil
}

// newMetricKey returns a new metricKey with the given parameters with the tags sorted and joined by commas.
func newMetricKey(namespace Namespace, kind transport.MetricType, name string, tags []string) metricKey {
	sort.Strings(tags)
	return metricKey{namespace: namespace, kind: kind, name: name, tags: strings.Join(tags, ",")}
}

// metricsHandle is the internal equivalent of MetricHandle for Count/Rate/Gauge metrics that are sent via the payload [transport.GenerateMetrics].
type metricHandle interface {
	MetricHandle
	Payload() transport.MetricData
}

type metrics struct {
	store         sync.Map // metricKey -> metricHandle
	initMu        sync.Mutex
	skipAllowlist bool // Debugging feature to skip the allowlist of known metrics
}

// LoadOrStore returns a MetricHandle for the given metric key. If the metric key does not exist, it will be created.
func (m *metrics) LoadOrStore(namespace Namespace, kind transport.MetricType, name string, tags []string) MetricHandle {

	key := newMetricKey(namespace, kind, name, tags)
	if handle, ok := m.store.Load(key); ok {
		return handle.(metricHandle)
	}

	// Serialize only registration. Existing handles and their updates
	// remain independent of this lock.
	m.initMu.Lock()
	handle, loaded := m.store.Load(key)
	if !loaded {
		switch kind {
		case transport.CountMetric:
			handle = &count{metric: metric{key: key}}
		case transport.GaugeMetric:
			handle = &gauge{metric: metric{key: key}}
		case transport.RateMetric:
			rate := &rate{count: count{metric: metric{key: key}}}
			now := time.Now()
			rate.intervalStart.Store(&now)
			handle = rate
		default:
			m.initMu.Unlock()
			log.Warn("telemetry: unknown metric type %q", kind)
			return nil
		}
		m.store.Store(key, handle)
	}
	m.initMu.Unlock()

	if !loaded && !m.skipAllowlist { // The metric is new: validate and log issues about it
		if err := validateMetricKey(namespace, kind, name, tags); err != nil {
			log.Warn("telemetry: %s", err.Error())
		}
	}

	return handle.(metricHandle)
}

func (m *metrics) Payload() transport.Payload {
	var series []transport.MetricData
	m.store.Range(func(_, value any) bool {
		if payload := value.(metricHandle).Payload(); payload.Type != "" {
			series = append(series, payload)
		}
		return true
	})

	if len(series) == 0 {
		return nil
	}

	return transport.GenerateMetrics{Series: series, SkipAllowlist: m.skipAllowlist}
}

type metricPoint struct {
	value float64
	time  time.Time
}

// metric owns one coherent value/timestamp pair. Submit and collection share a
// short lock so a flush cannot split the pair or lose a concurrent increment.
// The point stays inline; updating a counter does not allocate a new snapshot.
type metric struct {
	key     metricKey
	mu      sync.Mutex
	point   metricPoint
	present bool
}

func (m *metric) Get() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.present {
		return m.point.value
	}

	return math.NaN()
}

func (m *metric) Payload() transport.MetricData {
	point, present := m.takePoint()
	if !present {
		return transport.MetricData{}
	}
	return m.payload(point)
}

func (m *metric) takePoint() (metricPoint, bool) {
	m.mu.Lock()
	point, present := m.point, m.present
	m.point = metricPoint{}
	m.present = false
	m.mu.Unlock()
	return point, present
}

func (m *metric) payload(point metricPoint) transport.MetricData {
	return transport.MetricData{
		Metric:    m.key.name,
		Namespace: m.key.namespace,
		Tags:      m.key.SplitTags(),
		Type:      m.key.kind,
		Common:    knownmetrics.IsCommonMetric(m.key.namespace, m.key.kind, m.key.name),
		Points: [][2]any{
			{point.time.Unix(), point.value},
		},
	}
}

// count is a metric that represents a single value that is incremented over time and flush and reset at zero when flushed
type count struct {
	metric
}

func (m *count) Submit(newValue float64) {
	now := time.Now()
	m.mu.Lock()
	m.point.value += newValue
	m.point.time = now
	m.present = true
	m.mu.Unlock()
}

// gauge is a metric that represents a single value at a point in time that is not incremental
type gauge struct {
	metric
}

func (g *gauge) Submit(value float64) {
	now := time.Now()
	g.mu.Lock()
	g.point = metricPoint{value: value, time: now}
	g.present = true
	g.mu.Unlock()
}

// rate is like a count metric but the value sent is divided by an interval of time that is also sent/
type rate struct {
	count
	intervalStart atomic.Pointer[time.Time]
}

func (r *rate) Get() float64 {
	sum := r.count.Get()
	intervalStart := r.intervalStart.Load()
	if intervalStart == nil {
		return math.NaN()
	}

	intervalSeconds := time.Since(*intervalStart).Seconds()
	if int64(intervalSeconds) == 0 { // Interval for rate is too small, we prefer not sending data over sending something wrong
		return math.NaN()
	}

	return sum / intervalSeconds
}

func (r *rate) Payload() transport.MetricData {
	now := time.Now()
	intervalStart := r.intervalStart.Swap(&now)
	if intervalStart == nil {
		return transport.MetricData{}
	}

	intervalSeconds := time.Since(*intervalStart).Seconds()
	if int64(intervalSeconds) == 0 { // Interval for rate is too small, we prefer not sending data over sending something wrong
		return transport.MetricData{}
	}

	point, present := r.takePoint()
	if !present {
		return transport.MetricData{}
	}

	point.value /= intervalSeconds
	payload := r.metric.payload(point)
	payload.Interval = int64(intervalSeconds)
	return payload
}
