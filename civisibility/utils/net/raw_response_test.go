package net

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newRawResponseTestClient(server *httptest.Server) *client {
	return &client{
		agentless:     true,
		baseURL:       server.URL,
		environment:   "ci",
		serviceName:   "service",
		repositoryURL: "https://github.com/DataDog/ddtest.git",
		commitSha:     "abc123",
		commitMessage: "commit message",
		branchName:    "main",
		headers:       map[string]string{},
		handler:       NewRequestHandlerWithClient(server.Client()),
		testConfigurations: testConfigurations{
			OsPlatform:     "linux",
			OsArchitecture: "amd64",
			RuntimeName:    "ruby",
			RuntimeVersion: "3.3.0",
		},
	}
}

func newRawResponseTestServer(t *testing.T, responses map[string]string) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST request, got %s", r.Method)
		}

		path := strings.TrimPrefix(r.URL.Path, "/")
		responseBody, ok := responses[path]
		if !ok {
			t.Fatalf("unexpected request path %s", path)
		}

		w.Header().Set(HeaderContentType, ContentTypeJSON)
		_, _ = w.Write([]byte(responseBody))
	}))
}

func TestClientStoresRawBackendResponses(t *testing.T) {
	settingsResponse := `{"data":{"id":"settings-id","type":"ci_app_test_service_libraries_settings","attributes":{"itr_enabled":true,"tests_skipping":true,"known_tests_enabled":true,"test_management":{"enabled":true,"attempt_to_fix_retries":3}}}}`
	knownTestsResponse := `{"data":{"id":"known-tests-id","type":"ci_app_libraries_tests_request","attributes":{"tests":{"module-a":{"suite-a":["test-a"]}}}}}`
	skippableTestsResponse := `{"meta":{"correlation_id":"correlation-id"},"data":[{"id":"skippable-id","type":"test","attributes":{"suite":"suite-a","name":"test-a","parameters":"params","configurations":{"os.platform":"linux","os.architecture":"amd64","runtime.name":"ruby","runtime.version":"3.3.0"}}}]}`
	testManagementResponse := `{"data":{"id":"test-management-id","type":"ci_app_libraries_tests_request","attributes":{"modules":{"module-a":{"suites":{"suite-a":{"tests":{"test-a":{"properties":{"quarantined":true,"disabled":false,"attempt_to_fix":true}}}}}}}}}}`

	server := newRawResponseTestServer(t, map[string]string{
		settingsURLPath:            settingsResponse,
		knownTestsURLPath:          knownTestsResponse,
		skippableURLPath:           skippableTestsResponse,
		testManagementTestsURLPath: testManagementResponse,
	})
	defer server.Close()

	client := newRawResponseTestClient(server)

	settings, err := client.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings() returned error: %v", err)
	}
	if !settings.ItrEnabled || !settings.TestsSkipping {
		t.Fatalf("GetSettings() returned unexpected processed data: %+v", settings)
	}
	if string(client.GetSettingsRawResponse()) != settingsResponse {
		t.Fatalf("settings raw response mismatch:\nexpected: %s\nactual:   %s", settingsResponse, string(client.GetSettingsRawResponse()))
	}

	knownTests, err := client.GetKnownTests()
	if err != nil {
		t.Fatalf("GetKnownTests() returned error: %v", err)
	}
	if knownTests.Tests["module-a"]["suite-a"][0] != "test-a" {
		t.Fatalf("GetKnownTests() returned unexpected processed data: %+v", knownTests)
	}
	if string(client.GetKnownTestsRawResponse()) != knownTestsResponse {
		t.Fatalf("known tests raw response mismatch:\nexpected: %s\nactual:   %s", knownTestsResponse, string(client.GetKnownTestsRawResponse()))
	}

	correlationID, skippableTests, err := client.GetSkippableTests()
	if err != nil {
		t.Fatalf("GetSkippableTests() returned error: %v", err)
	}
	if correlationID != "correlation-id" || skippableTests["suite-a"]["test-a"][0].Parameters != "params" {
		t.Fatalf("GetSkippableTests() returned unexpected processed data: correlationID=%s skippableTests=%+v", correlationID, skippableTests)
	}
	if string(client.GetSkippableTestsRawResponse()) != skippableTestsResponse {
		t.Fatalf("skippable tests raw response mismatch:\nexpected: %s\nactual:   %s", skippableTestsResponse, string(client.GetSkippableTestsRawResponse()))
	}

	testManagement, err := client.GetTestManagementTests()
	if err != nil {
		t.Fatalf("GetTestManagementTests() returned error: %v", err)
	}
	testProperties := testManagement.Modules["module-a"].Suites["suite-a"].Tests["test-a"].Properties
	if !testProperties.Quarantined || !testProperties.AttemptToFix {
		t.Fatalf("GetTestManagementTests() returned unexpected processed data: %+v", testManagement)
	}
	if string(client.GetTestManagementTestsRawResponse()) != testManagementResponse {
		t.Fatalf("test management raw response mismatch:\nexpected: %s\nactual:   %s", testManagementResponse, string(client.GetTestManagementTestsRawResponse()))
	}
}

func TestClientRawResponseIsCloned(t *testing.T) {
	settingsResponse := `{"data":{"id":"settings-id","type":"ci_app_test_service_libraries_settings","attributes":{"itr_enabled":true}}}`
	server := newRawResponseTestServer(t, map[string]string{settingsURLPath: settingsResponse})
	defer server.Close()

	client := newRawResponseTestClient(server)
	if _, err := client.GetSettings(); err != nil {
		t.Fatalf("GetSettings() returned error: %v", err)
	}

	rawResponse := client.GetSettingsRawResponse()
	rawResponse[0] = '['

	if string(client.GetSettingsRawResponse()) != settingsResponse {
		t.Fatalf("raw response getter should return a defensive copy")
	}
}

func TestClientSettingsRawResponseUsesLatestResponse(t *testing.T) {
	firstSettingsResponse := `{"data":{"id":"first","type":"ci_app_test_service_libraries_settings","attributes":{"require_git":true}}}`
	secondSettingsResponse := `{"data":{"id":"second","type":"ci_app_test_service_libraries_settings","attributes":{"require_git":false}}}`
	settingsResponses := []string{firstSettingsResponse, secondSettingsResponse}
	requestCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path != settingsURLPath {
			t.Fatalf("unexpected request path %s", path)
		}
		w.Header().Set(HeaderContentType, ContentTypeJSON)
		_, _ = w.Write([]byte(settingsResponses[requestCount]))
		requestCount++
	}))
	defer server.Close()

	client := newRawResponseTestClient(server)
	if _, err := client.GetSettings(); err != nil {
		t.Fatalf("first GetSettings() returned error: %v", err)
	}
	if _, err := client.GetSettings(); err != nil {
		t.Fatalf("second GetSettings() returned error: %v", err)
	}

	if string(client.GetSettingsRawResponse()) != secondSettingsResponse {
		t.Fatalf("settings raw response should be replaced by the latest response")
	}
}
