package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/constants"
)

type durationsRequestRecord struct {
	RepositoryURL string
	Service       string
	Cursor        string
	PageSize      int
}

func newDurationsTestClient(server *httptest.Server) *transport {
	return &transport{
		agentless:     true,
		baseURL:       server.URL,
		serviceName:   "my-service",
		repositoryURL: "github.com/DataDog/foo",
		headers:       map[string]string{},
		handler:       NewRequestHandlerWithClient(server.Client()),
	}
}

func newDurationsTestServer(t *testing.T, responses []string, records *[]durationsRequestRecord) *httptest.Server {
	t.Helper()
	requestCount := 0

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST request, got %s", r.Method)
		}
		if strings.TrimPrefix(r.URL.Path, "/") != durationsURLPath {
			t.Fatalf("unexpected request path %s", r.URL.Path)
		}
		if requestCount >= len(responses) {
			t.Fatalf("unexpected extra request")
		}

		var request durationsRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}

		record := durationsRequestRecord{
			RepositoryURL: request.Data.Attributes.RepositoryURL,
			Service:       request.Data.Attributes.Service,
		}
		if request.Data.Attributes.PageInfo != nil {
			record.Cursor = request.Data.Attributes.PageInfo.PageState
			record.PageSize = request.Data.Attributes.PageInfo.PageSize
		}
		*records = append(*records, record)

		w.Header().Set(HeaderContentType, constants.ContentTypeJSON)
		_, _ = w.Write([]byte(responses[requestCount]))
		requestCount++
	}))
}

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	originalLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() {
		slog.SetDefault(originalLogger)
	})
	return &buf
}

func TestClientGetTestSuiteDurationsLogsSuccess(t *testing.T) {
	logs := captureLogs(t)
	var records []durationsRequestRecord
	server := newDurationsTestServer(t, []string{
		`{"data":{"id":"durations","type":"ci_app_ddtest_test_suite_durations_request","attributes":{"test_suites":{"module1":{"suite1":{"source_file":"spec/user_spec.rb","duration":{"p50":"280000000","p90":"350000000"}}}}}}}`,
	}, &records)
	defer server.Close()

	client := newDurationsTestClient(server)
	result := client.GetTestSuiteDurations()

	if len(records) != 1 {
		t.Fatalf("expected 1 durations request, got %d", len(records))
	}
	if records[0].RepositoryURL != "github.com/DataDog/foo" {
		t.Errorf("expected repository URL 'github.com/DataDog/foo', got %q", records[0].RepositoryURL)
	}
	if records[0].Service != "my-service" {
		t.Errorf("expected service 'my-service', got %q", records[0].Service)
	}
	if records[0].Cursor != "" || records[0].PageSize != defaultDurationsPageSize {
		t.Errorf("expected first page request with default page size, got cursor=%q pageSize=%d", records[0].Cursor, records[0].PageSize)
	}
	if len(result.TestSuites) != 1 {
		t.Errorf("expected 1 module, got %d", len(result.TestSuites))
	}
	if !strings.Contains(logs.String(), "level=INFO") ||
		!strings.Contains(logs.String(), "Fetched test suite durations") ||
		!strings.Contains(logs.String(), "modulesCount=1") ||
		!strings.Contains(logs.String(), "testSuitesCount=1") ||
		!strings.Contains(logs.String(), "duration=") {
		t.Errorf("expected INFO log for non-empty durations response, got logs: %s", logs.String())
	}
}

func TestClientGetTestSuiteDurationsMissingRepositoryReturnsEmptyAndLogsError(t *testing.T) {
	logs := captureLogs(t)
	client := &transport{}

	result := client.GetTestSuiteDurations()

	if len(result.TestSuites) != 0 {
		t.Errorf("expected empty durations on missing repository URL, got %v", result)
	}
	if !strings.Contains(logs.String(), "level=ERROR") ||
		!strings.Contains(logs.String(), "Test durations API errored") ||
		!strings.Contains(logs.String(), "repository URL is required") ||
		!strings.Contains(logs.String(), "duration=") {
		t.Errorf("expected ERROR log for missing repository URL, got logs: %s", logs.String())
	}
}

func TestClientGetTestSuiteDurationsAPIErrorReturnsEmptyAndLogsError(t *testing.T) {
	logs := captureLogs(t)
	var records []durationsRequestRecord
	server := newDurationsTestServer(t, []string{`{"data":`}, &records)
	defer server.Close()

	client := newDurationsTestClient(server)
	result := client.GetTestSuiteDurations()

	if len(result.TestSuites) != 0 {
		t.Errorf("expected empty durations on API error, got %v", result)
	}
	if !strings.Contains(logs.String(), "level=ERROR") ||
		!strings.Contains(logs.String(), "Test durations API errored") ||
		!strings.Contains(logs.String(), "repositoryURL=github.com/DataDog/foo") ||
		!strings.Contains(logs.String(), "service=my-service") ||
		!strings.Contains(logs.String(), "unmarshalling") ||
		!strings.Contains(logs.String(), "duration=") {
		t.Errorf("expected ERROR log for durations API failure, got logs: %s", logs.String())
	}
}

