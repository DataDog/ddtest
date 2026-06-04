package testoptimization

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/civisibility/constants"
	ciUtils "github.com/DataDog/ddtest/civisibility/utils"
)

// MockDurationsAPI implements DurationsAPI for testing (equivalent of MockCIVisibilityIntegrations)
type MockDurationsAPI struct {
	FetchCalled    bool
	RepositoryURL  string
	Service        string
	Cursors        []string
	Responses      []*durationsResponseAttributes
	ResponseErrors []error
	callIndex      int
}

func (m *MockDurationsAPI) FetchTestSuiteDurations(repositoryURL, service, cursor string, pageSize int) (*durationsResponseAttributes, error) {
	m.FetchCalled = true
	m.RepositoryURL = repositoryURL
	m.Service = service
	m.Cursors = append(m.Cursors, cursor)

	if m.callIndex < len(m.ResponseErrors) && m.ResponseErrors[m.callIndex] != nil {
		err := m.ResponseErrors[m.callIndex]
		m.callIndex++
		return nil, err
	}

	if m.callIndex < len(m.Responses) {
		resp := m.Responses[m.callIndex]
		m.callIndex++
		return resp, nil
	}

	return &durationsResponseAttributes{
		TestSuites: make(map[string]map[string]TestSuiteDurationInfo),
	}, nil
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

func addRepositoryTag(t *testing.T, repositoryURL string) {
	t.Helper()
	ciUtils.ResetCITags()
	t.Cleanup(ciUtils.ResetCITags)
	ciUtils.AddCITagsMap(map[string]string{constants.GitRepositoryURL: repositoryURL})
}

func TestNewDurationsClientWithDependencies(t *testing.T) {
	mockAPI := &MockDurationsAPI{}
	client := NewDurationsClientWithDependencies(mockAPI)

	if client == nil {
		t.Error("NewDurationsClientWithDependencies() should return non-nil client")
	}
}

func TestDurationsClient_GetTestSuiteDurations_DerivesInputsAndLogsSuccess(t *testing.T) {
	addRepositoryTag(t, "github.com/DataDog/foo")
	t.Setenv("DD_SERVICE", "my-service")
	logs := captureLogs(t)
	mockAPI := &MockDurationsAPI{
		Responses: []*durationsResponseAttributes{
			{
				TestSuites: map[string]map[string]TestSuiteDurationInfo{
					"module1": {
						"suite1": {
							SourceFile: "spec/user_spec.rb",
							Duration:   DurationPercentiles{P50: "280000000", P90: "350000000"},
						},
					},
				},
			},
		},
	}

	client := NewDurationsClientWithDependencies(mockAPI)
	result := client.GetTestSuiteDurations()

	if !mockAPI.FetchCalled {
		t.Error("GetTestSuiteDurations() should call FetchTestSuiteDurations")
	}
	if mockAPI.RepositoryURL != "github.com/DataDog/foo" {
		t.Errorf("Expected repository URL 'github.com/DataDog/foo', got '%s'", mockAPI.RepositoryURL)
	}
	if mockAPI.Service != "my-service" {
		t.Errorf("Expected service 'my-service', got '%s'", mockAPI.Service)
	}
	if len(result) != 1 {
		t.Errorf("Expected 1 module, got %d", len(result))
	}
	if !strings.Contains(logs.String(), "level=INFO") ||
		!strings.Contains(logs.String(), "Fetched test suite durations") ||
		!strings.Contains(logs.String(), "modulesCount=1") ||
		!strings.Contains(logs.String(), "testSuitesCount=1") ||
		!strings.Contains(logs.String(), "duration=") {
		t.Errorf("Expected INFO log for non-empty durations response, got logs: %s", logs.String())
	}
}

func TestDurationsClient_GetTestSuiteDurations_MissingRepositoryReturnsEmptyAndLogsError(t *testing.T) {
	ciUtils.ResetCITags()
	t.Cleanup(ciUtils.ResetCITags)
	ciUtils.AddCITagsMap(map[string]string{constants.GitRepositoryURL: ""})
	logs := captureLogs(t)
	mockAPI := &MockDurationsAPI{}

	client := NewDurationsClientWithDependencies(mockAPI)
	result := client.GetTestSuiteDurations()

	if mockAPI.FetchCalled {
		t.Error("GetTestSuiteDurations() should not fetch without a repository URL")
	}
	if len(result) != 0 {
		t.Errorf("Expected empty durations on missing repository URL, got %v", result)
	}
	if !strings.Contains(logs.String(), "level=ERROR") ||
		!strings.Contains(logs.String(), "Test durations API errored") ||
		!strings.Contains(logs.String(), "repository URL is required") ||
		!strings.Contains(logs.String(), "duration=") {
		t.Errorf("Expected ERROR log for missing repository URL, got logs: %s", logs.String())
	}
}

func TestDurationsClient_GetTestSuiteDurations_APIErrorReturnsEmptyAndLogsError(t *testing.T) {
	addRepositoryTag(t, "github.com/DataDog/foo")
	t.Setenv("DD_SERVICE", "")
	logs := captureLogs(t)
	mockAPI := &MockDurationsAPI{
		ResponseErrors: []error{fmt.Errorf("connection refused")},
	}

	client := NewDurationsClientWithDependencies(mockAPI)
	result := client.GetTestSuiteDurations()

	if len(result) != 0 {
		t.Errorf("Expected empty durations on API error, got %v", result)
	}
	if !strings.Contains(logs.String(), "level=ERROR") ||
		!strings.Contains(logs.String(), "Test durations API errored") ||
		!strings.Contains(logs.String(), "repositoryURL=github.com/DataDog/foo") ||
		!strings.Contains(logs.String(), "service=foo") ||
		!strings.Contains(logs.String(), "connection refused") ||
		!strings.Contains(logs.String(), "duration=") {
		t.Errorf("Expected ERROR log for durations API failure, got logs: %s", logs.String())
	}
}

func TestDurationsClient_GetTestSuiteDurations_EmptyResponseReturnsEmptyAndLogsWarn(t *testing.T) {
	addRepositoryTag(t, "github.com/DataDog/foo")
	t.Setenv("DD_SERVICE", "")
	logs := captureLogs(t)
	mockAPI := &MockDurationsAPI{
		Responses: []*durationsResponseAttributes{
			{
				TestSuites: map[string]map[string]TestSuiteDurationInfo{},
			},
		},
	}

	client := NewDurationsClientWithDependencies(mockAPI)
	result := client.GetTestSuiteDurations()

	if len(result) != 0 {
		t.Errorf("Expected empty durations on empty response, got %v", result)
	}
	if !strings.Contains(logs.String(), "level=WARN") ||
		!strings.Contains(logs.String(), "Test durations API returned no test suites") ||
		!strings.Contains(logs.String(), "modulesCount=0") ||
		!strings.Contains(logs.String(), "testSuitesCount=0") ||
		!strings.Contains(logs.String(), "duration=") {
		t.Errorf("Expected WARN log for empty durations response, got logs: %s", logs.String())
	}
}

func TestDurationsClient_FetchTestSuiteDurations_SinglePage(t *testing.T) {
	mockAPI := &MockDurationsAPI{
		Responses: []*durationsResponseAttributes{
			{
				TestSuites: map[string]map[string]TestSuiteDurationInfo{
					"module1": {
						"suite1": {
							SourceFile: "spec/user_spec.rb",
							Duration:   DurationPercentiles{P50: "280000000", P90: "350000000"},
						},
						"suite2": {
							SourceFile: "spec/order_spec.rb",
							Duration:   DurationPercentiles{P50: "100000000", P90: "150000000"},
						},
					},
					"module2": {
						"suite3": {
							SourceFile: "spec/product_spec.rb",
							Duration:   DurationPercentiles{P50: "500000000", P90: "600000000"},
						},
					},
				},
			},
		},
	}

	client := NewDurationsClientWithDependencies(mockAPI)
	result, err := client.fetchTestSuiteDurations("github.com/DataDog/foo", "my-service")

	if err != nil {
		t.Errorf("fetchTestSuiteDurations() should not return error, got: %v", err)
	}

	if !mockAPI.FetchCalled {
		t.Error("fetchTestSuiteDurations() should call FetchTestSuiteDurations")
	}

	if mockAPI.RepositoryURL != "github.com/DataDog/foo" {
		t.Errorf("Expected repository URL 'github.com/DataDog/foo', got '%s'", mockAPI.RepositoryURL)
	}

	if mockAPI.Service != "my-service" {
		t.Errorf("Expected service 'my-service', got '%s'", mockAPI.Service)
	}

	if len(result) != 2 {
		t.Errorf("Expected 2 modules, got %d", len(result))
	}

	module1, exists := result["module1"]
	if !exists {
		t.Error("Expected module1 to exist")
		return
	}

	if len(module1) != 2 {
		t.Errorf("Expected 2 suites in module1, got %d", len(module1))
	}

	suite1, exists := module1["suite1"]
	if !exists {
		t.Error("Expected suite1 to exist in module1")
		return
	}

	if suite1.SourceFile != "spec/user_spec.rb" {
		t.Errorf("Expected source file 'spec/user_spec.rb', got '%s'", suite1.SourceFile)
	}
	if suite1.Duration.P50 != "280000000" {
		t.Errorf("Expected P50 '280000000', got '%s'", suite1.Duration.P50)
	}
	if suite1.Duration.P90 != "350000000" {
		t.Errorf("Expected P90 '350000000', got '%s'", suite1.Duration.P90)
	}

	module2, exists := result["module2"]
	if !exists {
		t.Error("Expected module2 to exist")
		return
	}

	if len(module2) != 1 {
		t.Errorf("Expected 1 suite in module2, got %d", len(module2))
	}
}

func TestDurationsClient_FetchTestSuiteDurations_Pagination(t *testing.T) {
	mockAPI := &MockDurationsAPI{
		Responses: []*durationsResponseAttributes{
			{
				TestSuites: map[string]map[string]TestSuiteDurationInfo{
					"module1": {
						"suite1": {
							SourceFile: "spec/user_spec.rb",
							Duration:   DurationPercentiles{P50: "280000000", P90: "350000000"},
						},
					},
				},
				PageInfo: &durationsResponsePageInfo{
					Cursor:  "abc123",
					Size:    500,
					HasNext: true,
				},
			},
			{
				TestSuites: map[string]map[string]TestSuiteDurationInfo{
					"module1": {
						"suite2": {
							SourceFile: "spec/order_spec.rb",
							Duration:   DurationPercentiles{P50: "100000000", P90: "150000000"},
						},
					},
					"module2": {
						"suite3": {
							SourceFile: "spec/product_spec.rb",
							Duration:   DurationPercentiles{P50: "500000000", P90: "600000000"},
						},
					},
				},
				PageInfo: &durationsResponsePageInfo{
					Cursor:  "",
					Size:    500,
					HasNext: false,
				},
			},
		},
	}

	client := NewDurationsClientWithDependencies(mockAPI)
	result, err := client.fetchTestSuiteDurations("github.com/DataDog/foo", "my-service")

	if err != nil {
		t.Errorf("fetchTestSuiteDurations() should not return error, got: %v", err)
	}

	// Verify pagination cursors were passed correctly
	if len(mockAPI.Cursors) != 2 {
		t.Errorf("Expected 2 API calls, got %d", len(mockAPI.Cursors))
	}

	if mockAPI.Cursors[0] != "" {
		t.Errorf("First call should have empty cursor, got '%s'", mockAPI.Cursors[0])
	}

	if mockAPI.Cursors[1] != "abc123" {
		t.Errorf("Second call should have cursor 'abc123', got '%s'", mockAPI.Cursors[1])
	}

	// Verify merged results
	if len(result) != 2 {
		t.Errorf("Expected 2 modules, got %d", len(result))
	}

	module1, exists := result["module1"]
	if !exists {
		t.Error("Expected module1 to exist")
		return
	}

	if len(module1) != 2 {
		t.Errorf("Expected 2 suites in module1 (merged from both pages), got %d", len(module1))
	}

	if _, exists := module1["suite1"]; !exists {
		t.Error("Expected suite1 to exist in module1 (from page 1)")
	}
	if _, exists := module1["suite2"]; !exists {
		t.Error("Expected suite2 to exist in module1 (from page 2)")
	}

	module2, exists := result["module2"]
	if !exists {
		t.Error("Expected module2 to exist")
		return
	}

	if len(module2) != 1 {
		t.Errorf("Expected 1 suite in module2, got %d", len(module2))
	}
}

func TestDurationsClient_FetchTestSuiteDurations_EmptyResponse(t *testing.T) {
	mockAPI := &MockDurationsAPI{
		Responses: []*durationsResponseAttributes{
			{
				TestSuites: map[string]map[string]TestSuiteDurationInfo{},
			},
		},
	}

	client := NewDurationsClientWithDependencies(mockAPI)
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

func TestDurationsClient_FetchTestSuiteDurations_NilTestSuites(t *testing.T) {
	mockAPI := &MockDurationsAPI{
		Responses: []*durationsResponseAttributes{
			{
				TestSuites: nil,
			},
		},
	}

	client := NewDurationsClientWithDependencies(mockAPI)
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

func TestDurationsClient_FetchTestSuiteDurations_APIError(t *testing.T) {
	mockAPI := &MockDurationsAPI{
		ResponseErrors: []error{fmt.Errorf("connection refused")},
	}

	client := NewDurationsClientWithDependencies(mockAPI)
	result, err := client.fetchTestSuiteDurations("github.com/DataDog/foo", "my-service")

	if err == nil {
		t.Error("fetchTestSuiteDurations() should return error when API fails")
	}

	if result != nil {
		t.Error("fetchTestSuiteDurations() should return nil result when API fails")
	}
}

func TestDurationsClient_FetchTestSuiteDurations_PaginationError(t *testing.T) {
	mockAPI := &MockDurationsAPI{
		Responses: []*durationsResponseAttributes{
			{
				TestSuites: map[string]map[string]TestSuiteDurationInfo{
					"module1": {
						"suite1": {
							SourceFile: "spec/user_spec.rb",
							Duration:   DurationPercentiles{P50: "280000000", P90: "350000000"},
						},
					},
				},
				PageInfo: &durationsResponsePageInfo{
					Cursor:  "abc123",
					Size:    500,
					HasNext: true,
				},
			},
		},
		ResponseErrors: []error{nil, fmt.Errorf("timeout on second page")},
	}

	client := NewDurationsClientWithDependencies(mockAPI)
	result, err := client.fetchTestSuiteDurations("github.com/DataDog/foo", "my-service")

	if err == nil {
		t.Error("fetchTestSuiteDurations() should return error when pagination fails")
	}

	if result != nil {
		t.Error("fetchTestSuiteDurations() should return nil result when pagination fails")
	}
}

func TestDurationsClient_FetchTestSuiteDurations_NilPageInfo(t *testing.T) {
	mockAPI := &MockDurationsAPI{
		Responses: []*durationsResponseAttributes{
			{
				TestSuites: map[string]map[string]TestSuiteDurationInfo{
					"module1": {
						"suite1": {
							SourceFile: "spec/user_spec.rb",
							Duration:   DurationPercentiles{P50: "280000000", P90: "350000000"},
						},
					},
				},
				PageInfo: nil,
			},
		},
	}

	client := NewDurationsClientWithDependencies(mockAPI)
	result, err := client.fetchTestSuiteDurations("github.com/DataDog/foo", "my-service")

	if err != nil {
		t.Errorf("fetchTestSuiteDurations() should not return error, got: %v", err)
	}

	if len(result) != 1 {
		t.Errorf("Expected 1 module, got %d", len(result))
	}

	// Should only make one API call (no pagination)
	if len(mockAPI.Cursors) != 1 {
		t.Errorf("Expected 1 API call when PageInfo is nil, got %d", len(mockAPI.Cursors))
	}
}

func TestDurationsClient_FetchTestSuiteDurations_ThreePages(t *testing.T) {
	mockAPI := &MockDurationsAPI{
		Responses: []*durationsResponseAttributes{
			{
				TestSuites: map[string]map[string]TestSuiteDurationInfo{
					"module1": {
						"suite1": {
							SourceFile: "spec/a_spec.rb",
							Duration:   DurationPercentiles{P50: "100", P90: "200"},
						},
					},
				},
				PageInfo: &durationsResponsePageInfo{Cursor: "page2", HasNext: true},
			},
			{
				TestSuites: map[string]map[string]TestSuiteDurationInfo{
					"module1": {
						"suite2": {
							SourceFile: "spec/b_spec.rb",
							Duration:   DurationPercentiles{P50: "300", P90: "400"},
						},
					},
				},
				PageInfo: &durationsResponsePageInfo{Cursor: "page3", HasNext: true},
			},
			{
				TestSuites: map[string]map[string]TestSuiteDurationInfo{
					"module1": {
						"suite3": {
							SourceFile: "spec/c_spec.rb",
							Duration:   DurationPercentiles{P50: "500", P90: "600"},
						},
					},
				},
				PageInfo: &durationsResponsePageInfo{HasNext: false},
			},
		},
	}

	client := NewDurationsClientWithDependencies(mockAPI)
	result, err := client.fetchTestSuiteDurations("github.com/DataDog/foo", "my-service")

	if err != nil {
		t.Errorf("fetchTestSuiteDurations() should not return error, got: %v", err)
	}

	if len(mockAPI.Cursors) != 3 {
		t.Errorf("Expected 3 API calls, got %d", len(mockAPI.Cursors))
	}

	if mockAPI.Cursors[0] != "" {
		t.Errorf("First cursor should be empty, got '%s'", mockAPI.Cursors[0])
	}
	if mockAPI.Cursors[1] != "page2" {
		t.Errorf("Second cursor should be 'page2', got '%s'", mockAPI.Cursors[1])
	}
	if mockAPI.Cursors[2] != "page3" {
		t.Errorf("Third cursor should be 'page3', got '%s'", mockAPI.Cursors[2])
	}

	module1 := result["module1"]
	if len(module1) != 3 {
		t.Errorf("Expected 3 suites merged in module1, got %d", len(module1))
	}
}

func TestDatadogDurationsAPI_FetchTestSuiteDurations_EmptyRepositoryURL(t *testing.T) {
	api := &DatadogDurationsAPI{
		baseURL: "https://api.datadoghq.com",
		headers: map[string]string{"dd-api-key": "test-key"},
	}

	_, err := api.FetchTestSuiteDurations("", "my-service", "", 100)

	if err == nil {
		t.Error("FetchTestSuiteDurations() should return error when repository URL is empty")
	}
}

func TestNewDatadogDurationsAPI_AgentlessMissingAPIKeyReturnsErroringClient(t *testing.T) {
	t.Setenv(constants.CIVisibilityAgentlessEnabledEnvironmentVariable, "true")
	t.Setenv(constants.APIKeyEnvironmentVariable, "")

	api := NewDatadogDurationsAPI()
	if api == nil {
		t.Fatal("NewDatadogDurationsAPI() should return an erroring client, not nil")
	}

	_, err := api.FetchTestSuiteDurations("github.com/DataDog/foo", "my-service", "", 100)
	if err == nil {
		t.Fatal("FetchTestSuiteDurations() should return an error when API key is missing")
	}
	if !strings.Contains(err.Error(), constants.APIKeyEnvironmentVariable) {
		t.Errorf("Expected error to mention missing API key, got %v", err)
	}
}

func TestNewDatadogDurationsAPI_AgentUnixSocketConfiguresHTTPTransport(t *testing.T) {
	socketPath := "/tmp/ddtest-agent.sock"
	t.Setenv(constants.CIVisibilityAgentlessEnabledEnvironmentVariable, "false")
	t.Setenv("DD_TRACE_AGENT_URL", "unix://"+socketPath)
	t.Setenv("DD_AGENT_HOST", "")
	t.Setenv("DD_TRACE_AGENT_PORT", "")
	t.Cleanup(func() {
		_ = os.Unsetenv("DD_TRACE_AGENT_URL")
	})

	api := NewDatadogDurationsAPI()
	if api == nil {
		t.Fatal("NewDatadogDurationsAPI() should return a client")
	}
	if api.baseURL != "http://UDS__tmp_ddtest-agent.sock" {
		t.Errorf("Expected UDS base URL host, got %q", api.baseURL)
	}
	if api.httpClient == nil {
		t.Fatal("Expected HTTP client to be configured")
	}
	if _, ok := api.httpClient.Transport.(*http.Transport); !ok {
		t.Fatalf("Expected Unix socket HTTP transport, got %T", api.httpClient.Transport)
	}
}
