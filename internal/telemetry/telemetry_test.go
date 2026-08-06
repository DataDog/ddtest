// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/constants"
)

type receivedRequest struct {
	header http.Header
	body   []byte
}

type wireBody struct {
	APIVersion  string          `json:"api_version"`
	RequestType string          `json:"request_type"`
	TracerTime  int64           `json:"tracer_time"`
	RuntimeID   string          `json:"runtime_id"`
	SequenceID  int64           `json:"seq_id"`
	Payload     json.RawMessage `json:"payload"`
	Application application     `json:"application"`
	Host        host            `json:"host"`
}

type wireMessage struct {
	RequestType string          `json:"request_type"`
	Payload     json.RawMessage `json:"payload"`
}

func clientForTest(t *testing.T, endpoint string, httpClient *http.Client) *client {
	t.Helper()

	parsedEndpoint, err := parseEndpoint(endpoint)
	if err != nil {
		t.Fatalf("parseEndpoint() error = %v", err)
	}
	telemetryClient, err := newClient(Config{
		ServiceName:    "test-service",
		Environment:    "ci",
		LibraryVersion: "1.2.3",
	}, destination{
		endpoint:   parsedEndpoint,
		apiKey:     "api-key",
		httpClient: httpClient,
	})
	if err != nil {
		t.Fatalf("newClient() error = %v", err)
	}
	client := telemetryClient.(*client)
	client.sender.body.RuntimeID = "runtime-id"
	return client
}

func clearDestinationEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		constants.TestOptimizationAgentlessEnabledEnvironmentVariable,
		constants.TestOptimizationAgentlessURLEnvironmentVariable,
		constants.APIKeyEnvironmentVariable,
		"DD_SITE",
		"DD_TRACE_AGENT_URL",
		"DD_AGENT_HOST",
		"DD_TRACE_AGENT_PORT",
	} {
		t.Setenv(name, "")
	}
}

func decodeWireBody(t *testing.T, encoded []byte) wireBody {
	t.Helper()

	var body wireBody
	if err := json.Unmarshal(encoded, &body); err != nil {
		t.Fatalf("decode telemetry body: %v", err)
	}
	return body
}

func TestNewClientValidation(t *testing.T) {
	clearDestinationEnvironment(t)
	tests := []struct {
		name    string
		config  Config
		wantErr string
	}{
		{
			name: "valid",
			config: Config{
				ServiceName:    "ddtest",
				LibraryVersion: "1.0.0",
			},
		},
		{
			name: "missing service name",
			config: Config{
				LibraryVersion: "1.0.0",
			},
			wantErr: "service name must not be empty",
		},
		{
			name: "missing library version",
			config: Config{
				ServiceName: "ddtest",
			},
			wantErr: "library version must not be empty",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, err := NewClient(test.config)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("NewClient() error = %v", err)
				}
				if client == nil {
					t.Fatal("NewClient() returned a nil client")
				}
				return
			}
			if err == nil || !regexp.MustCompile(regexp.QuoteMeta(test.wantErr)).MatchString(err.Error()) {
				t.Fatalf("NewClient() error = %v, want error containing %q", err, test.wantErr)
			}
		})
	}
}

