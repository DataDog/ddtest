// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package telemetry

import (
	"errors"
	"os"
	"strconv"
	"sync"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/compat"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/telemetry/internal"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/telemetry/internal/mapper"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/telemetry/internal/transport"
)

// NewClient creates a new telemetry client with the given service, environment, and version and config.
func NewClient(service, env, version string, config ClientConfig) (Client, error) {
	if service == "" {
		return nil, errors.New("service name must not be empty")
	}

	config = defaultConfig(config)
	if err := config.validateConfig(); err != nil {
		return nil, err
	}

	return newClient(internal.TracerConfig{Service: service, Env: env, Version: version}, config)
}

func newClient(tracerConfig internal.TracerConfig, config ClientConfig) (*client, error) {
	writerConfig, err := newWriterConfig(config, tracerConfig)
	if err != nil {
		return nil, err
	}

	writer, err := internal.NewWriter(writerConfig)
	if err != nil {
		return nil, err
	}

	client := &client{
		tracerConfig: tracerConfig,
		writer:       writer,
		clientConfig: config,
		payloadQueue: internal.NewRingQueue[transport.Payload](config.PayloadQueueSize),

		metrics: metrics{
			skipAllowlist: config.Debug,
		},
		distributions: distributions{
			pool:          internal.NewSyncPool(func() []float64 { return make([]float64, config.DistributionsSize.Min) }),
			skipAllowlist: config.Debug,
			queueSize:     config.DistributionsSize,
		},

		backend: newLoggerBackend(config.MaxDistinctLogs),
	}

	client.flushMapper = mapper.NewDefaultMapper()

	client.dataSources = append(client.dataSources,
		&client.products,
		&client.configuration,
	)

	if config.LogsEnabled {
		client.dataSources = append(client.dataSources, client.backend)
	}

	if config.MetricsEnabled {
		client.dataSources = append(client.dataSources, &client.metrics, &client.distributions)
	}

	client.flushTicker = internal.NewTicker(client.Flush, config.FlushInterval)

	return client, nil
}

// dataSources is where the data that will be flushed is coming from. I.e metrics, logs, configurations, etc.
type dataSource interface {
	Payload() transport.Payload
}

type client struct {
	tracerConfig internal.TracerConfig
	clientConfig ClientConfig

	// Data sources
	dataSources   []dataSource
	products      products
	configuration configuration
	backend       *loggerBackend
	metrics       metrics
	distributions distributions

	// flushMapper is the transformer to use for the next flush on the gathered bodies on this tick
	flushMapper   mapper.Mapper
	flushMapperMu sync.Mutex

	// flushTicker is the ticker that triggers a call to client.Flush every flush interval
	flushTicker *internal.Ticker
	// flushMu is used to ensure that only one flush is happening at a time
	flushMu sync.Mutex

	// writer is the writer to use to send the payloads to the backend or the agent
	writer internal.Writer

	// payloadQueue is used when we cannot flush previously built payload for multiple reasons.
	payloadQueue *internal.RingQueue[transport.Payload]
}

func (c *client) Log(record Record, options ...LogOption) {
	if !c.clientConfig.LogsEnabled {
		return
	}

	c.backend.Add(record, options...)
}

func (c *client) Count(namespace Namespace, name string, tags []string) MetricHandle {
	if !c.clientConfig.MetricsEnabled {
		return noopMetricHandle{}
	}
	return c.metrics.LoadOrStore(namespace, transport.CountMetric, name, tags)
}

func (c *client) Distribution(namespace Namespace, name string, tags []string) MetricHandle {
	if !c.clientConfig.MetricsEnabled {
		return noopMetricHandle{}
	}
	return c.distributions.LoadOrStore(namespace, name, tags)
}

func (c *client) ProductStarted(product Namespace) {
	c.products.Add(product, true, nil)
}

func (c *client) RegisterAppConfig(key string, value any, origin Origin) {
	c.configuration.Add(Configuration{Name: key, Value: value, Origin: origin})
}

func (c *client) RegisterAppConfigs(kvs ...Configuration) {
	for _, value := range kvs {
		c.configuration.Add(value)
	}
}

func (c *client) Config() ClientConfig {
	return c.clientConfig
}

// Flush sends all the data sources before calling flush
// This function is called by the flushTicker so it should not panic, or it will crash the whole customer application.
// If a panic occurs, we stop the telemetry and log the error.
func (c *client) Flush() { c.flushData(0) }

// flushStartup sends the standalone app-started request before tests. Remaining
// snapshots stay in the existing queue until a regular flush or shutdown.
func (c *client) flushStartup() { c.flushData(1) }

func (c *client) flushData(limit int) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		if err, ok := r.(error); ok {
			log.Warn("panic while flushing telemetry data, stopping telemetry: %s", err.Error())
		} else {
			log.Warn("panic while flushing telemetry data, stopping telemetry!")
		}
		telemetryClientEnabled = false
		if gc, ok := GlobalClient().(*client); ok && gc == c {
			SwapClient(nil)
		}
	}()

	payloads := make([]transport.Payload, 0, 8)
	for _, ds := range c.dataSources {
		if payload := ds.Payload(); payload != nil {
			payloads = append(payloads, payload)
		}
	}

	nbBytes, err := c.flushPayloads(payloads, limit)
	if err != nil {
		log.Debug("telemetry: error while flushing CI telemetry data: %s", err.Error())

		return
	}

	if c.clientConfig.Debug {
		log.Debug("telemetry: flushed %d bytes of data", nbBytes)
	}
}

