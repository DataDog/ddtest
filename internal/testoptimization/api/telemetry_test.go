// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/environment"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/telemetry"
)

type apiRecordedMetric struct {
	kind  string
	name  string
	tags  []string
	value float64
}

type apiRecordingClient struct {
	mu      sync.Mutex
	metrics []apiRecordedMetric
}

type apiRecordingMetric struct {
	client *apiRecordingClient
	kind   string
	name   string
	tags   []string
}

func (c *apiRecordingClient) Count(name string, tags []string) telemetry.Metric {
	return &apiRecordingMetric{client: c, kind: "count", name: name, tags: slices.Clone(tags)}
}

func (c *apiRecordingClient) Distribution(name string, tags []string) telemetry.Metric {
	return &apiRecordingMetric{client: c, kind: "distribution", name: name, tags: slices.Clone(tags)}
}

func (c *apiRecordingClient) Flush(context.Context) error { return nil }

func (m *apiRecordingMetric) Submit(value float64) {
	m.client.mu.Lock()
	defer m.client.mu.Unlock()
	m.client.metrics = append(m.client.metrics, apiRecordedMetric{
		kind:  m.kind,
		name:  m.name,
		tags:  m.tags,
		value: value,
	})
}

func (c *apiRecordingClient) values(kind, name string, tags []string) []float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	var values []float64
	for _, metric := range c.metrics {
		if metric.kind == kind && metric.name == name && slices.Equal(metric.tags, tags) {
			values = append(values, metric.value)
		}
	}
	return values
}

func (c *apiRecordingClient) assertValue(t *testing.T, kind, name string, tags []string, want float64) {
	t.Helper()
	values := c.values(kind, name, tags)
	if len(values) != 1 || values[0] != want {
		t.Errorf("%s %s %v values = %v, want [%v]", kind, name, tags, values, want)
	}
}

func (c *apiRecordingClient) assertSamples(t *testing.T, kind, name string, tags []string, want int) {
	t.Helper()
	if values := c.values(kind, name, tags); len(values) != want {
		t.Errorf("%s %s %v sample count = %d, want %d; values=%v", kind, name, tags, len(values), want, values)
	}
}

