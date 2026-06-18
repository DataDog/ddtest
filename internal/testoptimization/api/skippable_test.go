package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTransportGetSkippableTestsRequestAndResponse(t *testing.T) {
	var captured skippableRequest
	response := skippableResponse{
		Meta: skippableResponseMeta{CorrelationID: "correlation-id"},
		Data: []skippableResponseData{
			{
				ID:   "id",
				Type: "test",
				Attributes: SkippableResponseDataAttributes{
					Suite:      "suite",
					Name:       "name",
					Parameters: "params",
					Configurations: testConfigurations{
						TestBundle:     "bundle",
						OsPlatform:     "linux",
						OsArchitecture: "amd64",
						RuntimeName:    "ruby",
						RuntimeVersion: "3.3.0",
					},
				},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST request, got %s", r.Method)
		}
		if r.URL.Path != "/"+skippableURLPath {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if !strings.Contains(r.Header.Get(HeaderContentType), ContentTypeJSON) {
			t.Fatalf("content type = %q, want JSON", r.Header.Get(HeaderContentType))
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		w.Header().Set(HeaderContentType, ContentTypeJSON)
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := newRawResponseTestClient(server)
	correlationID, skippable, err := client.GetSkippableTests()
	if err != nil {
		t.Fatalf("GetSkippableTests() returned error: %v", err)
	}

	if captured.Data.Type != skippableRequestType {
		t.Fatalf("request type = %q, want %q", captured.Data.Type, skippableRequestType)
	}
	attributes := captured.Data.Attributes
	if attributes.TestLevel != "test" {
		t.Fatalf("test level = %q, want test", attributes.TestLevel)
	}
	if attributes.Service != client.serviceName || attributes.Env != client.environment {
		t.Fatalf("service/env = %q/%q, want %q/%q", attributes.Service, attributes.Env, client.serviceName, client.environment)
	}
	if attributes.RepositoryURL != client.repositoryURL || attributes.Sha != client.commitSha {
		t.Fatalf("repository/sha = %q/%q, want %q/%q", attributes.RepositoryURL, attributes.Sha, client.repositoryURL, client.commitSha)
	}
	if attributes.Configurations.OsPlatform != client.testConfigurations.OsPlatform ||
		attributes.Configurations.OsArchitecture != client.testConfigurations.OsArchitecture ||
		attributes.Configurations.RuntimeName != client.testConfigurations.RuntimeName ||
		attributes.Configurations.RuntimeVersion != client.testConfigurations.RuntimeVersion {
		t.Fatalf("configurations = %#v, want %#v", attributes.Configurations, client.testConfigurations)
	}

	if correlationID != "correlation-id" {
		t.Fatalf("correlation ID = %q, want correlation-id", correlationID)
	}
	if len(skippable) != 1 || !skippable["bundle.suite.name.params"] {
		t.Fatalf("unexpected skippable map: %#v", skippable)
	}
	if len(client.GetSkippableTestsRawResponse()) == 0 {
		t.Fatal("expected raw skippable response to be stored")
	}
}

func TestTransportGetSkippableTestsErrors(t *testing.T) {
	if _, _, err := (&transport{}).GetSkippableTests(); err == nil {
		t.Fatal("expected repository/sha error")
	}

	t.Run("request failure", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "backend error", http.StatusInternalServerError)
		}))
		defer server.Close()

		_, _, err := newRawResponseTestClient(server).GetSkippableTests()
		if err == nil || !strings.Contains(err.Error(), "sending skippable tests request") {
			t.Fatalf("expected request error, got %v", err)
		}
	})

	t.Run("unmarshal failure", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set(HeaderContentType, ContentTypeJSON)
			_, _ = w.Write([]byte(`{"data":`))
		}))
		defer server.Close()

		_, _, err := newRawResponseTestClient(server).GetSkippableTests()
		if err == nil || !strings.Contains(err.Error(), "unmarshalling skippable tests response") {
			t.Fatalf("expected unmarshal error, got %v", err)
		}
	})
}

func TestTransportGetSkippableTestsFiltersConfigurations(t *testing.T) {
	response := `{"meta":{"correlation_id":"cid"},"data":[
		{"attributes":{"suite":"suite-a","name":"match","parameters":"","configurations":{"test.bundle":"rspec","os.platform":"linux","os.version":"ubuntu","os.architecture":"amd64","runtime.name":"ruby","runtime.architecture":"x86_64","runtime.version":"3.3.0"}}},
		{"attributes":{"suite":"suite-a","name":"no-config","parameters":"","configurations":{"test.bundle":"rspec"}}},
		{"attributes":{"suite":"suite-a","name":"wrong-os","parameters":"","configurations":{"test.bundle":"rspec","os.platform":"windows"}}},
		{"attributes":{"suite":"suite-a","name":"wrong-os-version","parameters":"","configurations":{"test.bundle":"rspec","os.version":"debian"}}},
		{"attributes":{"suite":"suite-a","name":"wrong-os-arch","parameters":"","configurations":{"test.bundle":"rspec","os.architecture":"arm64"}}},
		{"attributes":{"suite":"suite-a","name":"wrong-runtime","parameters":"","configurations":{"test.bundle":"rspec","runtime.name":"python"}}},
		{"attributes":{"suite":"suite-a","name":"wrong-runtime-arch","parameters":"","configurations":{"test.bundle":"rspec","runtime.architecture":"arm64"}}},
		{"attributes":{"suite":"suite-a","name":"wrong-runtime-version","parameters":"","configurations":{"test.bundle":"rspec","runtime.version":"3.2.0"}}}
	]}`
	server := newRawResponseTestServer(t, map[string]string{skippableURLPath: response})
	defer server.Close()

	client := newRawResponseTestClient(server)
	client.testConfigurations.OsVersion = "ubuntu"
	client.testConfigurations.RuntimeArchitecture = "x86_64"

	correlationID, skippable, err := client.GetSkippableTests()
	if err != nil {
		t.Fatalf("GetSkippableTests() returned error: %v", err)
	}
	if correlationID != "cid" {
		t.Fatalf("correlation ID = %q", correlationID)
	}
	expected := []string{
		"rspec.suite-a.match.",
		"rspec.suite-a.no-config.",
	}
	if len(skippable) != len(expected) {
		t.Fatalf("unexpected skippable map: %#v", skippable)
	}
	for _, key := range expected {
		if !skippable[key] {
			t.Fatalf("expected skippable key %q in %#v", key, skippable)
		}
	}
}

func TestSkippableTestKey(t *testing.T) {
	test := SkippableResponseDataAttributes{
		Suite:      "suite-a",
		Name:       "test-a",
		Parameters: "params",
		Configurations: testConfigurations{
			TestBundle: "rspec",
		},
	}
	if got, want := skippableTestKey(test), "rspec.suite-a.test-a.params"; got != want {
		t.Fatalf("skippableTestKey() = %q, want %q", got, want)
	}

	test.Configurations.TestBundle = ""
	if got, want := skippableTestKey(test), ".suite-a.test-a.params"; got != want {
		t.Fatalf("skippableTestKey() without bundle = %q, want %q", got, want)
	}
}