func (c *client) transform(payloads []transport.Payload) []transport.Payload {
	c.flushMapperMu.Lock()
	defer c.flushMapperMu.Unlock()
	payloads, c.flushMapper = c.flushMapper.Transform(payloads)
	return payloads
}

// flush sends all the data sources to the writer after having sent them through the [transform] function.
// It returns the amount of bytes sent to the writer.
func (c *client) flush(payloads []transport.Payload) (int, error) {
	return c.flushPayloads(payloads, 0)
}

func (c *client) flushPayloads(payloads []transport.Payload, limit int) (int, error) {
	c.flushMu.Lock()
	defer c.flushMu.Unlock()
	payloads = c.transform(payloads)

	if c.payloadQueue.IsEmpty() && len(payloads) == 0 {
		return 0, nil
	}

	emptyQueue := c.payloadQueue.IsEmpty()
	// We enqueue the new payloads to preserve the order of the payloads
	c.payloadQueue.Enqueue(payloads...)
	payloads = c.payloadQueue.Flush()

	var (
		nbBytes        int
		speedIncreased bool
		failedCalls    []internal.EndpointRequestResult
	)

	for i, payload := range payloads {
		if limit != 0 && i >= limit {
			c.payloadQueue.Enqueue(payloads[i:]...)
			break
		}
		results, err := c.writer.Flush(payload)
		c.computeFlushMetrics(results, err)
		if err != nil {
			// We stop flushing when we encounter a fatal error, put the bodies in the queue and return the error
			if results[len(results)-1].StatusCode == 413 { // If the payload is too large we have no way to divide it, we can only skip it...
				log.Warn("telemetry: tried sending a payload that was too large, dropping it")
				continue
			}
			c.payloadQueue.Enqueue(payloads[i:]...)
			return nbBytes, err
		}

		failedCalls = append(failedCalls, results[:len(results)-1]...)
		successfulCall := results[len(results)-1]

		if successfulCall.RequestAttempted && !speedIncreased && successfulCall.PayloadByteSize > c.clientConfig.EarlyFlushPayloadSize {
			// We increase the speed of the flushTicker to try to flush the remaining bodies faster as we are at risk of sending too large bodies to the backend
			c.flushTicker.CanIncreaseSpeed()
			speedIncreased = true
		}

		nbBytes += successfulCall.PayloadByteSize
	}

	if emptyQueue && !speedIncreased { // If we did not send a very big payload, and we have no payloads
		c.flushTicker.CanDecreaseSpeed()
	}

	if len(failedCalls) > 0 {
		var errs []error
		for _, call := range failedCalls {
			errs = append(errs, call.Error)
		}
		log.Debug("telemetry: non-fatal error(s) while flushing telemetry data: %v", errors.Join(errs...).Error())
	}

	return nbBytes, nil
}

// computeFlushMetrics computes and submits the metrics for the flush operation using the output from the writer.Flush method.
// It will submit the number of requests, responses, errors, the number of bytes sent and the duration of the call that was successful.
func (c *client) computeFlushMetrics(results []internal.EndpointRequestResult, reason error) {
	if !c.clientConfig.internalMetricsEnabled {
		return
	}

	indexToEndpoint := func(i int) string {
		if i == 0 && c.clientConfig.AgentURL != "" {
			return "agent"
		}
		return "agentless"
	}

	for i, result := range results {
		if !result.RequestAttempted {
			continue
		}
		endpoint := "endpoint:" + indexToEndpoint(i)
		c.Count(transport.NamespaceTelemetry, "telemetry_api.requests", []string{endpoint}).Submit(1)
		if result.StatusCode != 0 {
			c.Count(transport.NamespaceTelemetry, "telemetry_api.responses", []string{endpoint, "status_code:" + strconv.Itoa(result.StatusCode)}).Submit(1)
		}

		if result.Error != nil {
			typ := "type:network"
			if os.IsTimeout(result.Error) {
				typ = "type:timeout"
			}
			if _, ok := compat.AsType[*internal.WriterStatusCodeError](result.Error); ok {
				typ = "type:status_code"
			}
			c.Count(transport.NamespaceTelemetry, "telemetry_api.errors", []string{endpoint, typ}).Submit(1)
		}
	}

	if len(results) == 0 {
		return
	}

	if !results[len(results)-1].RequestAttempted {
		return
	}

	if reason != nil {
		return
	}

	successfulCall := results[len(results)-1]
	endpoint := "endpoint:" + indexToEndpoint(len(results)-1)
	c.Distribution(transport.NamespaceTelemetry, "telemetry_api.bytes", []string{endpoint}).Submit(float64(successfulCall.PayloadByteSize))
	c.Distribution(transport.NamespaceTelemetry, "telemetry_api.ms", []string{endpoint}).Submit(float64(successfulCall.CallDuration.Milliseconds()))
}

func (c *client) AppStart() {
	c.flushMapperMu.Lock()
	defer c.flushMapperMu.Unlock()
	c.flushMapper = mapper.NewAppStartedMapper(c.flushMapper)
}

func (c *client) AppStop() {
	c.flushMapperMu.Lock()
	defer c.flushMapperMu.Unlock()
	c.flushMapper = mapper.NewAppClosingMapper(c.flushMapper)
}

func (c *client) Close() error {
	c.flushTicker.Stop()
	return nil
}