func TestTransportRecordsBackendTelemetry(t *testing.T) {
	settingsBody := `{"data":{"attributes":{"code_coverage":true,"early_flake_detection":{"enabled":true},"flaky_test_retries_enabled":true,"tests_skipping":true,"test_management":{"enabled":true}}}}`
	knownBodies := []string{
		`{"data":{"attributes":{"tests":{"module-a":{"suite-a":["test-a"]}},"page_info":{"cursor":"page-2","has_next":true}}}}`,
		`{"data":{"attributes":{"tests":{"module-a":{"suite-a":["test-b"]},"module-b":{"suite-b":["test-c"]}},"page_info":{"has_next":false}}}}`,
	}
	skippableBody := `{"meta":{"correlation_id":"cid"},"data":[{"type":"test","attributes":{"suite":"suite-a","name":"test-a","configurations":{"test.bundle":"module-a"}}},{"type":"test","attributes":{"suite":"suite-b","name":"test-b","configurations":{"test.bundle":"module-b"}}}]}`
	testManagementBody := `{"data":{"attributes":{"modules":{"module-a":{"suites":{"suite-a":{"tests":{"test-a":{"properties":{}},"test-b":{"properties":{}}}}}}}}}}`
	durationsBodies := []string{
		`{"data":{"attributes":{"test_suites":{"module-a":{"suite-a":{"source_file":"a_test.go","duration":{"p50":"100","p90":"200"}}}},"page_info":{"cursor":"page-2","has_next":true}}}}`,
		`{"data":{"attributes":{"test_suites":{"module-a":{"suite-b":{"source_file":"b_test.go","duration":{"p50":"300","p90":"400"}}},"module-b":{"suite-c":{"source_file":"c_test.go","duration":{"p50":"500","p90":"600"}}}},"page_info":{"has_next":false}}}}`,
	}
	searchCommitsBody := `{"data":[{"id":"remote-commit","type":"commit"}]}`
	knownRequests := 0
	durationsRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(HeaderContentType, constants.ContentTypeJSON)
		var body string
		switch strings.TrimPrefix(r.URL.Path, "/") {
		case settingsURLPath:
			body = settingsBody
		case knownTestsURLPath:
			body = knownBodies[knownRequests]
			knownRequests++
		case skippableURLPath:
			body = skippableBody
		case testManagementTestsURLPath:
			body = testManagementBody
		case durationsURLPath:
			body = durationsBodies[durationsRequests]
			durationsRequests++
		case searchCommitsURLPath:
			body = searchCommitsBody
		case sendPackFilesURLPath:
			body = `{}`
		default:
			t.Fatalf("unexpected request path %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()

	recorder := &apiRecordingClient{}
	client := newRawResponseTestClient(server)
	client.telemetryClient = recorder

	if _, err := client.GetSettings(); err != nil {
		t.Fatalf("GetSettings() error = %v", err)
	}
	if _, err := client.GetKnownTests(); err != nil {
		t.Fatalf("GetKnownTests() error = %v", err)
	}
	if _, _, err := client.GetSkippableTests(); err != nil {
		t.Fatalf("GetSkippableTests() error = %v", err)
	}
	if _, err := client.GetTestManagementTests(); err != nil {
		t.Fatalf("GetTestManagementTests() error = %v", err)
	}
	if durations := client.GetTestSuiteDurations(); len(durations.TestSuites) != 2 {
		t.Fatalf("GetTestSuiteDurations() modules = %d, want 2", len(durations.TestSuites))
	}
	if _, err := client.GetCommits([]string{"local-commit"}); err != nil {
		t.Fatalf("GetCommits() error = %v", err)
	}
	packDir := t.TempDir()
	packFiles := []string{filepath.Join(packDir, "one.pack"), filepath.Join(packDir, "two.pack")}
	for i, file := range packFiles {
		if err := os.WriteFile(file, []byte(strings.Repeat("x", i+1)), 0o600); err != nil {
			t.Fatalf("write packfile: %v", err)
		}
	}
	if _, err := client.SendPackFiles("", packFiles); err != nil {
		t.Fatalf("SendPackFiles() error = %v", err)
	}

	recorder.assertValue(t, "count", "ddtest.git_requests.settings", nil, 1)
	recorder.assertValue(t, "count", "ddtest.git_requests.settings_response", []string{
		"coverage_enabled",
		"itrskip_enabled",
		"early_flake_detection_enabled:true",
		"flaky_test_retries_enabled:true",
		"test_management_enabled:true",
	}, 1)
	recorder.assertSamples(t, "distribution", "ddtest.git_requests.settings_ms", nil, 1)
	recorder.assertSamples(t, "count", "ddtest.known_tests.request", nil, 2)
	recorder.assertSamples(t, "distribution", "ddtest.known_tests.request_ms", nil, 2)
	recorder.assertSamples(t, "distribution", "ddtest.known_tests.response_bytes", nil, 2)
	recorder.assertValue(t, "distribution", "ddtest.known_tests.response_tests", nil, 3)
	recorder.assertValue(t, "count", "ddtest.itr_skippable_tests.request", nil, 1)
	recorder.assertSamples(t, "distribution", "ddtest.itr_skippable_tests.request_ms", nil, 1)
	recorder.assertValue(t, "distribution", "ddtest.itr_skippable_tests.response_bytes", nil, float64(len(skippableBody)))
	recorder.assertValue(t, "count", "ddtest.itr_skippable_tests.response_tests", nil, 2)
	recorder.assertSamples(t, "count", "ddtest.itr_skippable_tests.response_suites", nil, 0)
	recorder.assertSamples(t, "count", "ddtest.itr_skippable_tests.is_empty", nil, 0)
	recorder.assertValue(t, "count", "ddtest.test_management_tests.request", nil, 1)
	recorder.assertSamples(t, "distribution", "ddtest.test_management_tests.request_ms", nil, 1)
	recorder.assertValue(t, "distribution", "ddtest.test_management_tests.response_bytes", nil, float64(len(testManagementBody)))
	recorder.assertValue(t, "distribution", "ddtest.test_management_tests.response_tests", nil, 2)
	recorder.assertSamples(t, "count", "test_suite_durations.request", nil, 2)
	recorder.assertSamples(t, "distribution", "test_suite_durations.request_ms", nil, 2)
	recorder.assertSamples(t, "distribution", "test_suite_durations.response_bytes", nil, 2)
	recorder.assertValue(t, "distribution", "test_suite_durations.response_suites", nil, 3)
	recorder.assertSamples(t, "count", "test_suite_durations.is_empty", nil, 0)
	recorder.assertValue(t, "count", "ddtest.git_requests.search_commits", nil, 1)
	recorder.assertSamples(t, "distribution", "ddtest.git_requests.search_commits_ms", nil, 1)
	recorder.assertSamples(t, "count", "ddtest.git_requests.objects_pack", nil, 2)
	recorder.assertSamples(t, "distribution", "ddtest.git_requests.objects_pack_ms", nil, 2)
	recorder.assertValue(t, "distribution", "ddtest.git_requests.objects_pack_files", nil, 2)
	recorder.assertValue(t, "distribution", "ddtest.git_requests.objects_pack_bytes", nil, 3)
}

func TestTransportRecordsSuiteOnlySkippableResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(HeaderContentType, constants.ContentTypeJSON)
		_, _ = io.WriteString(w, `{"meta":{"correlation_id":"cid"},"data":[{"type":"suite","attributes":{"suite":"suite-a","configurations":{"test.bundle":"module-a"}}}]}`)
	}))
	defer server.Close()

	recorder := &apiRecordingClient{}
	client := newRawResponseTestClientWithTestSkippingLevel(server, settings.TestSkippingLevelSuite)
	client.telemetryClient = recorder

	if _, _, err := client.GetSkippableTests(); err != nil {
		t.Fatalf("GetSkippableTests() error = %v", err)
	}

	recorder.assertValue(t, "count", "ddtest.itr_skippable_tests.response_suites", nil, 1)
	recorder.assertSamples(t, "count", "ddtest.itr_skippable_tests.response_tests", nil, 0)
	recorder.assertSamples(t, "count", "ddtest.itr_skippable_tests.is_empty", nil, 0)
}

func TestTransportRecordsEmptySkippableResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(HeaderContentType, constants.ContentTypeJSON)
		_, _ = io.WriteString(w, `{"meta":{"correlation_id":"cid"},"data":[]}`)
	}))
	defer server.Close()

	recorder := &apiRecordingClient{}
	client := newRawResponseTestClient(server)
	client.telemetryClient = recorder

	if _, skippables, err := client.GetSkippableTests(); err != nil {
		t.Fatalf("GetSkippableTests() error = %v", err)
	} else if len(skippables.Tests) != 0 || len(skippables.Suites) != 0 {
		t.Fatalf("GetSkippableTests() = %#v, want no skippables", skippables)
	}

	recorder.assertValue(t, "count", "ddtest.itr_skippable_tests.response_tests", nil, 0)
	recorder.assertValue(t, "count", "ddtest.itr_skippable_tests.is_empty", nil, 1)
}

func TestTransportRecordsCompressedResponseWireBytes(t *testing.T) {
	responseBodies := map[string]string{
		knownTestsURLPath:          `{"data":{"attributes":{"tests":{"module-a":{"suite-a":["test-a"]}}}}}`,
		skippableURLPath:           `{"meta":{"correlation_id":"cid"},"data":[{"type":"test","attributes":{"suite":"suite-a","name":"test-a","configurations":{"test.bundle":"module-a"}}}]}`,
		testManagementTestsURLPath: `{"data":{"attributes":{"modules":{"module-a":{"suites":{"suite-a":{"tests":{"test-a":{"properties":{}}}}}}}}}}`,
		durationsURLPath:           `{"data":{"attributes":{"test_suites":{"module-a":{"suite-a":{"source_file":"a_test.go","duration":{"p50":"100","p90":"200"}}}}}}}`,
	}
	compressedBodies := make(map[string][]byte, len(responseBodies))
	for path, body := range responseBodies {
		compressed, err := compressData([]byte(body))
		if err != nil {
			t.Fatalf("compress %s response: %v", path, err)
		}
		compressedBodies[path] = compressed
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		body, ok := compressedBodies[path]
		if !ok {
			t.Fatalf("unexpected request path %s", r.URL.Path)
		}
		w.Header().Set(HeaderContentType, constants.ContentTypeJSON)
		w.Header().Set(HeaderContentEncoding, ContentEncodingGzip)
		_, _ = w.Write(body)
	}))
	defer server.Close()

	recorder := &apiRecordingClient{}
	client := newRawResponseTestClient(server)
	client.telemetryClient = recorder

	if _, err := client.GetKnownTests(); err != nil {
		t.Fatalf("GetKnownTests() error = %v", err)
	}
	if _, _, err := client.GetSkippableTests(); err != nil {
		t.Fatalf("GetSkippableTests() error = %v", err)
	}
	if _, err := client.GetTestManagementTests(); err != nil {
		t.Fatalf("GetTestManagementTests() error = %v", err)
	}
	if durations := client.GetTestSuiteDurations(); len(durations.TestSuites) != 1 {
		t.Fatalf("GetTestSuiteDurations() modules = %d, want 1", len(durations.TestSuites))
	}

	tags := []string{"rs_compressed:true"}
	recorder.assertValue(t, "distribution", "ddtest.known_tests.response_bytes", tags, float64(len(compressedBodies[knownTestsURLPath])))
	recorder.assertValue(t, "distribution", "ddtest.itr_skippable_tests.response_bytes", tags, float64(len(compressedBodies[skippableURLPath])))
	recorder.assertValue(t, "distribution", "ddtest.test_management_tests.response_bytes", tags, float64(len(compressedBodies[testManagementTestsURLPath])))
	recorder.assertValue(t, "distribution", "test_suite_durations.response_bytes", tags, float64(len(compressedBodies[durationsURLPath])))
}

