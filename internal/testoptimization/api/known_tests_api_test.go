package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/DataDog/ddtest/internal/constants"
)

func TestClientGetKnownTestsPaginatesWithAttributesPageInfo(t *testing.T) {
	requests := []map[string]any{}
	responses := []string{
		`{"data":{"id":"known-tests-id","type":"ci_app_libraries_tests","attributes":{"tests":{"module-a":{"suite-a":["test-a"]}},"page_info":{"cursor":"page-2","size":1,"has_next":true}}}}`,
		`{"data":{"id":"known-tests-id","type":"ci_app_libraries_tests","attributes":{"tests":{"module-a":{"suite-a":["test-b"]},"module-b":{"suite-b":["test-c"]}},"page_info":{"cursor":"page-3","size":2,"has_next":true}}}}`,
		`{"data":{"id":"known-tests-id","type":"ci_app_libraries_tests","attributes":{"tests":{"module-a":{"suite-c":["test-d"]}},"page_info":{"size":1,"has_next":false}}}}`,
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST request, got %s", r.Method)
		}
		if r.URL.Path != constants.KnownTestsURLPath {
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

	if len(requests) != len(responses) {
		t.Fatalf("expected %d known-tests requests, got %d", len(responses), len(requests))
	}

	assertKnownTestsRequestPageInfo(t, requests[0], "")
	assertKnownTestsRequestPageInfo(t, requests[1], "page-2")
	assertKnownTestsRequestPageInfo(t, requests[2], "page-3")

	expectedTests := KnownTestsResponseDataModules{
		"module-a": {"suite-a": {"test-a", "test-b"}, "suite-c": {"test-d"}},
		"module-b": {"suite-b": {"test-c"}},
	}
	if !reflect.DeepEqual(knownTests.Tests, expectedTests) {
		t.Fatalf("expected all pages to be merged, got %#v", knownTests.Tests)
	}

	var rawResponse knownTestsResponse
	if err := json.Unmarshal(client.GetKnownTestsRawResponse(), &rawResponse); err != nil {
		t.Fatalf("failed to decode merged raw response: %v", err)
	}
	if !reflect.DeepEqual(rawResponse.Data.Attributes.Tests, expectedTests) {
		t.Fatalf("expected raw response to include all merged known tests, got %#v", rawResponse.Data.Attributes.Tests)
	}
}

func TestClientGetKnownTestsSinglePage(t *testing.T) {
	for _, tt := range []struct {
		name     string
		pageInfo string
	}{
		{name: "without pagination metadata"},
		{name: "final page", pageInfo: `,"page_info":{"size":1,"has_next":false}`},
		{name: "final page with cursor", pageInfo: `,"page_info":{"cursor":"unused","size":1,"has_next":false}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			response := `{"data":{"id":"known-tests-id","type":"ci_app_libraries_tests","attributes":{"tests":{"module-a":{"suite-a":["test-a"]}}` + tt.pageInfo + `}}}`
			var requests []map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request map[string]any
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Errorf("failed to decode request: %v", err)
					http.Error(w, "invalid request", http.StatusBadRequest)
					return
				}
				requests = append(requests, request)
				if len(requests) > 1 {
					t.Error("unexpected extra request for a single-page response")
					http.Error(w, "unexpected extra request", http.StatusBadRequest)
					return
				}
				w.Header().Set(HeaderContentType, constants.ContentTypeJSON)
				_, _ = w.Write([]byte(response))
			}))
			defer server.Close()

			client := newRawResponseTestClient(server)
			knownTests, err := client.GetKnownTests()
			if err != nil {
				t.Fatalf("GetKnownTests() returned error: %v", err)
			}
			if len(requests) != 1 {
				t.Fatalf("expected 1 known-tests request, got %d", len(requests))
			}
			assertKnownTestsRequestPageInfo(t, requests[0], "")
			expectedTests := KnownTestsResponseDataModules{"module-a": {"suite-a": {"test-a"}}}
			if !reflect.DeepEqual(knownTests.Tests, expectedTests) {
				t.Fatalf("unexpected known tests: %#v", knownTests.Tests)
			}
			if string(client.GetKnownTestsRawResponse()) != response {
				t.Fatalf("single-page raw response should be preserved unchanged, got %s", client.GetKnownTestsRawResponse())
			}
		})
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

func assertKnownTestsRequestPageInfo(t *testing.T, request map[string]any, expectedPageState string) {
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
	if !ok {
		t.Fatalf("every request should include data.attributes.page_info as an object, got %#v", attributes["page_info"])
	}
	if expectedPageState == "" {
		if len(pageInfo) != 0 {
			t.Fatalf("first request should send an empty page_info object, got %#v", pageInfo)
		}
	} else if pageInfo["page_state"] != expectedPageState {
		t.Fatalf("expected page_state %q, got %#v", expectedPageState, pageInfo["page_state"])
	}
	if _, ok := pageInfo["page_size"]; ok {
		t.Fatalf("request should not send page_size: %#v", pageInfo)
	}
}
