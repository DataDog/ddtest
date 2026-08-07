package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestCreateTelemetryClientUsesRunMetadata(t *testing.T) {
	clearTelemetryEnvironment(t)
	setTelemetryVersion(t, "2.3.4")

	type application struct {
		ServiceName    string `json:"service_name"`
		Environment    string `json:"env"`
		LibraryVersion string `json:"tracer_version"`
		LanguageName   string `json:"language_name"`
	}
	var received application
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var requestBody struct {
			Application application `json:"application"`
		}
		if err := json.NewDecoder(request.Body).Decode(&requestBody); err != nil {
			t.Errorf("decode telemetry request: %v", err)
		}
		received = requestBody.Application
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

	if received.ServiceName != "checkout-service" || received.Environment != "ci" {
		t.Errorf("unexpected service metadata: %#v", received)
	}
	if received.LibraryVersion != "2.3.4" || received.LanguageName != "ddtest" {
		t.Errorf("unexpected library metadata: %#v", received)
	}
}
