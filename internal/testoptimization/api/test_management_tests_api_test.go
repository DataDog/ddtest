package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/constants"
)

func TestTransportGetTestManagementTestsRequiresRepository(t *testing.T) {
	if _, err := (&transport{}).GetTestManagementTests(); err == nil {
		t.Fatal("expected test management repository error")
	}
}

func TestTransportGetTestManagementTestsRequestAndResponse(t *testing.T) {
	var captured testManagementTestsRequest
	expectedResponse := testManagementTestsResponse{}
	expectedResponse.Data.Type = testManagementTestsRequestType
	expectedResponse.Data.Attributes.Modules = map[string]TestManagementTestsResponseDataSuites{
		"module-a": {
			Suites: map[string]TestManagementTestsResponseDataTests{
				"suite-a": {
					Tests: map[string]TestManagementTestsResponseDataTestProperties{
						"test-a": {
							Properties: TestManagementTestsResponseDataTestPropertiesAttributes{
								Quarantined:  true,
								Disabled:     false,
								AttemptToFix: true,
							},
						},
						"test-b": {
							Properties: TestManagementTestsResponseDataTestPropertiesAttributes{
								Quarantined: false,
								Disabled:    true,
							},
						},
					},
				},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST request, got %s", r.Method)
		}
		if r.URL.Path != "/"+testManagementTestsURLPath {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		expectedResponse.Data.ID = captured.Data.ID
		w.Header().Set(HeaderContentType, constants.ContentTypeJSON)
		_ = json.NewEncoder(w).Encode(expectedResponse)
	}))
	defer server.Close()

	client := newRawResponseTestClient(server)
	client.headCommitSha = "head-sha"
	client.headCommitMessage = "head commit message"

	tests, err := client.GetTestManagementTests()
	if err != nil {
		t.Fatalf("GetTestManagementTests() returned error: %v", err)
	}

	if captured.Data.ID != client.id {
		t.Fatalf("request id = %q, want %q", captured.Data.ID, client.id)
	}
	if captured.Data.Type != testManagementTestsRequestType {
		t.Fatalf("request type = %q, want %q", captured.Data.Type, testManagementTestsRequestType)
	}
	attributes := captured.Data.Attributes
	if attributes.RepositoryURL != client.repositoryURL || attributes.Branch != client.branchName {
		t.Fatalf("repository/branch = %q/%q, want %q/%q",
			attributes.RepositoryURL, attributes.Branch, client.repositoryURL, client.branchName)
	}
	if attributes.CommitSha != client.headCommitSha {
		t.Fatalf("commit sha = %q, want head commit sha %q", attributes.CommitSha, client.headCommitSha)
	}
	if attributes.CommitMessage != client.headCommitMessage {
		t.Fatalf("commit message = %q, want head commit message %q", attributes.CommitMessage, client.headCommitMessage)
	}

	testA := tests.Modules["module-a"].Suites["suite-a"].Tests["test-a"].Properties
	if !testA.Quarantined || !testA.AttemptToFix || testA.Disabled {
		t.Fatalf("unexpected test-a properties: %#v", testA)
	}
	testB := tests.Modules["module-a"].Suites["suite-a"].Tests["test-b"].Properties
	if !testB.Disabled || testB.Quarantined || testB.AttemptToFix {
		t.Fatalf("unexpected test-b properties: %#v", testB)
	}
	if len(client.GetTestManagementTestsRawResponse()) == 0 {
		t.Fatal("expected raw test management response to be stored")
	}
}

func TestTransportGetTestManagementTestsUsesCommitWhenHeadCommitIsMissing(t *testing.T) {
	var captured testManagementTestsRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set(HeaderContentType, constants.ContentTypeJSON)
		_, _ = w.Write([]byte(`{"data":{"attributes":{"modules":{}}}}`))
	}))
	defer server.Close()

	client := newRawResponseTestClient(server)
	if _, err := client.GetTestManagementTests(); err != nil {
		t.Fatalf("GetTestManagementTests() returned error: %v", err)
	}
	if captured.Data.Attributes.CommitSha != client.commitSha {
		t.Fatalf("commit sha = %q, want %q", captured.Data.Attributes.CommitSha, client.commitSha)
	}
	if captured.Data.Attributes.CommitMessage != client.commitMessage {
		t.Fatalf("commit message = %q, want %q", captured.Data.Attributes.CommitMessage, client.commitMessage)
	}
}

func TestTransportGetTestManagementTestsErrors(t *testing.T) {
	t.Run("unmarshal failure", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set(HeaderContentType, constants.ContentTypeJSON)
			_, _ = w.Write([]byte(`{"data":`))
		}))
		defer server.Close()

		tests, err := newRawResponseTestClient(server).GetTestManagementTests()
		if tests != nil {
			t.Fatalf("expected nil test management tests, got %#v", tests)
		}
		if err == nil || !strings.Contains(err.Error(), "unmarshalling test management tests response") {
			t.Fatalf("expected unmarshal error, got %v", err)
		}
	})

	t.Run("request failure", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "backend error", http.StatusInternalServerError)
		}))
		defer server.Close()

		tests, err := newRawResponseTestClient(server).GetTestManagementTests()
		if tests != nil {
			t.Fatalf("expected nil test management tests, got %#v", tests)
		}
		if err == nil || !strings.Contains(err.Error(), "sending test management tests request") {
			t.Fatalf("expected request error, got %v", err)
		}
	})
}