func TestResolveDestinationAgentless(t *testing.T) {
	t.Run("standard intake", func(t *testing.T) {
		clearDestinationEnvironment(t)
		t.Setenv(constants.TestOptimizationAgentlessEnabledEnvironmentVariable, "true")
		t.Setenv(constants.APIKeyEnvironmentVariable, "api-key")
		t.Setenv("DD_SITE", "datadoghq.eu")

		destination, err := resolveDestination()
		if err != nil {
			t.Fatalf("resolveDestination() error = %v", err)
		}
		want := "https://instrumentation-telemetry-intake.datadoghq.eu/api/v2/apmtelemetry"
		if destination.endpoint.String() != want || destination.apiKey != "api-key" {
			t.Fatalf("destination = %#v, want endpoint %q and API key", destination, want)
		}
		if destination.httpClient == nil || destination.httpClient.Timeout != telemetryHTTPTimeout {
			t.Fatalf("unexpected HTTP client: %#v", destination.httpClient)
		}
	})

	t.Run("CI Visibility test intake", func(t *testing.T) {
		clearDestinationEnvironment(t)
		t.Setenv(constants.TestOptimizationAgentlessEnabledEnvironmentVariable, "true")
		t.Setenv(constants.APIKeyEnvironmentVariable, "api-key")
		t.Setenv(constants.TestOptimizationAgentlessURLEnvironmentVariable, "https://tests.example/intake")

		destination, err := resolveDestination()
		if err != nil {
			t.Fatalf("resolveDestination() error = %v", err)
		}
		want := "https://tests.example/intake/api/v2/apmtelemetry"
		if destination.endpoint.String() != want {
			t.Fatalf("endpoint = %q, want %q", destination.endpoint, want)
		}
	})

	t.Run("missing API key", func(t *testing.T) {
		clearDestinationEnvironment(t)
		t.Setenv(constants.TestOptimizationAgentlessEnabledEnvironmentVariable, "true")

		_, err := resolveDestination()
		if err == nil || !strings.Contains(err.Error(), "API key must not be empty") {
			t.Fatalf("resolveDestination() error = %v, want missing API key error", err)
		}
	})

	t.Run("invalid CI Visibility test intake", func(t *testing.T) {
		clearDestinationEnvironment(t)
		t.Setenv(constants.TestOptimizationAgentlessEnabledEnvironmentVariable, "true")
		t.Setenv(constants.APIKeyEnvironmentVariable, "api-key")
		t.Setenv(constants.TestOptimizationAgentlessURLEnvironmentVariable, "://bad")

		_, err := resolveDestination()
		if err == nil {
			t.Fatal("resolveDestination() error = nil, want invalid URL error")
		}
	})
}

func TestResolveDestinationAgent(t *testing.T) {
	t.Run("explicit HTTP URL", func(t *testing.T) {
		clearDestinationEnvironment(t)
		t.Setenv("DD_TRACE_AGENT_URL", "https://agent.example:9126")

		destination, err := resolveDestination()
		if err != nil {
			t.Fatalf("resolveDestination() error = %v", err)
		}
		want := "https://agent.example:9126/telemetry/proxy/api/v2/apmtelemetry"
		if destination.endpoint.String() != want || destination.apiKey != "" {
			t.Fatalf("destination = %#v, want endpoint %q without API key", destination, want)
		}
		if destination.httpClient == nil || destination.httpClient.Timeout != telemetryHTTPTimeout {
			t.Fatalf("unexpected HTTP client: %#v", destination.httpClient)
		}
	})

	t.Run("host and port", func(t *testing.T) {
		clearDestinationEnvironment(t)
		t.Setenv("DD_AGENT_HOST", "agent.internal")
		t.Setenv("DD_TRACE_AGENT_PORT", "8127")

		destination, err := resolveDestination()
		if err != nil {
			t.Fatalf("resolveDestination() error = %v", err)
		}
		want := "http://agent.internal:8127/telemetry/proxy/api/v2/apmtelemetry"
		if destination.endpoint.String() != want {
			t.Fatalf("endpoint = %q, want %q", destination.endpoint, want)
		}
	})

	t.Run("Unix socket", func(t *testing.T) {
		clearDestinationEnvironment(t)
		t.Setenv("DD_TRACE_AGENT_URL", "unix:///tmp/ddtest-apm.socket")

		destination, err := resolveDestination()
		if err != nil {
			t.Fatalf("resolveDestination() error = %v", err)
		}
		want := "http://UDS__tmp_ddtest-apm.socket/telemetry/proxy/api/v2/apmtelemetry"
		if destination.endpoint.String() != want {
			t.Fatalf("endpoint = %q, want %q", destination.endpoint, want)
		}
		if _, ok := destination.httpClient.Transport.(*http.Transport); !ok {
			t.Fatalf("HTTP transport = %T, want *http.Transport", destination.httpClient.Transport)
		}
	})
}

