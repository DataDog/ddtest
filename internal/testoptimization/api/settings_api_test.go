package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTransportGetSettingsRequiresRepositoryAndCommit(t *testing.T) {
	if _, err := (&transport{}).GetSettings(); err == nil {
		t.Fatal("expected settings repository/sha error")
	}
}

func TestTransportGetSettingsRequestAndResponse(t *testing.T) {
	var captured settingsRequest
	expectedResponse := settingsResponse{}
	expectedResponse.Data.Type = settingsRequestType
	expectedResponse.Data.Attributes.CodeCoverage = true
	expectedResponse.Data.Attributes.EarlyFlakeDetection.Enabled = true
	expectedResponse.Data.Attributes.EarlyFlakeDetection.FaultySessionThreshold = 30
	expectedResponse.Data.Attributes.EarlyFlakeDetection.SlowTestRetries.FiveS = 25
	expectedResponse.Data.Attributes.EarlyFlakeDetection.SlowTestRetries.TenS = 20
	expectedResponse.Data.Attributes.EarlyFlakeDetection.SlowTestRetries.ThirtyS = 10
	expectedResponse.Data.Attributes.EarlyFlakeDetection.SlowTestRetries.FiveM = 5
	expectedResponse.Data.Attributes.FlakyTestRetriesEnabled = true
	expectedResponse.Data.Attributes.ItrEnabled = true
	expectedResponse.Data.Attributes.RequireGit = true
	expectedResponse.Data.Attributes.TestsSkipping = true
	expectedResponse.Data.Attributes.KnownTestsEnabled = true
	expectedResponse.Data.Attributes.TestManagement.Enabled = true
	expectedResponse.Data.Attributes.TestManagement.AttemptToFixRetries = 3

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST request, got %s", r.Method)
		}
		if r.URL.Path != "/"+settingsURLPath {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		expectedResponse.Data.ID = captured.Data.ID
		w.Header().Set(HeaderContentType, ContentTypeJSON)
		_ = json.NewEncoder(w).Encode(expectedResponse)
	}))
	defer server.Close()

	client := newRawResponseTestClient(server)
	settings, err := client.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings() returned error: %v", err)
	}

	if captured.Data.ID != client.id {
		t.Fatalf("request id = %q, want %q", captured.Data.ID, client.id)
	}
	if captured.Data.Type != settingsRequestType {
		t.Fatalf("request type = %q, want %q", captured.Data.Type, settingsRequestType)
	}
	attributes := captured.Data.Attributes
	if attributes.Service != client.serviceName || attributes.Env != client.environment {
		t.Fatalf("service/env = %q/%q, want %q/%q", attributes.Service, attributes.Env, client.serviceName, client.environment)
	}
	if attributes.RepositoryURL != client.repositoryURL || attributes.Branch != client.branchName || attributes.Sha != client.commitSha {
		t.Fatalf("repository/branch/sha = %q/%q/%q, want %q/%q/%q",
			attributes.RepositoryURL, attributes.Branch, attributes.Sha,
			client.repositoryURL, client.branchName, client.commitSha)
	}
	if attributes.Configurations.OsPlatform != client.testConfigurations.OsPlatform ||
		attributes.Configurations.OsArchitecture != client.testConfigurations.OsArchitecture ||
		attributes.Configurations.RuntimeName != client.testConfigurations.RuntimeName ||
		attributes.Configurations.RuntimeVersion != client.testConfigurations.RuntimeVersion {
		t.Fatalf("configurations = %#v, want %#v", attributes.Configurations, client.testConfigurations)
	}

	if *settings != expectedResponse.Data.Attributes {
		t.Fatalf("settings = %#v, want %#v", *settings, expectedResponse.Data.Attributes)
	}
	if len(client.GetSettingsRawResponse()) == 0 {
		t.Fatal("expected raw settings response to be stored")
	}
}

func TestTransportGetSettingsErrors(t *testing.T) {
	t.Run("request failure", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "backend error", http.StatusInternalServerError)
		}))
		defer server.Close()

		settings, err := newRawResponseTestClient(server).GetSettings()
		if settings != nil {
			t.Fatalf("expected nil settings, got %#v", settings)
		}
		if err == nil || !strings.Contains(err.Error(), "sending get settings request") {
			t.Fatalf("expected request error, got %v", err)
		}
	})

	t.Run("unmarshal failure", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set(HeaderContentType, ContentTypeJSON)
			_, _ = w.Write([]byte(`{"data":`))
		}))
		defer server.Close()

		settings, err := newRawResponseTestClient(server).GetSettings()
		if settings != nil {
			t.Fatalf("expected nil settings, got %#v", settings)
		}
		if err == nil || !strings.Contains(err.Error(), "unmarshalling settings response") {
			t.Fatalf("expected unmarshal error, got %v", err)
		}
	})
}
