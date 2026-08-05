package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/buildinfo"
	"github.com/DataDog/ddtest/internal/constants"
)

func clearTelemetryEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		constants.TestOptimizationAgentlessEnabledEnvironmentVariable,
		constants.TestOptimizationAgentlessURLEnvironmentVariable,
		constants.APIKeyEnvironmentVariable,
		"DD_SITE",
		"DD_SERVICE",
		"DD_ENV",
		"DD_TRACE_AGENT_URL",
		"DD_AGENT_HOST",
		"DD_TRACE_AGENT_PORT",
	} {
		t.Setenv(name, "")
	}
}

func setTelemetryVersion(t *testing.T, version string) {
	t.Helper()
	originalVersion := buildinfo.Version
	buildinfo.Version = version
	t.Cleanup(func() { buildinfo.Version = originalVersion })
}

func TestCreateTelemetryClientUsesCIVisibilityTestURL(t *testing.T) {
	clearTelemetryEnvironment(t)
	setTelemetryVersion(t, "2.3.4")

	type application struct {
		ServiceName    string `json:"service_name"`
		Environment    string `json:"env"`
		LibraryVersion string `json:"tracer_version"`
		LanguageName   string `json:"language_name"`
	}
	var received struct {
		path        string
		apiKey      string
		application application
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		received.path = request.URL.Path
		received.apiKey = request.Header.Get("DD-API-KEY")
		var requestBody struct {
			Application application `json:"application"`
		}
		if err := json.NewDecoder(request.Body).Decode(&requestBody); err != nil {
			t.Errorf("decode telemetry request: %v", err)
		}
		received.application = requestBody.Application
		response.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(server.Close)

	t.Setenv(constants.TestOptimizationAgentlessEnabledEnvironmentVariable, "true")
	t.Setenv(constants.TestOptimizationAgentlessURLEnvironmentVariable, server.URL)
	t.Setenv(constants.APIKeyEnvironmentVariable, "api-key")
	t.Setenv("DD_SERVICE", "checkout-service")
	t.Setenv("DD_ENV", "ci")

	client, err := createTelemetryClient()
	if err != nil {
		t.Fatalf("createTelemetryClient() error = %v", err)
	}
	client.Count("command", nil).Submit(1)
	if err := client.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	if received.path != telemetryAPIPath || received.apiKey != "api-key" {
		t.Errorf("request path/API key = %q/%q, want %q/api-key", received.path, received.apiKey, telemetryAPIPath)
	}
	if received.application.ServiceName != "checkout-service" || received.application.Environment != "ci" {
		t.Errorf("unexpected service metadata: %#v", received.application)
	}
	if received.application.LibraryVersion != "2.3.4" || received.application.LanguageName != "ddtest" {
		t.Errorf("unexpected library metadata: %#v", received.application)
	}
}

func TestCreateTelemetryClientUsesAgentTelemetryProxy(t *testing.T) {
	clearTelemetryEnvironment(t)
	setTelemetryVersion(t, "2.3.4")

	var receivedPath, receivedAPIKey string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		receivedPath = request.URL.Path
		receivedAPIKey = request.Header.Get("DD-API-KEY")
		response.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(server.Close)
	t.Setenv("DD_TRACE_AGENT_URL", server.URL)

	client, err := createTelemetryClient()
	if err != nil {
		t.Fatalf("createTelemetryClient() error = %v", err)
	}
	client.Count("command", nil).Submit(1)
	if err := client.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	if receivedPath != telemetryAgentPath {
		t.Errorf("request path = %q, want %q", receivedPath, telemetryAgentPath)
	}
	if receivedAPIKey != "" {
		t.Errorf("agent request API key = %q, want empty", receivedAPIKey)
	}
}

func TestCreateTelemetryClientAgentlessDefaults(t *testing.T) {
	clearTelemetryEnvironment(t)
	setTelemetryVersion(t, "2.3.4")
	t.Setenv(constants.TestOptimizationAgentlessEnabledEnvironmentVariable, "true")
	t.Setenv(constants.APIKeyEnvironmentVariable, "api-key")
	t.Setenv("DD_SITE", "datadoghq.eu")

	client, err := createTelemetryClient()
	if err != nil {
		t.Fatalf("createTelemetryClient() error = %v", err)
	}
	if client == nil {
		t.Fatal("createTelemetryClient() returned nil")
	}
}

func TestCreateTelemetryClientRequiresAgentlessAPIKey(t *testing.T) {
	clearTelemetryEnvironment(t)
	t.Setenv(constants.TestOptimizationAgentlessEnabledEnvironmentVariable, "true")

	_, err := createTelemetryClient()
	if err == nil || !strings.Contains(err.Error(), "API key must not be empty") {
		t.Fatalf("createTelemetryClient() error = %v, want missing API key error", err)
	}
}

func TestCreateTelemetryClientRejectsInvalidCIVisibilityTestURL(t *testing.T) {
	clearTelemetryEnvironment(t)
	setTelemetryVersion(t, "2.3.4")
	t.Setenv(constants.TestOptimizationAgentlessEnabledEnvironmentVariable, "true")
	t.Setenv(constants.APIKeyEnvironmentVariable, "api-key")
	t.Setenv(constants.TestOptimizationAgentlessURLEnvironmentVariable, "://bad")

	_, err := createTelemetryClient()
	if err == nil {
		t.Fatal("createTelemetryClient() error = nil, want invalid URL error")
	}
}
