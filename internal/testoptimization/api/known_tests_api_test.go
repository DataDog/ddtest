package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/constants"
)

func TestClientGetKnownTestsPaginatesWithAttributesPageInfo(t *testing.T) {
	requests := []map[string]any{}
	responses := []string{
		`{"data":{"id":"known-tests-id","type":"ci_app_libraries_tests","attributes":{"tests":{"module-a":{"suite-a":["test-a"]}},"page_info":{"cursor":"page-2","size":1,"has_next":true}}}}`,
		`{"data":{"id":"known-tests-id","type":"ci_app_libraries_tests","attributes":{"tests":{"module-a":{"suite-a":["test-b"]},"module-b":{"suite-b":["test-c"]}},"page_info":{"size":1,"has_next":false}}}}`,
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST request, got %s", r.Method)
		}
		if strings.TrimPrefix(r.URL.Path, "/") != knownTestsURLPath {
			t.Fatalf("unexpected request path %s", r.URL.Path)
		}
		if len(requests) >= len(responses) {
			t.Fatalf("unexpected extra request")
		}

		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		requests = append(requests, request)

		w.Header().Set(HeaderContentType, constants.ContentTypeJSON)
		_, _ = w.Write([]byte(responses[len(requests)-1]))
	}))
	defer server.Close()

	client := newRawResponseTestClient(server)
	knownTests, err := client.GetKnownTests()
	if err != nil {
		t.Fatalf("GetKnownTests() returned error: %v", err)
	}

	if len(requests) != 2 {
		t.Fatalf("expected 2 known-tests requests, got %d", len(requests))
	}

	assertKnownTestsRequestPageInfo(t, requests[0], "", false)
	assertKnownTestsRequestPageInfo(t, requests[1], "page-2", true)

	if tests := knownTests.Tests["module-a"]["suite-a"]; len(tests) != 2 || tests[0] != "test-a" || tests[1] != "test-b" {
		t.Fatalf("expected module-a/suite-a tests to be merged, got %#v", tests)
	}
	if tests := knownTests.Tests["module-b"]["suite-b"]; len(tests) != 1 || tests[0] != "test-c" {
		t.Fatalf("expected module-b/suite-b tests from page 2, got %#v", tests)
	}

	var rawResponse knownTestsResponse
	if err := json.Unmarshal(client.GetKnownTestsRawResponse(), &rawResponse); err != nil {
		t.Fatalf("failed to decode merged raw response: %v", err)
	}
	if tests := rawResponse.Data.Attributes.Tests["module-a"]["suite-a"]; len(tests) != 2 {
		t.Fatalf("expected raw response to include merged known tests, got %#v", tests)
	}
}

func TestClientGetKnownTestsDoesNotCachePartialRawResponseWhenFollowUpFails(t *testing.T) {
	responses := []string{
		`{"data":{"id":"known-tests-id","type":"ci_app_libraries_tests","attributes":{"tests":{"module-a":{"suite-a":["test-a"]}},"page_info":{"cursor":"page-2","size":1,"has_next":true}}}}`,
		`{"data":`,
	}
	requests := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests > len(responses) {
			t.Fatalf("unexpected extra request")
		}

		w.Header().Set(HeaderContentType, constants.ContentTypeJSON)
		_, _ = w.Write([]byte(responses[requests-1]))
	}))
	defer server.Close()

	client := newRawResponseTestClient(server)
	if _, err := client.GetKnownTests(); err == nil {
		t.Fatalf("GetKnownTests() should fail when a later page is invalid")
	}
	if rawResponse := client.GetKnownTestsRawResponse(); rawResponse != nil {
		t.Fatalf("known tests raw response should stay unset after partial pagination failure, got %s", string(rawResponse))
	}
}

func TestClientGetKnownTestsDoesNotCachePartialRawResponseWithoutFollowUpCursor(t *testing.T) {
	response := `{"data":{"id":"known-tests-id","type":"ci_app_libraries_tests","attributes":{"tests":{"module-a":{"suite-a":["test-a"]}},"page_info":{"size":1,"has_next":true}}}}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(HeaderContentType, constants.ContentTypeJSON)
		_, _ = w.Write([]byte(response))
	}))
	defer server.Close()

	client := newRawResponseTestClient(server)
	if _, err := client.GetKnownTests(); err == nil {
		t.Fatalf("GetKnownTests() should fail when a paginated response omits the follow-up cursor")
	}
	if rawResponse := client.GetKnownTestsRawResponse(); rawResponse != nil {
		t.Fatalf("known tests raw response should stay unset after missing cursor failure, got %s", string(rawResponse))
	}
}

func assertKnownTestsRequestPageInfo(t *testing.T, request map[string]any, expectedPageState string, expectPageInfo bool) {
	t.Helper()

	if _, ok := request["page_info"]; ok {
		t.Fatalf("request should not send top-level page_info: %#v", request)
	}

	data, ok := request["data"].(map[string]any)
	if !ok {
		t.Fatalf("request data has unexpected shape: %#v", request["data"])
	}
	attributes, ok := data["attributes"].(map[string]any)
	if !ok {
		t.Fatalf("request attributes have unexpected shape: %#v", data["attributes"])
	}

	pageInfo, ok := attributes["page_info"].(map[string]any)
	if !expectPageInfo {
		if ok {
			t.Fatalf("first request should let the backend choose pagination without page_info, got %#v", pageInfo)
		}
		return
	}
	if !ok {
		t.Fatalf("follow-up request should include data.attributes.page_info")
	}
	if pageInfo["page_state"] != expectedPageState {
		t.Fatalf("expected page_state %q, got %#v", expectedPageState, pageInfo["page_state"])
	}
	if _, ok := pageInfo["page_size"]; ok {
		t.Fatalf("request should not send page_size: %#v", pageInfo)
	}
}