func TestParseEndpointValidation(t *testing.T) {
	for _, test := range []struct {
		name    string
		value   string
		wantErr string
	}{
		{name: "valid", value: "https://example.com/api/v2/apmtelemetry"},
		{name: "invalid", value: "://bad", wantErr: "parse endpoint"},
		{name: "unsupported scheme", value: "ftp://example.com/telemetry", wantErr: "must use HTTP(S)"},
		{name: "missing host", value: "http:///telemetry", wantErr: "host must not be empty"},
	} {
		t.Run(test.name, func(t *testing.T) {
			endpoint, err := parseEndpoint(test.value)
			if test.wantErr == "" {
				if err != nil || endpoint.String() != test.value {
					t.Fatalf("parseEndpoint() = %v, %v, want %q, nil", endpoint, err, test.value)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("parseEndpoint() error = %v, want error containing %q", err, test.wantErr)
			}
		})
	}
}

func TestNoopClient(t *testing.T) {
	client := NoopClient()
	client.Count("count", nil).Submit(1)
	client.Distribution("distribution", nil).Submit(2)
	if err := client.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
}

func TestFlushSendsOnlyMetricPayloads(t *testing.T) {
	var received []receivedRequest
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		encoded, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		received = append(received, receivedRequest{header: request.Header.Clone(), body: encoded})
		return httpResponse(http.StatusAccepted, ""), nil
	})}

	client := clientForTest(t, "https://example.com/telemetry", httpClient)
	client.now = func() time.Time { return time.Unix(1_700_000_000, 0) }
	client.sender.requestTime = func() time.Time { return time.Unix(1_700_000_010, 0) }

	countTags := []string{"rq_compressed:true", "endpoint:test_cycle"}
	client.Count("endpoint_payload.requests", countTags).Submit(1)
	client.Count("endpoint_payload.requests", []string{"endpoint:test_cycle", "rq_compressed:true"}).Submit(2)
	if !slices.Equal(countTags, []string{"rq_compressed:true", "endpoint:test_cycle"}) {
		t.Fatalf("Count() mutated caller tags: %v", countTags)
	}

	distributionTags := []string{"endpoint:test_cycle"}
	distribution := client.Distribution("endpoint_payload.bytes", distributionTags)
	distribution.Submit(10)
	distribution.Submit(20)

	if err := client.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	if len(received) != 1 {
		t.Fatalf("request count = %d, want 1", len(received))
	}
	request := received[0]
	if got := request.header.Get("DD-Telemetry-Request-Type"); got != requestTypeMessageBatch {
		t.Fatalf("request type = %q, want %q", got, requestTypeMessageBatch)
	}
	if got := request.header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if got := request.header.Get("DD-Telemetry-API-Version"); got != apiVersion {
		t.Errorf("DD-Telemetry-API-Version = %q, want %q", got, apiVersion)
	}
	if got := request.header.Get("DD-Client-Library-Language"); got != languageName {
		t.Errorf("DD-Client-Library-Language = %q, want %q", got, languageName)
	}
	if got := request.header.Get("DD-Client-Library-Version"); got != "1.2.3" {
		t.Errorf("DD-Client-Library-Version = %q, want 1.2.3", got)
	}
	if got := request.header.Get("DD-API-KEY"); got != "api-key" {
		t.Errorf("DD-API-KEY = %q, want api-key", got)
	}

	batchBody := decodeWireBody(t, request.body)
	if batchBody.RequestType != requestTypeMessageBatch || batchBody.SequenceID != 1 {
		t.Fatalf("batch request type/sequence = %q/%d, want %q/1", batchBody.RequestType, batchBody.SequenceID, requestTypeMessageBatch)
	}
	if batchBody.APIVersion != apiVersion || batchBody.RuntimeID != "runtime-id" || batchBody.TracerTime != 1_700_000_010 {
		t.Errorf("unexpected batch envelope: %#v", batchBody)
	}
	if batchBody.Application.ServiceName != "test-service" || batchBody.Application.LibraryVersion != "1.2.3" {
		t.Errorf("unexpected application metadata: %#v", batchBody.Application)
	}
	if batchBody.Application.ServiceVersion != "" {
		t.Errorf("service version = %q, want empty", batchBody.Application.ServiceVersion)
	}
	if batchBody.Application.LanguageName != languageName || batchBody.Application.LanguageVersion != runtime.Version() {
		t.Errorf("unexpected language metadata: %#v", batchBody.Application)
	}
	if batchBody.Host.Hostname == "" || batchBody.Host.OS == "" || batchBody.Host.Architecture == "" {
		t.Errorf("incomplete host metadata: %#v", batchBody.Host)
	}

	var batch []wireMessage
	if err := json.Unmarshal(batchBody.Payload, &batch); err != nil {
		t.Fatalf("decode message-batch payload: %v", err)
	}
	if len(batch) != 2 {
		t.Fatalf("message-batch message count = %d, want 2", len(batch))
	}
	if batch[0].RequestType != requestTypeMetrics || batch[1].RequestType != requestTypeDistribution {
		t.Fatalf("nested request types = [%q %q], want [%q %q]", batch[0].RequestType, batch[1].RequestType, requestTypeMetrics, requestTypeDistribution)
	}

	var metricsPayload generateMetrics
	if err := json.Unmarshal(batch[0].Payload, &metricsPayload); err != nil {
		t.Fatalf("decode generate-metrics payload: %v", err)
	}
	if len(metricsPayload.Series) != 1 {
		t.Fatalf("generate-metrics series count = %d, want 1", len(metricsPayload.Series))
	}
	metric := metricsPayload.Series[0]
	if metric.Metric != "endpoint_payload.requests" || metric.Type != "count" || metric.Namespace != namespaceCIVisibility || !metric.Common {
		t.Errorf("unexpected count metric: %#v", metric)
	}
	if !slices.Equal(metric.Tags, []string{"endpoint:test_cycle", "rq_compressed:true"}) {
		t.Errorf("count tags = %v, want sorted tags", metric.Tags)
	}
	if len(metric.Points) != 1 || metric.Points[0][0].(float64) != 1_700_000_000 || metric.Points[0][1].(float64) != 3 {
		t.Errorf("count points = %#v, want [[1700000000, 3]]", metric.Points)
	}

	var distributionPayload distributions
	if err := json.Unmarshal(batch[1].Payload, &distributionPayload); err != nil {
		t.Fatalf("decode distributions payload: %v", err)
	}
	if len(distributionPayload.Series) != 1 {
		t.Fatalf("distributions series count = %d, want 1", len(distributionPayload.Series))
	}
	distributionMetric := distributionPayload.Series[0]
	if distributionMetric.Metric != "endpoint_payload.bytes" || distributionMetric.Namespace != namespaceCIVisibility || !distributionMetric.Common {
		t.Errorf("unexpected distribution metric: %#v", distributionMetric)
	}
	if !slices.Equal(distributionMetric.Points, []float64{10, 20}) {
		t.Errorf("distribution points = %v, want [10 20]", distributionMetric.Points)
	}

	if err := client.Flush(context.Background()); err != nil {
		t.Fatalf("empty Flush() error = %v", err)
	}
	if len(received) != 1 {
		t.Fatalf("empty Flush() sent a request; request count = %d, want 1", len(received))
	}
}