func TestTransportRecordsTerminalRetryStatusAndFailedSearchCommitsLatency(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	recorder := &apiRecordingClient{}
	client := newRawResponseTestClient(server)
	client.telemetryClient = recorder

	if _, err := client.GetCommits([]string{"local-commit"}); err == nil {
		t.Fatal("GetCommits() should fail after exhausting retries")
	}

	recorder.assertValue(t, "count", "ddtest.git_requests.search_commits_errors", []string{"error_type:status_code_5xx_response"}, 1)
	recorder.assertSamples(t, "count", "ddtest.git_requests.search_commits_errors", []string{"error_type:network"}, 0)
	recorder.assertSamples(t, "distribution", "ddtest.git_requests.search_commits_ms", nil, 1)
}

func TestTransportRecordsTestSuiteDurationsNetworkError(t *testing.T) {
	recorder := &apiRecordingClient{}
	client := &transport{
		agentless:     true,
		baseURL:       "https://example.test",
		serviceName:   "my-service",
		repositoryURL: "github.com/DataDog/foo",
		headers:       map[string]string{},
		handler: NewRequestHandlerWithClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("network unavailable")
		})}),
		telemetryClient: recorder,
	}

	if durations := client.GetTestSuiteDurations(); len(durations.TestSuites) != 0 {
		t.Fatalf("GetTestSuiteDurations() suites = %d, want 0", len(durations.TestSuites))
	}

	recorder.assertValue(t, "count", "test_suite_durations.request", nil, 1)
	recorder.assertSamples(t, "distribution", "test_suite_durations.request_ms", nil, 1)
	recorder.assertValue(t, "count", "test_suite_durations.request_errors", []string{"error_type:network"}, 1)
	recorder.assertSamples(t, "distribution", "test_suite_durations.response_bytes", nil, 0)
	recorder.assertSamples(t, "distribution", "test_suite_durations.response_suites", nil, 0)
	recorder.assertSamples(t, "count", "test_suite_durations.is_empty", nil, 0)
}