func TestClientGetTestSuiteDurationsEmptyResponseReturnsEmptyAndLogsWarn(t *testing.T) {
	logs := captureLogs(t)
	var records []durationsRequestRecord
	server := newDurationsTestServer(t, []string{
		`{"data":{"id":"durations","type":"ci_app_ddtest_test_suite_durations_request","attributes":{"test_suites":{}}}}`,
	}, &records)
	defer server.Close()

	client := newDurationsTestClient(server)
	result := client.GetTestSuiteDurations()

	if len(result.TestSuites) != 0 {
		t.Errorf("expected empty durations on empty response, got %v", result)
	}
	if !strings.Contains(logs.String(), "level=WARN") ||
		!strings.Contains(logs.String(), "Test durations API returned no test suites") ||
		!strings.Contains(logs.String(), "modulesCount=0") ||
		!strings.Contains(logs.String(), "testSuitesCount=0") ||
		!strings.Contains(logs.String(), "duration=") {
		t.Errorf("expected WARN log for empty durations response, got logs: %s", logs.String())
	}
}

func TestClientFetchTestSuiteDurationsSinglePage(t *testing.T) {
	var records []durationsRequestRecord
	server := newDurationsTestServer(t, []string{
		`{"data":{"id":"durations","type":"ci_app_ddtest_test_suite_durations_request","attributes":{"test_suites":{"module1":{"suite1":{"source_file":"spec/user_spec.rb","duration":{"p50":"280000000","p90":"350000000"}},"suite2":{"source_file":"spec/order_spec.rb","duration":{"p50":"100000000","p90":"150000000"}}},"module2":{"suite3":{"source_file":"spec/product_spec.rb","duration":{"p50":"500000000","p90":"600000000"}}}}}}}`,
	}, &records)
	defer server.Close()

	client := newDurationsTestClient(server)
	result, err := client.fetchTestSuiteDurations("github.com/DataDog/foo", "my-service")

	if err != nil {
		t.Errorf("fetchTestSuiteDurations() should not return error, got: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 durations request, got %d", len(records))
	}
	if records[0].RepositoryURL != "github.com/DataDog/foo" {
		t.Errorf("expected repository URL 'github.com/DataDog/foo', got %q", records[0].RepositoryURL)
	}
	if records[0].Service != "my-service" {
		t.Errorf("expected service 'my-service', got %q", records[0].Service)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 modules, got %d", len(result))
	}

	module1, exists := result["module1"]
	if !exists {
		t.Error("expected module1 to exist")
		return
	}
	if len(module1) != 2 {
		t.Errorf("expected 2 suites in module1, got %d", len(module1))
	}

	suite1, exists := module1["suite1"]
	if !exists {
		t.Error("expected suite1 to exist in module1")
		return
	}
	if suite1.SourceFile != "spec/user_spec.rb" {
		t.Errorf("expected source file 'spec/user_spec.rb', got %q", suite1.SourceFile)
	}
	if suite1.Duration.P50 != "280000000" {
		t.Errorf("expected P50 '280000000', got %q", suite1.Duration.P50)
	}
	if suite1.Duration.P90 != "350000000" {
		t.Errorf("expected P90 '350000000', got %q", suite1.Duration.P90)
	}

	module2, exists := result["module2"]
	if !exists {
		t.Error("expected module2 to exist")
		return
	}
	if len(module2) != 1 {
		t.Errorf("expected 1 suite in module2, got %d", len(module2))
	}
}

