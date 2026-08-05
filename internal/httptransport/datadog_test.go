package httptransport

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/constants"
)

func clearDatadogEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		constants.TestOptimizationAgentlessEnabledEnvironmentVariable,
		constants.TestOptimizationAgentlessURLEnvironmentVariable,
		constants.APIKeyEnvironmentVariable,
		"DD_SITE",
		"DD_TRACE_AGENT_URL",
		"DD_AGENT_HOST",
		"DD_TRACE_AGENT_PORT",
	} {
		t.Setenv(name, "")
	}
}

func TestResolveDatadogConnectionAgentless(t *testing.T) {
	clearDatadogEnvironment(t)
	t.Setenv(constants.TestOptimizationAgentlessEnabledEnvironmentVariable, "true")
	t.Setenv(constants.APIKeyEnvironmentVariable, "api-key")
	t.Setenv(constants.TestOptimizationAgentlessURLEnvironmentVariable, "https://tests.example")
	t.Setenv("DD_SITE", "datadoghq.eu")

	connection, err := ResolveDatadogConnection()
	if err != nil {
		t.Fatalf("ResolveDatadogConnection() error = %v", err)
	}
	if !connection.Agentless || connection.APIKey != "api-key" || connection.Site != "datadoghq.eu" {
		t.Fatalf("unexpected agentless connection: %#v", connection)
	}
	if connection.AgentlessURL != "https://tests.example" || connection.AgentURL != nil {
		t.Fatalf("unexpected agentless URLs: %#v", connection)
	}

	t.Setenv("DD_SITE", "")
	connection, err = ResolveDatadogConnection()
	if err != nil {
		t.Fatalf("ResolveDatadogConnection() with default site error = %v", err)
	}
	if connection.Site != "datadoghq.com" {
		t.Fatalf("default site = %q, want datadoghq.com", connection.Site)
	}
}

func TestResolveDatadogConnectionRequiresAgentlessAPIKey(t *testing.T) {
	clearDatadogEnvironment(t)
	t.Setenv(constants.TestOptimizationAgentlessEnabledEnvironmentVariable, "true")

	if _, err := ResolveDatadogConnection(); err == nil {
		t.Fatal("ResolveDatadogConnection() error = nil, want missing API key error")
	}
}

func TestAgentURLFromEnv(t *testing.T) {
	originalSocketPath := defaultAgentSocketPath
	t.Cleanup(func() { defaultAgentSocketPath = originalSocketPath })

	t.Run("defaults to localhost", func(t *testing.T) {
		clearDatadogEnvironment(t)
		defaultAgentSocketPath = filepath.Join(t.TempDir(), "missing.sock")
		if got := AgentURLFromEnv().String(); got != "http://localhost:8126" {
			t.Fatalf("AgentURLFromEnv() = %q, want http://localhost:8126", got)
		}
	})

	t.Run("uses explicit URL", func(t *testing.T) {
		clearDatadogEnvironment(t)
		t.Setenv("DD_TRACE_AGENT_URL", "https://agent.example:8127")
		if got := AgentURLFromEnv().String(); got != "https://agent.example:8127" {
			t.Fatalf("AgentURLFromEnv() = %q, want explicit URL", got)
		}
	})

	t.Run("uses host and port", func(t *testing.T) {
		clearDatadogEnvironment(t)
		t.Setenv("DD_AGENT_HOST", "agent.internal")
		t.Setenv("DD_TRACE_AGENT_PORT", "9126")
		if got := AgentURLFromEnv().String(); got != "http://agent.internal:9126" {
			t.Fatalf("AgentURLFromEnv() = %q, want host and port URL", got)
		}
	})

	t.Run("uses default Unix socket", func(t *testing.T) {
		clearDatadogEnvironment(t)
		defaultAgentSocketPath = filepath.Join(t.TempDir(), "apm.socket")
		if err := os.WriteFile(defaultAgentSocketPath, nil, 0o600); err != nil {
			t.Fatalf("write socket placeholder: %v", err)
		}
		got := AgentURLFromEnv()
		if got.Scheme != "unix" || got.Path != defaultAgentSocketPath {
			t.Fatalf("AgentURLFromEnv() = %#v, want Unix socket", got)
		}
	})

	for _, test := range []struct {
		name string
		url  string
	}{
		{name: "malformed URL", url: "http://%"},
		{name: "missing HTTP host", url: "http:///agent"},
		{name: "missing Unix path", url: "unix://"},
		{name: "unsupported scheme", url: "ftp://agent.example"},
	} {
		t.Run(test.name+" falls back", func(t *testing.T) {
			clearDatadogEnvironment(t)
			defaultAgentSocketPath = filepath.Join(t.TempDir(), "missing.sock")
			t.Setenv("DD_TRACE_AGENT_URL", test.url)
			if got := AgentURLFromEnv().String(); got != "http://localhost:8126" {
				t.Fatalf("AgentURLFromEnv() = %q, want fallback", got)
			}
		})
	}
}

func TestNewHTTPClient(t *testing.T) {
	client := NewHTTPClient(17 * time.Second)
	if client.Timeout != 17*time.Second {
		t.Fatalf("client timeout = %s, want 17s", client.Timeout)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok || !transport.ForceAttemptHTTP2 {
		t.Fatalf("client transport = %#v, want HTTP/2-enabled transport", client.Transport)
	}
}

func TestAgentHTTPTransport(t *testing.T) {
	t.Run("HTTP", func(t *testing.T) {
		agentURL := &url.URL{Scheme: "http", Host: "agent.example:8126"}
		resolvedURL, client := AgentHTTPTransport(agentURL, 7*time.Second)
		if resolvedURL == agentURL {
			t.Fatal("AgentHTTPTransport() returned the mutable input URL")
		}
		if client.Timeout != 7*time.Second {
			t.Fatalf("client timeout = %s, want 7s", client.Timeout)
		}
	})

	t.Run("Unix socket", func(t *testing.T) {
		agentURL := &url.URL{Scheme: "unix", Path: "/tmp/apm.socket"}
		resolvedURL, client := AgentHTTPTransport(agentURL, 7*time.Second)
		if got := resolvedURL.String(); got != "http://UDS__tmp_apm.socket" {
			t.Fatalf("resolved URL = %q, want Unix socket URL", got)
		}
		if _, ok := client.Transport.(*http.Transport); !ok {
			t.Fatalf("client transport = %T, want *http.Transport", client.Transport)
		}
	})
}