func TestTransportRecordsEmptyTestSuiteDurationsResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(HeaderContentType, constants.ContentTypeJSON)
		_, _ = io.WriteString(w, `{"data":{"attributes":{"test_suites":{}}}}`)
	}))
	defer server.Close()

	recorder := &apiRecordingClient{}
	client := newRawResponseTestClient(server)
	client.telemetryClient = recorder

	if durations := client.GetTestSuiteDurations(); len(durations.TestSuites) != 0 {
		t.Fatalf("GetTestSuiteDurations() suites = %d, want 0", len(durations.TestSuites))
	}

	recorder.assertValue(t, "distribution", "test_suite_durations.response_suites", nil, 0)
	recorder.assertValue(t, "count", "test_suite_durations.is_empty", nil, 1)
}

func TestTransportRecordsBackendStatusErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer server.Close()

	recorder := &apiRecordingClient{}
	client := newRawResponseTestClient(server)
	client.telemetryClient = recorder
	_, _ = client.GetSettings()
	_, _ = client.GetKnownTests()
	_, _, _ = client.GetSkippableTests()
	_, _ = client.GetTestManagementTests()
	_ = client.GetTestSuiteDurations()
	_, _ = client.GetCommits([]string{"local-commit"})
	packFile := filepath.Join(t.TempDir(), "objects.pack")
	if err := os.WriteFile(packFile, []byte("pack"), 0o600); err != nil {
		t.Fatalf("write packfile: %v", err)
	}
	_, _ = client.SendPackFiles("", []string{packFile})

	tags := []string{"error_type:status_code_4xx_response", "status_code:400"}
	for _, name := range []string{
		"ddtest.git_requests.settings_errors",
		"ddtest.known_tests.request_errors",
		"ddtest.itr_skippable_tests.request_errors",
		"ddtest.test_management_tests.request_errors",
		"test_suite_durations.request_errors",
		"ddtest.git_requests.search_commits_errors",
		"ddtest.git_requests.objects_pack_errors",
	} {
		recorder.assertValue(t, "count", name, tags, 1)
	}
	recorder.assertSamples(t, "count", "ddtest.itr_skippable_tests.is_empty", nil, 0)
}

func TestNewTransportWithTelemetryRetainsClient(t *testing.T) {
	environment.ResetCITags()
	t.Cleanup(environment.ResetCITags)
	environment.AddCITagsMap(map[string]string{
		constants.GitRepositoryURL: "https://github.com/DataDog/ddtest.git",
		constants.GitCommitSHA:     "sha",
	})
	t.Setenv(constants.TestOptimizationAgentlessEnabledEnvironmentVariable, "true")
	t.Setenv(constants.TestOptimizationAgentlessURLEnvironmentVariable, "https://example.test")
	t.Setenv(constants.APIKeyEnvironmentVariable, "api-key")

	recorder := &apiRecordingClient{}
	created, ok := NewTransportWithTelemetry("service", settings.TestSkippingLevelSuite, recorder).(*transport)
	if !ok {
		t.Fatal("NewTransportWithTelemetry() did not return *transport")
	}
	if created.telemetryClient != recorder {
		t.Fatal("NewTransportWithTelemetry() did not retain telemetry client")
	}
	if created.testSkippingLevel != settings.TestSkippingLevelSuite {
		t.Fatalf("test skipping level = %q, want suite", created.testSkippingLevel)
	}
}