func TestFlushRestoresFailedMetrics(t *testing.T) {
	var calls atomic.Int32
	var received [][]byte
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		encoded, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		received = append(received, encoded)
		if calls.Add(1) == 1 {
			return httpResponse(http.StatusServiceUnavailable, "try again"), nil
		}
		return httpResponse(http.StatusAccepted, ""), nil
	})}

	client := clientForTest(t, "https://example.com/telemetry", httpClient)
	client.now = func() time.Time { return time.Unix(1_700_000_000, 0) }
	client.Count("git.command", nil).Submit(1)

	if err := client.Flush(context.Background()); err == nil {
		t.Fatal("first Flush() error = nil, want an HTTP status error")
	}
	client.Count("git.command", nil).Submit(2)
	if err := client.Flush(context.Background()); err != nil {
		t.Fatalf("second Flush() error = %v", err)
	}

	if len(received) != 2 {
		t.Fatalf("request count = %d, want 2", len(received))
	}
	retryBody := decodeWireBody(t, received[1])
	if retryBody.RequestType != requestTypeMetrics {
		t.Fatalf("single count retry request type = %q, want %q", retryBody.RequestType, requestTypeMetrics)
	}
	var retryPayload generateMetrics
	if err := json.Unmarshal(retryBody.Payload, &retryPayload); err != nil {
		t.Fatalf("decode retry payload: %v", err)
	}
	if got := retryPayload.Series[0].Points[0][1].(float64); got != 3 {
		t.Fatalf("retried count = %v, want 3", got)
	}
}

