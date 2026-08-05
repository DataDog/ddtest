// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package telemetry

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/DataDog/ddtest/internal/utils/osinfo"
)

const (
	apiVersion              = "v2"
	languageName            = "ddtest"
	requestTypeMetrics      = "generate-metrics"
	requestTypeDistribution = "distributions"
	requestTypeMessageBatch = "message-batch"
)

type payload interface {
	requestType() string
}

type message struct {
	RequestType string  `json:"request_type"`
	Payload     payload `json:"payload"`
}

type messageBatch []message

func (messageBatch) requestType() string {
	return requestTypeMessageBatch
}

func reducePayloads(payloads []payload) payload {
	switch len(payloads) {
	case 0:
		return nil
	case 1:
		return payloads[0]
	}

	messages := make(messageBatch, len(payloads))
	for index, payload := range payloads {
		messages[index] = message{
			RequestType: payload.requestType(),
			Payload:     payload,
		}
	}
	return messages
}

type metricData struct {
	Metric    string   `json:"metric"`
	Points    [][2]any `json:"points"`
	Type      string   `json:"type"`
	Tags      []string `json:"tags,omitempty"`
	Common    bool     `json:"common"`
	Namespace string   `json:"namespace"`
}

type generateMetrics struct {
	Series []metricData `json:"series"`
}

func (generateMetrics) requestType() string {
	return requestTypeMetrics
}

func generateMetricsPayload(counts map[metricKey]countPoint) generateMetrics {
	series := make([]metricData, 0, len(counts))
	for key, point := range counts {
		series = append(series, metricData{
			Metric:    key.name,
			Points:    [][2]any{{point.time.Unix(), point.value}},
			Type:      "count",
			Tags:      key.splitTags(),
			Common:    true,
			Namespace: namespaceCIVisibility,
		})
	}
	slices.SortFunc(series, func(a, b metricData) int {
		if result := strings.Compare(a.Metric, b.Metric); result != 0 {
			return result
		}
		return strings.Compare(strings.Join(a.Tags, ","), strings.Join(b.Tags, ","))
	})
	return generateMetrics{Series: series}
}

type distributionSeries struct {
	Metric    string    `json:"metric"`
	Points    []float64 `json:"points"`
	Tags      []string  `json:"tags,omitempty"`
	Common    bool      `json:"common"`
	Namespace string    `json:"namespace"`
}

type distributions struct {
	Series []distributionSeries `json:"series"`
}

func (distributions) requestType() string {
	return requestTypeDistribution
}

func distributionsPayload(values map[metricKey][]float64) distributions {
	series := make([]distributionSeries, 0, len(values))
	for key, points := range values {
		series = append(series, distributionSeries{
			Metric:    key.name,
			Points:    points,
			Tags:      key.splitTags(),
			Common:    true,
			Namespace: namespaceCIVisibility,
		})
	}
	slices.SortFunc(series, func(a, b distributionSeries) int {
		if result := strings.Compare(a.Metric, b.Metric); result != 0 {
			return result
		}
		return strings.Compare(strings.Join(a.Tags, ","), strings.Join(b.Tags, ","))
	})
	return distributions{Series: series}
}

type application struct {
	ServiceName string `json:"service_name"`
	Environment string `json:"env"`
	// ServiceVersion is required by the telemetry v2 schema. ddtest does not
	// have a service version, so it is always empty.
	ServiceVersion  string `json:"service_version"`
	LibraryVersion  string `json:"tracer_version"`
	LanguageName    string `json:"language_name"`
	LanguageVersion string `json:"language_version"`
}

type host struct {
	Hostname      string `json:"hostname"`
	OS            string `json:"os"`
	OSVersion     string `json:"os_version"`
	Architecture  string `json:"architecture"`
	KernelName    string `json:"kernel_name"`
	KernelRelease string `json:"kernel_release"`
	KernelVersion string `json:"kernel_version"`
}

type body struct {
	APIVersion  string      `json:"api_version"`
	RequestType string      `json:"request_type"`
	TracerTime  int64       `json:"tracer_time"`
	RuntimeID   string      `json:"runtime_id"`
	SequenceID  int64       `json:"seq_id"`
	Payload     payload     `json:"payload"`
	Application application `json:"application"`
	Host        host        `json:"host"`
}

type sender struct {
	mu          sync.Mutex
	endpoint    *url.URL
	apiKey      string
	httpClient  *http.Client
	body        body
	requestTime func() time.Time
}

func newSender(config Config, endpoint *url.URL) (*sender, error) {
	runtimeID := config.RuntimeID
	if runtimeID == "" {
		var err error
		runtimeID, err = newRuntimeID()
		if err != nil {
			return nil, fmt.Errorf("telemetry: create runtime ID: %w", err)
		}
	}

	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "unknown"
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}
	return &sender{
		endpoint:    endpoint,
		apiKey:      config.APIKey,
		httpClient:  httpClient,
		requestTime: time.Now,
		body: body{
			APIVersion: apiVersion,
			RuntimeID:  runtimeID,
			Application: application{
				ServiceName:     config.ServiceName,
				Environment:     config.Environment,
				LibraryVersion:  config.LibraryVersion,
				LanguageName:    languageName,
				LanguageVersion: runtime.Version(),
			},
			Host: host{
				Hostname:      hostname,
				OS:            runtime.GOOS,
				OSVersion:     osinfo.OSVersion(),
				Architecture:  runtime.GOARCH,
				KernelName:    "unknown",
				KernelRelease: "unknown",
				KernelVersion: "unknown",
			},
		},
	}, nil
}

func newRuntimeID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80

	var encoded [36]byte
	hex.Encode(encoded[0:8], id[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], id[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], id[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], id[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], id[10:16])
	return string(encoded[:]), nil
}

func (s *sender) send(ctx context.Context, metrics payload) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.body.SequenceID++
	s.body.TracerTime = s.requestTime().Unix()
	s.body.RequestType = metrics.requestType()
	s.body.Payload = metrics

	encoded, err := json.Marshal(s.body)
	if err != nil {
		return fmt.Errorf("telemetry: encode %s payload: %w", metrics.requestType(), err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint.String(), bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("telemetry: create %s request: %w", metrics.requestType(), err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("DD-Telemetry-API-Version", apiVersion)
	request.Header.Set("DD-Telemetry-Request-Type", metrics.requestType())
	request.Header.Set("DD-Client-Library-Language", s.body.Application.LanguageName)
	request.Header.Set("DD-Client-Library-Version", s.body.Application.LibraryVersion)
	request.Header.Set("DD-Agent-Env", s.body.Application.Environment)
	request.Header.Set("DD-Agent-Hostname", s.body.Host.Hostname)
	if s.apiKey != "" {
		request.Header.Set("DD-API-KEY", s.apiKey)
	}

	response, err := s.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("telemetry: send %s payload: %w", metrics.requestType(), err)
	}
	defer func() {
		_ = response.Body.Close()
	}()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 256))
		return fmt.Errorf("telemetry: send %s payload: unexpected status %s: %s", metrics.requestType(), response.Status, responseBody)
	}
	return nil
}
