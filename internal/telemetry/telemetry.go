// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package telemetry sends ddtest's internal CI Visibility metrics through the
// Datadog instrumentation telemetry API.
//
// It deliberately supports metrics only. It does not send application
// lifecycle, configuration, dependency, integration, log, or heartbeat events.
package telemetry

import (
	"context"
	"errors"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	namespaceCIVisibility               = "civisibility"
	telemetryEnabledEnvironmentVariable = "DD_INSTRUMENTATION_TELEMETRY_ENABLED"
)

// Metric receives values for one metric name and tag combination. Metric
// handles are safe for concurrent use.
type Metric interface {
	Submit(value float64)
}

// Client collects and sends ddtest's internal telemetry metrics. Implementations
// must be safe for concurrent use.
type Client interface {
	// Count returns a handle whose submitted values are summed between flushes.
	Count(name string, tags []string) Metric

	// Distribution returns a handle that retains every submitted value between
	// flushes.
	Distribution(name string, tags []string) Metric

	// Flush synchronously sends all metrics submitted since the last successful
	// flush. Failed payloads remain buffered for the next call.
	Flush(ctx context.Context) error
}

// Config describes the application metadata attached to every telemetry
// request. NewClient resolves the destination from ddtest's standard Datadog
// Agent/agentless configuration.
type Config struct {
	ServiceName    string
	Environment    string
	LibraryVersion string
}

type metricKind uint8

const (
	countMetric metricKind = iota
	distributionMetric
)

type metricKey struct {
	name string
	tags string
}

func newMetricKey(name string, tags []string) metricKey {
	tags = slices.Clone(tags)
	slices.Sort(tags)
	return metricKey{name: name, tags: strings.Join(tags, ",")}
}

func (k metricKey) splitTags() []string {
	if k.tags == "" {
		return nil
	}
	return strings.Split(k.tags, ",")
}

type metric struct {
	client *client
	kind   metricKind
	key    metricKey
}

func (m *metric) Submit(value float64) {
	m.client.submit(m.kind, m.key, value)
}

type countPoint struct {
	value float64
	time  time.Time
}

type client struct {
	mu            sync.Mutex
	flushMu       sync.Mutex
	counts        map[metricKey]countPoint
	distributions map[metricKey][]float64
	sender        *sender
	now           func() time.Time
}

var _ Client = (*client)(nil)

type noopMetric struct{}

func (noopMetric) Submit(float64) {}

type noopClient struct{}

func (noopClient) Count(string, []string) Metric        { return noopMetric{} }
func (noopClient) Distribution(string, []string) Metric { return noopMetric{} }
func (noopClient) Flush(context.Context) error          { return nil }

// NoopClient returns a telemetry client that safely discards all metrics.
func NoopClient() Client {
	return noopClient{}
}

// NewClient creates a metrics-only telemetry client.
func NewClient(config Config) (Client, error) {
	if !telemetryEnabled() {
		return NoopClient(), nil
	}
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	destination, err := resolveDestination()
	if err != nil {
		return nil, err
	}
	return newClient(config, destination)
}

func telemetryEnabled() bool {
	value, found := os.LookupEnv(telemetryEnabledEnvironmentVariable)
	if !found {
		return true
	}
	enabled, err := strconv.ParseBool(strings.TrimSpace(value))
	return err != nil || enabled
}

func validateConfig(config Config) error {
	if config.ServiceName == "" {
		return errors.New("telemetry: service name must not be empty")
	}
	if config.LibraryVersion == "" {
		return errors.New("telemetry: library version must not be empty")
	}
	return nil
}

func newClient(config Config, destination destination) (Client, error) {
	telemetrySender, err := newSender(config, destination)
	if err != nil {
		return nil, err
	}

	return &client{
		counts:        make(map[metricKey]countPoint),
		distributions: make(map[metricKey][]float64),
		sender:        telemetrySender,
		now:           time.Now,
	}, nil
}

func (c *client) Count(name string, tags []string) Metric {
	return &metric{client: c, kind: countMetric, key: newMetricKey(name, tags)}
}

func (c *client) Distribution(name string, tags []string) Metric {
	return &metric{client: c, kind: distributionMetric, key: newMetricKey(name, tags)}
}

func (c *client) submit(kind metricKind, key metricKey, value float64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch kind {
	case countMetric:
		point := c.counts[key]
		point.value += value
		point.time = c.now()
		c.counts[key] = point
	case distributionMetric:
		c.distributions[key] = append(c.distributions[key], value)
	}
}

func (c *client) Flush(ctx context.Context) error {
	c.flushMu.Lock()
	defer c.flushMu.Unlock()

	counts, distributions := c.drain()
	payloads := make([]payload, 0, 2)
	if len(counts) > 0 {
		payloads = append(payloads, generateMetricsPayload(counts))
	}
	if len(distributions) > 0 {
		payloads = append(payloads, distributionsPayload(distributions))
	}

	payload := reducePayloads(payloads)
	if payload == nil {
		return nil
	}
	if err := c.sender.send(ctx, payload); err != nil {
		c.restoreCounts(counts)
		c.restoreDistributions(distributions)
		return err
	}
	return nil
}

func (c *client) drain() (map[metricKey]countPoint, map[metricKey][]float64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	counts := c.counts
	distributions := c.distributions
	c.counts = make(map[metricKey]countPoint)
	c.distributions = make(map[metricKey][]float64)
	return counts, distributions
}

func (c *client) restoreCounts(failed map[metricKey]countPoint) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for key, old := range failed {
		current, ok := c.counts[key]
		if !ok {
			c.counts[key] = old
			continue
		}
		current.value += old.value
		if old.time.After(current.time) {
			current.time = old.time
		}
		c.counts[key] = current
	}
}

func (c *client) restoreDistributions(failed map[metricKey][]float64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for key, old := range failed {
		values := make([]float64, 0, len(old)+len(c.distributions[key]))
		values = append(values, old...)
		values = append(values, c.distributions[key]...)
		c.distributions[key] = values
	}
}