func TestFlushRestoresFailedBatch(t *testing.T) {
	var calls atomic.Int32
	var received [][]byte
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		encoded, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		received = append(received, encoded)
		if calls.Add(1) == 1 {
			return httpResponse(http.StatusServiceUnavailable, "try again"), nil
		}
		return httpResponse(http.StatusAccepted, ""), nil
	})}

	client := clientForTest(t, "https://example.com/telemetry", httpClient)
	client.Count("git.command", nil).Submit(1)
	client.Distribution("git.command_ms", nil).Submit(10)
	if err := client.Flush(context.Background()); err == nil {
		t.Fatal("first Flush() error = nil, want batch error")
	}
	client.Count("git.command", nil).Submit(2)
	client.Distribution("git.command_ms", nil).Submit(20)
	if err := client.Flush(context.Background()); err != nil {
		t.Fatalf("second Flush() error = %v", err)
	}

	if len(received) != 2 {
		t.Fatalf("request count = %d, want 2", len(received))
	}
	retryBody := decodeWireBody(t, received[1])
	if retryBody.RequestType != requestTypeMessageBatch {
		t.Fatalf("retry request type = %q, want %q", retryBody.RequestType, requestTypeMessageBatch)
	}
	var retryBatch []wireMessage
	if err := json.Unmarshal(retryBody.Payload, &retryBatch); err != nil {
		t.Fatalf("decode retry batch: %v", err)
	}
	if len(retryBatch) != 2 || retryBatch[0].RequestType != requestTypeMetrics || retryBatch[1].RequestType != requestTypeDistribution {
		t.Fatalf("unexpected retry batch: %#v", retryBatch)
	}
	var metricsPayload generateMetrics
	if err := json.Unmarshal(retryBatch[0].Payload, &metricsPayload); err != nil {
		t.Fatalf("decode retried count: %v", err)
	}
	if got := metricsPayload.Series[0].Points[0][1].(float64); got != 3 {
		t.Fatalf("retried count = %v, want 3", got)
	}
	var distributionPayload distributions
	if err := json.Unmarshal(retryBatch[1].Payload, &distributionPayload); err != nil {
		t.Fatalf("decode retried distribution: %v", err)
	}
	if got := distributionPayload.Series[0].Points; !slices.Equal(got, []float64{10, 20}) {
		t.Fatalf("retried distribution = %v, want [10 20]", got)
	}
}

func TestReducePayloads(t *testing.T) {
	distribution := distributions{Series: []distributionSeries{{Metric: "git.command_ms"}}}
	if got := reducePayloads(nil); got != nil {
		t.Fatalf("reducePayloads(nil) = %#v, want nil", got)
	}
	if got := reducePayloads([]payload{distribution}); got.requestType() != requestTypeDistribution {
		t.Fatalf("single payload request type = %q, want %q", got.requestType(), requestTypeDistribution)
	}
	reduced := reducePayloads([]payload{generateMetrics{}, distribution})
	batch, ok := reduced.(messageBatch)
	if !ok {
		t.Fatalf("two payloads reduced to %T, want messageBatch", reduced)
	}
	if len(batch) != 2 || batch[0].RequestType != requestTypeMetrics || batch[1].RequestType != requestTypeDistribution {
		t.Fatalf("unexpected reduced batch: %#v", batch)
	}
}

func TestCountIsSafeForConcurrentUse(t *testing.T) {
	client := clientForTest(t, "https://example.com/telemetry", &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusAccepted,
				Status:     "202 Accepted",
				Body:       io.NopCloser(&emptyReader{}),
			}, nil
		}),
	})
	count := client.Count("git.command", nil)

	const submissions = 100
	var waitGroup sync.WaitGroup
	for range submissions {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			count.Submit(1)
		}()
	}
	waitGroup.Wait()

	counts, _ := client.drain()
	if got := counts[newMetricKey("git.command", nil)].value; got != submissions {
		t.Fatalf("concurrent count = %v, want %d", got, submissions)
	}
}

func TestNewRuntimeID(t *testing.T) {
	id, err := newRuntimeID()
	if err != nil {
		t.Fatalf("newRuntimeID() error = %v", err)
	}
	if matched := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(id); !matched {
		t.Fatalf("newRuntimeID() = %q, want a version 4 UUID", id)
	}
}

func TestFlushHonorsContextCancellationAndRetainsMetrics(t *testing.T) {
	client := clientForTest(t, "https://example.com/telemetry", &http.Client{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return nil, request.Context().Err()
		}),
	})
	client.Count("git.command", nil).Submit(1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := client.Flush(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Flush() error = %v, want context.Canceled", err)
	}
	counts, _ := client.drain()
	if got := counts[newMetricKey("git.command", nil)].value; got != 1 {
		t.Fatalf("retained count = %v, want 1", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func httpResponse(statusCode int, responseBody string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Status:     fmt.Sprintf("%d %s", statusCode, http.StatusText(statusCode)),
		Body:       io.NopCloser(strings.NewReader(responseBody)),
	}
}

type emptyReader struct{}

func (*emptyReader) Read([]byte) (int, error) {
	return 0, io.EOF
}