func TestClientFetchTestSuiteDurationsPagination(t *testing.T) {
	var records []durationsRequestRecord
	server := newDurationsTestServer(t, []string{
		`{"data":{"id":"durations","type":"ci_app_ddtest_test_suite_durations_request","attributes":{"test_suites":{"module1":{"suite1":{"source_file":"spec/user_spec.rb","duration":{"p50":"280000000","p90":"350000000"}}}},"page_info":{"cursor":"abc123","size":500,"has_next":true}}}}`,
		`{"data":{"id":"durations","type":"ci_app_ddtest_test_suite_durations_request","attributes":{"test_suites":{"module1":{"suite2":{"source_file":"spec/order_spec.rb","duration":{"p50":"100000000","p90":"150000000"}}},"module2":{"suite3":{"source_file":"spec/product_spec.rb","duration":{"p50":"500000000","p90":"600000000"}}}},"page_info":{"size":500,"has_next":false}}}}`,
	}, &records)
	defer server.Close()

	client := newDurationsTestClient(server)
	result, err := client.fetchTestSuiteDurations("github.com/DataDog/foo", "my-service")

	if err != nil {
		t.Errorf("fetchTestSuiteDurations() should not return error, got: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 durations requests, got %d", len(records))
	}
	if records[0].Cursor != "" {
		t.Errorf("first request should have empty cursor, got %q", records[0].Cursor)
	}
	if records[1].Cursor != "abc123" {
		t.Errorf("second request should have cursor 'abc123', got %q", records[1].Cursor)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 modules, got %d", len(result))
	}

	module1, exists := result["module1"]
	if !exists {
		t.Error("expected module1 to exist")
		return
	}
	if len(module1) != 2 {
		t.Errorf("expected 2 suites in module1 merged from both pages, got %d", len(module1))
	}
	if _, exists := module1["suite1"]; !exists {
		t.Error("expected suite1 to exist in module1 from page 1")
	}
	if _, exists := module1["suite2"]; !exists {
		t.Error("expected suite2 to exist in module1 from page 2")
	}
}

func TestClientFetchTestSuiteDurationsEmptyResponse(t *testing.T) {
	var records []durationsRequestRecord
	server := newDurationsTestServer(t, []string{
		`{"data":{"id":"durations","type":"ci_app_ddtest_test_suite_durations_request","attributes":{"test_suites":{}}}}`,
	}, &records)
	defer server.Close()

	client := newDurationsTestClient(server)
	result, err := client.fetchTestSuiteDurations("github.com/DataDog/foo", "my-service")

	if err != nil {
		t.Errorf("fetchTestSuiteDurations() should not return error, got: %v", err)
	}
	if result == nil {
		t.Error("fetchTestSuiteDurations() should return non-nil map even with empty data")
	}
	if len(result) != 0 {
		t.Errorf("fetchTestSuiteDurations() should return empty map, got %d modules", len(result))
	}
}

func TestClientFetchTestSuiteDurationsNilTestSuites(t *testing.T) {
	var records []durationsRequestRecord
	server := newDurationsTestServer(t, []string{
		`{"data":{"id":"durations","type":"ci_app_ddtest_test_suite_durations_request","attributes":{}}}`,
	}, &records)
	defer server.Close()

	client := newDurationsTestClient(server)
	result, err := client.fetchTestSuiteDurations("github.com/DataDog/foo", "my-service")

	if err != nil {
		t.Errorf("fetchTestSuiteDurations() should not return error, got: %v", err)
	}
	if result == nil {
		t.Error("fetchTestSuiteDurations() should return non-nil map even with nil test suites")
	}
	if len(result) != 0 {
		t.Errorf("fetchTestSuiteDurations() should return empty map, got %d modules", len(result))
	}
}

func TestClientFetchTestSuiteDurationsAPIError(t *testing.T) {
	var records []durationsRequestRecord
	server := newDurationsTestServer(t, []string{`{"data":`}, &records)
	defer server.Close()

	client := newDurationsTestClient(server)
	result, err := client.fetchTestSuiteDurations("github.com/DataDog/foo", "my-service")

	if err == nil {
		t.Error("fetchTestSuiteDurations() should return error when API fails")
	}
	if result != nil {
		t.Error("fetchTestSuiteDurations() should return nil result when API fails")
	}
}

func TestClientFetchTestSuiteDurationsPaginationError(t *testing.T) {
	var records []durationsRequestRecord
	server := newDurationsTestServer(t, []string{
		`{"data":{"id":"durations","type":"ci_app_ddtest_test_suite_durations_request","attributes":{"test_suites":{"module1":{"suite1":{"source_file":"spec/user_spec.rb","duration":{"p50":"280000000","p90":"350000000"}}}},"page_info":{"cursor":"abc123","size":500,"has_next":true}}}}`,
		`{"data":`,
	}, &records)
	defer server.Close()

	client := newDurationsTestClient(server)
	result, err := client.fetchTestSuiteDurations("github.com/DataDog/foo", "my-service")

	if err == nil {
		t.Error("fetchTestSuiteDurations() should return error when pagination fails")
	}
	if result != nil {
		t.Error("fetchTestSuiteDurations() should return nil result when pagination fails")
	}
}

func TestClientFetchTestSuiteDurationsNilPageInfo(t *testing.T) {
	var records []durationsRequestRecord
	server := newDurationsTestServer(t, []string{
		`{"data":{"id":"durations","type":"ci_app_ddtest_test_suite_durations_request","attributes":{"test_suites":{"module1":{"suite1":{"source_file":"spec/user_spec.rb","duration":{"p50":"280000000","p90":"350000000"}}}}}}}`,
	}, &records)
	defer server.Close()

	client := newDurationsTestClient(server)
	result, err := client.fetchTestSuiteDurations("github.com/DataDog/foo", "my-service")

	if err != nil {
		t.Errorf("fetchTestSuiteDurations() should not return error, got: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("expected 1 module, got %d", len(result))
	}
	if len(records) != 1 {
		t.Errorf("expected 1 request when PageInfo is nil, got %d", len(records))
	}
}

func TestClientFetchTestSuiteDurationsThreePages(t *testing.T) {
	var records []durationsRequestRecord
	server := newDurationsTestServer(t, []string{
		`{"data":{"id":"durations","type":"ci_app_ddtest_test_suite_durations_request","attributes":{"test_suites":{"module1":{"suite1":{"source_file":"spec/a_spec.rb","duration":{"p50":"100","p90":"200"}}}},"page_info":{"cursor":"page2","has_next":true}}}}`,
		`{"data":{"id":"durations","type":"ci_app_ddtest_test_suite_durations_request","attributes":{"test_suites":{"module1":{"suite2":{"source_file":"spec/b_spec.rb","duration":{"p50":"300","p90":"400"}}}},"page_info":{"cursor":"page3","has_next":true}}}}`,
		`{"data":{"id":"durations","type":"ci_app_ddtest_test_suite_durations_request","attributes":{"test_suites":{"module1":{"suite3":{"source_file":"spec/c_spec.rb","duration":{"p50":"500","p90":"600"}}}},"page_info":{"has_next":false}}}}`,
	}, &records)
	defer server.Close()

	client := newDurationsTestClient(server)
	result, err := client.fetchTestSuiteDurations("github.com/DataDog/foo", "my-service")

	if err != nil {
		t.Errorf("fetchTestSuiteDurations() should not return error, got: %v", err)
	}
	if len(records) != 3 {
		t.Errorf("expected 3 requests, got %d", len(records))
	}
	if records[0].Cursor != "" {
		t.Errorf("first cursor should be empty, got %q", records[0].Cursor)
	}
	if records[1].Cursor != "page2" {
		t.Errorf("second cursor should be 'page2', got %q", records[1].Cursor)
	}
	if records[2].Cursor != "page3" {
		t.Errorf("third cursor should be 'page3', got %q", records[2].Cursor)
	}

	module1 := result["module1"]
	if len(module1) != 3 {
		t.Errorf("expected 3 suites merged in module1, got %d", len(module1))
	}
}

func TestClientFetchTestSuiteDurationsEmptyRepositoryURL(t *testing.T) {
	client := &transport{}

	_, err := client.fetchTestSuiteDurationsPage("", "my-service", "", 100)
	if err == nil {
		t.Error("fetchTestSuiteDurationsPage() should return error when repository URL is empty")
	}
}

func TestNewTransportWithServiceNameAgentlessMissingAPIKeyReturnsNil(t *testing.T) {
	t.Setenv(constants.TestOptimizationAgentlessEnabledEnvironmentVariable, "true")
	t.Setenv(constants.APIKeyEnvironmentVariable, "")

	client := NewTransportWithServiceName("my-service")
	if client != nil {
		t.Fatal("NewTransportWithServiceName() should return nil when agentless mode is missing an API key")
	}
}

func TestNewTransportWithServiceNameAgentUnixSocketConfiguresHTTPTransport(t *testing.T) {
	socketPath := "/tmp/ddtest-agent.sock"
	t.Setenv(constants.TestOptimizationAgentlessEnabledEnvironmentVariable, "false")
	t.Setenv("DD_TRACE_AGENT_URL", "unix://"+socketPath)
	t.Setenv("DD_AGENT_HOST", "")
	t.Setenv("DD_TRACE_AGENT_PORT", "")

	apiTransport := NewTransportWithServiceName("my-service")
	client, ok := apiTransport.(*transport)
	if !ok {
		t.Fatalf("NewTransportWithServiceName() returned %T", apiTransport)
	}
	if client.baseURL != "http://UDS__tmp_ddtest-agent.sock" {
		t.Errorf("expected UDS base URL host, got %q", client.baseURL)
	}
	if client.handler == nil || client.handler.Client == nil {
		t.Fatal("expected HTTP client to be configured")
	}
	if _, ok := client.handler.Client.Transport.(*http.Transport); !ok {
		t.Fatalf("expected Unix socket HTTP transport, got %T", client.handler.Client.Transport)
	}
}
